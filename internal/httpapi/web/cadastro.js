// Página de cadastro: confirma o e-mail pelo Supabase Auth (Google ou código), coleta os dados,
// mostra os planos e envia tudo para a api, que confere a sessão no Supabase antes de gravar.
(() => {
  "use strict";
  const token = decodeURIComponent(location.pathname.split("/").filter(Boolean).pop() || "");
  const $ = (id) => document.getElementById(id);
  const api = `/api/cadastro/${encodeURIComponent(token)}`;
  const screens = ["loading", "erro", "passo-email", "passo-dados", "passo-plano", "fim"];
  const stepOf = { "passo-email": "email", "passo-dados": "dados", "passo-plano": "plano", fim: "fim" };
  let cfg = null;
  let sb = null;
  let session = null;
  let formData = null;

  function show(id) {
    screens.forEach((s) => { $(s).hidden = s !== id; });
    const order = ["email", "dados", "plano", "fim"];
    const current = order.indexOf(stepOf[id]);
    document.querySelectorAll(".steps li").forEach((li) => {
      const i = order.indexOf(li.dataset.step);
      li.classList.toggle("active", i === current);
      li.classList.toggle("done", current > i);
    });
    window.scrollTo(0, 0);
  }

  function msg(id, text, isError) {
    const el = $(id);
    el.textContent = text || "";
    el.classList.toggle("error", !!isError);
  }

  function busy(btn, on) {
    if (!btn) return;
    btn.disabled = on;
  }

  function fail(text) {
    $("erro-texto").textContent = text;
    show("erro");
  }

  // ---------- seletor em janela: escolha única fecha sozinho, múltipla tem "Pronto" ----------
  const pickers = {};

  function initPicker(field, options, onChange) {
    const btn = $(field);
    pickers[field] = { options: options.map((o) => (typeof o === "string" ? { code: o, label: o } : o)), multi: btn.hasAttribute("data-multi"), value: btn.hasAttribute("data-multi") ? [] : "", onChange };
    btn.addEventListener("click", () => openPick(field));
    renderPicker(field);
  }

  function setPick(field, value) {
    const p = pickers[field];
    p.value = p.multi ? [...value] : value;
    renderPicker(field);
    if (p.onChange) p.onChange(p.value);
  }

  function renderPicker(field) {
    const p = pickers[field];
    const btn = $(field);
    const chosen = p.options.filter((o) => (p.multi ? p.value.includes(o.code) : o.code === p.value));
    const empty = btn.dataset.placeholder || "Escolha…";
    let text = empty;
    if (chosen.length === 1) text = chosen[0].label;
    else if (chosen.length && chosen.length === p.options.length) text = "Todas as opções";
    else if (chosen.length) text = `${chosen[0].label} +${chosen.length - 1}`;
    btn.querySelector(".picker-val").textContent = text;
    btn.classList.toggle("empty", !chosen.length);
    $("passo-dados").querySelector(`input[name="${field}"]`).value = p.multi ? p.value.join(",") : p.value;
    if (chosen.length) btn.classList.remove("invalid");
  }

  function openPick(field) {
    const p = pickers[field];
    const sheet = $("sheet-pick");
    const list = $("pick-list");
    $("pick-title").textContent = $(field).dataset.title;
    $("pick-hint").textContent = p.multi ? "Pode marcar quantas quiser." : "";
    $("pick-hint").hidden = !p.multi;
    $("pick-done").hidden = !p.multi;
    list.innerHTML = "";
    let draft = p.multi ? [...p.value] : p.value;
    const row = (code, label, i, cls) => {
      const el = document.createElement("label");
      el.className = "opt" + (p.multi ? " multi" : "") + (cls ? " " + cls : "");
      el.style.setProperty("--i", i);
      const input = document.createElement("input");
      input.type = p.multi ? "checkbox" : "radio";
      input.name = "pick";
      input.value = code;
      const mark = document.createElement("span");
      mark.className = "opt-mark";
      const text = document.createElement("span");
      text.textContent = label;
      el.append(input, mark, text);
      list.appendChild(el);
      return input;
    };
    if (p.multi) {
      const all = row("__all", "Todas as opções", 0, "all");
      const boxes = p.options.map((o, i) => row(o.code, o.label, i + 1));
      const sync = () => { boxes.forEach((b) => { b.checked = draft.includes(b.value); }); all.checked = draft.length === p.options.length; };
      all.addEventListener("change", () => { draft = all.checked ? p.options.map((o) => o.code) : []; sync(); });
      boxes.forEach((b) => b.addEventListener("change", () => {
        draft = p.options.map((o) => o.code).filter((c) => (c === b.value ? b.checked : draft.includes(c)));
        sync();
      }));
      sync();
      $("pick-done").onclick = () => { setPick(field, draft); sheet.close(); };
    } else {
      p.options.forEach((o, i) => {
        const r = row(o.code, o.label, i);
        r.checked = o.code === draft;
        r.addEventListener("change", () => { setPick(field, o.code); setTimeout(() => sheet.close(), 160); });
      });
    }
    $(field).setAttribute("aria-expanded", "true");
    sheet.onclose = () => { $(field).setAttribute("aria-expanded", "false"); $(field).focus({ preventScroll: true }); };
    sheet.showModal();
    const first = list.querySelector("input:checked") || list.querySelector("input");
    if (first) {
      first.focus({ preventScroll: true });
      first.closest(".opt").scrollIntoView({ block: "nearest" });
    }
  }

  // "SAO PAULO" -> "Sao Paulo"; preposições ficam minúsculas.
  function titleCase(s) {
    const small = ["da", "de", "do", "das", "dos", "e"];
    return s.toLowerCase().split(" ").map((w, i) => (i > 0 && small.includes(w)) ? w : w.charAt(0).toUpperCase() + w.slice(1)).join(" ");
  }

  function money(cents) {
    return cents === 0 ? "Grátis" : (cents / 100).toLocaleString("pt-BR", { style: "currency", currency: "BRL" }) + "/mês";
  }

  function renderPlans(plans) {
    const box = $("planos");
    box.innerHTML = "";
    plans.forEach((p, i) => {
      const el = document.createElement("div");
      el.className = "plan" + (p.code === "free" ? " selected" : "") + (p.available ? "" : " soon");
      el.style.setProperty("--i", i);
      const info = document.createElement("div");
      const h = document.createElement("h2");
      h.textContent = p.name;
      const ul = document.createElement("ul");
      [`Até ${p.daily_message_limit} mensagens por dia`,
       `Investimento de até ${p.max_daily_budget_brl.toLocaleString("pt-BR", { style: "currency", currency: "BRL" })} por dia`,
       `Até ${p.max_projects} projetos`].forEach((t) => { const li = document.createElement("li"); li.textContent = t; ul.appendChild(li); });
      info.append(h, ul);
      if (!p.available) { const b = document.createElement("span"); b.className = "badge"; b.textContent = "Em breve"; info.appendChild(b); }
      const price = document.createElement("div");
      price.className = "price";
      price.textContent = money(p.price_cents);
      el.append(info, price);
      box.appendChild(el);
    });
  }

  function markErrors(fields) {
    document.querySelectorAll(".invalid").forEach((el) => el.classList.remove("invalid"));
    document.querySelectorAll(".err[data-dyn]").forEach((el) => el.remove());
    $("passo-dados").querySelector('.err[data-for="accept_terms"]').textContent = "";
    Object.entries(fields || {}).forEach(([key, text]) => {
      const name = key === "cpf" || key === "cnpj" ? "doc_number" : key;
      if (name === "accept_terms") { $("passo-dados").querySelector('.err[data-for="accept_terms"]').textContent = text; return; }
      const input = $(name);
      if (!input) return;
      input.classList.add("invalid");
      const small = document.createElement("small");
      small.className = "err";
      small.dataset.dyn = "1";
      small.textContent = text;
      input.insertAdjacentElement("afterend", small);
    });
    const first = document.querySelector(".invalid");
    if (first) first.focus();
  }

  function collect() {
    const f = $("passo-dados");
    const get = (n) => { const el = f.querySelector(`[name="${n}"]`); return el ? el.value : ""; };
    return {
      full_name: get("full_name"),
      cpf: get("doc_type") === "cpf" ? get("doc_number") : "",
      cnpj: get("doc_type") === "cnpj" ? get("doc_number") : "",
      business_name: get("business_name"),
      legal_name: get("legal_name"), segment: get("segment"), segment_other: get("segment_other"),
      city: get("city"), uf: get("uf"), website: get("website"), instagram: get("instagram"),
      objectives: pickers.objectives ? [...pickers.objectives.value] : [], monthly_budget: get("monthly_budget"), ads_experience: get("ads_experience"),
      meta_ad_account: get("meta_ad_account"), approver: get("approver"), approver_name: get("approver_name"),
      referral_source: get("referral_source"), accept_terms: $("accept_terms").checked,
    };
  }

  function localCheck(d) {
    const errs = {};
    ["full_name", "business_name", "segment", "city", "uf", "monthly_budget", "ads_experience", "meta_ad_account", "approver"]
      .forEach((n) => { if (!String(d[n] || "").trim()) errs[n] = "obrigatório"; });
    if (!d.objectives.length) errs.objectives = "escolha pelo menos um";
    if (d.segment === "outro" && !d.segment_other.trim()) errs.segment_other = "diga qual é o segmento";
    if (d.approver === "other" && !d.approver_name.trim()) errs.approver_name = "diga quem aprova";
    if (!d.accept_terms) errs.accept_terms = "é preciso aceitar os Termos e a Política de Privacidade";
    return errs;
  }

  function goDados() {
    const user = session.user;
    $("email-confirmado").textContent = user.email;
    const meta = user.user_metadata || {};
    if (!$("full_name").value) $("full_name").value = cfg.prefill.full_name || meta.full_name || meta.name || "";
    show("passo-dados");
  }

  async function lookupCNPJ() {
    if (pickers.doc_type.value !== "cnpj") return;
    const digits = $("doc_number").value.replace(/\D/g, "");
    const hint = $("cnpj-hint");
    if (digits.length !== 14) { hint.textContent = ""; return; }
    hint.textContent = "Buscando dados do CNPJ…";
    try {
      const r = await fetch(`${api}/cnpj/${digits}`);
      if (r.status === 404) { hint.textContent = "CNPJ não encontrado. Preencha os dados abaixo."; return; }
      if (r.status === 422) { hint.textContent = "CNPJ inválido. Confira os números."; return; }
      if (!r.ok) { hint.textContent = "Não deu pra buscar agora. Preencha os dados abaixo."; return; }
      const c = await r.json();
      $("legal_name").value = c.legal_name || "";
      if (!$("business_name").value) $("business_name").value = c.trade_name || c.legal_name || "";
      if (!$("city").value && c.city) $("city").value = titleCase(c.city);
      if (!pickers.uf.value && c.uf) setPick("uf", c.uf);
      hint.textContent = `Encontrado: ${c.legal_name}${c.status && c.status !== "ATIVA" ? " (situação: " + c.status + ")" : ""}`;
    } catch (_) {
      hint.textContent = "Não deu pra buscar agora. Preencha os dados abaixo.";
    }
  }

  async function submit() {
    const btn = $("btn-concluir");
    busy(btn, true);
    msg("msg-plano", "Salvando…");
    try {
      const { data } = await sb.auth.getSession();
      session = data.session;
      if (!session) { msg("msg-email", "Sua sessão expirou. Confirme o e-mail de novo.", true); show("passo-email"); return; }
      const r = await fetch(api, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${session.access_token}` },
        body: JSON.stringify({ ...formData, plan: "free" }),
      });
      const body = await r.json().catch(() => ({}));
      if (r.ok || r.status === 409) {
        $("fim-whatsapp").href = body.whatsapp || cfg.whatsapp;
        show("fim");
        sb.auth.signOut().catch(() => {});
        return;
      }
      if (r.status === 401) { await sb.auth.signOut().catch(() => {}); msg("msg-email", "Confirme seu e-mail de novo, por favor.", true); show("passo-email"); return; }
      if (r.status === 410) { fail("Esse link expirou. Manda uma mensagem pra gente no WhatsApp que enviamos um novo."); return; }
      if (r.status === 403) { fail("O cadastro da conta precisa ser feito pelo responsável. Peça pra ele mandar uma mensagem no WhatsApp."); return; }
      if (r.status === 429) { msg("msg-plano", "Muitas tentativas seguidas. Espere um minuto e tente de novo.", true); return; }
      if (r.status === 422 && body.error === "plano_indisponivel") { msg("msg-plano", "Esse plano ainda não está disponível. Siga com o Free.", true); return; }
      if (r.status === 422) { show("passo-dados"); markErrors(body.fields); msg("msg-dados", "Confira os campos marcados.", true); return; }
      msg("msg-plano", "Não deu certo agora. Tenta de novo em instantes.", true);
    } catch (_) {
      msg("msg-plano", "Sem conexão. Tenta de novo em instantes.", true);
    } finally {
      busy(btn, false);
    }
  }

  function bind() {
    $("btn-google").addEventListener("click", async () => {
      if ($("btn-google").classList.contains("unavailable")) {
        msg("msg-choice", "Entrar com Google chega em breve. Por enquanto, use seu e-mail.");
        return;
      }
      busy($("btn-google"), true);
      try {
        const { error } = await sb.auth.signInWithOAuth({ provider: "google", options: { redirectTo: location.origin + location.pathname } });
        if (error) throw error;
      } catch (_) {
        busy($("btn-google"), false);
        msg("msg-choice", "Não deu pra abrir o Google agora. Tente pelo e-mail.", true);
      }
    });

    const sheet = $("sheet-email");
    $("btn-email").addEventListener("click", () => {
      msg("msg-choice", "");
      sheet.showModal();
      ($("form-codigo").hidden ? $("otp-email") : $("otp-code")).focus();
    });
    $("btn-sheet-close").addEventListener("click", () => sheet.close());
    sheet.addEventListener("click", (ev) => { if (ev.target === sheet) sheet.close(); });

    $("form-otp").addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const email = $("otp-email").value.trim();
      if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) { msg("msg-email", "Digite um e-mail válido.", true); return; }
      const btn = ev.submitter;
      busy(btn, true);
      msg("msg-email", "Enviando código…");
      let error = null;
      try {
        ({ error } = await sb.auth.signInWithOtp({ email, options: { shouldCreateUser: true, emailRedirectTo: location.origin + location.pathname } }));
      } catch (e) {
        error = e;
      }
      busy(btn, false);
      if (error) { msg("msg-email", "Não deu pra enviar o código. Espere um pouco e tente de novo.", true); return; }
      $("otp-destino").textContent = email;
      $("form-otp").hidden = true;
      $("form-codigo").hidden = false;
      $("otp-code").focus();
      msg("msg-email", "");
    });

    $("form-codigo").addEventListener("submit", async (ev) => {
      ev.preventDefault();
      const code = $("otp-code").value.replace(/\D/g, "");
      if (code.length !== 6) { msg("msg-email", "O código tem 6 dígitos.", true); return; }
      const btn = ev.submitter;
      busy(btn, true);
      let data = null;
      let error = null;
      try {
        ({ data, error } = await sb.auth.verifyOtp({ email: $("otp-destino").textContent, token: code, type: "email" }));
      } catch (e) {
        error = e;
      }
      busy(btn, false);
      if (error || !data || !data.session) { msg("msg-email", "Código inválido ou expirado.", true); return; }
      session = data.session;
      msg("msg-email", "");
      sheet.close();
      goDados();
    });

    // código completo = confirma sozinho
    $("otp-code").addEventListener("input", () => {
      const v = $("otp-code").value.replace(/\D/g, "").slice(0, 6);
      $("otp-code").value = v;
      if (v.length === 6) $("form-codigo").requestSubmit();
    });

    $("btn-reenviar").addEventListener("click", () => {
      $("form-codigo").hidden = true;
      $("form-otp").hidden = false;
      msg("msg-email", "");
    });

    $("pick-close").addEventListener("click", () => $("sheet-pick").close());
    $("sheet-pick").addEventListener("click", (ev) => { if (ev.target === $("sheet-pick")) $("sheet-pick").close(); });
    $("doc_number").addEventListener("blur", lookupCNPJ);

    $("passo-dados").addEventListener("submit", (ev) => {
      ev.preventDefault();
      const d = collect();
      const errs = localCheck(d);
      markErrors(errs);
      if (Object.keys(errs).length) { msg("msg-dados", "Confira os campos marcados.", true); return; }
      msg("msg-dados", "");
      formData = d;
      show("passo-plano");
    });

    $("btn-voltar").addEventListener("click", () => show("passo-dados"));
    $("btn-concluir").addEventListener("click", submit);
  }

  async function start() {
    let r;
    try {
      r = await fetch(api);
    } catch (_) {
      fail("Sem conexão. Recarregue a página em instantes.");
      return;
    }
    const body = await r.json().catch(() => ({}));
    const back = body.whatsapp || "#";
    $("erro-whatsapp").href = back;
    if (r.status === 409) { $("fim-whatsapp").href = back; show("fim"); return; }
    if (!r.ok) { fail("Esse link expirou ou já foi usado. Manda uma mensagem pra gente no WhatsApp que enviamos um novo."); return; }
    cfg = body;
    $("erro-whatsapp").href = cfg.whatsapp;
    const o = cfg.options;
    initPicker("doc_type", [
      { code: "cpf", label: "CPF (pessoa física)" },
      { code: "cnpj", label: "CNPJ (empresa)" },
    ], (v) => {
      $("doc_box").hidden = !v;
      $("doc_label").textContent = v === "cnpj" ? "Número do CNPJ" : "Número do CPF";
      $("doc_number").placeholder = v === "cnpj" ? "00.000.000/0000-00" : "000.000.000-00";
      $("doc_number").value = "";
      $("cnpj-hint").textContent = v === "cnpj" ? "A gente busca os dados da empresa e preenche o resto pra você." : "";
      if (v) setTimeout(() => $("doc_number").focus(), 200);
    });
    initPicker("segment", o.segments, (v) => { $("segment_other_box").hidden = v !== "outro"; });
    initPicker("uf", o.ufs);
    initPicker("objectives", o.objectives);
    initPicker("monthly_budget", o.monthly_budgets);
    initPicker("ads_experience", o.ads_experiences);
    initPicker("meta_ad_account", o.meta_ad_accounts);
    initPicker("approver", o.approvers, (v) => { $("approver_name_box").hidden = v !== "other"; });
    const p = cfg.prefill;
    $("business_name").value = p.business_name || "";
    $("city").value = p.city || "";
    if (p.uf) setPick("uf", p.uf);
    renderPlans(cfg.plans);

    if (!window.supabase || !cfg.supabase.url) { fail("O cadastro está indisponível agora. Tente de novo em instantes."); return; }
    // Fluxo implícito: o link do e-mail funciona mesmo aberto em outro navegador (app de e-mail),
    // porque a sessão volta no próprio link. Com PKCE ele só funcionaria no navegador que pediu.
    sb = window.supabase.createClient(cfg.supabase.url, cfg.supabase.key, {
      auth: { flowType: "implicit", detectSessionInUrl: true, persistSession: true },
    });
    bind();
    // Botão do Google só aparece se o provedor estiver ativo no Supabase.
    try {
      const r = await fetch(`${cfg.supabase.url}/auth/v1/settings`, { headers: { apikey: cfg.supabase.key } });
      const st = r.ok ? await r.json() : null;
      if (!st || !st.external || !st.external.google) $("btn-google").classList.add("unavailable");
    } catch (_) {
      $("btn-google").classList.add("unavailable");
    }
    try {
      const { data } = await sb.auth.getSession();
      session = data.session;
    } catch (_) {
      session = null;
    }
    if (location.search.includes("code=") || location.hash.includes("access_token")) {
      history.replaceState(null, "", location.pathname);
    }
    if (session && session.user && session.user.email) goDados(); else show("passo-email");
  }

  start();
})();
