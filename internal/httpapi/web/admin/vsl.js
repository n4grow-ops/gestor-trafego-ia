/* Aba VSL: quantos abriram a página inicial, deram play no vídeo, até onde assistiram e quantos
   clicaram para começar no WhatsApp. Sessões anônimas (sem nome, telefone ou IP).
   Depende das utilidades do admin.js. */
'use strict';

const VSL = { days: 30 };
const VSL_DEVICE = { mobile: 'Celular', desktop: 'Computador', tablet: 'Tablet', outro: 'Outro' };

const mmss = s => { s = Math.max(0, Math.round(s || 0)); return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };
const share = (a, b) => b ? pct(a / b) : '—';

// Retenção: barra por faixa de 5% = parte dos plays que assistiu aquela faixa.
function vslRetention(ret, plays, dur) {
  if (!plays) return '<div class="empty"><b>Ninguém deu play ainda</b>Quando alguém assistir, a curva aparece aqui.</div>';
  const W = 640, H = 200, padB = 24, padT = 16, n = ret.length, slot = W / n, bw = slot * 0.72;
  let g = '';
  for (let k = 0; k <= 4; k++) { const yy = padT + (H - padT - padB) * k / 4; g += `<line class="grid-line" x1="0" x2="${W}" y1="${yy}" y2="${yy}"/>`; }
  ret.forEach((v, i) => {
    const f = v / plays, h = (H - padT - padB) * f, x = slot * i + (slot - bw) / 2, y = H - padB - h;
    const from = Math.round(dur * i / n), to = Math.round(dur * (i + 1) / n);
    g += `<rect class="bar-rev" x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bw.toFixed(1)}" height="${Math.max(0, h).toFixed(1)}" rx="3" data-delay="${i * 25}"><title>${i * 5}% a ${(i + 1) * 5}% (${mmss(from)}–${mmss(to)}): ${num(v)} de ${num(plays)} plays · ${pct(f)}</title></rect>`;
    if (i % 4 === 0) g += `<text class="axis" x="${(slot * i + slot / 2).toFixed(1)}" y="${H - 6}" text-anchor="middle">${dur ? mmss(from) : i * 5 + '%'}</text>`;
  });
  return `<svg class="chart" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label="Retenção do vídeo por trecho">${g}</svg>`;
}

// Funil: da abertura da página ao clique no WhatsApp.
function vslFunnel(r) {
  const steps = [
    ['Abriram a página', r.views],
    ['Deram play', r.plays],
    ['Chegaram a 25%', r.milestones['25']],
    ['Chegaram a 50%', r.milestones['50']],
    ['Chegaram a 75%', r.milestones['75']],
    ['Viram até o fim', r.milestones['100']],
    ['Clicaram no WhatsApp', r.cta],
  ];
  const top = Math.max(1, r.views);
  return `<ol class="vsl-funnel">${steps.map(([l, v], i) => `<li>
    <span class="vf-l">${l}</span>
    <span class="vf-bar"><i data-w="${Math.max(v ? 1.5 : 0, v / top * 100).toFixed(1)}%" data-delay="${i * 40}"></i></span>
    <b class="num">${num(v)}</b><span class="faint small num">${i ? share(v, i === 1 || i === 6 ? r.views : r.plays) : ''}</span>
  </li>`).join('')}</ol>`;
}

function vslDaily(series) {
  const max = Math.max(1, ...series.map(d => d.views));
  const show = series.length <= 31 ? series : series.slice(-31);
  return `<div class="vsl-days" role="img" aria-label="Aberturas e plays por dia">${show.map((d, i) => `<span class="vd" title="${esc(d.day.split('-').reverse().join('/'))}: ${num(d.views)} abriram, ${num(d.plays)} deram play">
    <i class="vd-v" data-h="${(d.views / max * 100).toFixed(1)}%" data-delay="${i * 15}"></i><i class="vd-p" data-h="${(d.plays / max * 100).toFixed(1)}%" data-delay="${i * 15}"></i></span>`).join('')}</div>`;
}

