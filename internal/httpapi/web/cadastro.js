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

  function fillSelect(id, options, placeholder) {
    const sel = $(id);
    sel.innerHTML = "";
    const first = document.createElement("option");
    first.value = "";
    first.textContent = placeholder || "Escolha…";
    sel.appendChild(first);
    options.forEach((o) => {
      const opt = document.createElement("option");
      if (typeof o === "string") { opt.value = o; opt.textContent = o; } else { opt.value = o.code; opt.textContent = o.label; }
      sel.appendChild(opt);
    });
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
    plans.forEach((p) => {
      const el = document.createElement("div");
      el.className = "plan" + (p.code === "free" ? " selected" : "") + (p.available ? "" : " soon");
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
    Object.entries(fields || {}).forEach(([name, text]) => {
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
    const get = (n) => (f.elements[n] ? f.elements[n].value : "");
    return {
      full_name: get("full_name"), cpf: get("cpf"), business_name: get("business_name"), cnpj: get("cnpj"),
      legal_name: get("legal_name"), segment: get("segment"), segment_other: get("segment_other"),
      city: get("city"), uf: get("uf"), website: get("website"), instagram: get("instagram"),
      objective: get("objective"), monthly_budget: get("monthly_budget"), ads_experience: get("ads_experience"),
      meta_ad_account: get("meta_ad_account"), approver: get("approver"), approver_name: get("approver_name"),
      referral_source: get("referral_source"), accept_terms: $("accept_terms").checked,
    };
  }

  function localCheck(d) {
    const errs = {};
    ["full_name", "business_name", "segment", "city", "uf", "objective", "monthly_budget", "ads_experience", "meta_ad_account", "approver"]
      .forEach((n) => { if (!String(d[n] || "").trim()) errs[n] = "obrigatório"; });
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
    const digits = $("cnpj").value.replace(/\D/g, "");
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
      if (!$("uf").value && c.uf) $("uf").value = c.uf;
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
      busy($("btn-google"), true);
      try {
        const { error } = await sb.auth.signInWithOAuth({ provider: "google", options: { redirectTo: location.origin + location.pathname } });
        if (error) throw error;
      } catch (_) {
        busy($("btn-google"), false);
        msg("msg-email", "Não deu pra abrir o Google agora. Tente o código por e-mail.", true);
      }
    });

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
      msg("msg-email", "Enviamos um e-mail. Toque no link dele (ou digite o código, se vier um). Confira também o spam.");
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
      goDados();
    });

    $("btn-reenviar").addEventListener("click", () => {
      $("form-codigo").hidden = true;
      $("form-otp").hidden = false;
      msg("msg-email", "");
    });

    $("segment").addEventListener("change", () => { $("segment_other_box").hidden = $("segment").value !== "outro"; });
    $("approver").addEventListener("change", () => { $("approver_name_box").hidden = $("approver").value !== "other"; });
    $("cnpj").addEventListener("blur", lookupCNPJ);

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
    fillSelect("segment", o.segments);
    fillSelect("uf", o.ufs, "UF");
    fillSelect("objective", o.objectives);
    fillSelect("monthly_budget", o.monthly_budgets);
    fillSelect("ads_experience", o.ads_experiences);
    fillSelect("meta_ad_account", o.meta_ad_accounts);
    fillSelect("approver", o.approvers);
    const p = cfg.prefill;
    $("business_name").value = p.business_name || "";
    $("city").value = p.city || "";
    if (p.uf) $("uf").value = p.uf;
    renderPlans(cfg.plans);

    if (!window.supabase || !cfg.supabase.url) { fail("O cadastro está indisponível agora. Tente de novo em instantes."); return; }
    // Fluxo implícito: o link do e-mail funciona mesmo aberto em outro navegador (app de e-mail),
    // porque a sessão volta no próprio link. Com PKCE ele só funcionaria no navegador que pediu.
    sb = window.supabase.createClient(cfg.supabase.url, cfg.supabase.key, {
      auth: { flowType: "implicit", detectSessionInUrl: true, persistSession: true },
    });
    bind();
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
