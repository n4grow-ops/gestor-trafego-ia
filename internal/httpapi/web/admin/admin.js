/* Painel da equipe · meu anúncio.io
   Tudo vem da API (/admin/api ou /api no host do painel). Sem estilo inline: larguras e alturas
   dinâmicas entram por data-w/data-h e são aplicadas via CSSOM (permitido pela CSP). */
'use strict';

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const BASE = location.pathname.startsWith('/admin') ? '/admin' : '';
const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)');

const state = {
  me: null, mode: '', route: '', timers: [], health: null,
  clients: { q: '', filter: '', stage: '' }, clientList: [],
  conv: { filter: '', q: '', sel: null, lastId: 0, thread: null, items: [] },
  days: 7, month: '', feedSeen: new Set(), drawer: null,
};

/* ===================== utilidades ===================== */
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const icon = (id, cls = 'ico-sm') => `<svg class="${cls}" aria-hidden="true"><use href="#i-${id}"/></svg>`;
const initials = n => String(n || '?').trim().split(/\s+/).map(p => p[0]).slice(0, 2).join('') || '?';
const BRL = new Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' });
const brl = cents => BRL.format((cents || 0) / 100);
const brlShort = cents => { const v = (cents || 0) / 100; return v >= 10000 ? 'R$ ' + (v / 1000).toLocaleString('pt-BR', { maximumFractionDigits: 1 }) + ' mil' : BRL.format(v); };
const num = n => (n || 0).toLocaleString('pt-BR');
const pct = f => (f * 100).toLocaleString('pt-BR', { maximumFractionDigits: 1 }) + '%';
const usd = v => 'US$ ' + (v || 0).toLocaleString('pt-BR', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
function phone(p) {
  const d = String(p || '').replace(/\D/g, '');
  if (d.length === 13 && d.startsWith('55')) return `+55 ${d.slice(2, 4)} ${d.slice(4, 9)}-${d.slice(9)}`;
  if (d.length === 12 && d.startsWith('55')) return `+55 ${d.slice(2, 4)} ${d.slice(4, 8)}-${d.slice(8)}`;
  return d ? '+' + d : '';
}
function ago(iso) {
  if (!iso) return '';
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return 'agora';
  if (s < 3600) return Math.round(s / 60) + ' min';
  if (s < 86400) return Math.round(s / 3600) + ' h';
  return Math.round(s / 86400) + ' d';
}
const dt = iso => iso ? new Date(iso).toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }) : '';
const dateOnly = iso => iso ? new Date(iso).toLocaleDateString('pt-BR') : '';
const hm = iso => new Date(iso).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' });
function dayLabel(iso) {
  const d = new Date(iso), t = new Date();
  const y = new Date(); y.setDate(t.getDate() - 1);
  if (d.toDateString() === t.toDateString()) return 'Hoje';
  if (d.toDateString() === y.toDateString()) return 'Ontem';
  return d.toLocaleDateString('pt-BR', { weekday: 'short', day: '2-digit', month: 'short' });
}
// Larguras e alturas calculadas: CSSOM, nunca atributo style (CSP).
function applyDyn(root = document) {
  $$('[data-w]', root).forEach(el => { el.style.width = el.dataset.w; });
  $$('[data-h]', root).forEach(el => { el.style.height = el.dataset.h; });
  $$('[data-delay]', root).forEach(el => { el.style.animationDelay = el.dataset.delay + 'ms'; });
}
function toast(msg, kind = 'ok', ico = 'check') {
  const t = document.createElement('div');
  t.className = 'toast';
  t.innerHTML = `<span class="ev-ico ${kind}">${icon(ico)}</span><p>${esc(msg)}</p>`;
  $('#toasts').append(t);
  setTimeout(() => { t.classList.add('out'); setTimeout(() => t.remove(), 200); }, 3800);
}
// Timer da tela atual: morre sozinho quando outra tela é aberta.
function every(ms, fn) {
  const seq = renderSeq;
  const id = setInterval(() => { if (seq !== renderSeq) { clearInterval(id); return; } if (!document.hidden) fn(); }, ms);
  state.timers.push(id);
}
function clearTimers() { state.timers.forEach(clearInterval); state.timers = []; }
function debounce(fn, ms) { let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; }

/* ===================== API ===================== */
class ApiError extends Error { constructor(status, data) { super((data && data.error) || 'erro_' + status); this.status = status; this.data = data || {}; } }
const ERRORS = {
  ia_atendendo: 'A IA está atendendo. Assuma a conversa antes de escrever.',
  outra_pessoa: 'Outra pessoa assumiu esta conversa. Assuma você antes de escrever.',
  somente_admin: 'Só administradores podem fazer isso.', csrf: 'Sessão desatualizada. Recarregue a página.',
  sessao_invalida: 'Sua sessão expirou. Entre de novo.', nao_encontrado: 'Não encontrado (pode ter sido apagado).',
  cadastro_concluido: 'Esse cliente já concluiu o cadastro.', cadastro_desligado: 'O cadastro pela página está desligado.',
  sem_assinatura: 'Esse cliente não tem assinatura para receber pagamento.', ultimo_admin: 'A equipe precisa de ao menos um administrador.',
  cliente_real: 'Cliente real (com pagamento ou plano pago) não pode ser zerado. Só contas de teste.',
  voce_mesmo: 'Você não pode tirar a si mesmo da equipe.', muitas_tentativas: 'Muitas tentativas. Espere alguns minutos e peça um código novo.',
  texto_invalido: 'Escreva uma mensagem (até 4.000 caracteres).', dados_invalidos: 'Confira os dados do formulário.',
  sem_telefone: 'Esse cliente não tem telefone cadastrado.', erro_interno: 'Erro no servidor. Tente de novo.',
};
const errMsg = e => ERRORS[e.message] || (e.status ? 'Não deu certo (' + e.status + ').' : 'Sem conexão com o servidor.');
async function api(method, path, body) {
  const opt = { method, headers: {}, credentials: 'same-origin' };
  if (body !== undefined) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  if (method !== 'GET' && state.me) opt.headers['X-CSRF-Token'] = state.me.csrf;
  let res;
  try { res = await fetch(BASE + '/api' + path, opt); } catch { throw new ApiError(0, { error: 'rede' }); }
  let data = null;
  try { data = await res.json(); } catch { /* sem corpo */ }
  if (res.status === 401 && state.me && !path.startsWith('/login')) { showLogin('Sua sessão expirou. Entre de novo.'); }
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}
async function act(btn, fn) {
  btn?.classList.add('loading');
  try { return await fn(); } catch (e) { toast(errMsg(e), 'err', 'alert'); return null; } finally { btn?.classList.remove('loading'); }
}

/* ===================== textos ===================== */
const ACTIONS = {
  auto_signup: ['Primeiro contato no WhatsApp', 'info', 'users'], signup_link_sent: ['Link de cadastro enviado', 'info', 'link'],
  signup_reminder_sent: ['Lembrete de cadastro enviado', 'info', 'clock'], registration_completed: ['Cadastro concluído', 'ok', 'check'],
  project_started: ['Projeto iniciado', 'info', 'mega'], project_resumed: ['Projeto retomado', 'info', 'mega'],
  proposal_created: ['Proposta gerada pelo Gestor', 'info', 'mega'], proposal_invalid: ['Proposta inválida do Gestor', 'warn', 'alert'],
  approval_granted: ['Proposta aprovada', 'ok', 'check'], approval_refused: ['Código de aprovação recusado', 'warn', 'alert'],
  creative_received: ['Anúncio recebido', 'ok', 'check'], creative_rejected: ['Anúncio recusado pela qualidade', 'warn', 'alert'],
  creative_deleted: ['Anúncio apagado', 'info', 'trash'], meta_link_sent: ['Link de conexão Meta enviado', 'info', 'link'],
  meta_connected: ['Conta Meta conectada', 'ok', 'meta'], escalated_human: ['Pediu atendimento humano', 'warn', 'hand'],
  daily_limit_reached: ['Chegou no limite diário de mensagens', 'warn', 'clock'], media_failed: ['Falha ao processar mídia', 'err', 'alert'],
  llm_failed: ['Falha na IA', 'err', 'alert'],
  login: ['Entrou no painel', 'info', 'out'], logout: ['Saiu do painel', 'info', 'out'],
  client_block: ['Bloqueou o cliente', 'warn', 'lock'], client_unblock: ['Desbloqueou o cliente', 'ok', 'unlock'],
  client_signup_link: ['Reenviou o link de cadastro', 'info', 'link'], client_reset: ['Zerou o contato', 'err', 'trash'],
  conversation_takeover: ['Assumiu a conversa', 'warn', 'hand'], conversation_release: ['Devolveu a conversa à IA', 'ok', 'bot'],
  conversation_send: ['Respondeu pelo painel', 'info', 'send'], payment_record: ['Registrou pagamento', 'ok', 'money'],
  team_add: ['Adicionou à equipe', 'info', 'user-plus'], team_remove: ['Removeu da equipe', 'warn', 'trash'],
};
const actionOf = a => ACTIONS[a] || [String(a).replace(/_/g, ' '), 'info', 'log'];
const PROJECT_STAGE = { collecting: 'Coleta', awaiting_proposal: 'Esperando proposta', awaiting_approval: 'Esperando aprovação', approved: 'Aprovada', awaiting_creatives: 'Esperando anúncios', ready: 'Pronta para publicar', archived: 'Arquivado' };
const CONV_STATE = { idle: 'Livre', onboarding: 'Conhecendo o negócio', collecting: 'Coletando dados', awaiting_proposal: 'Esperando proposta', awaiting_approval: 'Esperando aprovação', awaiting_creatives: 'Esperando anúncios', needs_human: 'Atendimento humano' };
const PROFILE = [['business_name', 'Nome do negócio'], ['offer', 'O que faz'], ['audience', 'Público'], ['audience_age', 'Idade'], ['audience_gender', 'Gênero'], ['service_area', 'Região atendida'], ['differentials', 'Diferenciais'], ['average_ticket', 'Ticket médio'], ['sales_channel', 'Onde vende'], ['city', 'Cidade']];
const PLAN_STATUS = { active: ['ativa', 'ok'], trialing: ['teste', 'info'], past_due: ['em atraso', 'warn'], canceled: ['cancelada', ''] };
const PAY_STATUS = { paid: ['pago', 'ok'], pending: ['pendente', 'warn'], failed: ['falhou', 'err'], refunded: ['estornado', ''] };
const TABLES = { payments: 'pagamentos', subscriptions: 'assinatura', consents: 'aceites', creatives: 'anúncios', approval_codes: 'códigos de aprovação',
  campaign_drafts: 'propostas', outbox_jobs: 'envios na fila', messages: 'mensagens', conversations: 'conversa', projects: 'projetos',
  meta_connections: 'conexão Meta', meta_connect_links: 'links de conexão Meta', signup_links: 'links de cadastro', whatsapp_identities: 'telefones',
  members: 'pessoas', llm_usage: 'registros de uso de IA', audit_events: 'eventos', organizations: 'cliente', inbox_events: 'webhooks recebidos' };
