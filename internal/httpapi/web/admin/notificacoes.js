/* Aba Notificações: avisos para apps de automação por webhook (Pushcut no iPhone, MacroDroid no
   Android ou um webhook qualquer) quando entra venda, cliente novo, cadastro ou mensagem. Só
   administrador (a API confere de novo). A URL do destino é segredo: a API só devolve o final.
   Depende das utilidades do admin.js. */
'use strict';

const NOTIF = { d: null, edit: null, open: {}, results: {}, token: null, lastTpl: 'title_tpl', preview: '' };
const NT_KINDS = {
  pushcut: { label: 'Pushcut', sub: 'iPhone', ph: 'https://api.pushcut.io/SEU-SEGREDO/notifications/Venda' },
  macrodroid: { label: 'MacroDroid', sub: 'Android', ph: 'https://trigger.macrodroid.com/SEU-ID/venda' },
  webhook: { label: 'Webhook', sub: 'genérico', ph: 'https://seu-sistema.com/webhooks/meuanuncio' },
};
const ntEvent = k => (NOTIF.d?.events || []).find(e => e.key === k);
const ntEventLabel = k => ntEvent(k)?.label || k;
// Mesmo modelo do servidor: {variavel} vira o valor; variável desconhecida fica como está.
const ntRender = (tpl, vars) => String(tpl || '').replace(/\{([a-z_]+)\}/g, (m, k) => Object.prototype.hasOwnProperty.call(vars, k) ? vars[k] : m);
function ntVars(ev) {
  const now = new Date(), two = n => String(n).padStart(2, '0');
  return { ...(ev?.sample || {}), evento: ev?.label || '', quando: `${two(now.getDate())}/${two(now.getMonth() + 1)} ${two(now.getHours())}:${two(now.getMinutes())}` };
}
function ntMessage(ev, titleTpl, textTpl) {
  const v = ntVars(ev);
  return { title: ntRender(titleTpl.trim() || ev.default_title, v), text: ntRender(textTpl.trim() || ev.default_text, v) };
}

const ntAgo = iso => { const a = ago(iso); return a === 'agora' ? 'agora' : 'há ' + a; };
function ntDelivery(x) {
  if (!x) return `<span class="chip">Nada enviado ainda</span>`;
  const when = `<time datetime="${esc(x.created_at)}" title="${esc(dt(x.created_at))}">${esc(ntAgo(x.created_at))}</time>`;
  return x.ok
    ? `<span class="chip ok">${icon('check')}Entregue</span><span class="faint small">${esc(ntEventLabel(x.event))} · HTTP ${x.status_code} · ${when}</span>`
    : `<span class="chip err">${icon('alert')}Falhou</span><span class="faint small nt-errline" title="${esc(x.error)}">${esc(ntEventLabel(x.event))}${x.attempt > 1 ? ` · tentativa ${x.attempt}` : ''} · ${esc(x.error || 'erro')} · ${when}</span>`;
}

// "HTTP 503" já vem no erro: não repete o status.
const ntDetail = x => [x.status_code && !String(x.error).startsWith('HTTP') ? 'HTTP ' + x.status_code : '', x.error].filter(Boolean).join(' · ');
function ntHistory(id) {
  const h = NOTIF.open[id];
  if (!h) return '';
  if (h === 'loading') return `<div class="nt-hist"><p class="faint small">Carregando…</p></div>`;
  if (!h.length) return `<div class="nt-hist"><p class="faint small">Nenhuma entrega ainda. Use “Enviar teste”.</p></div>`;
  return `<div class="nt-hist" role="region" aria-label="Últimas entregas"><table class="nt-table"><thead><tr><th>Quando</th><th>Evento</th><th>Situação</th><th>Detalhe</th></tr></thead><tbody>
    ${h.map(x => `<tr><td class="num">${esc(dt(x.created_at))}</td><td>${esc(ntEventLabel(x.event))}${x.attempt > 1 ? ` <span class="faint">(${x.attempt}ª)</span>` : ''}</td>
      <td><span class="chip ${x.ok ? 'ok' : 'err'}">${x.ok ? 'Entregue' : 'Falhou'}</span></td>
      <td class="nt-detail">${esc(ntDetail(x))}</td></tr>`).join('')}
    </tbody></table><p class="faint small">Guardamos as últimas ${num(NOTIF.d?.keep || 100)} entregas deste destino.</p></div>`;
}

