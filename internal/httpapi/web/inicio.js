// meuanuncio.io — página inicial: conversa de demonstração, anúncio "nascendo",
// gráficos e contadores que realçam ao entrar na tela. Sem JS (ou com movimento
// reduzido) tudo já aparece no estado final.
(function () {
  "use strict";
  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var hasIO = "IntersectionObserver" in window;

  // ---------- topo ganha borda depois da rolagem ----------
  var top = document.getElementById("topo");
  if (top) {
    var onScroll = function () { top.classList.toggle("scrolled", window.scrollY > 8); };
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
  }

  // ---------- contadores ----------
  var fmtInt = new Intl.NumberFormat("pt-BR");
  var fmtMil = new Intl.NumberFormat("pt-BR", { minimumFractionDigits: 1, maximumFractionDigits: 1 });
  function format(el, v) {
    return el.dataset.format === "mil" ? fmtMil.format(v / 1000) + " mil" : fmtInt.format(Math.round(v));
  }
  function count(el) {
    var end = Number(el.dataset.count);
    if (!end || reduce) return;
    var t0 = performance.now(), dur = 1600;
    (function tick(now) {
      var p = Math.min(1, (now - t0) / dur);
      var e = 1 - Math.pow(1 - p, 4); // ease-out-quart
      el.textContent = format(el, end * e);
      if (p < 1) requestAnimationFrame(tick);
    })(t0);
  }

  // ---------- realces ao entrar na tela ----------
  // Só "arma" (esconde o estado inicial) o que ainda está abaixo da dobra;
  // o que já está visível fica como está.
  var anims = document.querySelectorAll("[data-anim], .closer-mark");
  if (!reduce && hasIO) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (!en.isIntersecting) return;
        var el = en.target;
        el.classList.remove("armed");
        el.querySelectorAll("[data-count]").forEach(count);
        io.unobserve(el);
      });
    }, { threshold: 0.2 });
    anims.forEach(function (el) {
      if (el.getBoundingClientRect().top > window.innerHeight * 0.9) {
        el.classList.add("armed");
        io.observe(el);
      }
    });
  }

  // ---------- planos: brilho segue o cursor ----------
  var tiers = document.querySelector(".tiers");
  if (tiers && !reduce && window.matchMedia("(hover: hover)").matches) {
    tiers.querySelectorAll("[data-tier]").forEach(function (card) {
      card.addEventListener("pointermove", function (e) {
        var r = card.getBoundingClientRect();
        card.style.setProperty("--mx", (((e.clientX - r.left) / r.width) * 100).toFixed(1) + "%");
        card.style.setProperty("--my", (((e.clientY - r.top) / r.height) * 100).toFixed(1) + "%");
      });
    });
  }

  // ---------- conversa de demonstração ----------
  var chat = document.querySelector("[data-demo]");
  var ad = document.querySelector("[data-ad]");
  if (!chat || !ad || reduce) return;

  var step = function (n) { return chat.querySelector('[data-step="' + n + '"]'); };
  // [ms, ação]
  var script = [
    [500, function () { show(1); }],
    [1300, function () { show(2); }],
    [2500, function () { hide(2); show(3); }],
    [3500, function () { show(4); }],
    [4200, function () { show(5); }],
    [5500, function () { hide(5); show(6); ad.dataset.state = "draft"; }],
    [7000, function () { show(7); }],
    [7900, function () { show(8); }],
    [9100, function () { show(9); ad.dataset.state = "ready"; }],
    [10100, function () { show(10); }],
    [11600, function () { ad.dataset.state = "live"; }],
    [17000, function () { reset(); }]
  ];
  function show(n) { var el = step(n); if (el) el.classList.add("shown"); }
  function hide(n) { var el = step(n); if (el) el.classList.remove("shown"); }
  function reset() {
    chat.querySelectorAll(".shown").forEach(function (el) { el.classList.remove("shown"); });
    ad.dataset.state = "empty";
  }

  var timers = [];
  var running = false;
  function play() {
    if (running) return;
    running = true;
    chat.classList.add("playing");
    reset();
    script.forEach(function (s, i) {
      timers.push(setTimeout(function () {
        s[1]();
        if (i === script.length - 1) { running = false; timers = []; play(); }
      }, s[0]));
    });
  }
  function stop() {
    timers.forEach(clearTimeout);
    timers = [];
    running = false;
    // pausa no estado final: a conversa inteira e o anúncio no ar
    chat.querySelectorAll("[data-step]").forEach(function (el) {
      el.classList.toggle("shown", !el.classList.contains("typing"));
    });
    ad.dataset.state = "live";
  }

  if (hasIO) {
    var inView = false;
    new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        inView = en.isIntersecting;
        if (inView && !document.hidden) play(); else stop();
      });
    }, { threshold: 0.25 }).observe(chat);
    document.addEventListener("visibilitychange", function () {
      if (document.hidden) stop(); else if (inView) play();
    });
  } else {
    play();
  }
})();