const METHOD = { pix: 'Pix', boleto: 'Boleto', card: 'Cartão' };

/* ===================== login ===================== */
function showLogin(msg = '', isErr = true) {
  clearTimers();
  state.me = null;
  document.body.classList.remove('booting', 'is-admin');
  $('#app').hidden = true;
  $('#login').hidden = false;
  closeDrawer();
  const dev = state.mode === 'dev', off = !state.mode;
  $('#dev-banner').hidden = !dev;
  $('#login-sub').textContent = off ? 'O login do painel não está configurado neste servidor.' : dev ? 'Entre com o e-mail de alguém da equipe.' : 'Entre com o seu e-mail da equipe. Mandamos um código para confirmar.';
  $('#btn-email').textContent = dev ? 'Entrar' : 'Enviar código';
  $('#form-email').hidden = off;
  $('#form-code').hidden = true;
  setLoginMsg(msg, isErr && !!msg);
  setTimeout(() => $('#login-email').focus(), 50);
}
function setLoginMsg(t, err = false) { const m = $('#login-msg'); m.textContent = t; m.classList.toggle('err', err); }
$('#form-email').addEventListener('submit', async e => {
  e.preventDefault();
  const email = $('#login-email').value.trim();
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) { setLoginMsg('Digite um e-mail válido.', true); return; }
  const btn = $('#btn-email');
  btn.classList.add('loading');
  try {
    if (state.mode === 'dev') { state.me = await api('POST', '/login/dev', { email }); enterApp(); return; }
    await api('POST', '/login/start', { email });
    $('#form-email').hidden = true; $('#form-code').hidden = false;
    setLoginMsg('Se ' + email + ' for da equipe, o código chega em instantes. Confira também o spam.');
    $('#login-code').focus();
  } catch (err) {
    setLoginMsg(err.message === 'fora_da_equipe' ? 'Esse e-mail não está na equipe.' : errMsg(err), true);
  } finally { btn.classList.remove('loading'); }
});
$('#form-code').addEventListener('submit', async e => {
  e.preventDefault();
  const code = $('#login-code').value.replace(/\D/g, '');
  if (code.length < 6) { setLoginMsg('O código tem 6 dígitos.', true); return; }
  const btn = $('#btn-code');
  btn.classList.add('loading');
  try { state.me = await api('POST', '/login/verify', { email: $('#login-email').value.trim(), code }); enterApp(); }
  catch (err) { setLoginMsg(err.message === 'codigo_invalido' ? 'Código inválido ou expirado. Confira e tente de novo.' : errMsg(err), true); }
  finally { btn.classList.remove('loading'); }
});
$('#login-code').addEventListener('input', e => { e.target.value = e.target.value.replace(/\D/g, '').slice(0, 10); if (e.target.value.length === 6) $('#form-code').requestSubmit(); });
$('#btn-change').addEventListener('click', () => { $('#form-code').hidden = true; $('#form-email').hidden = false; $('#login-code').value = ''; setLoginMsg(''); $('#login-email').focus(); });

function enterApp() {
  const me = state.me;
  document.body.classList.remove('booting');
  document.body.classList.toggle('is-admin', me.role === 'admin');
  $('#login').hidden = true;
  $('#app').hidden = false;
  $('#me-avatar').textContent = initials(me.name);
  $('#me-name').textContent = me.name;
  $('#me-role').textContent = (me.role === 'admin' ? 'Administrador' : 'Equipe') + (me.mode === 'dev' ? ' · dev' : '');
  render();
  refreshPulse();
  if (!state.pulseTimer) state.pulseTimer = setInterval(() => { if (state.me && !document.hidden) refreshPulse(); }, 30000);
}
$('#logout').addEventListener('click', async () => { try { await api('POST', '/logout'); } catch { /* sai mesmo assim */ } showLogin('Você saiu do painel.', false); });

/* ===================== status do topo ===================== */
function setPulse(health, inbox) {
  if (health) {
    state.health = health;
    const wa = (health.whatsapp || []).find(w => w.Provider === health.provider) || (health.whatsapp || [])[0];
    $('#dot-wa').className = 'dot ' + (!wa ? 'off' : wa.Connected ? '' : 'err');
    $('#pill-wa').title = !wa ? 'Sem aviso de conexão do provedor ainda' : wa.Connected ? 'Conectado' : 'Desconectado';
    const w = health.worker || {};
    const late = w.oldest_pending_at && (Date.now() - new Date(w.oldest_pending_at)) > 2 * 60000;
    $('#dot-worker').className = 'dot ' + (!health.db_ok ? 'err' : late ? 'err' : w.failed_last_24h ? 'warn' : '');
    $('#pill-worker').title = late ? 'Fila parada há ' + ago(w.oldest_pending_at) : (w.outbox_pending || 0) + ' na fila';
  }
  if (inbox) {
    const n = inbox.needs_human || 0, un = inbox.unassigned || 0;
    $('#nav-human').hidden = !n; $('#nav-human').textContent = n;
    $('#pill-human').hidden = !un; $('#pill-human-n').textContent = un;
  }
}
async function refreshPulse() {
  try { const d = await api('GET', '/overview?days=1'); setPulse(d.health, d.overview.inbox); } catch { /* o próximo ciclo tenta de novo */ }
}

/* ===================== gráficos ===================== */
function spark(vals) {
  const w = 120, h = 32, max = Math.max(...vals, 1), min = Math.min(...vals, 0);
  if (vals.length < 2) return '';
  const pts = vals.map((v, i) => [i / (vals.length - 1) * w, h - 3 - ((v - min) / (max - min || 1)) * (h - 6)]);
  const d = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' ');
  return `<svg class="spark" viewBox="0 0 ${w} ${h}" preserveAspectRatio="none" aria-hidden="true"><path class="area" d="${d} L ${w} ${h} L 0 ${h} Z"/><path class="line" d="${d}" vector-effect="non-scaling-stroke"/></svg>`;
}
// Gráfico de barras agrupadas (recebido x custo de IA), SVG próprio.
function barChart(series) {
  const W = 640, H = 220, padL = 8, padB = 26, padT = 18, n = series.length;
  const max = Math.max(1, ...series.flatMap(s => [s.received_cents, s.ai_cost_cents]));
  const slot = (W - padL * 2) / n, bw = Math.min(26, slot / 3.2);
  const y = v => padT + (H - padT - padB) * (1 - v / max);
  const months = ['jan', 'fev', 'mar', 'abr', 'mai', 'jun', 'jul', 'ago', 'set', 'out', 'nov', 'dez'];
  let g = '';
  for (let k = 0; k <= 3; k++) { const yy = padT + (H - padT - padB) * k / 3; g += `<line class="grid-line" x1="0" x2="${W}" y1="${yy}" y2="${yy}"/>`; }
  series.forEach((s, i) => {
    const cx = padL + slot * i + slot / 2;
    const r = y(s.received_cents), c = y(s.ai_cost_cents), base = H - padB;
    g += `<rect class="bar-rev" x="${cx - bw - 2}" y="${r}" width="${bw}" height="${Math.max(0, base - r)}" rx="4" data-delay="${i * 50}"><title>Recebido em ${s.month}: ${brl(s.received_cents)}</title></rect>`;
    g += `<rect class="bar-cost" x="${cx + 2}" y="${c}" width="${bw}" height="${Math.max(0, base - c)}" rx="4" data-delay="${i * 50 + 25}"><title>Custo de IA em ${s.month}: ${brl(s.ai_cost_cents)}</title></rect>`;
    if (s.received_cents) g += `<text class="val" x="${cx - bw / 2 - 2}" y="${r - 5}" text-anchor="middle">${esc(brlShort(s.received_cents).replace('R$ ', ''))}</text>`;
    g += `<text class="axis" x="${cx}" y="${H - 8}" text-anchor="middle">${months[+s.month.slice(5) - 1]}</text>`;
  });
  return `<svg class="chart" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label="Recebido e custo de IA nos últimos 6 meses">${g}</svg>`;
}

/* ===================== telas ===================== */
const views = {};
const ADMIN_ROUTES = new Set(['faturamento', 'equipe']);
const lockwall = what => `<div class="page"><div class="lockwall"><span class="big-ico">${icon('lock', 'ico')}</span><b>Só administradores</b><p class="muted">${esc(what)}</p></div></div>`;
const loadingPage = () => `<div class="loading-page"><span class="spinner" aria-label="Carregando"></span></div>`;
function errorPage(e, retry) {
  return `<div class="page"><div class="empty"><b>Não deu para carregar</b>${esc(errMsg(e))}<div class="actions"><button class="btn sm" data-retry="${retry}">${icon('refresh')}Tentar de novo</button></div></div></div>`;
}