function ntCard(x) {
  const k = NT_KINDS[x.kind] || { label: x.kind, sub: '' }, res = NOTIF.results[x.id];
  const evs = x.events.length ? x.events.map(e => `<span class="chip info">${esc(ntEventLabel(e))}</span>`).join('') : '<span class="faint small">Nenhum evento marcado: só o teste chega.</span>';
  return `<article class="nt-card ${x.active ? '' : 'off'}" aria-label="${esc(x.name)}">
    <div class="nt-head"><div class="nt-title"><h2>${esc(x.name)}</h2><span class="faint small">${esc(k.label)} · ${esc(k.sub)}</span></div>
      <label class="switch-wrap"><input type="checkbox" data-nt-active="${x.id}" ${x.active ? 'checked' : ''} aria-label="Destino ligado"><span>${x.active ? 'Ligado' : 'Desligado'}</span></label></div>
    <code class="nt-url num" title="A URL completa fica guardada e não aparece aqui">${esc(x.url_masked)}</code>
    <div class="nt-evs">${evs}</div>
    <div class="nt-last">${ntDelivery(x.last_delivery)}</div>
    ${res ? `<p class="nt-result ${res.ok ? 'ok' : 'err'}" role="status">${res.ok ? `${icon('check')}Teste entregue (HTTP ${res.status_code}). Confira o celular.` : `${icon('alert')}Teste falhou: ${esc(res.error || 'erro')}`}</p>` : ''}
    <div class="nt-act">
      <button class="btn sm primary" type="button" data-nt-test="${x.id}">${icon('send')}Enviar teste</button>
      <button class="btn sm" type="button" data-nt-hist="${x.id}" aria-expanded="${!!NOTIF.open[x.id]}">${icon('history')}Histórico</button>
      <button class="btn sm ghost" type="button" data-nt-edit="${x.id}" aria-label="Editar ${esc(x.name)}">Editar</button></div>
    ${ntHistory(x.id)}
  </article>`;
}

function ntTokenPanel(t) {
  const fresh = NOTIF.token;
  const status = fresh
    ? `<div class="nt-token" role="status"><p><b>Copie agora:</b> este token não aparece de novo. O anterior parou de valer.</p>
        <div class="nt-token-row"><code class="num">${esc(fresh)}</code><button class="btn sm" type="button" data-nt-copy="${esc(fresh)}">${icon('copy')}Copiar</button></div></div>`
    : t.exists ? `<p class="small">Token ativo terminando em <code class="num">${esc(t.last4)}</code>, gerado ${t.created_at ? 'em ' + esc(dt(t.created_at)) : ''}${t.created_by ? ' por ' + esc(t.created_by.split('@')[0]) : ''}. ${t.used_at ? 'Último uso ' + esc(dt(t.used_at)) + '.' : 'Ainda não usado.'}</p>`
    : `<p class="small faint">Nenhum token gerado: o endpoint responde 401.</p>`;
  const origin = location.origin.replace('admin.', 'cadastro.');
  return `<section class="panel nt-panel"><div class="panel-head"><h2>Venda de outra plataforma</h2>
      <button class="btn sm" type="button" data-nt-token>${icon('lock')}${t.exists ? 'Gerar token novo' : 'Gerar token'}</button></div>
    <div class="panel-body"><p class="small">Quando vender fora do sistema (outra plataforma, n8n, planilha), mande um POST com o token. Vira o evento <b>Venda aprovada</b>.</p>
      ${status}
      <pre class="nt-code">POST ${esc(origin)}/api/hooks/venda
Authorization: Bearer SEU_TOKEN
Content-Type: application/json

{"nome":"Ana Souza","whatsapp":"5511988887777","valor":97.00,"produto":"Plano Pro","origem":"hotmart"}</pre></div></section>`;
}

