/* Aba Planos: preço, limites e situação de cada plano. A equipe toda vê; só administrador cria,
   edita e apaga (a API confere de novo). A edição abre na gaveta lateral. Depende das utilidades
   do admin.js. */
'use strict';

const PLANOS = { list: [], canEdit: false, defaultPlan: 'free', edit: null, confirmed: false };
const PLAN_SIT = { available: ['Disponível', 'ok'], soon: ['Em breve', 'warn'], hidden: ['Oculto', ''] };
const planSit = p => p.available ? 'available' : p.visible ? 'soon' : 'hidden';
const PLAN_CODE_RE = /^[a-z][a-z0-9-]{1,29}$/;
const reais = v => Number(v || 0).toLocaleString('pt-BR', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const planPrice = p => p.price_cents ? brl(p.price_cents) : 'Grátis';
const perInterval = p => p.interval === 'year' ? 'ano' : 'mês';

// "97", "97,00", "1.500", "1.500,50" e "97.5" viram número; vazio ou lixo vira NaN.
function parseReais(s) {
  const raw = String(s ?? '').trim().replace(/[^\d,.-]/g, '');
  if (!raw) return NaN;
  if (raw.includes(',')) return Number(raw.replace(/\./g, '').replace(',', '.'));
  if (/^\d{1,3}(\.\d{3})+$/.test(raw)) return Number(raw.replace(/\./g, ''));
  return Number(raw);
}

views.planos = async view => {
  const d = await api('GET', '/plans');
  PLANOS.list = d.plans; PLANOS.canEdit = d.can_edit; PLANOS.defaultPlan = d.default_plan;
  const active = d.plans.reduce((n, p) => n + p.active_subs, 0);
  const cards = d.plans.map(p => {
    const [sit, cls] = PLAN_SIT[planSit(p)], canceled = p.subscribers - p.active_subs;
    return `<article class="plan-card ${planSit(p)}">
      <div class="pc-head"><div><h2>${esc(p.name)}</h2><span class="faint small num">${esc(p.code)}</span></div><span class="chip ${cls}">${sit}</span></div>
      <p class="pc-price"><b class="num">${esc(planPrice(p))}</b>${p.price_cents ? `<span>/${perInterval(p)}</span>` : ''}</p>
      <dl class="kv pc-kv"><dt>Mensagens por dia</dt><dd class="num">${num(p.daily_message_limit)}</dd>
        <dt>Investimento por dia</dt><dd class="num">até R$ ${esc(reais(p.max_daily_budget_brl))}</dd>
        <dt>Projetos ativos</dt><dd class="num">${num(p.max_projects)}</dd></dl>
      <div class="pc-foot"><span class="pc-subs">${icon('users')}<span><b class="num">${num(p.active_subs)}</b> ${p.active_subs === 1 ? 'assinatura ativa' : 'assinaturas ativas'}${canceled ? ` · ${num(canceled)} cancelada${canceled > 1 ? 's' : ''}` : ''}</span></span>
        ${PLANOS.canEdit ? `<button class="btn sm" type="button" data-plan-edit="${esc(p.code)}" aria-label="Editar ${esc(p.name)}">Editar</button>` : ''}</div>
    </article>`;
  }).join('');
  view.innerHTML = `<div class="page">
    <div class="page-head"><div><h1>Planos</h1><p>${num(d.plans.length)} plano${d.plans.length === 1 ? '' : 's'} · ${num(active)} assinatura${active === 1 ? '' : 's'} ativa${active === 1 ? '' : 's'}</p></div>
      ${PLANOS.canEdit ? `<div class="actions"><button class="btn primary" type="button" data-plan-new>${icon('plus')}Novo plano</button></div>` : ''}</div>
    ${PLANOS.canEdit ? '' : `<div class="alert note">${icon('lock', 'ico')}<p>Só administradores mudam os planos. Aqui você vê os valores em vigor.</p></div>`}
    <div class="plan-cards">${cards || '<div class="empty"><b>Nenhum plano</b>Crie o primeiro.</div>'}</div>
    <section class="panel"><div class="panel-head"><h2>O que muda quando você edita</h2></div>
      <div class="panel-body"><ul class="plan-rules">
        <li><b>Preço:</b> vale para as cobranças novas. Pagamentos já registrados não mudam; o MRR do Faturamento passa a usar o preço novo.</li>
        <li><b>Mensagens e investimento por dia:</b> são copiados para o cliente quando ele conclui o cadastro. A mudança vale para quem se cadastrar depois.</li>
        <li><b>Projetos ativos:</b> vale na hora para todos os assinantes do plano.</li>
        <li><b>Situação:</b> disponível dá para assinar no cadastro; em breve aparece sem poder assinar; oculto some do cadastro, e quem já assina continua com ele.</li>
        <li><b>Plano com assinatura</b> não troca de código nem pode ser apagado. Oculte em vez de apagar.</li>
        <li><b>Página inicial:</b> os planos de meuanuncio.io estão escritos na própria página. Mudar aqui não muda lá.</li>
      </ul></div></section></div>`;
};

/* ---------- gaveta de edição ---------- */
function openPlan(code) {
  const p = code ? PLANOS.list.find(x => x.code === code) : null;
  if (code && !p) return;
  PLANOS.edit = p; PLANOS.confirmed = false;
  const isDefault = p && p.code === PLANOS.defaultPlan;
  const codeLocked = p && (p.subscribers > 0 || isDefault);
  const v = p || { code: '', name: '', price_cents: 0, interval: 'month', daily_message_limit: 60, max_daily_budget_brl: 50, max_projects: 3,
    available: false, visible: true, sort_order: (PLANOS.list.reduce((m, x) => Math.max(m, x.sort_order), 0) + 1) };
  const sit = planSit(v);
  const d = $('#drawer');
  d.setAttribute('aria-label', p ? 'Editar plano' : 'Novo plano');
  d.innerHTML = `<form class="plan-form" id="plan-form" novalidate>
    <div class="d-head"><div class="d-top"><span class="avatar">${icon('tag', 'ico')}</span><div><h2>${p ? esc(p.name) : 'Novo plano'}</h2>
      <p>${p ? `${num(p.active_subs)} assinatura${p.active_subs === 1 ? '' : 's'} ativa${p.active_subs === 1 ? '' : 's'} · editado em ${esc(dt(p.updated_at))}` : 'Começa como “em breve”: só dá para assinar quando você deixar disponível.'}</p></div>
      <button class="icon-btn" type="button" data-close-drawer aria-label="Fechar">${icon('x')}</button></div></div>
    <div class="d-body"><div class="form-grid">
      <label>Nome<input class="field" name="name" maxlength="40" required autocomplete="off" value="${esc(v.name)}"></label>
      <label>Código<input class="field num" name="code" maxlength="30" required autocomplete="off" spellcheck="false" value="${esc(v.code)}" ${codeLocked ? 'readonly aria-describedby="code-hint"' : 'placeholder="ex.: starter"'}>
        <small class="hint" id="code-hint">${isDefault ? 'O free é o plano padrão do cadastro: o código não muda.' : codeLocked ? 'Tem assinatura: o código não muda.' : 'Letras minúsculas, números e hífen.'}</small></label>
      <label>Preço (R$)<input class="field num" name="price" inputmode="decimal" required autocomplete="off" value="${esc(reais(v.price_cents / 100))}"></label>
      <label>Cobrança<select class="field" name="interval"><option value="month" ${v.interval === 'month' ? 'selected' : ''}>Mensal</option><option value="year" ${v.interval === 'year' ? 'selected' : ''}>Anual</option></select></label>
      <label>Mensagens com a IA por dia<input class="field num" name="daily_message_limit" type="number" inputmode="numeric" min="1" max="100000" step="1" required value="${v.daily_message_limit}"></label>
      <label>Investimento máximo por dia (R$)<input class="field num" name="budget" inputmode="decimal" required autocomplete="off" value="${esc(reais(v.max_daily_budget_brl))}"></label>
      <label>Projetos ativos<input class="field num" name="max_projects" type="number" inputmode="numeric" min="1" max="1000" step="1" required value="${v.max_projects}"></label>
      <label>Ordem na lista<input class="field num" name="sort_order" type="number" inputmode="numeric" min="0" max="999" step="1" required value="${v.sort_order}"></label>
      <label class="full">Situação no cadastro<select class="field" name="situation" ${isDefault ? 'disabled aria-describedby="sit-hint"' : ''}>
        ${Object.entries({ available: 'Disponível: dá para assinar', soon: 'Em breve: aparece sem poder assinar', hidden: 'Oculto: some do cadastro' }).map(([k, l]) => `<option value="${k}" ${k === sit ? 'selected' : ''}>${l}</option>`).join('')}</select>
        ${isDefault ? '<small class="hint" id="sit-hint">O free é o padrão do cadastro e fica sempre disponível.</small>' : ''}</label>
    </div>
    <p class="plan-note">${icon('alert')}<span>O preço novo vale para as cobranças novas. Mensagens e investimento por dia valem para quem se cadastrar depois; projetos ativos valem na hora.</span></p>
    <p class="form-err" id="plan-err" role="alert"></p></div>
    <div class="d-foot">
      <button class="btn primary" type="submit" id="plan-save">${icon('check')}${p ? 'Salvar' : 'Criar plano'}</button>
      <button class="btn ghost" type="button" data-close-drawer>Cancelar</button>
      ${p && !isDefault ? (p.subscribers ? `<span class="faint small foot-note">Com assinatura: oculte em vez de apagar.</span>` : `<button class="btn danger" type="button" data-plan-del="${esc(p.code)}">${icon('trash')}Apagar</button>`) : ''}
      <div id="plan-confirm" class="full"></div>
    </div></form>`;
  d.inert = false; d.classList.add('on'); d.setAttribute('aria-hidden', 'false');
  $('#drawer-backdrop').classList.add('on');
  state.drawer = { plan: true };
  setTimeout(() => $('[name="name"]', d)?.focus(), 60);
}

function planFormData(f) {
  const el = n => f.elements[n];
  const sit = el('situation').disabled ? 'available' : el('situation').value;
  const price = parseReais(el('price').value), budget = parseReais(el('budget').value);
  const body = {
    code: el('code').value.trim().toLowerCase(), name: el('name').value.trim().replace(/\s+/g, ' '),
    price_cents: Math.round(price * 100), interval: el('interval').value,
    daily_message_limit: Number(el('daily_message_limit').value), max_daily_budget_brl: Math.round(budget * 100) / 100,
    max_projects: Number(el('max_projects').value), sort_order: Number(el('sort_order').value),
    available: sit === 'available', visible: sit !== 'hidden',
  };
  const int = (n, lo, hi) => Number.isInteger(n) && n >= lo && n <= hi;
  const errs = [];
  if (body.name.length < 2 || body.name.length > 40) errs.push(['name', 'O nome precisa de 2 a 40 caracteres.']);
  if (!PLAN_CODE_RE.test(body.code)) errs.push(['code', 'Código: de 2 a 30 caracteres, só letras minúsculas sem acento, números e hífen, começando por letra.']);
  if (!(price >= 0) || body.price_cents > 100_000_00) errs.push(['price', 'Preço: um valor de R$ 0 a R$ 100.000.']);
  if (!int(body.daily_message_limit, 1, 100000)) errs.push(['daily_message_limit', 'Mensagens por dia: número inteiro de 1 a 100.000.']);
  if (!(budget >= 1 && budget <= 1_000_000)) errs.push(['budget', 'Investimento por dia: de R$ 1 a R$ 1.000.000.']);
  if (!int(body.max_projects, 1, 1000)) errs.push(['max_projects', 'Projetos ativos: número inteiro de 1 a 1.000.']);
  if (!int(body.sort_order, 0, 999)) errs.push(['sort_order', 'Ordem: número inteiro de 0 a 999.']);
  return { body, errs };
}

function showPlanErrors(f, errs) {
  $$('[aria-invalid]', f).forEach(x => x.removeAttribute('aria-invalid'));
  errs.forEach(([n]) => f.elements[n]?.setAttribute('aria-invalid', 'true'));
  $('#plan-err').textContent = errs.map(e => e[1]).join(' ');
  if (errs.length) f.elements[errs[0][0]]?.focus();
}

async function savePlan(f) {
  const p = PLANOS.edit, { body, errs } = planFormData(f);
  showPlanErrors(f, errs);
  if (errs.length) return;
  // Preço mudando em plano com assinante: confirma antes, dizendo o que acontece.
  if (p && p.active_subs > 0 && body.price_cents !== p.price_cents && !PLANOS.confirmed) {
    $('#plan-confirm').innerHTML = `<div class="confirm warn" role="alertdialog" aria-label="Confirmar preço novo">
      <p><b>O preço de ${esc(p.name)} vai de ${esc(brl(p.price_cents))} para ${esc(brl(body.price_cents))}.</b> Vale para as cobranças novas dos ${num(p.active_subs)} assinante${p.active_subs === 1 ? '' : 's'} ativo${p.active_subs === 1 ? '' : 's'}; pagamentos já registrados não mudam.</p>
      <div class="actions"><button class="btn sm ghost" type="button" data-plan-back>Voltar</button><button class="btn sm primary" type="button" data-plan-confirm>Confirmar preço novo</button></div></div>`;
    $('[data-plan-confirm]').focus();
    return;
  }
  const req = p ? () => api('POST', '/plans/' + encodeURIComponent(p.code), { ...body, base_updated_at: p.updated_at }) : () => api('POST', '/plans', body);
  const btn = $('#plan-save');
  btn.classList.add('loading');
  try {
    const saved = await req();
    closeDrawer();
    toast(p ? `Plano ${saved.name} salvo.` : `Plano ${saved.name} criado.`, 'ok', 'check');
    rerender();
  } catch (e) {
    PLANOS.confirmed = false;
    $('#plan-confirm').innerHTML = '';
    if (e.message === 'dados_invalidos' && e.data.detail) { $('#plan-err').textContent = 'Confira: ' + e.data.detail + '.'; return; }
    if (e.message === 'codigo_em_uso') { showPlanErrors(f, [['code', 'Já existe um plano com esse código.']]); return; }
    $('#plan-err').textContent = errMsg(e);
  } finally { btn.classList.remove('loading'); }
}

document.addEventListener('click', async e => {
  const t = e.target.closest('button');
  if (!t || !state.me) return;
  const ds = t.dataset;
  if (ds.planNew !== undefined) { openPlan(null); return; }
  if (ds.planEdit) { openPlan(ds.planEdit); return; }
  if (ds.planBack !== undefined) { $('#plan-confirm').innerHTML = ''; $('#plan-save')?.focus(); return; }
  if (ds.planConfirm !== undefined) { PLANOS.confirmed = true; savePlan($('#plan-form')); return; }
  if (ds.planDel) {
    if (t.dataset.armed !== '1') {
      t.dataset.armed = '1'; t.classList.add('solid'); t.textContent = 'Apagar de vez?';
      setTimeout(() => { if (t.isConnected) { t.dataset.armed = ''; t.classList.remove('solid'); t.innerHTML = icon('trash') + 'Apagar'; } }, 4000);
      return;
    }
    const name = PLANOS.edit?.name || ds.planDel;
    const ok = await act(t, () => api('DELETE', '/plans/' + encodeURIComponent(ds.planDel)));
    if (ok) { closeDrawer(); toast(`Plano ${name} apagado.`, 'warn', 'trash'); rerender(); }
  }
});
document.addEventListener('submit', e => {
  if (e.target.id !== 'plan-form') return;
  e.preventDefault();
  PLANOS.confirmed = false;
  savePlan(e.target);
});
document.addEventListener('input', e => {
  const f = e.target.closest?.('#plan-form');
  if (!f) return;
  if (e.target.name === 'code' && !e.target.readOnly) e.target.value = e.target.value.toLowerCase().replace(/\s+/g, '-');
  if (e.target.getAttribute('aria-invalid')) e.target.removeAttribute('aria-invalid');
  if (e.target.name === 'price') { $('#plan-confirm').innerHTML = ''; }
});