/* ---------- visão geral ---------- */
views.visao = async view => {
  const d = await api('GET', '/overview?days=' + state.days);
  const ov = d.overview, h = d.health, s = ov.series;
  setPulse(h, ov.inbox);
  const wa = (h.whatsapp || []).find(w => w.Provider === h.provider) || (h.whatsapp || [])[0];
  const hour = new Date().getHours();
  const hello = (hour < 12 ? 'Bom dia' : hour < 18 ? 'Boa tarde' : 'Boa noite') + ', ' + state.me.name.split(' ')[0];
  const sliceN = arr => arr.slice(-Math.max(state.days, 7));
  const stats = [
    ['Cadastros concluídos', ov.signups, sliceN(s.signups), ''],
    ['Contas Meta conectadas', ov.meta_new, sliceN(s.meta), num(ov.meta_active) + ' ativas no total'],
    ['Propostas aprovadas', ov.approved, sliceN(s.approved), ''],
    ['Prontas para publicar', ov.ready, sliceN(s.ready), 'agora'],
    ['Mensagens trocadas', ov.messages, sliceN(s.messages), ''],
  ];
  const funnel = ov.funnel, top = Math.max(1, funnel[0]?.count || 0);
  let worst = null;
  // Só compara etapas encadeadas: "Meta conectada" pode vir antes dos anúncios e passar de 100%.
  funnel.forEach((f, i) => { if (i && funnel[i - 1].count && f.count <= funnel[i - 1].count) { const p = f.count / funnel[i - 1].count; if (!worst || p < worst.p) worst = { i, p }; } });
  const alerts = [];
  if (wa && !wa.Connected) alerts.push(`<div class="alert">${icon('alert', 'ico')}<p><b>O WhatsApp do bot está desconectado</b> desde ${esc(dt(wa.UpdatedAt))}${wa.Reason ? ' (' + esc(wa.Reason) + ')' : ''}. Clientes não recebem respostas.</p><a class="btn sm" href="#/whatsapp">Ver WhatsApp</a></div>`);
  if (ov.inbox.unassigned) alerts.push(`<div class="alert warn">${icon('hand', 'ico')}<p><b>${ov.inbox.unassigned} ${ov.inbox.unassigned > 1 ? 'clientes pediram' : 'cliente pediu'} atendimento humano</b> e ninguém assumiu ainda.</p><a class="btn sm warn" href="#/conversas" data-conv-filter="humano">Atender agora</a></div>`);
  if (!h.db_ok) alerts.push(`<div class="alert">${icon('db', 'ico')}<p><b>Banco sem resposta.</b> Os números abaixo podem estar desatualizados.</p></div>`);
  const w = h.worker || {};
  const late = w.oldest_pending_at && (Date.now() - new Date(w.oldest_pending_at)) > 2 * 60000;
  view.innerHTML = `<div class="page">
    ${alerts.join('')}
    <div class="page-head"><div><h1>${esc(hello)}</h1><p>${new Date().toLocaleDateString('pt-BR', { weekday: 'long', day: 'numeric', month: 'long' })} · ${num(funnel[0]?.count)} contatos na base</p></div>
      <div class="seg" role="group" aria-label="Período">${[[1, 'Hoje'], [7, '7 dias'], [30, '30 dias']].map(([n, l]) => `<button type="button" data-days="${n}" aria-pressed="${state.days === n}">${l}</button>`).join('')}</div></div>
    <section class="stats" aria-label="Números do período">
      ${stats.map(([l, v, ser, sub]) => `<div class="stat"><div class="stat-label">${l}</div><div class="stat-val"><b>${num(v)}</b>${sub ? `<span class="stat-sub">${esc(sub)}</span>` : ''}</div>${spark(ser)}</div>`).join('')}
    </section>
    <div class="grid-2">
      <section class="panel"><div class="panel-head"><h2>Funil</h2><span class="muted">quantos clientes já chegaram em cada etapa</span></div>
        <div class="panel-body"><div class="funnel">
          ${funnel.map((f, i) => { const prev = i ? funnel[i - 1].count : 0; const p = i && prev && f.count <= prev ? Math.round(f.count / prev * 100) : null; const drop = worst && worst.i === i && worst.p < 0.7;
            return `<div class="f-row ${drop ? 'drop' : ''}"><span class="f-name">${esc(f.label)}</span><span class="f-track"><span class="f-bar" data-w="${(f.count / top * 100).toFixed(1)}%" data-delay="${i * 50}"></span></span><span class="f-n">${num(f.count)}</span><span class="f-pct">${p === null ? '' : p + '%'}</span></div>`; }).join('')}
        </div>
        ${worst && worst.p < 0.7 ? `<p class="funnel-note">${icon('alert')}<span>A maior perda está entre <b>${esc(funnel[worst.i - 1].label)}</b> e <b>${esc(funnel[worst.i].label)}</b>: só ${Math.round(worst.p * 100)}% seguem.</span></p>` : ''}</div>
      </section>
      <section class="panel"><div class="panel-head"><h2>Ao vivo</h2><span class="live-tag"><span class="dot"></span>atualiza sozinho</span></div>
        <div class="panel-body"><div class="feed" id="feed">${feedHTML(ov.feed, true)}</div></div>
      </section>
    </div>
    <div class="grid-2b">
      <section class="panel"><div class="panel-head"><h2>Travados há mais de 24 h</h2><a class="btn sm ghost" href="#/clientes" data-goto-filter="travados">Ver todos (${num(ov.stuck_count)}) ${icon('chev')}</a></div>
        <div class="panel-body">${ov.stuck.length ? ov.stuck.map(c => `<div class="stuck-row"><span class="mini-av">${esc(initials(c.contact || c.name))}</span><div><b>${esc(c.name)}</b><small>parado em ${esc(c.stage_label)}${c.segment ? ' · ' + esc(c.segment) : ''}</small></div><span class="since">${ago(c.last_activity)}</span><button class="btn sm" type="button" data-open-client="${c.org_id}">Abrir</button></div>`).join('') : '<div class="empty"><b>Ninguém travado</b>Todos os clientes andaram nas últimas 24 h.</div>'}</div>
      </section>
      <section class="panel"><div class="panel-head"><h2>Saúde do sistema</h2><span class="muted">conferido agora</span></div>
        <div class="panel-body">
          <div class="h-item"><b><span class="dot ${!wa ? 'off' : wa.Connected ? '' : 'err'}"></span>WhatsApp</b><span class="h-state">${!wa ? 'Sem aviso do provedor ainda' : wa.Connected ? 'Conectado' : 'Desconectado'}<small>${esc(h.provider)}${wa ? ' · desde ' + esc(dt(wa.UpdatedAt)) : ''}${state.me.bot_number ? ' · ' + esc(phone(state.me.bot_number)) : ''}</small></span></div>
          <div class="h-item"><b><span class="dot ${h.db_ok ? '' : 'err'}"></span>Banco</b><span class="h-state">${h.db_ok ? 'Respondendo · ' + h.db_ms + ' ms' : 'Sem resposta'}</span></div>
          <div class="h-item"><b><span class="dot ${late ? 'err' : w.failed_last_24h ? 'warn' : ''}"></span>Worker</b><span class="h-state">${late ? 'Fila parada há ' + ago(w.oldest_pending_at) : (w.outbox_pending || 0) + ' envio(s) na fila · ' + (w.inbox_pending || 0) + ' mensagem(ns) a processar'}<small>${w.failed_last_24h || 0} falha(s) nas últimas 24 h</small>${w.last_error ? `<small>Último erro (${esc(w.last_error.kind)}, ${ago(w.last_error.at)}): <code>${esc(w.last_error.error)}</code></small>` : ''}</span></div>
          <div class="h-item"><b><span class="dot ${ov.inbox.unassigned ? 'warn' : ''}"></span>Atendimento</b><span class="h-state">${ov.inbox.needs_human} em atendimento humano · ${ov.inbox.unassigned} sem ninguém<small>${ov.inbox.waiting} conversa(s) com mensagem do cliente por último</small></span></div>
        </div>
      </section>
    </div>
  </div>`;
  applyDyn(view);
  ov.feed.forEach(e => state.feedSeen.add(feedKey(e)));
  every(15000, refreshOverview);
};
const feedKey = e => e.at + e.action + e.actor;
function feedHTML(feed, initial) {
  if (!feed.length) return '<div class="empty"><b>Nada ainda</b>Os eventos aparecem aqui assim que acontecem.</div>';
  return feed.map(e => {
    const [label, kind, ico] = actionOf(e.action);
    const isNew = !initial && !state.feedSeen.has(feedKey(e));
    const who = e.source === 'equipe' ? `<b>${esc(e.actor)}</b> · ` : '';
    return `<div class="ev ${isNew ? 'new' : ''}"><span class="ev-ico ${kind}">${icon(ico)}</span><p>${who}${esc(label)}${e.org ? ` · <b>${esc(e.org)}</b>` : ''}</p><time>${ago(e.at)}</time></div>`;
  }).join('');
}
async function refreshOverview() {
  try {
    const d = await api('GET', '/overview?days=' + state.days);
    setPulse(d.health, d.overview.inbox);
    const feed = $('#feed');
    if (feed) { feed.innerHTML = feedHTML(d.overview.feed, false); d.overview.feed.forEach(e => state.feedSeen.add(feedKey(e))); }
  } catch { /* tenta no próximo ciclo */ }
}