const NT_HOWTO = `<section class="panel nt-panel"><div class="panel-head"><h2>Como configurar</h2></div>
  <div class="panel-body nt-howto">
    <div><h3>${icon('bell')}Pushcut (iPhone)</h3><ol>
      <li>Instale o Pushcut e abra <b>Notifications</b> › <b>+</b>. Dê um nome (ex.: <code>Venda</code>) e salve.</li>
      <li>Abra a notificação, toque em <b>Webhook</b> e copie a URL (<code>https://api.pushcut.io/…/notifications/Venda</code>).</li>
      <li>Aqui: <b>Novo destino</b> › tipo Pushcut › cole a URL › marque os eventos › <b>Enviar teste</b>.</li></ol>
      <p class="faint small">O título e o texto do modelo substituem os da notificação no Pushcut.</p></div>
    <div><h3>${icon('bell')}MacroDroid (Android)</h3><ol>
      <li>Crie uma macro com o gatilho <b>Webhook (URL)</b>, dê um identificador (ex.: <code>venda</code>) e copie a URL (<code>https://trigger.macrodroid.com/…/venda</code>).</li>
      <li>No gatilho, crie as variáveis locais <b>title</b> e <b>text</b> (texto) e ligue cada uma ao parâmetro de mesmo nome.</li>
      <li>Ação: <b>Notificação › Exibir notificação</b>, com título <code>{lv=title}</code> e texto <code>{lv=text}</code>.</li>
      <li>Aqui: <b>Novo destino</b> › tipo MacroDroid › cole a URL › <b>Enviar teste</b>.</li></ol>
      <p class="faint small">Mandamos <code>title</code> e <code>text</code> na URL e também no corpo JSON.</p></div>
    <div><h3>${icon('link')}Webhook genérico</h3><ol>
      <li>Qualquer endereço <b>https</b> público que aceite POST JSON.</li>
      <li>Corpo: <code>{"evento","dados","titulo","texto","enviado_em"}</code>.</li>
      <li>Respondeu 2xx: entregue. Erro de rede, 429 ou 5xx: tentamos mais 2 vezes (30 s e 60 s depois).</li></ol></div>
  </div></section>`;

views.notificacoes = async view => {
  const d = NOTIF.d = await api('GET', '/notifications');
  const on = d.destinations.filter(x => x.active).length;
  view.innerHTML = `<div class="page nt-page">
    <div class="page-head"><div><h1>Notificações</h1><p>${num(d.destinations.length)} destino${d.destinations.length === 1 ? '' : 's'} · ${num(on)} ligado${on === 1 ? '' : 's'}</p></div>
      <div class="actions"><button class="btn primary" type="button" data-nt-new>${icon('plus')}Novo destino</button></div></div>
    <div class="alert note">${icon('bell', 'ico')}<p>Receba no celular quando entra venda, cliente chama, alguém conclui o cadastro ou manda mensagem. O envio é em segundo plano: nunca atrasa a venda nem a resposta da Mia.</p></div>
    <div class="nt-cards">${d.destinations.map(ntCard).join('') || `<div class="empty nt-empty"><b>Nenhum destino ainda</b>Crie o primeiro: Pushcut no iPhone, MacroDroid no Android ou um webhook.</div>`}</div>
    ${ntTokenPanel(d.sale_token)}
    ${NT_HOWTO}</div>`;
};

