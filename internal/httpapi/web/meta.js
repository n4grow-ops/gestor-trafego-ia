// Filtro da tela de escolha da conta de anúncios e da Página (/meta). Sem JS a lista aparece inteira.
(function () {
  function norm(s) {
    return (s || '').toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '');
  }
  document.querySelectorAll('[data-pick]').forEach(function (box) {
    var input = box.querySelector('[data-filter]');
    if (!input) return;
    var opts = Array.prototype.slice.call(box.querySelectorAll('.pick-opt'));
    var empty = box.querySelector('[data-empty]');
    input.hidden = false;
    input.addEventListener('input', function () {
      var terms = norm(input.value).split(/\s+/).filter(Boolean);
      var shown = 0;
      opts.forEach(function (o) {
        var hay = norm(o.getAttribute('data-search'));
        var hit = terms.every(function (t) { return hay.indexOf(t.replace(/^act_/, '')) !== -1; });
        // A opção marcada nunca some: o cliente vê o que escolheu mesmo filtrando outra coisa.
        var keep = hit || o.querySelector('input').checked;
        o.hidden = !keep;
        if (keep) shown++;
      });
      if (empty) empty.hidden = shown > 0;
    });
  });
})();