/* ---------- clientes ---------- */
function trackDots(c) {
  return `<span class="track-dots" aria-hidden="true">${[0, 1, 2, 3, 4, 5].map(i => `<i class="${i < c.stage ? 'on' : i === c.stage ? (c.stuck ? 'stuck' : 'cur') : ''}"></i>`).join('')}</span>`;
}
views.clientes = async view => {
  const f = state.clients;
  const qs = new URLSearchParams({ q: f.q, filter: f.filter, stage: f.stage });
  const d = await api('GET', '/clients?' + qs);
  state.clientList = d.clients;
  $('#nav-clients').hidden = !d.counts.all; $('#nav-clients').textContent = d.counts.all;
  const filters = [['', 'Todos', d.counts.all], ['travados', 'Travados', d.counts.stuck], ['humano', 'Pedindo humano', d.counts.human], ['pendentes', 'Cadastro pendente', d.counts.pending], ['conectados', 'Meta conectada', d.counts.connected], ['bloqueados', 'Bloqueados', d.counts.blocked]];
  const head = `<div class="page-head"><div><h1>Clientes</h1><p>${num(d.counts.all)} na base · ${num(d.counts.stuck)} travados há mais de 24 h</p></div>
      <div class="actions"><button class="btn" type="button" id="export-csv">${icon('down')}Exportar CSV</button></div></div>`;
  const toolbar = `<div class="toolbar">
      <div class="search-wrap">${icon('search')}<input class="field" id="client-q" type="search" placeholder="Nome, telefone, e-mail, cidade…" value="${esc(f.q)}" aria-label="Buscar clientes"></div>
      <div class="seg" role="group" aria-label="Filtro">${filters.map(([k, l, n]) => `<button type="button" data-filter="${k}" aria-pressed="${f.filter === k}">${l}<span class="n">${num(n)}</span></button>`).join('')}</div>
      <select class="field" id="client-stage" aria-label="Etapa"><option value="">Todas as etapas</option>${d.stages.map((s, i) => `<option value="${i}" ${String(i) === f.stage ? 'selected' : ''}>${esc(s)}</option>`).join('')}</select>
    </div>`;
  const rows = d.clients.map(c => `<button class="l-row" type="button" data-open-client="${c.org_id}">
      <span class="who"><span class="mini-av ${c.needs_human ? 'hot' : ''}">${esc(initials(c.contact || c.name))}</span><span><b>${esc(c.name)}</b><small>${esc([c.contact, c.segment, c.city].filter(Boolean).join(' · ') || phone(c.phone))}</small></span></span>
      <span>${c.plan ? `<span class="chip ${c.plan === 'pro' ? 'info' : ''}">${esc(c.plan_name)}</span>` : '<span class="faint small">sem plano</span>'}</span>
      <span class="track">${trackDots(c)}<small>${esc(c.stage_label)}${c.status === 'blocked' ? ' <span class="chip err">bloqueado</span>' : ''}${c.needs_human ? ` <span class="chip warn">${c.human_by ? 'com a equipe' : 'pediu humano'}</span>` : ''}</small></span>
      <span class="num ${c.stuck ? 'since' : 'muted small'}">${ago(c.last_activity)}</span>
      <span>${c.meta_connected ? `<span class="meta-ok">${icon('check')}Meta</span>` : '<span class="meta-no">sem Meta</span>'}</span>
      ${icon('chev', 'ico-sm chev')}
    </button>`).join('');
  view.innerHTML = `<div class="page">${head}${toolbar}
    <div class="list" role="list">
      <div class="l-head" aria-hidden="true"><span>Cliente</span><span>Plano</span><span>Etapa</span><span>Sem conversa há</span><span>Meta</span><span></span></div>
      ${rows || `<div class="empty"><b>Nenhum cliente com esse filtro</b>Limpe a busca ou escolha outra etapa.</div>`}
    </div>
    ${d.clients.length >= 500 ? '<p class="list-foot">Mostrando os 500 primeiros. Refine a busca para ver o resto.</p>' : ''}</div>`;
  const q = $('#client-q', view);
  q.addEventListener('input', debounce(() => { state.clients.q = q.value.trim(); rerender(); }, 280));
  $('#client-stage', view).addEventListener('change', e => { state.clients.stage = e.target.value; rerender(); });
  $('#export-csv', view).addEventListener('click', exportCSV);
};
function exportCSV() {
  const cols = [['Cliente', c => c.name], ['Contato', c => c.contact], ['Telefone', c => c.phone], ['E-mail', c => c.email], ['Segmento', c => c.segment],
    ['Cidade', c => c.city], ['UF', c => c.uf], ['Plano', c => c.plan_name], ['Etapa', c => c.stage_label], ['Meta', c => c.meta_connected ? 'sim' : 'não'],
    ['Situação', c => c.status === 'blocked' ? 'bloqueado' : 'ativo'], ['Última atividade', c => c.last_activity]];
  // Célula que começa com = + - @ vira fórmula no Excel/Sheets: o apóstrofo a deixa como texto.
  const cell = v => { let s = String(v ?? ''); if (/^[=+\-@]/.test(s)) s = "'" + s; return /[";\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s; };
  const csv = [cols.map(c => c[0]).join(';'), ...state.clientList.map(r => cols.map(c => cell(c[1](r))).join(';'))].join('\n');
  const a = document.createElement('a');
  a.href = URL.createObjectURL(new Blob(['﻿' + csv], { type: 'text/csv;charset=utf-8' }));
  a.download = 'clientes-meuanuncio-' + new Date().toISOString().slice(0, 10) + '.csv';
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
  toast(state.clientList.length + ' clientes exportados.', 'ok', 'down');
}

/* ---------- ficha do cliente (gaveta) ---------- */
async function openClient(id) {
  const d = $('#drawer');
  d.innerHTML = loadingPage();
  d.inert = false; d.classList.add('on'); d.setAttribute('aria-hidden', 'false');
  $('#drawer-backdrop').classList.add('on');
  try {
    state.drawer = await api('GET', '/clients/' + id);
  } catch (e) { d.innerHTML = `<div class="d-body"><div class="empty"><b>Não deu para abrir</b>${esc(errMsg(e))}</div><button class="btn sm" type="button" data-close-drawer>Fechar</button></div>`; return; }
  renderDrawer(0);
  $('[data-close-drawer]', d)?.focus();
}
function renderDrawer(tab) {
  const c = state.drawer, d = $('#drawer'), admin = state.me.role === 'admin';
  const stages = ['Contato', 'Cadastro', 'Coleta', 'Proposta', 'Aprovada', 'Pronta'];
  const tabs = ['Resumo', 'Negócio', 'Meta', 'Projetos', 'Linha do tempo'];
  d.innerHTML = `
    <div class="d-head">
      <div class="d-top"><span class="avatar">${esc(initials(c.contact || c.name))}</span><div><h2>${esc(c.name)}</h2><p>${esc([c.segment, [c.city, c.uf].filter(Boolean).join('/')].filter(Boolean).join(' · ') || 'Sem dados do negócio')} · cliente há ${ago(c.created_at)}</p>
        <div class="d-chips">${c.status === 'blocked' ? '<span class="chip err">bloqueado</span>' : ''}${c.registration === 'pending' ? '<span class="chip warn">cadastro pendente</span>' : ''}${c.needs_human ? `<span class="chip warn">${c.human_by ? 'com ' + esc(c.human_by.split('@')[0]) : 'pediu humano'}</span>` : ''}${c.meta_connected ? '<span class="chip ok">Meta conectada</span>' : ''}${c.plan ? `<span class="chip info">${esc(c.plan_name)}</span>` : ''}</div></div>
        <button class="icon-btn" type="button" data-close-drawer aria-label="Fechar">${icon('x')}</button></div>
      <div class="d-steps" aria-hidden="true">${stages.map((_, i) => `<i class="${i < c.stage ? 'on' : i === c.stage ? (c.stuck ? 'stuck' : 'cur') : ''}"></i>`).join('')}</div>
      <p class="d-stepname">Etapa: <b>${esc(c.stage_label)}</b>${c.stuck ? ` · <span class="warn-txt">sem conversa há ${ago(c.last_activity)}</span>` : ''}</p>
      <div class="tabs" role="tablist">${tabs.map((t, i) => `<button type="button" role="tab" aria-selected="${i === tab}" data-dtab="${i}">${t}</button>`).join('')}</div>
    </div>
    <div class="d-body" id="d-body">${drawerTab(tab)}</div>
    <div class="d-foot">
      ${c.registration === 'pending' && state.me.signup_enabled ? `<button class="btn sm" type="button" data-signup="${c.org_id}">${icon('link')}Reenviar cadastro</button>` : ''}
      ${c.conversation_id ? `<button class="btn sm" type="button" data-goto-conv="${c.conversation_id}">${icon('chat')}Abrir conversa</button>` : ''}
      ${c.status === 'blocked' ? `<button class="btn sm" type="button" data-block="unblock">${icon('unlock')}Desbloquear</button>` : `<button class="btn sm" type="button" data-block="block">${icon('lock')}Bloquear</button>`}
      ${admin ? `<button class="btn sm danger" type="button" data-reset-ask>${icon('trash')}Zerar contato</button>` : ''}
      <div id="confirm-slot" class="full"></div>
    </div>`;
}
function drawerTab(n) {
  const c = state.drawer;
  if (n === 0) {
    const owner = c.members.find(m => m.role === 'owner') || c.members[0] || {};
    const consents = c.consents.length ? c.consents.map(x => (x.kind === 'terms' ? 'Termos' : 'Privacidade') + ' v' + x.version + ' em ' + dateOnly(x.accepted_at)).join('<br>') : 'nenhum';
    const sub = c.subscription;
    return `<div><h3 class="sec-title">Contato</h3><dl class="kv">
      <dt>Responsável</dt><dd>${esc(owner.name || '—')}</dd>
      <dt>WhatsApp</dt><dd class="num">${esc(c.members.flatMap(m => m.phones).map(phone).join(', ') || '—')}</dd>
      <dt>E-mail</dt><dd>${esc(owner.email || '—')}${owner.email_verified ? ' <span class="chip ok">confirmado</span>' : ''}</dd>
      <dt>CNPJ</dt><dd class="num">${esc(c.cnpj ? c.cnpj.replace(/^(\d{2})(\d{3})(\d{3})(\d{4})(\d{2})$/, '$1.$2.$3/$4-$5') : 'não informado')}</dd>
      <dt>Razão social</dt><dd>${esc(c.legal_name || '—')}</dd>
      <dt>Site</dt><dd>${esc(c.website || '—')}</dd>
      <dt>Instagram</dt><dd>${esc(c.instagram || '—')}</dd>
      <dt>Aceites</dt><dd>${consents}</dd></dl>
      <h3 class="sec-title">Plano e limites</h3><dl class="kv">
      <dt>Assinatura</dt><dd>${sub ? `${esc(sub.plan_name)} · ${brl(sub.price_cents)}/mês <span class="chip ${PLAN_STATUS[sub.status]?.[1] || ''}">${PLAN_STATUS[sub.status]?.[0] || esc(sub.status)}</span>` : 'sem assinatura'}</dd>
      <dt>Teto por dia</dt><dd>${brl(c.max_daily_budget_brl * 100)} de anúncio · ${num(c.daily_message_limit)} mensagens</dd>
      <dt>Últimos 30 dias</dt><dd>${num(c.messages_30d)} mensagens · ${usd(c.cost_usd_30d)} de IA</dd>
      <dt>Origem</dt><dd>${c.signup_source === 'auto' ? 'Cadastro automático pelo WhatsApp' : 'Trazido pela equipe'}${c.referral_source ? ' · ' + esc(c.referral_source) : ''}</dd></dl>
      ${c.members.length > 1 ? `<h3 class="sec-title">Pessoas com acesso</h3><dl class="kv">${c.members.map(m => `<dt>${esc(m.role)}</dt><dd>${esc(m.name)} · ${esc(m.phones.map(phone).join(', '))}</dd>`).join('')}</dl>` : ''}</div>`;
  }
  if (n === 1) {
    let p = {};
    try { p = c.profile || {}; } catch { p = {}; }
    const filled = PROFILE.filter(([k]) => p[k]).length;
    return `<div><p class="muted small">Ficha que a IA preencheu na conversa · ${filled} de ${PROFILE.length} itens</p>
      <div class="biz">${PROFILE.map(([k, l]) => `<div class="${p[k] ? '' : 'miss'}"><small>${l}</small><span>${esc(p[k] || 'não informado')}</span></div>`).join('')}</div>
      <h3 class="sec-title">Do cadastro</h3><dl class="kv">
      <dt>Objetivos</dt><dd>${esc((c.ads_objective || []).join(', ') || '—')}</dd><dt>Verba por mês</dt><dd>${esc(c.monthly_budget || '—')}</dd>
      <dt>Experiência</dt><dd>${esc(c.ads_experience || '—')}</dd><dt>Conta de anúncios</dt><dd>${esc(c.has_meta_ad_account || '—')}</dd>
      <dt>Quem aprova</dt><dd>${esc(c.approver || '—')}</dd></dl></div>`;
  }
  if (n === 2) {
    const m = c.meta;
    if (!m) return `<div class="empty"><b>Conta Meta não conectada</b>${c.stage >= 4 ? 'A campanha já foi aprovada: o cliente precisa conectar a conta para publicar. Ele pede o link mandando “conectar meta” no WhatsApp.' : 'O cliente ainda não chegou nessa etapa.'}</div>`;
    return `<div><div class="metarow"><span class="ev-ico ok">${icon('meta')}</span><div><b>Conta de anúncios</b><small class="num">${esc(m.ad_account_name)} · ${esc(m.ad_account_id)} · ${esc(m.currency)} · ${esc(m.timezone)}</small></div><span class="chip ${m.account_status === 1 ? 'ok' : 'warn'}">${m.account_status === 1 ? 'ativa' : 'status ' + m.account_status}</span></div>
      <div class="metarow"><span class="ev-ico info">${icon('users')}</span><div><b>Página</b><small>${esc(m.page_name || '—')}${m.instagram_username ? ' · @' + esc(m.instagram_username) : ''}</small></div><span class="chip ${m.page_whatsapp ? 'ok' : 'warn'}">${m.page_whatsapp ? 'com WhatsApp' : 'sem WhatsApp'}</span></div>
      <div class="metarow"><span class="ev-ico info">${icon('lock')}</span><div><b>Autorização</b><small>conectada em ${esc(dt(m.connected_at))} · token guardado cifrado, nunca exibido</small></div></div></div>`;
  }
  if (n === 3) {
    if (!c.projects.length) return `<div class="empty"><b>Nenhum projeto ainda</b>A IA abre um projeto quando o cliente conta o que quer anunciar.</div>`;
    return `<div>${c.projects.map(p => { const f = p.fields || {}; const dr = p.draft;
      return `<div class="proj"><div class="proj-top"><b>${esc(p.name)}</b><span class="chip ${p.stage === 'ready' ? 'ok' : p.stage.startsWith('awaiting') ? 'warn' : 'info'}">${esc(PROJECT_STAGE[p.stage] || p.stage)}</span></div>
        <small>${esc([f.offer, f.audience, f.location].filter(Boolean).join(' · ') || 'dados em coleta')}${f.daily_budget_brl ? ' · ' + brl(f.daily_budget_brl * 100) + '/dia' : ''}</small>
        ${dr ? `<small>Proposta: ${brl(dr.daily_budget_brl * 100)}/dia · ${dr.duration_days} dias · ${esc(dr.objective.replace('OUTCOME_', '').toLowerCase())} · <b>${dr.status === 'approved' ? 'aprovada em ' + esc(dt(dr.approved_at)) : esc(dr.status)}</b></small>` : ''}
        <small>${p.creatives} anúncio(s) recebido(s) · atualizado ${ago(p.updated_at)} atrás</small></div>`; }).join('')}</div>`;
  }
  if (!c.timeline.length) return `<div class="empty"><b>Sem eventos</b>Nada registrado para este cliente.</div>`;
  return `<ol class="timeline">${c.timeline.map(e => { const [label, kind] = actionOf(e.action);
    return `<li class="${e.source === 'equipe' ? 'team' : kind === 'warn' || kind === 'err' ? 'warn' : ''}"><b>${esc(label)}</b><small>${esc(dt(e.at))} · ${e.source === 'equipe' ? esc(e.actor) + ' (equipe)' : 'sistema'}</small></li>`; }).join('')}</ol>`;
}
function closeDrawer() {
  const d = $('#drawer');
  if (d.contains(document.activeElement)) document.activeElement.blur();
  d.classList.remove('on'); d.setAttribute('aria-hidden', 'true'); d.inert = true;
  $('#drawer-backdrop').classList.remove('on');
  state.drawer = null;
}
async function askReset(btn) {
  const c = state.drawer;
  const prev = await act(btn, () => api('GET', '/clients/' + c.org_id + '/reset'));
  if (!prev) return;
  const items = prev.steps.filter(s => s.rows > 0).map(s => `<li>${esc(TABLES[s.table] || s.table)}: ${num(s.rows)}</li>`).join('');
  $('#confirm-slot').innerHTML = `<div class="confirm" role="alertdialog" aria-label="Confirmar zerar contato">
    <p><b>Zerar ${esc(c.name)}?</b> Apaga cadastro, conversa, projetos, assinatura e conexão Meta de ${esc(prev.phones.map(phone).join(', '))}. O número volta como contato novo. Não dá para desfazer.</p>
    <ul>${items}</ul>
    <div class="actions"><button class="btn sm ghost" type="button" data-reset-no>Cancelar</button><button class="btn sm danger solid" type="button" data-reset-yes>Zerar de vez</button></div></div>`;
  $('[data-reset-no]').focus();
}

/* ---------- conversas ---------- */
views.conversas = async view => {
  view.innerHTML = `<div class="chat-wrap ${state.conv.sel ? 'open' : ''}" id="chat-wrap">
    <div class="c-list">
      <div class="c-list-head"><h1>Conversas</h1>
        <div class="search-wrap">${icon('search')}<input class="field" id="conv-q" type="search" placeholder="Nome ou telefone" value="${esc(state.conv.q)}" aria-label="Buscar conversas"></div>
        <div class="seg" role="group" aria-label="Filtro" id="conv-filters"></div></div>
      <div class="c-items" id="c-items" role="list"></div>
    </div>
    <section class="thread" id="thread"></section>
    <aside class="c-side" id="c-side"></aside>
  </div>`;
  const q = $('#conv-q');
  q.addEventListener('input', debounce(() => { state.conv.q = q.value.trim(); loadInbox(); }, 280));
  await loadInbox();
  if (state.conv.sel) await openThread(state.conv.sel, true); else renderThreadEmpty();
  every(4000, async () => { await loadInbox(); if (state.conv.sel) await pollThread(); });
};
async function loadInbox() {
  const c = state.conv;
  const d = await api('GET', '/conversations?' + new URLSearchParams({ filter: c.filter, q: c.q }));
  c.items = d.items;
  setPulse(null, d.counts);
  const fl = $('#conv-filters'), list = $('#c-items');
  if (!fl || !list) return;
  fl.innerHTML = [['', 'Todas', null], ['humano', 'Humano', d.counts.needs_human], ['sem_resposta', 'Sem resposta', d.counts.waiting]]
    .map(([k, l, n]) => `<button type="button" data-conv-filter="${k}" aria-pressed="${c.filter === k}">${l}${n !== null ? `<span class="n">${num(n)}</span>` : ''}</button>`).join('');
  list.innerHTML = d.items.length ? d.items.map(it => `<button class="c-item" type="button" data-chat="${it.conversation_id}" aria-current="${it.conversation_id === c.sel}">
      <span class="mini-av ${it.needs_human ? 'hot' : ''}">${esc(initials(it.client_name || it.org_name))}</span>
      <span><b>${esc(it.org_name)}</b><p>${it.needs_human ? icon('hand') : ''}${it.last_direction === 'out' ? `<span class="faint">${it.last_kind === 'staff' ? 'Equipe' : 'IA'}:</span> ` : ''}${esc(it.last_body || 'sem mensagens')}</p></span>
      <span><time>${it.last_at ? ago(it.last_at) : ''}</time>${it.waiting ? `<span class="unread">${it.waiting}</span>` : ''}</span></button>`).join('')
    : '<div class="empty"><b>Nenhuma conversa</b>Nada com esse filtro.</div>';
}
function renderThreadEmpty() {
  const t = $('#thread'); if (!t) return;
  t.innerHTML = `<div class="c-empty"><div>${icon('chat', 'ico')}<p>Escolha uma conversa.<br><span class="faint small">As que pediram humano ficam no topo.</span></p></div></div>`;
  $('#c-side').innerHTML = '';
}
async function openThread(id, keep) {
  const c = state.conv;
  c.sel = id; c.lastId = 0;
  $$('.c-item').forEach(b => b.setAttribute('aria-current', String(+b.dataset.chat === id)));
  $('#chat-wrap')?.classList.add('open');
  if (!keep) $('#thread').innerHTML = loadingPage();
  try {
    const d = await api('GET', '/conversations/' + id);
    c.thread = d;
    renderThread(d);
  } catch (e) { $('#thread').innerHTML = `<div class="c-empty"><p>${esc(errMsg(e))}</p></div>`; }
}
function bubble(m, isNew) {
  const cls = m.direction === 'in' ? 'in' : 'out' + (m.kind === 'staff' ? ' staff' : '');
  const who = m.direction === 'out' ? `<span class="who-ai">${m.kind === 'staff' ? esc(m.author || 'Equipe') + ' · equipe' : 'Atendente · IA'}</span>` : '';
  const skipped = m.kind === 'skipped' ? ' skipped' : '';
  const kind = m.kind === 'audio' ? '🎤 ' : m.kind === 'image' ? '🖼️ ' : m.kind === 'video' ? '🎬 ' : '';
  return `<div class="b ${cls}${skipped}${isNew ? ' new' : ''}" data-id="${m.id}">${who}${kind}${esc(m.body)}<span class="t">${hm(m.created_at)}${m.kind === 'skipped' ? ' · sem resposta da IA' : ''}</span></div>`;
}
function pendingBubbles(p) {
  return p.map(x => `<div class="b out staff ${x.status === 'failed' ? 'failed' : 'pending'}"><span class="who-ai">${esc(x.author)} · equipe</span>${esc(x.text)}<span class="t">${x.status === 'failed' ? 'não entregue' : 'enviando…'}</span>${x.error ? `<span class="err-txt">${esc(x.error)}</span>` : ''}</div>`).join('');
}
function msgsHTML(messages) {
  let last = '', out = '';
  for (const m of messages) { const dl = dayLabel(m.created_at); if (dl !== last) { out += `<span class="day">${dl}</span>`; last = dl; } out += bubble(m, false); }
  return out;
}
function renderThread(d) {
  const t = d.thread, c = state.conv, human = t.state === 'needs_human', mine = human && t.human_by === state.me.email;
  c.lastId = t.messages.length ? t.messages[t.messages.length - 1].id : 0;
  c.lastDay = t.messages.length ? dayLabel(t.messages[t.messages.length - 1].created_at) : '';
  const banner = !human ? '' : t.human_by
    ? `<div class="banner human">${icon('hand')}${mine ? 'Você está atendendo. A IA está pausada para este cliente.' : esc(t.human_by.split('@')[0]) + ' está atendendo. A IA está pausada.'}</div>`
    : `<div class="banner asked">${icon('alert')}O cliente pediu uma pessoa. A IA parou de responder: assuma para conversar.</div>`;
  const windowBanner = human && !d.window_open ? `<div class="banner window">${icon('clock')}Passou de 24 h desde a última mensagem do cliente: a Cloud API recusa texto livre até ele escrever de novo.</div>` : '';
  $('#thread').innerHTML = `
    <div class="t-head"><button class="icon-btn" type="button" data-back-chats aria-label="Voltar para a lista">${icon('back')}</button><span class="mini-av ${human ? 'hot' : ''}">${esc(initials(t.client_name || t.org_name))}</span>
      <div><b>${esc(t.org_name)}</b><small>${esc(t.client_name)} · ${esc(phone(t.phone))} · ${esc(CONV_STATE[t.state] || t.state)}</small></div>
      <div class="actions"><button class="btn sm" type="button" data-open-client="${t.org_id}">Ficha</button>
        ${human ? `${t.human_by && !mine ? `<button class="btn sm" type="button" data-takeover>${icon('hand')}Assumir eu</button>` : ''}${!t.human_by ? `<button class="btn sm primary" type="button" data-takeover>${icon('hand')}Assumir conversa</button>` : ''}<button class="btn sm" type="button" data-release>${icon('bot')}Devolver à IA</button>` : `<button class="btn sm primary" type="button" data-takeover>${icon('hand')}Assumir conversa</button>`}</div></div>
    ${banner}${windowBanner}
    <div class="msgs" id="msgs">${msgsHTML(t.messages) || '<div class="c-empty"><p class="faint">Sem mensagens ainda.</p></div>'}<div id="pending">${pendingBubbles(t.pending)}</div></div>
    <form class="composer" id="composer">${mine ? `<textarea class="field" id="composer-text" rows="1" maxlength="4000" placeholder="Responder como ${esc(state.me.name)}…" title="Enter envia; Shift+Enter quebra a linha" aria-label="Mensagem para o cliente"></textarea><button class="btn primary" type="submit" id="send">${icon('send')}Enviar</button>`
      : `<p class="note">${human && t.human_by ? esc(t.human_by.split('@')[0]) + ' está atendendo. Toque em “Assumir eu” para escrever.' : human ? 'Assuma a conversa para escrever.' : 'A IA está respondendo. Assuma a conversa para escrever.'}</p>`}</form>`;
  $('#c-side').innerHTML = `<h3>Cliente</h3><dl class="kv one"><dt class="faint small">Negócio</dt><dd>${esc(t.org_name)}</dd><dt class="faint small">Contato</dt><dd>${esc(t.client_name)}<br><span class="num">${esc(phone(t.phone))}</span></dd><dt class="faint small">Estado</dt><dd>${esc(CONV_STATE[t.state] || t.state)}${t.blocked ? ' <span class="chip err">bloqueado</span>' : ''}</dd></dl>
    <h3>Projeto ativo</h3><p class="small">${t.project_name ? esc(t.project_name) + '<br><span class="muted">' + esc(PROJECT_STAGE[t.project_stage] || t.project_stage) + '</span>' : '<span class="muted">nenhum</span>'}</p>
    <h3>Atendimento</h3><p class="small muted">${t.human_by ? 'Com ' + esc(t.human_by) + (t.human_since ? ' desde ' + esc(dt(t.human_since)) : '') : human ? 'Esperando alguém da equipe' : 'IA respondendo'}</p>
    <h3>Última mensagem do cliente</h3><p class="small muted">${t.last_inbound_at ? esc(dt(t.last_inbound_at)) + ' (' + ago(t.last_inbound_at) + ')' : '—'}</p>`;
  const msgs = $('#msgs'); msgs.scrollTop = msgs.scrollHeight;
  const ta = $('#composer-text');
  if (ta) {
    ta.addEventListener('keydown', e => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); $('#composer').requestSubmit(); } });
    ta.addEventListener('input', () => { ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight, 144) + 'px'; });
    if (mine) ta.focus();
  }
}
async function pollThread() {
  const c = state.conv, id = c.sel;
  try {
    const d = await api('GET', '/conversations/' + id + '?after=' + c.lastId);
    if (c.sel !== id || !$('#msgs')) return;
    const t = d.thread, prev = c.thread.thread;
    // Mudou quem atende ou o estado: redesenha tudo (mantendo o que foi digitado).
    if (t.state !== prev.state || t.human_by !== prev.human_by || d.window_open !== c.thread.window_open) {
      const draft = $('#composer-text')?.value || '';
      const full = await api('GET', '/conversations/' + id);
      c.thread = full; renderThread(full);
      if (draft && $('#composer-text')) $('#composer-text').value = draft;
      return;
    }
    const msgs = $('#msgs'), atBottom = msgs.scrollHeight - msgs.scrollTop - msgs.clientHeight < 80;
    if (t.messages.length) {
      let html = '';
      for (const m of t.messages) { const dl = dayLabel(m.created_at); if (dl !== c.lastDay) { html += `<span class="day">${dl}</span>`; c.lastDay = dl; } html += bubble(m, true); }
      $('#pending').insertAdjacentHTML('beforebegin', html);
      c.lastId = t.messages[t.messages.length - 1].id;
      prev.messages.push(...t.messages);
    }
    $('#pending').innerHTML = pendingBubbles(t.pending);
    if (atBottom) msgs.scrollTop = msgs.scrollHeight;
  } catch { /* próximo ciclo */ }
}