/* ---------- gaveta: criar e editar ---------- */
function ntOpen(id) {
  const x = id ? NOTIF.d.destinations.find(v => v.id === id) : null;
  if (id && !x) return;
  NOTIF.edit = x;
  const v = x || { name: '', kind: 'pushcut', events: ['venda.nova'], active: true, title_tpl: '', text_tpl: '' };
  const subs = NOTIF.d.events.filter(e => e.subscribable);
  const d = $('#drawer');
  d.setAttribute('aria-label', x ? 'Editar destino' : 'Novo destino');
  d.innerHTML = `<form class="nt-form" id="nt-form" novalidate>
    <div class="d-head"><div class="d-top"><span class="avatar">${icon('bell', 'ico')}</span><div><h2>${x ? esc(x.name) : 'Novo destino'}</h2>
      <p>${x ? `Criado em ${esc(dt(x.created_at))}` : 'Cole a URL do webhook do app e escolha os eventos.'}</p></div>
      <button class="icon-btn" type="button" data-close-drawer aria-label="Fechar">${icon('x')}</button></div></div>
    <div class="d-body"><div class="form-grid">
      <label>Nome<input class="field" name="name" maxlength="60" required autocomplete="off" value="${esc(v.name)}" placeholder="ex.: iPhone do Gui"></label>
      <label>App<select class="field" name="kind">${Object.entries(NT_KINDS).map(([k, o]) => `<option value="${k}" ${k === v.kind ? 'selected' : ''}>${o.label} (${o.sub})</option>`).join('')}</select></label>
      <label class="full">URL do webhook<input class="field num" name="url" type="url" inputmode="url" autocomplete="off" spellcheck="false" ${x ? '' : 'required'} placeholder="${esc((NT_KINDS[v.kind] || NT_KINDS.webhook).ph)}">
        <small class="hint">${x ? `Guardada: <code>${esc(x.url_masked)}</code>. Deixe vazio para manter.` : 'Só https. A URL é secreta: depois de salvar, aparece só o final.'}</small></label>
      <fieldset class="full nt-events"><legend>Eventos</legend>
        ${subs.map(e => `<label class="nt-ev"><input type="checkbox" name="events" value="${esc(e.key)}" ${v.events.includes(e.key) ? 'checked' : ''}>
          <span><b>${esc(e.label)}</b><small>${esc(e.description)}</small></span></label>`).join('')}</fieldset>
      <label class="full">Título<input class="field" name="title_tpl" maxlength="200" autocomplete="off" value="${esc(v.title_tpl)}" data-nt-tpl placeholder="Vazio = padrão do evento"></label>
      <label class="full">Mensagem<textarea class="field" name="text_tpl" maxlength="1000" rows="3" data-nt-tpl placeholder="Vazio = padrão do evento">${esc(v.text_tpl)}</textarea></label>
      <div class="full nt-vars"><span class="faint small">Variáveis (toque para inserir):</span>
        ${NOTIF.d.vars.map(o => `<button class="nt-var" type="button" data-nt-var="${esc(o.key)}" title="${esc(o.help)}">{${esc(o.key)}}</button>`).join('')}</div>
      <div class="full nt-preview-wrap"><div class="nt-preview-head"><span class="small"><b>Prévia</b> com dados de exemplo</span><div class="seg" role="group" aria-label="Prévia do evento" id="nt-pv-seg"></div></div>
        <div class="nt-phone" id="nt-preview" aria-live="polite"></div></div>
      <label class="switch-wrap full"><input type="checkbox" name="active" ${v.active ? 'checked' : ''}><span>Destino ligado</span></label>
    </div><p class="form-err" id="nt-err" role="alert"></p></div>
    <div class="d-foot">
      <button class="btn primary" type="submit" id="nt-save">${icon('check')}${x ? 'Salvar' : 'Criar destino'}</button>
      <button class="btn ghost" type="button" data-close-drawer>Cancelar</button>
      ${x ? `<button class="btn danger" type="button" data-nt-del="${x.id}">${icon('trash')}Apagar</button>` : ''}
    </div></form>`;
  d.inert = false; d.classList.add('on'); d.setAttribute('aria-hidden', 'false');
  $('#drawer-backdrop').classList.add('on');
  state.drawer = { notif: true };
  NOTIF.preview = ''; NOTIF.lastTpl = 'title_tpl';
  ntPreview();
  setTimeout(() => $('[name="name"]', d)?.focus(), 60);
}

function ntChecked(f) { return $$('input[name="events"]:checked', f).map(i => i.value); }

