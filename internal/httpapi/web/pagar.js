// meuanuncio.io — checkout próprio. Sem biblioteca de terceiros. Os dados do cartão só saem daqui no POST
// para a nossa API (que repassa ao Asaas): nada vai para localStorage, console ou URL.
(function () {
  "use strict";
  var body = document.body;
  var token = location.pathname.split("/").filter(Boolean).pop() || "";
  var api = "/api/pagar/" + encodeURIComponent(token);
  var csrf = body.getAttribute("data-csrf") || "";
  var sandbox = body.hasAttribute("data-sandbox");
  var $ = function (id) { return document.getElementById(id); };
  var digits = function (s) { return String(s || "").replace(/\D+/g, ""); };

  // ---------- abas ----------
  var tabs = [$("tab-pix"), $("tab-card")];
  var panes = { "tab-pix": $("pane-pix"), "tab-card": $("pane-card") };
  function select(tab, focus) {
    tabs.forEach(function (t) {
      var on = t === tab;
      t.setAttribute("aria-selected", on ? "true" : "false");
      t.tabIndex = on ? 0 : -1;
      panes[t.id].hidden = !on;
    });
    if (focus) tab.focus();
    if (tab.id === "tab-pix") loadPix();
  }
  tabs.forEach(function (t, i) {
    t.addEventListener("click", function () { select(t, false); });
    t.addEventListener("keydown", function (e) {
      if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
        e.preventDefault();
        select(tabs[(i + (e.key === "ArrowRight" ? 1 : tabs.length - 1)) % tabs.length], true);
      }
    });
  });

  // ---------- pago ----------
  var finished = false;
  function done(text) {
    if (finished) return;
    finished = true;
    stopPolling();
    document.querySelector(".pay").hidden = true;
    var d = $("done");
    if (text) $("done-text").textContent = text;
    d.hidden = false;
    d.focus();
  }

  // ---------- Pix ----------
  var pixLoaded = false, pixLoading = false;
  function pixError(msg) {
    var el = $("pix-error");
    el.textContent = msg;
    el.hidden = !msg;
  }
  function loadPix() {
    if (pixLoaded || pixLoading) return;
    pixLoading = true;
    pixError("");
    fetch(api + "/pix", { headers: { Accept: "application/json" }, credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, status: r.status, j: j }; }); })
      .then(function (res) {
        pixLoading = false;
        if (res.j.status === "paid") return done();
        if (!res.ok) {
          if (res.status === 404) return location.reload();
          return pixError(res.status === 429
            ? "Muitas tentativas seguidas. Espere um minutinho e toque em Pix de novo."
            : "Não conseguimos gerar o Pix agora. Tente de novo em instantes ou pague com cartão.");
        }
        pixLoaded = true;
        var img = $("pix-qr");
        img.src = res.j.image;
        img.hidden = false;
        $("pix-skel").hidden = true;
        $("pix-box").setAttribute("aria-busy", "false");
        $("pix-code").value = res.j.payload;
        $("pix-copy").disabled = false;
        // Vencimento da mensalidade (o QR do Asaas aceita pagamento bem depois; não confundir o cliente).
        var due = res.j.due ? res.j.due.split("-").reverse().join("/") : "";
        $("pix-exp").textContent = due ? "Vence em " + due + ". Pode ser pago uma vez só." : "";
      })
      .catch(function () {
        pixLoading = false;
        pixError("Sem conexão. Confira sua internet e toque em Pix de novo.");
      });
  }
  $("pix-copy").addEventListener("click", function () {
    var input = $("pix-code"), btn = this, label = btn.querySelector(".copy-label");
    var ok = function () {
      btn.classList.add("copied");
      label.textContent = "Copiado!";
      setTimeout(function () { btn.classList.remove("copied"); label.textContent = "Copiar"; }, 2200);
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(input.value).then(ok, fallback);
    } else fallback();
    function fallback() {
      input.removeAttribute("readonly");
      input.select();
      try { if (document.execCommand("copy")) ok(); } catch (e) { /* segue: o texto fica selecionado */ }
      input.setAttribute("readonly", "");
    }
  });

  // ---------- consulta do pagamento ----------
  var pollTimer = null, pollEvery = 5000, started = Date.now();
  function poll() {
    if (finished) return;
    if (document.hidden) { pollTimer = setTimeout(poll, pollEvery); return; }
    if (Date.now() - started > 45 * 60 * 1000) { $("pix-wait").textContent = "Já pagou? Recarregue a página para ver a confirmação."; return; }
    fetch(api + "/status", { headers: { Accept: "application/json" }, credentials: "same-origin", cache: "no-store" })
      .then(function (r) {
        if (r.status === 429) { pollEvery = Math.min(pollEvery * 2, 30000); return null; }
        if (r.status === 404) { location.reload(); return null; }
        return r.ok ? r.json() : null;
      })
      .then(function (j) { if (j && j.status === "paid") done(); })
      .catch(function () { /* rede caiu: tenta de novo no próximo ciclo */ })
      .then(function () { if (!finished) pollTimer = setTimeout(poll, pollEvery); });
  }
  function stopPolling() { if (pollTimer) clearTimeout(pollTimer); }

  // ---------- cartão desenhado ----------
  var card3d = $("card3d");
  var brands = [
    ["elo", /^(4011(78|79)|43(1274|8935)|45(1416|7393|763[12])|50(4175|6699|67[0-7]\d|9000)|627780|63(6297|6368)|650(0(3[1-35-9]|4\d|5[01])|4(0[5-9]|[1-3]\d|8[5-9]|9\d)|5([0-2]\d|3[0-8]|4[1-9]|[5-8]\d|9[0-8])|7(0\d|1[0-8]|2[0-7])|9(0[1-9]|[1-6]\d|7[0-8]))|6516(5[2-9]|[6-7]\d)|6550([0-1]\d|2[1-9]|[3-4]\d|5[0-8]))/],
    ["hipercard", /^(606282|3841(0|4|6)0)/],
    ["amex", /^3[47]/],
    ["mastercard", /^(5[1-5]|2(2[2-9]|[3-6]\d|7[01]|720))/],
    ["visa", /^4/]
  ];
  var brandLabel = { visa: "Visa", mastercard: "Mastercard", elo: "Elo", amex: "Amex", hipercard: "Hipercard" };
  function brandOf(n) {
    for (var i = 0; i < brands.length; i++) if (brands[i][1].test(n)) return brands[i][0];
    return "";
  }
  function groups(n, brand) {
    if (brand === "amex") return [n.slice(0, 4), n.slice(4, 10), n.slice(10, 15)].filter(Boolean).join(" ");
    return n.replace(/(.{4})/g, "$1 ").trim();
  }
  function luhn(n) {
    var sum = 0, dbl = false;
    for (var i = n.length - 1; i >= 0; i--) {
      var d = +n[i];
      if (dbl) { d *= 2; if (d > 9) d -= 9; }
      sum += d; dbl = !dbl;
    }
    return sum % 10 === 0;
  }

  var num = $("cc-number"), name = $("cc-name"), exp = $("cc-exp"), cvv = $("cc-cvv");
  function paintNumber() {
    var n = digits(num.value).slice(0, 19), brand = brandOf(n);
    num.value = groups(n, brand);
    card3d.setAttribute("data-brand", brand);
    $("cv-brand").textContent = brandLabel[brand] || "";
    var mask = brand === "amex" ? "•••• •••••• •••••" : "•••• •••• •••• ••••";
    var shown = groups(n, brand);
    $("cv-number").textContent = shown + mask.slice(shown.length);
    cvv.maxLength = brand === "amex" ? 4 : 3;
    cvv.placeholder = brand === "amex" ? "1234" : "123";
  }
  num.addEventListener("input", paintNumber);
  name.addEventListener("input", function () {
    $("cv-name").textContent = name.value.trim().toUpperCase() || "SEU NOME";
  });
  exp.addEventListener("input", function (e) {
    var d = digits(exp.value).slice(0, 4);
    if (d.length === 1 && +d > 1) d = "0" + d;
    exp.value = d.length > 2 || (d.length === 2 && e.inputType !== "deleteContentBackward") ? d.slice(0, 2) + "/" + d.slice(2) : d;
    $("cv-exp").textContent = exp.value || "MM/AA";
  });
  cvv.addEventListener("input", function () {
    cvv.value = digits(cvv.value).slice(0, cvv.maxLength);
    $("cv-cvv").textContent = cvv.value.replace(/./g, "•") || "•••";
  });
  cvv.addEventListener("focus", function () { card3d.classList.add("flipped"); });
  cvv.addEventListener("blur", function () { card3d.classList.remove("flipped"); });

  // ---------- máscaras do titular ----------
  var doc = $("h-doc"), phone = $("h-phone"), cep = $("h-cep");
  function maskDoc() {
    var d = digits(doc.value).slice(0, 14);
    doc.value = d.length <= 11
      ? d.replace(/(\d{3})(\d)/, "$1.$2").replace(/(\d{3})\.(\d{3})(\d)/, "$1.$2.$3").replace(/\.(\d{3})(\d{1,2})$/, ".$1-$2")
      : d.replace(/(\d{2})(\d)/, "$1.$2").replace(/(\d{2})\.(\d{3})(\d)/, "$1.$2.$3").replace(/\.(\d{3})(\d)/, ".$1/$2").replace(/\/(\d{4})(\d{1,2})$/, "/$1-$2");
  }
  function maskPhone() {
    var d = digits(phone.value).slice(0, 11);
    phone.value = d.length > 10 ? d.replace(/(\d{2})(\d{5})(\d{0,4})/, "($1) $2-$3")
      : d.length > 6 ? d.replace(/(\d{2})(\d{4})(\d{0,4})/, "($1) $2-$3")
      : d.length > 2 ? d.replace(/(\d{2})(\d+)/, "($1) $2") : d;
  }
  function maskCep() {
    var d = digits(cep.value).slice(0, 8);
    cep.value = d.length > 5 ? d.slice(0, 5) + "-" + d.slice(5) : d;
  }
  doc.addEventListener("input", maskDoc);
  phone.addEventListener("input", maskPhone);
  cep.addEventListener("input", maskCep);
  maskDoc(); maskPhone();

  // ---------- validação ----------
  function validCPF(c) {
    if (c.length !== 11 || /^(\d)\1+$/.test(c)) return false;
    for (var t = 9; t < 11; t++) {
      var s = 0;
      for (var i = 0; i < t; i++) s += +c[i] * (t + 1 - i);
      if (((10 * s) % 11) % 10 !== +c[t]) return false;
    }
    return true;
  }
  function validCNPJ(c) {
    if (c.length !== 14 || /^(\d)\1+$/.test(c)) return false;
    var calc = function (len) {
      var s = 0, pos = len - 7;
      for (var i = len; i >= 1; i--) { s += +c[len - i] * pos--; if (pos < 2) pos = 9; }
      var r = s % 11;
      return r < 2 ? 0 : 11 - r;
    };
    return calc(12) === +c[12] && calc(13) === +c[13];
  }
  var fields = {
    number: [num, function (v) {
      var n = digits(v);
      if (n.length < 13) return "Digite o número completo do cartão.";
      if (!luhn(n) && !(sandbox && n === "4444444444444444")) return "Esse número de cartão não é válido. Confira os dígitos.";
    }],
    name: [name, function (v) { if (v.trim().length < 3) return "Escreva o nome como está impresso no cartão."; }],
    expiry: [exp, function (v) {
      var d = digits(v);
      if (d.length !== 4) return "Use o formato MM/AA.";
      var m = +d.slice(0, 2), y = 2000 + +d.slice(2), now = new Date();
      if (m < 1 || m > 12) return "Mês inválido.";
      if (y < now.getFullYear() || (y === now.getFullYear() && m < now.getMonth() + 1)) return "Esse cartão está vencido.";
    }],
    cvv: [cvv, function (v) { if (digits(v).length !== cvv.maxLength) return "O CVV tem " + cvv.maxLength + " dígitos (fica no verso" + (cvv.maxLength === 4 ? ", ou na frente no Amex" : "") + ")."; }],
    holderName: [$("h-name"), function (v) { if (v.trim().split(/\s+/).length < 2) return "Escreva o nome completo do titular."; }],
    cpfCnpj: [doc, function (v) { var d = digits(v); if (!(validCPF(d) || validCNPJ(d))) return "CPF ou CNPJ inválido."; }],
    phone: [phone, function (v) { var d = digits(v); if (d.length < 10) return "Celular com DDD, só números."; }],
    email: [$("h-email"), function (v) { if (!/^[^@\s]+@[^@\s]+\.[^@\s]{2,}$/.test(v.trim())) return "E-mail inválido."; }],
    postalCode: [cep, function (v) { if (digits(v).length !== 8) return "CEP com 8 dígitos."; }],
    addressNumber: [$("h-num"), function (v) { if (!v.trim()) return "Número do endereço (use S/N se não tiver)."; }]
  };
  function setError(key, msg) {
    var f = fields[key];
    if (!f) return;
    f[0].setAttribute("aria-invalid", msg ? "true" : "false");
    $("err-" + key).textContent = msg || "";
  }
  Object.keys(fields).forEach(function (key) {
    var f = fields[key];
    f[0].addEventListener("blur", function () { if (f[0].value) setError(key, f[1](f[0].value)); });
    f[0].addEventListener("input", function () { if (f[0].getAttribute("aria-invalid") === "true") setError(key, f[1](f[0].value)); });
  });

  // ---------- envio ----------
  var form = $("card-form"), submit = $("card-submit"), formErr = $("card-error");
  function formError(msg) { formErr.textContent = msg; formErr.hidden = !msg; }
  function lock() {
    submit.disabled = true;
    formError("Por segurança, o pagamento com cartão foi pausado neste link depois de várias recusas. Tente de novo em 30 minutos ou pague com Pix.");
  }
  if (body.hasAttribute("data-locked")) lock();

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    formError("");
    var first = null;
    Object.keys(fields).forEach(function (key) {
      var msg = fields[key][1](fields[key][0].value);
      setError(key, msg);
      if (msg && !first) first = fields[key][0];
    });
    if (first) { first.focus(); return; }
    submit.disabled = true;
    submit.setAttribute("aria-busy", "true");
    var payload = {
      number: digits(num.value), name: name.value.trim(), expiry: exp.value, cvv: cvv.value,
      holder: {
        name: $("h-name").value.trim(), cpfCnpj: digits(doc.value), email: $("h-email").value.trim(),
        postalCode: digits(cep.value), addressNumber: $("h-num").value.trim(), phone: digits(phone.value)
      }
    };
    fetch(api + "/cartao", {
      method: "POST", credentials: "same-origin", cache: "no-store",
      headers: { "Content-Type": "application/json", Accept: "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify(payload)
    })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (j) { return { status: r.status, j: j }; }); })
      .then(function (res) {
        payload = null;
        var j = res.j || {};
        if (j.status === "paid") {
          cvv.value = ""; num.value = "";
          return done();
        }
        if (j.status === "analysis") {
          return done("Seu pagamento está em análise pelo banco. Assim que aprovar, a Mia te avisa no WhatsApp. Não precisa pagar de novo.");
        }
        if (j.status === "challenge" && /^https:\/\/([a-z0-9-]+\.)*asaas\.com\//.test(j.url || "")) {
          location.href = j.url;
          return;
        }
        submit.disabled = false;
        if (res.status === 400 && j.field) {
          setError(j.field, (fields[j.field] && fields[j.field][1](fields[j.field][0].value)) || "Confira este campo.");
          if (fields[j.field]) fields[j.field][0].focus();
          return;
        }
        if (res.status === 402) {
          if (j.locked) return lock();
          cvv.value = ""; $("cv-cvv").textContent = "•••";
          return formError("O cartão foi recusado. Confira os dados, use outro cartão ou pague com Pix.");
        }
        if (res.status === 429) return j.error === "bloqueado" ? lock() : formError("Muitas tentativas seguidas. Espere um minutinho e tente de novo.");
        if (res.status === 403) return formError("Sua sessão expirou. Recarregue a página e tente de novo.");
        if (res.status === 409) return formError(j.error === "em_andamento"
          ? "Já estamos processando um pagamento deste link. Aguarde alguns segundos."
          : "Não conseguimos confirmar esta cobrança. Fale com a Mia no WhatsApp que ela te ajuda.");
        if (res.status === 404) return location.reload();
        formError("Não conseguimos falar com o banco agora. Tente de novo em instantes: se algo tiver sido cobrado, esta página mostra a confirmação.");
      })
      .catch(function () {
        submit.disabled = false;
        formError("Sem conexão. Confira sua internet antes de tentar de novo: se o pagamento passou, esta página vai mostrar.");
      })
      .then(function () { submit.removeAttribute("aria-busy"); });
  });

  // Abre na forma escolhida no WhatsApp; a outra fica a um toque.
  if (body.getAttribute("data-method") === "card") select($("tab-card"), false);
  else loadPix();
  poll();
})();