/* ---------- faturamento ---------- */
views.faturamento = async view => {
  if (state.me.role !== 'admin') { view.innerHTML = lockwall('Receita, pagamentos e margem ficam restritos aos administradores.'); return; }
  if (!state.month) { const n = new Date(); state.month = n.getFullYear() + '-' + String(n.getMonth() + 1).padStart(2, '0'); }
  const [r, cl] = await Promise.all([api('GET', '/billing?month=' + state.month), api('GET', '/clients?stage=')]);
  const monthName = new Date(state.month + '-15').toLocaleDateString('pt-BR', { month: 'long', year: 'numeric' });
  const kpis = [
    ['MRR', brl(r.mrr_cents), `${num(r.paying_subs)} assinatura(s) paga(s) · ARPA ${brl(r.arpa_cents)}`, 'hero'],
    ['Recebido no mês', brl(r.received_cents), `${num(r.paid_count)} pagamento(s) · ticket médio ${brl(r.ticket_cents)}`],
    ['Previsão do mês', brl(r.forecast_cents), 'recebido + pendente + mensalidades sem cobrança'],
    ['Margem', brl(r.margin_cents), r.received_cents ? pct(r.margin_pct) + ' do recebido' : 'sem receita no mês'],
    ['Pendente', brl(r.pending_cents), `${num(r.pending_count)} cobrança(s) em aberto`],
    ['Inadimplência', pct(r.delinquency_rate), `${num(r.past_due_subs)} em atraso · ${brl(r.past_due_mrr_cents)}/mês em risco`],
    ['Custo de IA', brl(r.ai_cost_cents), `${usd(r.ai_cost_usd)} · cotação ${r.usd_brl.toLocaleString('pt-BR')}`],
    ['Assinaturas no mês', '+' + num(r.new_subs) + ' / −' + num(r.canceled), `${num(r.new_paying)} nova(s) paga(s) · ${num(r.failed_payments)} cobrança(s) falharam`],
  ];
  const today = new Date().toISOString().slice(0, 10);
  view.innerHTML = `<div class="page">
    <div class="page-head"><div><h1>Faturamento</h1><p>${esc(monthName)} · valores em reais; custo de IA convertido de dólar</p></div>
      <div class="actions"><label class="sr" for="month">Mês</label><input class="field" type="month" id="month" value="${esc(state.month)}" max="${today.slice(0, 7)}"></div></div>
    <section class="kpis" aria-label="Indicadores">${kpis.map(([l, v, s, cls]) => `<div class="kpi ${cls || ''}"><small>${l}</small><b class="num">${esc(v)}</b><span>${esc(s)}</span></div>`).join('')}</section>
    <div class="grid-2b">
      <section class="panel"><div class="panel-head"><h2>Últimos 6 meses</h2><span class="legend"><span><i class="l-rev"></i>recebido</span><span><i class="l-cost"></i>custo de IA</span></span></div>
        <div class="panel-body">${barChart(r.series)}</div></section>
      <section class="panel"><div class="panel-head"><h2>Assinaturas por plano</h2></div>
        <div class="panel-body table-wrap"><table class="table"><thead><tr><th>Plano</th><th class="r">Ativas</th><th class="r hide-sm">Teste</th><th class="r">Atraso</th><th class="r hide-sm">Canceladas</th><th class="r">MRR</th></tr></thead>
        <tbody>${r.plans.map(p => `<tr><td><b>${esc(p.name)}</b><br><span class="faint small">${brl(p.price_cents)}/${p.interval === 'year' ? 'ano' : 'mês'}</span></td><td class="r">${num(p.active)}</td><td class="r hide-sm">${num(p.trialing)}</td><td class="r">${p.past_due ? `<span class="chip warn">${num(p.past_due)}</span>` : '0'}</td><td class="r hide-sm">${num(p.canceled)}</td><td class="r">${brl(p.mrr_cents)}</td></tr>`).join('')}</tbody></table></div></section>
    </div>
    <div class="grid-2b">
      <section class="panel"><div class="panel-head"><h2>Pagamentos recentes</h2><span class="muted">os 30 mais novos</span></div>
        <div class="panel-body table-wrap">${r.recent.length ? `<table class="table"><thead><tr><th>Cliente</th><th class="hide-sm">Data</th><th>Método</th><th>Situação</th><th class="r">Valor</th></tr></thead><tbody>
        ${r.recent.map(p => `<tr><td>${esc(p.org_name)}${p.note ? `<br><span class="faint small">${esc(p.note)}</span>` : ''}</td><td class="hide-sm num">${esc(dateOnly(p.paid_at || p.created_at))}</td><td>${esc(METHOD[p.method] || p.method)}</td><td><span class="chip ${PAY_STATUS[p.status]?.[1] || ''}">${esc(PAY_STATUS[p.status]?.[0] || p.status)}</span></td><td class="r">${brl(p.amount_cents)}</td></tr>`).join('')}</tbody></table>` : '<div class="empty"><b>Nenhum pagamento</b>Registre o primeiro ao lado.</div>'}</div></section>
      <section class="panel"><div class="panel-head"><h2>Registrar pagamento</h2><span class="muted">enquanto não há gateway</span></div>
        <form class="panel-body form-grid" id="pay-form" novalidate>
          <label class="full">Cliente<select class="field" name="org_id" required><option value="">Escolha o cliente</option>${cl.clients.filter(c => c.plan).map(c => `<option value="${c.org_id}">${esc(c.name)} · ${esc(c.plan_name)}</option>`).join('')}</select></label>
          <label>Valor (R$)<input class="field num" name="amount" inputmode="decimal" placeholder="97,00" required></label>
          <label>Data<input class="field" name="date" type="date" value="${today}" max="${today}" required></label>
          <label>Método<select class="field" name="method"><option value="pix">Pix</option><option value="boleto">Boleto</option><option value="card">Cartão</option></select></label>
          <label>Situação<select class="field" name="status"><option value="paid">Pago</option><option value="pending">Pendente</option><option value="failed">Falhou</option><option value="refunded">Estornado</option></select></label>
          <label class="full">Observação<input class="field" name="note" maxlength="200" placeholder="ex.: pix da mensalidade de outubro"></label>
          <div class="full actions"><button class="btn primary" type="submit" id="pay-save">${icon('plus')}Registrar</button><span class="faint small">Só clientes com assinatura aparecem na lista.</span></div>
        </form></section>
    </div></div>`;
  applyDyn(view);
  $('#month', view).addEventListener('change', e => { if (e.target.value) { state.month = e.target.value; rerender(); } });
  $('#pay-form', view).addEventListener('submit', savePayment);
};
async function savePayment(e) {
  e.preventDefault();
  const f = new FormData(e.target);
  const raw = String(f.get('amount') || '').replace(/[^\d,.-]/g, '');
  const value = Number(raw.includes(',') ? raw.replace(/\./g, '').replace(',', '.') : raw);
  const amount = Math.round(value * 100);
  if (!f.get('org_id')) { toast('Escolha o cliente.', 'warn', 'alert'); return; }
  if (!(amount > 0)) { toast('Digite um valor maior que zero.', 'warn', 'alert'); return; }
  const ok = await act($('#pay-save'), () => api('POST', '/payments', { org_id: +f.get('org_id'), amount_cents: amount, method: f.get('method'), status: f.get('status'), date: f.get('date'), note: String(f.get('note') || '') }));
  if (ok) { toast('Pagamento de ' + brl(amount) + ' registrado.', 'ok', 'money'); rerender(); }
}

