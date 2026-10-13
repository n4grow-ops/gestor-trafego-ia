/* Aba Testadores: pedidos dos clientes para entrar como testador do app da Meta (enquanto o app
   está em desenvolvimento, só testador consegue conectar a conta). A equipe adiciona na Meta,
   aperta "Adicionei" e a Mia avisa o cliente. Depende das utilidades do admin.js. */
'use strict';

const TESTERS = { filter: '', appId: '' };
const TESTER_STATUS = { requested: ['Pedido', 'warn'], added: ['Adicionado', 'info'], accepted: ['Aceito', 'ok'], canceled: ['Cancelado', ''] };
const metaRolesURL = id => id ? `https://developers.facebook.com/apps/${encodeURIComponent(id)}/roles/roles/` : '';

function setTesterBadge(counts) {
  const n = (counts && counts.requested) || 0, b = $('#nav-testers');
  if (!b) return;
  b.hidden = !n; b.textContent = n;
  b.title = n === 1 ? '1 pedido esperando' : n + ' pedidos esperando';
}
async function refreshTesterBadge() {
  try { setTesterBadge((await api('GET', '/testers')).counts); } catch { /* o próximo ciclo tenta de novo */ }
}

function testerTimes(t) {
  const out = [`<li>${icon('clock')}<span>Pedido <time datetime="${esc(t.requested_at)}" title="${esc(dt(t.requested_at))}">há ${esc(ago(t.requested_at))}</time></span></li>`];
  if (t.added_at) out.push(`<li>${icon('user-plus')}<span>Adicionado em ${esc(dt(t.added_at))}${t.added_by ? ' por ' + esc(t.added_by.split('@')[0]) : ''}</span></li>`);
  if (t.reminded_at) out.push(`<li>${icon('mega')}<span>Lembrete em ${esc(dt(t.reminded_at))}</span></li>`);
  if (t.accepted_at) out.push(`<li>${icon('check')}<span>Conectou em ${esc(dt(t.accepted_at))}</span></li>`);
  return `<ul class="t-times">${out.join('')}</ul>`;
}

function testerRow(t) {
  const [label, cls] = TESTER_STATUS[t.status] || [t.status, ''];
  const who = t.member_name || t.org_name || 'Cliente';
  const panel = t.ref_value === 'painel' && !t.profile_url;
  const ref = panel ? 'marcado na conversa' : t.ref_value || '—', refKind = panel ? 'Origem' : t.ref_kind === 'username' ? 'Usuário' : 'ID';
  const actions = t.status === 'requested'
    ? `<button class="btn sm primary" type="button" data-tester-added="${t.id}">${icon('check')}Adicionei</button>`
    : t.status === 'added' ? `<button class="btn sm" type="button" data-tester-resend="${t.id}">${icon('send')}Reenviar aviso</button>` : '';
  return `<article class="t-row ${esc(t.status)}" role="listitem">
    <div class="who"><span class="mini-av ${t.status === 'requested' ? 'hot' : ''}">${esc(initials(who))}</span><span>
      <button class="t-name" type="button" data-open-client="${t.org_id}" title="Abrir a ficha do cliente"><b>${esc(who)}</b></button>
      <small>${esc([t.org_name !== who ? t.org_name : '', phone(t.phone)].filter(Boolean).join(' · '))}</small></span></div>
    <div class="t-ref"><span class="faint small">${refKind}</span>
      <span class="t-ref-v"><code class="num" title="${esc(ref)}">${esc(ref)}</code>
        ${t.ref_value && !panel ? `<button class="icon-btn t-copy" type="button" data-copy="${esc(t.ref_value)}" aria-label="Copiar ${refKind === 'ID' ? 'ID' : 'usuário'}" title="Copiar">${icon('copy')}</button>` : ''}</span>
      ${t.profile_url ? `<a class="t-link small" href="${esc(t.profile_url)}" target="_blank" rel="noopener noreferrer">${icon('ext')}Ver perfil</a>` : ''}</div>
    <div class="t-state"><span class="chip ${cls}">${esc(label)}</span>${testerTimes(t)}</div>
    <div class="t-act">${actions}</div>
  </article>`;
}

