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