/* ---------- WhatsApp ---------- */
views.whatsapp = async view => {
  const d = await api('GET', '/whatsapp');
  const st = d.stats, wa = st.statuses.find(s => s.Provider === d.provider) || st.statuses[0];
  const cls = !wa ? 'unknown' : wa.Connected ? '' : 'down';
  const max = Math.max(1, ...st.days.map(x => x.in + x.out));
  const wd = ['dom', 'seg', 'ter', 'qua', 'qui', 'sex', 'sáb'];
  view.innerHTML = `<div class="page">
    <div class="page-head"><div><h1>WhatsApp do bot</h1><p>Número que atende todos os clientes</p></div></div>
    <div class="wa-grid">
      <section class="panel"><div class="wa-state ${cls}"><span class="big-ico">${icon(!wa ? 'clock' : wa.Connected ? 'wa' : 'alert', 'ico')}</span>
        <div><h2>${!wa ? 'Sem aviso ainda' : wa.Connected ? 'Conectado' : 'Desconectado'}</h2><p>${!wa ? 'O provedor ainda não mandou aviso de conexão para este servidor.' : (wa.Connected ? 'Desde ' : 'Caiu em ') + esc(dt(wa.UpdatedAt)) + (wa.Reason ? ' · ' + esc(wa.Reason) : '') + (wa.Code ? ' · código ' + wa.Code : '')}</p></div></div>
        <div class="panel-body"><dl class="kv">
          <dt>Número do bot</dt><dd class="num">${d.bot_number ? esc(phone(d.bot_number)) : '<span class="faint">BOT_WHATSAPP_NUMBER vazio</span>'}</dd>
          <dt>Provedor</dt><dd>${esc(d.provider)}${d.window_limited ? ' · Cloud API (janela de 24 h)' : ''}</dd>
          ${st.statuses.filter(s => s !== wa).map(s => `<dt>${esc(s.Provider)}</dt><dd>${s.Connected ? 'conectado' : 'desconectado'} · ${esc(dt(s.UpdatedAt))}</dd>`).join('')}
          <dt>Falhas em 24 h</dt><dd>${st.failed.length ? `<span class="chip warn">${st.failed.length} envio(s)</span>` : '<span class="chip ok">nenhuma</span>'}</dd></dl>
          ${wa && !wa.Connected ? '<p class="muted small">Para reconectar, abra o painel do provedor (Wame ou WAHA) e leia o QR com o celular do bot. O estado aqui muda sozinho quando o aviso de conexão chegar.</p>' : ''}</div></section>
      <section class="panel"><div class="panel-head"><h2>Mensagens por dia</h2><span class="legend"><span><i class="l-rev"></i>enviadas</span><span><i class="l-in"></i>recebidas</span></span></div>
        <div class="panel-body"><div class="bars">${st.days.map((x, i) => `<div class="bar" title="${x.out} enviadas, ${x.in} recebidas"><div class="bar-stack"><i class="in" data-h="${(x.in / max * 100).toFixed(1)}%" data-delay="${i * 50}"></i><i class="out" data-h="${(x.out / max * 100).toFixed(1)}%" data-delay="${i * 50}"></i></div><small>${wd[new Date(x.day + 'T12:00').getDay()]}</small></div>`).join('')}</div></div>
        <div class="panel-head"><h2>Envios que falharam</h2><span class="muted">últimas 24 h</span></div>
        <div class="panel-body">${st.failed.length ? st.failed.map(f => `<div class="err-row"><span>Para ${esc(f.org_name)} · ${esc(f.preview)}<br><code>${esc(f.error)}</code></span><span class="faint num">${hm(f.at)}</span></div>`).join('') : '<p class="muted small">Nenhum envio falhou.</p>'}</div></section>
    </div></div>`;
  applyDyn(view);
  every(20000, async () => { if (state.route === 'whatsapp') { try { const n = await api('GET', '/whatsapp'); if (JSON.stringify(n) !== JSON.stringify(d)) rerender(true); } catch { /* próximo ciclo */ } } });
};