views.testadores = async view => {
  const d = await api('GET', '/testers?' + new URLSearchParams({ status: TESTERS.filter }));
  TESTERS.appId = d.meta_app_id || state.me.meta_app_id || '';
  setTesterBadge(d.counts);
  const c = d.counts || {}, open = (c.requested || 0) + (c.added || 0);
  const filters = [['', 'Abertos', open], ['requested', 'Pedido', c.requested], ['added', 'Adicionado', c.added], ['accepted', 'Aceito', null]];
  const metaURL = metaRolesURL(TESTERS.appId);
  const metaBtn = metaURL
    ? `<a class="btn" href="${esc(metaURL)}" target="_blank" rel="noopener noreferrer">${icon('meta')}Abrir no app da Meta</a>`
    : `<span class="faint small">Sem META_APP_ID na api: o link do app da Meta fica desligado.</span>`;
  const empty = TESTERS.filter === '' ? ['Nenhum pedido aberto', 'Quando um cliente mandar o link do perfil do Facebook, o pedido aparece aqui.']
    : TESTERS.filter === 'accepted' ? ['Ninguém conectou ainda', 'Quem aceitar o convite e conectar a conta aparece aqui.']
    : ['Nada com esse status', 'Escolha outro filtro.'];
  view.innerHTML = `<div class="page">
    <div class="page-head"><div><h1>Testadores</h1><p>${num(c.requested)} esperando a equipe · ${num(c.added)} esperando o cliente aceitar</p></div>
      <div class="actions">${metaBtn}</div></div>
    <div class="alert note">${icon('alert', 'ico')}<p>Enquanto o app da Meta está em desenvolvimento, só testador conecta a conta. Adicione o ID ou usuário em <b>Funções do app › Testadores</b> e aperte <b>Adicionei</b>: a Mia avisa o cliente para aceitar o convite e conectar.</p></div>
    <div class="toolbar"><div class="seg" role="group" aria-label="Filtro">${filters.map(([k, l, n]) => `<button type="button" data-tester-filter="${k}" aria-pressed="${TESTERS.filter === k}">${l}${n != null ? `<span class="n">${num(n)}</span>` : ''}</button>`).join('')}</div></div>
    <div class="list t-list" role="list">
      <div class="tq-head" aria-hidden="true"><span>Cliente</span><span>ID ou usuário no Facebook</span><span>Situação</span><span></span></div>
      ${d.testers.map(testerRow).join('') || `<div class="empty"><b>${empty[0]}</b>${empty[1]}</div>`}
    </div></div>`;
};

async function copyText(btn, text) {
  try { await navigator.clipboard.writeText(text); }
  catch {
    // Sem permissão de área de transferência: seleciona o texto para copiar na mão.
    const code = btn.parentElement.querySelector('code');
    if (code) { const r = document.createRange(); r.selectNodeContents(code); const s = getSelection(); s.removeAllRanges(); s.addRange(r); }
    toast('Não deu para copiar sozinho. O texto ficou selecionado.', 'warn', 'alert');
    return;
  }
  toast('Copiado: ' + text, 'ok', 'copy');
}

document.addEventListener('click', async e => {
  const t = e.target.closest('button');
  if (!t || !state.me) return;
  const ds = t.dataset;
  if (ds.testerFilter !== undefined) { TESTERS.filter = ds.testerFilter; rerender(); return; }
  if (ds.copy !== undefined && t.closest('.t-row')) { copyText(t, ds.copy); return; }
  if (ds.testerAdded) {
    const ok = await act(t, () => api('POST', '/testers/' + ds.testerAdded + '/added'));
    if (ok) {
      if (ok.notice === 'na_fila') toast('Marcado como adicionado. A Mia vai avisar o cliente no WhatsApp.', 'ok', 'check');
      else toast('Marcado como adicionado, mas o aviso ao cliente não entrou na fila. Use “Reenviar aviso”.', 'warn', 'alert');
    }
    rerender();
    return;
  }
  if (ds.testerResend) {
    const ok = await act(t, () => api('POST', '/testers/' + ds.testerResend + '/resend'));
    if (ok) toast('Aviso reenviado: a Mia manda de novo o passo a passo para o cliente.', 'ok', 'send');
  }
});