function ntPreview() {
  const f = $('#nt-form');
  if (!f) return;
  const sel = ntChecked(f), keys = sel.length ? sel : ['teste'];
  if (!keys.includes(NOTIF.preview)) NOTIF.preview = keys[0];
  $('#nt-pv-seg').innerHTML = keys.length > 1 ? keys.map(k => `<button type="button" data-nt-pv="${esc(k)}" aria-pressed="${k === NOTIF.preview}">${esc(ntEventLabel(k))}</button>`).join('') : `<span class="faint small">${esc(ntEventLabel(keys[0]))}</span>`;
  const ev = ntEvent(NOTIF.preview), kind = f.elements.kind.value;
  const m = ntMessage(ev, f.elements.title_tpl.value, f.elements.text_tpl.value);
  const app = (NT_KINDS[kind] || NT_KINDS.webhook).label;
  $('#nt-preview').innerHTML = kind === 'webhook'
    ? `<pre class="nt-code">${esc(JSON.stringify({ evento: ev.key, dados: ev.sample, titulo: m.title, texto: m.text, enviado_em: new Date().toISOString().slice(0, 19) + 'Z' }, null, 2))}</pre>`
    : `<div class="nt-notif"><div class="nt-notif-top"><span class="nt-app">${icon('bell')}${esc(app)}</span><span class="faint">agora</span></div>
        <b class="nt-notif-title">${esc(m.title)}</b><p class="nt-notif-text">${esc(m.text)}</p></div>`;
}

function ntBody(f) {
  const el = n => f.elements[n];
  return {
    name: el('name').value.trim().replace(/\s+/g, ' '), kind: el('kind').value, url: el('url').value.trim(),
    events: ntChecked(f), active: el('active').checked, title_tpl: el('title_tpl').value, text_tpl: el('text_tpl').value,
  };
}