/* ---------- equipe ---------- */
views.equipe = async view => {
  if (state.me.role !== 'admin') { view.innerHTML = lockwall('Só administradores adicionam e removem pessoas da equipe.'); return; }
  const d = await api('GET', '/team');
  view.innerHTML = `<div class="page">
    <div class="page-head"><div><h1>Equipe</h1><p>Quem entra no painel. O login é por código no e-mail.</p></div></div>
    <section class="panel"><div class="panel-body">
      ${d.team.map(m => `<div class="team-row"><span class="mini-av">${esc(initials(m.name || m.email))}</span><div><b>${esc(m.name || m.email.split('@')[0])}${m.id === state.me.id ? ' <span class="faint small">(você)</span>' : ''}</b><small>${esc(m.email)} · ${m.last_login_at ? 'último acesso ' + esc(dt(m.last_login_at)) : 'nunca entrou'}</small></div>
        <span class="chip ${m.role === 'admin' ? 'info' : ''}">${m.role === 'admin' ? 'Administrador' : 'Equipe'}</span>
        ${m.id === state.me.id ? '<span></span>' : `<button class="btn sm ghost" type="button" data-remove-member="${m.id}" data-email="${esc(m.email)}" aria-label="Remover ${esc(m.email)}">${icon('trash')}</button>`}</div>`).join('')}
      <form class="team-add" id="team-form" novalidate>
        <label class="sr" for="tm-email">E-mail</label><input class="field" id="tm-email" name="email" type="email" placeholder="e-mail da pessoa" required>
        <label class="sr" for="tm-name">Nome</label><input class="field" id="tm-name" name="name" placeholder="nome" maxlength="80">
        <label class="sr" for="tm-role">Papel</label><select class="field" id="tm-role" name="role"><option value="staff">Equipe</option><option value="admin">Administrador</option></select>
        <button class="btn primary" type="submit" id="tm-save">${icon('user-plus')}Adicionar</button>
      </form>
      <p class="muted small">Equipe vê clientes, conversas, WhatsApp e as próprias ações. Administrador também vê faturamento, equipe, o registro completo e pode zerar contatos.</p>
    </div></section></div>`;
  $('#team-form', view).addEventListener('submit', async e => {
    e.preventDefault();
    const f = new FormData(e.target);
    const ok = await act($('#tm-save'), () => api('POST', '/team', { email: String(f.get('email')).trim(), name: String(f.get('name')).trim(), role: f.get('role') }));
    if (ok) { toast(ok.email + ' entrou na equipe.', 'ok', 'user-plus'); rerender(); }
  });
};

/* ---------- registro ---------- */
views.registro = async view => {
  const d = await api('GET', '/audit');
  const admin = d.scope === 'todos';
  view.innerHTML = `<div class="page"><div class="page-head"><div><h1>Registro de ações</h1><p>${admin ? 'Tudo o que a equipe fez pelo painel' : 'As suas ações no painel'}</p></div></div>
    <div class="list">${d.entries.length ? d.entries.map(r => { const [label, kind, ico] = actionOf(r.action);
      return `<div class="log-row"><time>${esc(dt(r.created_at))}</time><span class="actor"><span class="mini-av">${esc(initials(r.actor_name))}</span>${esc(r.actor_name)}</span><span class="ev-txt ${kind}">${icon(ico)}${esc(label)}</span>${r.target_label ? `<span class="chip">${esc(r.target_label)}</span>` : '<span></span>'}</div>`; }).join('')
      : '<div class="empty"><b>Nada registrado</b>As ações feitas pelo painel aparecem aqui.</div>'}</div></div>`;
};

/* ===================== roteamento ===================== */
let renderSeq = 0;
// Conversas montam a casca antes de carregar e se atualizam por partes: desenham direto na view.
const DIRECT = new Set(['conversas']);
async function render(quiet = false) {
  const route = location.hash.replace('#/', '').split('?')[0] || 'visao';
  const name = views[route] ? route : 'visao';
  clearTimers();
  closeDrawer();
  if (name !== 'conversas') state.conv.sel = null;
  state.route = name;
  $$('#nav a').forEach(a => a.dataset.route === name ? a.setAttribute('aria-current', 'page') : a.removeAttribute('aria-current'));
  setSideOpen(false);
  const view = $('#view'), seq = ++renderSeq;
  try {
    if (DIRECT.has(name)) { await views[name](view); return; }
    if (!quiet) { view.innerHTML = loadingPage(); view.scrollTop = 0; }
    const holder = document.createElement('div'); // monta fora da tela e troca de uma vez
    await views[name](holder);
    if (seq !== renderSeq) return;
    view.replaceChildren(...holder.childNodes);
    applyDyn(view);
    if (!quiet) view.focus({ preventScroll: true });
  } catch (e) {
    if (seq !== renderSeq || e.status === 401) return;
    view.innerHTML = errorPage(e, name);
  }
}
const rerender = (quiet = true) => render(quiet);
window.addEventListener('hashchange', () => { if (state.me) render(); });