views.vsl = async view => {
  const r = await api('GET', '/vsl?days=' + VSL.days);
  const periods = [[1, 'Hoje'], [7, '7 dias'], [30, '30 dias'], [90, '90 dias']];
  const kpis = [
    ['Deram play', num(r.plays), `${share(r.plays, r.views)} de ${num(r.views)} que abriram · ${num(r.play_visitors)} pessoa(s)`, 'hero'],
    ['Tempo médio assistido', mmss(r.avg_watched_s), r.duration_s ? `de ${mmss(r.duration_s)} · ${share(r.avg_watched_s, r.duration_s)} do vídeo` : 'duração ainda desconhecida'],
    ['Viram até o fim', num(r.milestones['100']), share(r.milestones['100'], r.plays) + ' de quem deu play'],
    ['Clicaram no WhatsApp', num(r.cta), `${num(r.cta_played)} depois de ver o vídeo · ${share(r.cta, r.views)} das visitas`],
  ];
  const dev = Object.entries(r.devices || {}).sort((a, b) => b[1] - a[1]);
  view.innerHTML = `<div class="page">
    <div class="page-head"><div><h1>VSL</h1><p>Vídeo da página inicial · <a href="https://youtu.be/${esc(r.video_id)}" target="_blank" rel="noopener noreferrer">youtu.be/${esc(r.video_id)}</a> · ${num(r.visitors)} pessoa(s) abriram a página</p></div>
      <div class="actions"><div class="seg" role="group" aria-label="Período">${periods.map(([d, l]) => `<button type="button" data-vsl-days="${d}" aria-pressed="${VSL.days === d}">${l}</button>`).join('')}</div></div></div>
    <section class="kpis" aria-label="Indicadores">${kpis.map(([l, v, s, cls]) => `<div class="kpi ${cls || ''}"><small>${l}</small><b class="num">${esc(v)}</b><span>${esc(s)}</span></div>`).join('')}</section>
    <div class="grid-2b">
      <section class="panel"><div class="panel-head"><h2>Até onde assistiram</h2><span class="muted">parte dos plays que viu cada trecho</span></div>
        <div class="panel-body">${vslRetention(r.retention, r.plays, r.duration_s)}</div></section>
      <section class="panel"><div class="panel-head"><h2>Funil</h2><span class="muted">da visita ao WhatsApp</span></div>
        <div class="panel-body">${vslFunnel(r)}</div></section>
    </div>
    <div class="grid-2b">
      <section class="panel"><div class="panel-head"><h2>Por dia</h2><span class="legend"><span><i class="l-cost"></i>abriram</span><span><i class="l-rev"></i>deram play</span></span></div>
        <div class="panel-body">${vslDaily(r.series)}</div></section>
      <section class="panel"><div class="panel-head"><h2>Aparelho</h2><span class="muted">de quem deu play</span></div>
        <div class="panel-body">${dev.length ? `<ol class="vsl-funnel">${dev.map(([k, v]) => `<li><span class="vf-l">${esc(VSL_DEVICE[k] || k)}</span><span class="vf-bar"><i data-w="${(v / r.plays * 100).toFixed(1)}%"></i></span><b class="num">${num(v)}</b><span class="faint small num">${share(v, r.plays)}</span></li>`).join('')}</ol>` : '<div class="empty"><b>Sem plays no período</b></div>'}</div></section>
    </div>
    <section class="panel vsl-recent"><div class="panel-head"><h2>Quem assistiu</h2><span class="muted">os 50 plays mais novos · anônimos</span></div>
      <div class="panel-body table-wrap">${r.recent.length ? `<table class="table"><thead><tr><th>Quando</th><th>Aparelho</th><th class="hide-sm">Origem</th><th class="r">Assistiu</th><th class="r">Chegou até</th><th>WhatsApp</th></tr></thead><tbody>
      ${r.recent.map(s => `<tr><td class="num" title="${esc(dt(s.started_at))}">há ${esc(ago(s.started_at))}${s.returning ? ' <span class="chip info">voltou</span>' : ''}</td>
        <td>${esc(VSL_DEVICE[s.device] || '—')}</td><td class="hide-sm">${esc(s.source || 'direto')}</td>
        <td class="r num">${mmss(s.watched_s)}${r.duration_s ? ` <span class="faint small">(${s.watched_pct}%)</span>` : ''}</td><td class="r num">${mmss(s.max_s)}${r.duration_s ? ` <span class="faint small">(${s.max_pct}%)</span>` : ''}</td>
        <td>${s.cta ? '<span class="chip ok">clicou</span>' : '<span class="faint">—</span>'}</td></tr>`).join('')}</tbody></table>`
      : '<div class="empty"><b>Ninguém deu play no período</b>Escolha outro período ou espere as primeiras visitas.</div>'}</div></section>
  </div>`;
};

document.addEventListener('click', e => {
  const t = e.target.closest('button[data-vsl-days]');
  if (!t || !state.me) return;
  VSL.days = +t.dataset.vslDays;
  rerender();
});