async function ntSave(f) {
  const x = NOTIF.edit, body = ntBody(f), errs = [];
  if (!body.name) errs.push(['name', 'Dê um nome ao destino.']);
  if (!x && !body.url) errs.push(['url', 'Cole a URL do webhook.']);
  if (body.url && !/^https:\/\//i.test(body.url)) errs.push(['url', 'A URL precisa começar com https://.']);
  $$('[aria-invalid]', f).forEach(i => i.removeAttribute('aria-invalid'));
  errs.forEach(([n]) => f.elements[n]?.setAttribute('aria-invalid', 'true'));
  $('#nt-err').textContent = errs.map(e => e[1]).join(' ');
  if (errs.length) { f.elements[errs[0][0]]?.focus(); return; }
  const btn = $('#nt-save');
  btn.classList.add('loading');
  try {
    const saved = x ? await api('PUT', '/notifications/' + x.id, body) : await api('POST', '/notifications', body);
    closeDrawer();
    toast(x ? `Destino ${saved.name} salvo.` : `Destino ${saved.name} criado. Use “Enviar teste”.`, 'ok', 'check');
    rerender();
  } catch (e) {
    if (e.message === 'dados_invalidos' && e.data?.detail) {
      if (e.data.detail.startsWith('url')) f.elements.url.setAttribute('aria-invalid', 'true');
      $('#nt-err').textContent = 'Confira: ' + e.data.detail + '.';
      return;
    }
    $('#nt-err').textContent = errMsg(e);
  } finally { btn.classList.remove('loading'); }
}

async function ntLoadHistory(id) {
  NOTIF.open[id] = 'loading'; rerender();
  try { NOTIF.open[id] = (await api('GET', `/notifications/${id}/deliveries`)).deliveries; }
  catch (e) { delete NOTIF.open[id]; toast(errMsg(e), 'err', 'alert'); }
  rerender();
}

document.addEventListener('click', async e => {
  const t = e.target.closest('button');
  if (!t || !state.me) return;
  const ds = t.dataset;
  if (ds.ntNew !== undefined) { ntOpen(null); return; }
  if (ds.ntEdit) { ntOpen(Number(ds.ntEdit)); return; }
  if (ds.ntPv) { NOTIF.preview = ds.ntPv; ntPreview(); return; }
  if (ds.ntVar) {
    const f = $('#nt-form'), input = f?.elements[NOTIF.lastTpl];
    if (!input) return;
    const s = input.selectionStart ?? input.value.length, en = input.selectionEnd ?? s, ins = '{' + ds.ntVar + '}';
    input.value = input.value.slice(0, s) + ins + input.value.slice(en);
    input.focus(); input.setSelectionRange(s + ins.length, s + ins.length);
    ntPreview();
    return;
  }
  if (ds.ntTest) {
    const id = Number(ds.ntTest);
    const r = await act(t, () => api('POST', `/notifications/${id}/test`));
    if (r) { NOTIF.results[id] = r; if (NOTIF.open[id]) await ntLoadHistory(id); else rerender(); }
    return;
  }
  if (ds.ntHist) {
    const id = Number(ds.ntHist);
    if (NOTIF.open[id]) { delete NOTIF.open[id]; rerender(); } else ntLoadHistory(id);
    return;
  }
  if (ds.ntCopy !== undefined) {
    try { await navigator.clipboard.writeText(ds.ntCopy); toast('Token copiado.', 'ok', 'copy'); }
    catch { toast('Não deu para copiar sozinho. Selecione o token e copie.', 'warn', 'alert'); }
    return;
  }
  if (ds.ntToken !== undefined) {
    if (NOTIF.d?.sale_token?.exists && t.dataset.armed !== '1') {
      t.dataset.armed = '1'; t.classList.add('solid'); t.textContent = 'O token atual para de valer. Gerar?';
      setTimeout(() => { if (t.isConnected) { t.dataset.armed = ''; t.classList.remove('solid'); t.innerHTML = icon('lock') + 'Gerar token novo'; } }, 5000);
      return;
    }
    const r = await act(t, () => api('POST', '/notifications/sale-token'));
    if (r) { NOTIF.token = r.token; toast('Token gerado. Copie agora: ele não aparece de novo.', 'warn', 'lock'); rerender(); }
    return;
  }
  if (ds.ntDel) {
    if (t.dataset.armed !== '1') {
      t.dataset.armed = '1'; t.classList.add('solid'); t.textContent = 'Apagar de vez?';
      setTimeout(() => { if (t.isConnected) { t.dataset.armed = ''; t.classList.remove('solid'); t.innerHTML = icon('trash') + 'Apagar'; } }, 4000);
      return;
    }
    const name = NOTIF.edit?.name || '';
    const ok = await act(t, () => api('DELETE', '/notifications/' + ds.ntDel));
    if (ok) { delete NOTIF.open[ds.ntDel]; delete NOTIF.results[ds.ntDel]; closeDrawer(); toast(`Destino ${name} apagado.`, 'warn', 'trash'); rerender(); }
  }
});
document.addEventListener('change', async e => {
  const t = e.target;
  if (t.dataset?.ntActive) {
    t.disabled = true;
    const r = await act(null, () => api('POST', `/notifications/${t.dataset.ntActive}/active`, { active: t.checked }));
    if (r) toast(r.active ? `${r.name} ligado.` : `${r.name} desligado: não recebe eventos.`, r.active ? 'ok' : 'warn', 'bell');
    rerender();
    return;
  }
  if (t.closest?.('#nt-form')) ntPreview();
});
document.addEventListener('input', e => {
  const f = e.target.closest?.('#nt-form');
  if (!f) return;
  if (e.target.getAttribute('aria-invalid')) e.target.removeAttribute('aria-invalid');
  if (e.target.name === 'kind') f.elements.url.placeholder = (NT_KINDS[e.target.value] || NT_KINDS.webhook).ph;
  ntPreview();
});
document.addEventListener('focusin', e => {
  if (e.target.matches?.('#nt-form [data-nt-tpl]')) NOTIF.lastTpl = e.target.name;
});
document.addEventListener('submit', e => {
  if (e.target.id !== 'nt-form') return;
  e.preventDefault();
  ntSave(e.target);
});
// Sair da aba esquece o token mostrado e os resultados de teste.
window.addEventListener('hashchange', () => { if (!location.hash.startsWith('#/notificacoes')) { NOTIF.token = null; NOTIF.results = {}; NOTIF.open = {}; } });