// ---------- VSL: player do YouTube só no clique + estatística anônima ----------
// Manda para /api/vsl: abriu a página (view), deu play, faixas de 5% assistidas (tick) e clique
// para começar no WhatsApp (cta). Sem nome, telefone ou IP: só dois aleatórios (navegador e visita).
(function () {
  "use strict";
  var box = document.querySelector("[data-vsl]");
  if (!box || !window.fetch) return;
  var videoId = box.getAttribute("data-vsl");
  var BUCKETS = 20;

  function uuid() {
    if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
    var b = new Uint8Array(16);
    crypto.getRandomValues(b);
    b[6] = (b[6] & 15) | 64; b[8] = (b[8] & 63) | 128;
    var h = Array.prototype.map.call(b, function (x) { return (x + 256).toString(16).slice(1); }).join("");
    return h.slice(0, 8) + "-" + h.slice(8, 12) + "-" + h.slice(12, 16) + "-" + h.slice(16, 20) + "-" + h.slice(20);
  }
  var visitor = null;
  try { visitor = localStorage.getItem("ma_vsl_v"); } catch (e) { /* sem armazenamento: conta como visitante novo */ }
  if (!visitor || !/^[0-9a-f-]{36}$/.test(visitor)) {
    visitor = uuid();
    try { localStorage.setItem("ma_vsl_v", visitor); } catch (e) { /* idem */ }
  }
  var session = uuid();
  var source = "";
  try {
    source = new URLSearchParams(location.search).get("utm_source") || "";
    if (!source && document.referrer) {
      var host = new URL(document.referrer).hostname;
      if (host !== location.hostname) source = host;
    }
  } catch (e) { /* referrer estranho: fica sem origem */ }

  var mask = 0, maxT = 0, dur = 0, watched = 0, lastT = -1, sentMask = -1, sentMax = -1, sentW = -1;
  function send(ev) {
    var body = JSON.stringify({ s: session, v: visitor, e: ev, d: Math.round(dur), t: Math.round(maxT), m: mask, w: Math.round(watched), src: source.slice(0, 60) });
    try {
      fetch("/api/vsl", { method: "POST", headers: { "Content-Type": "application/json" }, body: body, keepalive: true, credentials: "omit" })
        .catch(function () { /* estatística: se falhar, segue */ });
    } catch (e) { /* idem */ }
    sentMask = mask; sentMax = Math.round(maxT); sentW = Math.round(watched);
  }
  function flush() { if (mask !== sentMask || Math.round(maxT) !== sentMax || Math.round(watched) !== sentW) send("tick"); }

  send("view");
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest('a[href^="https://wa.me/"]');
    if (a) send("cta");
  });
  document.addEventListener("visibilitychange", function () { if (document.visibilityState === "hidden") flush(); });
  window.addEventListener("pagehide", flush);

  var player = null, timer = null, played = false, samples = 0;
  function sample() {
    if (!player || !player.getCurrentTime) return;
    var t = player.getCurrentTime() || 0;
    dur = player.getDuration() || dur;
    // Só conta o tempo que andou sozinho (pulo pra frente ou pra trás não soma).
    if (lastT >= 0 && t > lastT && t - lastT <= 2.5) watched += t - lastT;
    lastT = t;
    if (dur > 0) {
      mask |= 1 << Math.min(BUCKETS - 1, Math.floor((t / dur) * BUCKETS));
      if (t > maxT) maxT = Math.min(t, dur);
    }
    if (++samples % 10 === 0) flush();
  }
  function onState(e) {
    var S = window.YT.PlayerState;
    if (e.data === S.PLAYING) {
      if (!played) { played = true; dur = player.getDuration() || 0; send("play"); }
      if (!timer) timer = setInterval(sample, 1000);
    } else {
      if (timer) { clearInterval(timer); timer = null; }
      lastT = -1;
      if (e.data === S.ENDED) { mask |= 1 << (BUCKETS - 1); maxT = dur; }
      sample();
      flush();
    }
  }
  function mount() {
    var holder = document.createElement("div");
    box.appendChild(holder);
    player = new window.YT.Player(holder, {
      host: "https://www.youtube-nocookie.com",
      videoId: videoId,
      playerVars: { autoplay: 1, rel: 0, playsinline: 1, modestbranding: 1 },
      events: {
        onReady: function (e) {
          var cover = box.querySelector(".vsl-cover");
          if (cover) cover.remove();
          box.classList.remove("loading");
          e.target.playVideo();
        },
        onStateChange: onState
      }
    });
  }
  function start() {
    if (box.classList.contains("loading") || player) return;
    box.classList.add("loading");
    if (window.YT && window.YT.Player) { mount(); return; }
    var prev = window.onYouTubeIframeAPIReady;
    window.onYouTubeIframeAPIReady = function () { if (prev) prev(); mount(); };
    var s = document.createElement("script");
    s.src = "https://www.youtube.com/iframe_api";
    s.onerror = function () { location.href = "https://www.youtube.com/watch?v=" + encodeURIComponent(videoId); };
    document.head.appendChild(s);
  }
  var cover = box.querySelector(".vsl-cover");
  if (cover) cover.addEventListener("click", start);
  // Capa em HD; se o vídeo não tiver (o YouTube devolve um cinza de 120 px), usa a menor.
  var img = cover && cover.querySelector("img");
  if (img) {
    var low = function () { if (img.src.indexOf("hqdefault") < 0) img.src = "https://i.ytimg.com/vi/" + encodeURIComponent(videoId) + "/hqdefault.jpg"; };
    img.addEventListener("error", low);
    img.addEventListener("load", function () { if (img.naturalWidth < 200) low(); });
    if (img.complete && img.naturalWidth && img.naturalWidth < 200) low();
  }
})();