/* ===================== eventos ===================== */
document.addEventListener('click', async e => {
  const t = e.target.closest('button, a');
  if (!t || !state.me) return;
  const ds = t.dataset;
  if (ds.openClient) { e.preventDefault(); openClient(ds.openClient); return; }
  if (ds.closeDrawer !== undefined) { closeDrawer(); return; }
  if (ds.dtab) { renderDrawer(+ds.dtab); return; }
  if (ds.days) { state.days = +ds.days; rerender(); return; }
  if (ds.filter !== undefined && t.closest('.seg')) { state.clients.filter = ds.filter; rerender(); return; }
  if (ds.gotoFilter) { state.clients = { q: '', filter: ds.gotoFilter, stage: '' }; return; }
  if (ds.convFilter !== undefined) {
    state.conv.filter = ds.convFilter;
    if (t.tagName === 'A') return; // segue o link para #/conversas
    loadInbox(); return;
  }
  if (ds.retry) { render(); return; }
  if (ds.chat) { openThread(+ds.chat); return; }
  if (ds.backChats !== undefined) { $('#chat-wrap')?.classList.remove('open'); state.conv.sel = null; return; }
  if (ds.gotoConv) { closeDrawer(); state.conv.sel = +ds.gotoConv; state.conv.filter = ''; if (location.hash === '#/conversas') render(); else location.hash = '#/conversas'; return; }
  if (ds.signup) {
    const ok = await act(t, () => api('POST', '/clients/' + ds.signup + '/signup-link'));
    if (ok) toast('Link de cadastro novo enviado no WhatsApp.', 'ok', 'link');
    return;
  }
  if (ds.block) {
    const c = state.drawer;
    const ok = await act(t, () => api('POST', '/clients/' + c.org_id + '/' + ds.block));
    if (ok) { toast(ds.block === 'block' ? c.name + ' bloqueado: o bot para de responder.' : c.name + ' desbloqueado.', ds.block === 'block' ? 'warn' : 'ok', ds.block === 'block' ? 'lock' : 'unlock'); state.drawer.status = ok.status; renderDrawer(0); if (state.route === 'clientes') rerender(); }
    return;
  }
  if (ds.resetAsk !== undefined) { askReset(t); return; }
  if (ds.resetNo !== undefined) { $('#confirm-slot').innerHTML = ''; return; }
  if (ds.resetYes !== undefined) {
    const c = state.drawer;
    const ok = await act(t, () => api('POST', '/clients/' + c.org_id + '/reset', { confirm: 'ZERAR' }));
    if (ok) { closeDrawer(); toast(c.name + ' zerado. O número volta como contato novo.', 'warn', 'trash'); rerender(); }
    return;
  }
  if (ds.takeover !== undefined) {
    const id = state.conv.sel;
    const ok = await act(t, () => api('POST', '/conversations/' + id + '/takeover'));
    if (ok) { toast('Você assumiu a conversa. A IA está pausada para este cliente.', 'warn', 'hand'); await openThread(id, true); loadInbox(); }
    return;
  }
  if (ds.release !== undefined) {
    const id = state.conv.sel;
    const ok = await act(t, () => api('POST', '/conversations/' + id + '/release'));
    if (ok) { toast('Conversa devolvida à IA.', 'ok', 'bot'); await openThread(id, true); loadInbox(); }
    return;
  }
  if (ds.removeMember) {
    if (t.dataset.armed !== '1') { t.dataset.armed = '1'; t.classList.add('danger'); t.innerHTML = 'Remover?'; setTimeout(() => { if (t.isConnected) { t.dataset.armed = ''; t.classList.remove('danger'); t.innerHTML = icon('trash'); } }, 4000); return; }
    const ok = await act(t, () => api('DELETE', '/team/' + ds.removeMember));
    if (ok) { toast(ds.email + ' saiu da equipe. A sessão dela cai na hora.', 'warn', 'trash'); rerender(); }
    return;
  }
  if (t.id === 'menu-btn') { setSideOpen(!document.body.classList.contains('side-open'), true); return; }
  if (t.id === 'pin-side') { togglePin(); return; }
  if (t.id === 'open-cmdk') { openCmdk(); return; }
});
document.addEventListener('submit', async e => {
  if (e.target.id !== 'composer') return;
  e.preventDefault();
  const ta = $('#composer-text'); if (!ta) return;
  const text = ta.value.trim(); if (!text) return;
  const id = state.conv.sel;
  ta.disabled = true;
  const ok = await act($('#send'), () => api('POST', '/conversations/' + id + '/send', { text }));
  ta.disabled = false;
  if (ok) { ta.value = ''; ta.style.height = ''; ta.focus(); pollThread(); }
});
$('#drawer-backdrop').addEventListener('click', closeDrawer);
$('#side-backdrop').addEventListener('click', () => setSideOpen(false));

/* ===================== menu lateral ===================== */
/* Desktop com mouse: trilho de ícones que abre por cima do conteúdo no hover (atraso curto na saída
   para não piscar) e no foco por teclado. Fixar mantém aberto e vira coluna (localStorage).
   Celular e telas sem mouse: o botão do topo abre; Esc, toque fora ou navegar fecham. */
const sideEl = $('#side'), menuBtn = $('#menu-btn'), pinBtn = $('#pin-side');
const wide = matchMedia('(min-width: 901px)');
const SIDE_KEY = 'meuanuncio.admin.sidePinned';
const side = { hover: false, kbd: false, pinned: false, timer: 0 };
try { side.pinned = localStorage.getItem(SIDE_KEY) === '1'; } catch { /* sem armazenamento: começa recolhido */ }
function syncSide() {
  const open = document.body.classList.contains('side-open');
  document.body.classList.toggle('side-x', wide.matches && (side.hover || side.kbd || side.pinned || open));
  document.body.classList.toggle('side-pinned', wide.matches && side.pinned);
  menuBtn.setAttribute('aria-expanded', String(open));
  menuBtn.setAttribute('aria-label', open ? 'Fechar menu' : 'Abrir menu');
  pinBtn.setAttribute('aria-pressed', String(side.pinned));
  pinBtn.setAttribute('aria-label', side.pinned ? 'Soltar menu' : 'Fixar menu aberto');
  pinBtn.title = pinBtn.getAttribute('aria-label');
  sideEl.inert = !wide.matches && !open; // gaveta fechada no celular fica fora do Tab
}
function setSideOpen(on, moveFocus = false) {
  const was = document.body.classList.contains('side-open');
  document.body.classList.toggle('side-open', on);
  $('#side-backdrop').classList.toggle('on', on && !wide.matches);
  syncSide();
  if (!moveFocus) return;
  if (on) ($('#nav a[aria-current="page"]') || $('#nav a')).focus();
  else if (was && getComputedStyle(menuBtn).display !== 'none') menuBtn.focus();
}
function togglePin() {
  side.pinned = !side.pinned;
  try { localStorage.setItem(SIDE_KEY, side.pinned ? '1' : '0'); } catch { /* vale só nesta aba */ }
  syncSide();
}
function hoverSide(on, delay) {
  clearTimeout(side.timer);
  side.timer = setTimeout(() => { side.hover = on; syncSide(); }, delay);
}
sideEl.addEventListener('pointerenter', e => { if (e.pointerType !== 'touch' && wide.matches) hoverSide(true, 40); });
sideEl.addEventListener('pointerleave', e => { if (e.pointerType !== 'touch') hoverSide(false, 200); });
sideEl.addEventListener('focusin', e => { if (wide.matches && e.target.matches(':focus-visible')) { side.kbd = true; syncSide(); } });
sideEl.addEventListener('focusout', e => { if (!sideEl.contains(e.relatedTarget)) { side.kbd = false; syncSide(); } });
document.addEventListener('pointerdown', e => {
  if (wide.matches && document.body.classList.contains('side-open') && !sideEl.contains(e.target) && !menuBtn.contains(e.target)) setSideOpen(false);
});
wide.addEventListener('change', () => { side.hover = side.kbd = false; setSideOpen(false); });
syncSide();

/* ===================== Ctrl+K ===================== */
const CMDS = [
  ['Visão geral', '#/visao', 'home'], ['Clientes', '#/clientes', 'users'], ['Conversas', '#/conversas', 'chat'],
  ['Conversas pedindo humano', 'humano', 'hand'], ['Clientes travados', 'travados', 'clock'], ['WhatsApp do bot', '#/whatsapp', 'wa'],
  ['Faturamento', '#/faturamento', 'money', true], ['Registrar pagamento', '#/faturamento', 'plus', true], ['Equipe', '#/equipe', 'team', true],
  ['Registro de ações', '#/registro', 'log'], ['Sair do painel', 'sair', 'out'],
];
let cmdSel = 0, cmdItems = [];
const searchClients = debounce(async q => {
  try {
    const d = await api('GET', '/clients?' + new URLSearchParams({ q }));
    if ($('#cmdk-q').value.trim() !== q) return;
    fillCmdk(q, d.clients.slice(0, 6));
  } catch { /* fica só com as ações */ }
}, 200);
function fillCmdk(q = '', clients = []) {
  const ql = q.toLowerCase();
  const acts = CMDS.filter(c => (!c[3] || state.me.role === 'admin') && (!ql || c[0].toLowerCase().includes(ql)));
  cmdItems = [
    ...clients.map(c => ({ label: c.name, sub: [c.contact, phone(c.phone)].filter(Boolean).join(' · '), ico: 'users', run: () => openClient(c.org_id) })),
    ...clients.filter(c => c.conversation_id).slice(0, 3).map(c => ({ label: 'Conversa com ' + c.name, sub: c.needs_human ? 'pediu humano' : '', ico: 'chat', run: () => { state.conv.sel = c.conversation_id; state.conv.filter = ''; location.hash === '#/conversas' ? render() : (location.hash = '#/conversas'); } })),
    ...acts.map(([l, h, ic]) => ({ label: l, ico: ic, run: () => runCmd(h) })),
  ];
  cmdSel = 0;
  const nClients = clients.length + clients.filter(c => c.conversation_id).slice(0, 3).length;
  $('#cmdk-list').innerHTML = cmdItems.length ? cmdItems.map((it, i) => (i === 0 && nClients ? '<h4>Clientes</h4>' : '') + (i === nClients && acts.length ? '<h4>Ir para</h4>' : '') +
    `<button class="cmdk-item" type="button" role="option" data-i="${i}" aria-selected="${i === 0}">${icon(it.ico)}${esc(it.label)}${it.sub ? `<small>${esc(it.sub)}</small>` : ''}</button>`).join('')
    : `<p class="muted small cmdk-none">Nada encontrado para “${esc(q)}”.</p>`;
}
function runCmd(h) {
  if (h === 'sair') { $('#logout').click(); return; }
  if (h === 'humano') { state.conv.filter = 'humano'; location.hash === '#/conversas' ? render() : (location.hash = '#/conversas'); return; }
  if (h === 'travados') { state.clients = { q: '', filter: 'travados', stage: '' }; location.hash === '#/clientes' ? render() : (location.hash = '#/clientes'); return; }
  location.hash === h ? render() : (location.hash = h);
}
function openCmdk() { const d = $('#cmdk'); $('#cmdk-q').value = ''; fillCmdk(); if (!d.open) d.showModal(); $('#cmdk-q').focus(); }
$('#cmdk-q').addEventListener('input', e => { const q = e.target.value.trim(); fillCmdk(q); if (q.length >= 2) searchClients(q); });
$('#cmdk-list').addEventListener('click', e => { const b = e.target.closest('.cmdk-item'); if (b) { $('#cmdk').close(); cmdItems[+b.dataset.i].run(); } });
$('#cmdk').addEventListener('click', e => { if (e.target === $('#cmdk')) $('#cmdk').close(); });
document.addEventListener('keydown', e => {
  if (!state.me) return;
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); openCmdk(); return; }
  if (e.key === 'Escape' && document.body.classList.contains('side-open') && !$('#cmdk').open) { setSideOpen(false, true); return; }
  if (e.key === 'Escape' && side.kbd && sideEl.contains(document.activeElement) && !$('#cmdk').open) { $('#view').focus(); return; }
  if (e.key === 'Escape' && $('#drawer').classList.contains('on') && !$('#cmdk').open) { closeDrawer(); return; }
  const d = $('#cmdk');
  if (d.open && ['ArrowDown', 'ArrowUp', 'Enter'].includes(e.key)) {
    const btns = $$('.cmdk-item'); if (!btns.length) return;
    e.preventDefault();
    if (e.key === 'Enter') { d.close(); cmdItems[cmdSel].run(); return; }
    cmdSel = (cmdSel + (e.key === 'ArrowDown' ? 1 : -1) + btns.length) % btns.length;
    btns.forEach((b, i) => b.setAttribute('aria-selected', String(i === cmdSel)));
    btns[cmdSel].scrollIntoView({ block: 'nearest' });
  }
});

/* ===================== início ===================== */
(async function boot() {
  try { state.mode = (await api('GET', '/session')).mode; } catch { state.mode = ''; }
  try { state.me = await api('GET', '/me'); enterApp(); } catch { showLogin(); }
})();
