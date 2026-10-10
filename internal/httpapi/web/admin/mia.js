/* Aba Mia (só administradores): textos, campos, persona, segmentos, funil, voz, histórico e teste.
   Edita uma cópia (rascunho) da configuração; "Salvar" grava uma versão nova no banco e o bot passa
   a usar em até 30 s. Depende das utilidades do admin.js. */
'use strict';

const MIA = { data: null, draft: null, dirty: false, tab: 'textos', test: [], testState: 'idle', testSegment: '', audioURL: '' };
const MIA_TABS = [['textos', 'Textos'], ['campos', 'Dados coletados'], ['persona', 'Personalidade'], ['segmentos', 'Segmentos'],
  ['funil', 'Funil'], ['voz', 'Voz'], ['testar', 'Testar a Mia'], ['historico', 'Histórico']];
const STATE_LABEL = { onboarding: 'Conhecendo o negócio (onboarding)', idle: 'Livre (idle)', collecting: 'Coletando dados (collecting)',
  awaiting_proposal: 'Esperando proposta', awaiting_approval: 'Esperando aprovação', awaiting_creatives: 'Esperando anúncios' };
const SAMPLE = { link: 'https://meuanuncio.io/c/AbC123xYz789', campos: 'CPF, e-mail', nome: 'Ana', email: 'ana@gmail.com' };

const clone = o => JSON.parse(JSON.stringify(o));
const getPath = (o, path) => path.split('.').reduce((x, k) => x?.[k], o);
function setPath(o, path, v) { const ks = path.split('.'); const last = ks.pop(); const t = ks.reduce((x, k) => x[k], o); t[last] = v; }
const fill = t => String(t || '').replace(/\{(\w+)\}/g, (m, k) => SAMPLE[k] ?? m);

views.mia = async view => {
  if (state.me.role !== 'admin') { view.innerHTML = lockwall('Só administradores mudam o que a Mia fala e como ela age.'); return; }
  if (!MIA.data || !MIA.dirty) { MIA.data = await api('GET', '/mia'); MIA.draft = clone(MIA.data.config); MIA.dirty = false; }
  view.innerHTML = `<div class="page mia-page">
    <div class="page-head"><div><h1>Mia</h1><p>Versão ${MIA.data.version || 'padrão'} em vigor · o bot passa a usar o que você salvar em até 30 segundos</p></div>
      <div class="actions"><button class="btn" type="button" data-mia-tab="testar">${icon('chat')}Testar a Mia</button></div></div>
    <div class="tabs mia-tabs" role="tablist">${MIA_TABS.map(([k, l]) => `<button type="button" role="tab" data-mia-tab="${k}" aria-selected="${MIA.tab === k}">${l}</button>`).join('')}</div>
    <div class="mia-body" id="mia-body">${miaTab()}</div>
    <div class="savebar ${MIA.dirty ? 'on' : ''}" id="mia-savebar" role="region" aria-label="Alterações não salvas">
      <span>Alterações não salvas</span><input class="field" id="mia-note" maxlength="200" placeholder="O que mudou? (opcional)" aria-label="Nota da versão">
      <button class="btn sm ghost" type="button" data-mia-discard>Descartar</button><button class="btn sm primary" type="button" data-mia-save>Salvar versão</button></div>
  </div>`;
};

function miaTab() {
  const d = MIA.draft, D = MIA.data;
  switch (MIA.tab) {
    case 'textos': return `<p class="muted small mia-intro">Mensagens fixas que a Mia manda. Os marcadores entre chaves viram o valor real (ex.: {link} vira o link do cadastro). A apresentação da Mia nas conversas fica em Personalidade.</p>
      ${D.text_specs.map(sp => `<section class="panel tpl"><div><label for="t-${sp.key}">${esc(sp.label)}</label>
        <textarea class="field" id="t-${sp.key}" rows="5" maxlength="1000" data-bind="texts.${sp.key}">${esc(d.texts[sp.key])}</textarea>
        <p class="faint small">${[...(sp.required || []).map(r => `<span class="chip info">${r} obrigatório</span>`), ...(sp.optional || []).map(r => `<span class="chip">${r}</span>`)].join(' ')}
        <button class="btn sm ghost" type="button" data-mia-reset-text="${sp.key}">Voltar ao padrão</button></p></div>
        <div class="tpl-prev" aria-label="Prévia no WhatsApp"><div class="b out" id="prev-${sp.key}">${esc(fill(d.texts[sp.key]))}<span class="t">agora ✓✓</span></div></div></section>`).join('')}`;
    case 'campos': return ['campaign', 'profile'].map(g => {
      const list = d.fields.map((f, i) => [f, i]).filter(([f]) => f.group === g).sort((a, b) => a[0].order - b[0].order);
      return `<section class="panel mia-sec"><div class="panel-head"><h2>${g === 'campaign' ? 'Dados da campanha' : 'Ficha do negócio'}</h2><span class="muted">${g === 'campaign' ? 'os obrigatórios seguram a proposta até a Mia coletar' : 'os obrigatórios precisam estar completos para o onboarding terminar'}</span></div>
        <div class="panel-body"><div class="fld-head" aria-hidden="true"><span>Ordem</span><span>Campo</span><span>Rótulo e como perguntar</span><span>Ativo</span><span>Obrigatório</span></div>
        ${list.map(([f, i], k) => `<div class="fld-row ${f.active ? '' : 'off'}">
          <span class="ord"><button class="icon-btn sm" type="button" data-mia-move="fields.${i}" data-dir="-1" aria-label="Subir" ${k === 0 ? 'disabled' : ''}>${icon('up')}</button><button class="icon-btn sm" type="button" data-mia-move="fields.${i}" data-dir="1" aria-label="Descer" ${k === list.length - 1 ? 'disabled' : ''}>${icon('down-c')}</button></span>
          <code>${esc(f.key)}</code>
          <span class="fld-txt"><input class="field" data-bind="fields.${i}.label" value="${esc(f.label)}" maxlength="120" aria-label="Rótulo de ${esc(f.key)}"><input class="field" data-bind="fields.${i}.hint" value="${esc(f.hint)}" maxlength="300" placeholder="Como perguntar (dica para a Mia)" aria-label="Como perguntar ${esc(f.key)}"></span>
          ${f.core ? `<span class="chip" title="Exigido pela proposta">${icon('lock')}sempre</span><span class="chip">${icon('lock')}sempre</span>`
            : `<label class="switch-wrap"><input type="checkbox" data-bind="fields.${i}.active" ${f.active ? 'checked' : ''}><span class="sr">Ativo</span></label><label class="switch-wrap"><input type="checkbox" data-bind="fields.${i}.required" ${f.required ? 'checked' : ''} ${f.active ? '' : 'disabled'}><span class="sr">Obrigatório</span></label>`}
        </div>`).join('')}</div></section>`;
    }).join('');
    case 'persona': return [['identity', 'Identidade', 'Quem a Mia é e como se apresenta.'], ['personality', 'Personalidade', 'Como ela pensa e se posiciona.'],
      ['communication', 'Comunicação e expressões preferidas', 'Jeito de escrever no WhatsApp, frases a preferir e a evitar.'], ['never', 'O que nunca fazer', 'Limites que valem também para o especialista (Gestor).']]
      .map(([k, l, h]) => `<section class="panel mia-sec"><div class="panel-head"><h2>${l}</h2><span class="muted">${h}</span></div><div class="panel-body">
        <textarea class="field mono" rows="${k === 'identity' ? 5 : 8}" maxlength="4000" data-bind="persona.${k}" aria-label="${l}">${esc(d.persona[k])}</textarea>
        <p class="faint small count-line"><span data-count="persona.${k}">${(d.persona[k] || '').length}</span>/4000</p></div></section>`).join('') +
      `<p class="muted small mia-intro">${icon('lock')} Fixo no código e fora do alcance deste editor: formato da resposta (JSON), intents, regras de aprovação, limites de orçamento e a regra de que o texto do cliente é dado, nunca instrução.</p>`;
    case 'segmentos': return `<p class="muted small mia-intro">Cada módulo entra no prompt só para clientes daquele segmento (casa pelo nome ou pelas palavras-chave com o segmento/nicho do cadastro).</p>
      ${d.segments.map((s, i) => `<section class="panel mia-sec ${s.active ? '' : 'off'}"><div class="panel-head"><h2>${esc(s.name || 'Novo segmento')}</h2>
        <div class="actions"><label class="switch-wrap">Ativo <input type="checkbox" data-bind="segments.${i}.active" ${s.active ? 'checked' : ''}></label><button class="btn sm ghost" type="button" data-mia-remove="segments.${i}">${icon('trash')}Remover</button></div></div>
        <div class="panel-body form-grid"><label>Nome<input class="field" data-bind="segments.${i}.name" value="${esc(s.name)}" maxlength="60"></label>
        <label>Palavras-chave (vírgula)<input class="field" data-bind-list="segments.${i}.keywords" value="${esc((s.keywords || []).join(', '))}" placeholder="imóveis, corretor, imobiliária"></label>
        <label class="full">Estratégia para este segmento<textarea class="field" rows="6" maxlength="4000" data-bind="segments.${i}.strategy">${esc(s.strategy)}</textarea></label></div></section>`).join('')}
      <button class="btn" type="button" data-mia-add="segment">${icon('plus')}Adicionar segmento</button>`;
    case 'funil': {
      const counts = D.stage_counts || {}, fields = [...new Set(d.fields.filter(f => f.active).map(f => f.key))];
      const human = counts.needs_human || 0;
      return `<p class="muted small mia-intro">Cada etapa está presa a um estado real da conversa. As instruções da etapa atual entram no prompt da Mia. Várias etapas em “Coletando dados” dividem a coleta: vale a primeira, na ordem, que ainda tem campo vazio. ${human ? `<b>${human}</b> cliente(s) em atendimento humano (fora do funil).` : ''}</p>
      ${d.stages.map((s, i) => `<section class="panel mia-sec stage ${s.active ? '' : 'off'}"><div class="panel-head"><h2><span class="stage-n num">${i + 1}</span>${esc(s.name || 'Nova etapa')}</h2>
        <div class="actions"><span class="chip ${counts[s.key] ? 'info' : ''}">${counts[s.key] || 0} cliente(s) agora</span>
          <button class="icon-btn sm" type="button" data-mia-move="stages.${i}" data-dir="-1" aria-label="Subir" ${i === 0 ? 'disabled' : ''}>${icon('up')}</button><button class="icon-btn sm" type="button" data-mia-move="stages.${i}" data-dir="1" aria-label="Descer" ${i === d.stages.length - 1 ? 'disabled' : ''}>${icon('down-c')}</button>
          <label class="switch-wrap">Ativa <input type="checkbox" data-bind="stages.${i}.active" ${s.active ? 'checked' : ''}></label><button class="btn sm ghost" type="button" data-mia-remove="stages.${i}" aria-label="Remover etapa">${icon('trash')}</button></div></div>
        <div class="panel-body form-grid">
          <label>Nome<input class="field" data-bind="stages.${i}.name" value="${esc(s.name)}" maxlength="60"></label>
          <label>Estado da conversa<select class="field" data-bind="stages.${i}.state">${D.states.map(st => `<option value="${st}" ${st === s.state ? 'selected' : ''}>${esc(STATE_LABEL[st] || st)}</option>`).join('')}</select></label>
          <label class="full">Objetivo<input class="field" data-bind="stages.${i}.objective" value="${esc(s.objective)}" maxlength="300"></label>
          <label class="full">Instruções para a Mia nesta etapa<textarea class="field" rows="4" maxlength="2000" data-bind="stages.${i}.instructions">${esc(s.instructions)}</textarea></label>
          <div class="full"><span class="lbl">Campos que coleta</span><div class="chips-pick">${fields.map(k => `<label class="pick"><input type="checkbox" data-stage-field="${i}" value="${k}" ${(s.fields || []).includes(k) ? 'checked' : ''}>${esc(k)}</label>`).join('')}</div></div>
          <label class="full">Critério de saída<input class="field" data-bind="stages.${i}.exit" value="${esc(s.exit)}" maxlength="300"></label>
        </div></section>`).join('')}
      <button class="btn" type="button" data-mia-add="stage">${icon('plus')}Adicionar etapa de coleta</button>`;
    }
    case 'voz': return `<section class="panel mia-sec"><div class="panel-head"><h2>Voz da Mia</h2><span class="muted">respostas em áudio quando o cliente manda áudio</span></div>
      <div class="panel-body form-grid">
        <label class="full switch-wrap big">Responder em voz <input type="checkbox" data-bind="voice.enabled" ${d.voice.enabled ? 'checked' : ''}></label>
        <label>Voz<select class="field" data-bind="voice.name"><option value="">Padrão do servidor${D.env_voice ? ' (' + esc(D.env_voice) + ')' : ''}</option>${D.voices.map(v => `<option value="${v}" ${v === d.voice.name ? 'selected' : ''}>${esc(v.replace('pt-BR-', '').replace('Neural', ''))}</option>`).join('')}</select></label>
        <label>Frase da amostra<input class="field" id="voice-text" maxlength="200" value="Oi! Eu sou a Mia, do Meu Anúncio. Vamos montar seus anúncios juntos?"></label>
        <div class="full actions"><button class="btn" type="button" data-mia-sample ${D.voice_enabled ? '' : 'disabled'}>${icon('play')}Ouvir amostra</button>
          <span class="faint small">${D.voice_enabled ? 'Gera um áudio curto pelo mesmo caminho do bot e gasta crédito de TTS.' : 'Amostra indisponível: a api não tem TTS_MODEL e OPENROUTER_API_KEY.'}</span></div>
        <audio class="full" id="voice-audio" controls hidden></audio>
        <p class="full muted small">Sem TTS_MODEL no worker, a Mia responde só em texto mesmo com a voz ligada aqui.</p>
      </div></section>`;
    case 'testar': return `<section class="panel mia-sec"><div class="panel-head"><h2>Testar a Mia</h2><span class="muted">usa a configuração em edição (mesmo sem salvar) e a mesma IA do bot; não manda WhatsApp</span></div>
      <div class="panel-body"><div class="form-grid"><label>Estado da conversa<select class="field" id="test-state">${D.states.map(st => `<option value="${st}" ${st === MIA.testState ? 'selected' : ''}>${esc(STATE_LABEL[st] || st)}</option>`).join('')}</select></label>
        <label>Segmento do cliente de teste<input class="field" id="test-segment" value="${esc(MIA.testSegment)}" placeholder="ex.: Imobiliária"></label></div>
        <div class="msgs test-msgs" id="test-msgs">${MIA.test.length ? MIA.test.map(m => `<div class="b ${m.from_client ? 'in' : 'out'}">${m.from_client ? '' : '<span class="who-ai">Mia · teste</span>'}${esc(m.text)}${m.meta ? `<span class="t">${esc(m.meta)}</span>` : ''}</div>`).join('') : '<p class="faint small">Escreva como se fosse o cliente.</p>'}</div>
        <form class="composer" id="test-form"><textarea class="field" id="test-text" rows="1" maxlength="1000" placeholder="Mensagem do cliente…" aria-label="Mensagem de teste"></textarea><button class="btn primary" type="submit" id="test-send" ${D.test_enabled ? '' : 'disabled'}>${icon('send')}Enviar</button></form>
        <p class="faint small">${D.test_enabled ? 'Cada mensagem gasta crédito de IA (entra no custo do mês).' : 'Teste indisponível: a api não tem OPENROUTER_API_KEY e ATTENDANT_MODEL.'} <button class="btn sm ghost" type="button" data-mia-clear-test>Limpar conversa</button></p></div></section>`;
    case 'historico': return `<section class="panel mia-sec"><div class="panel-head"><h2>Versões</h2><button class="btn sm danger" type="button" data-mia-restore>${icon('refresh')}Restaurar padrão</button></div>
      <div class="panel-body">${D.versions.length ? D.versions.map(v => `<div class="team-row"><span class="mini-av num">v${v.version}</span><div><b>${esc(v.note || 'sem nota')}</b><small>${esc(v.created_by)} · ${esc(dt(v.created_at))}</small></div>
        ${v.version === D.version ? '<span class="chip ok">em vigor</span><span></span>' : `<span></span><button class="btn sm" type="button" data-mia-revert="${v.version}">${icon('history')}Voltar a esta</button>`}</div>`).join('') : '<p class="muted">Nenhuma versão salva: vale o padrão do código.</p>'}
      <p class="muted small">Voltar a uma versão (ou restaurar o padrão) grava uma versão nova: dá para desfazer de novo.</p></div></section>`;
  }
  return '';
}

function miaRerender() { const b = $('#mia-body'); if (b) { b.innerHTML = miaTab(); applyDyn(b); } $$('[data-mia-tab]').forEach(t => t.getAttribute('role') === 'tab' && t.setAttribute('aria-selected', String(t.dataset.miaTab === MIA.tab))); }
function miaDirty() { MIA.dirty = true; $('#mia-savebar')?.classList.add('on'); }

document.addEventListener('input', e => {
  const t = e.target;
  if (!MIA.draft || !t.closest('#mia-body')) return;
  if (t.dataset.bind) {
    const v = t.type === 'checkbox' ? t.checked : t.value;
    setPath(MIA.draft, t.dataset.bind, v);
    const m = t.dataset.bind.match(/^texts\.(\w+)$/);
    if (m) { const p = $('#prev-' + m[1]); if (p) p.innerHTML = esc(fill(v)) + '<span class="t">agora ✓✓</span>'; }
    const c = $(`[data-count="${t.dataset.bind}"]`); if (c) c.textContent = String(v.length);
    if (t.type === 'checkbox' && /\.active$/.test(t.dataset.bind) && t.dataset.bind.startsWith('fields.')) { const f = getPath(MIA.draft, t.dataset.bind.replace(/\.active$/, '')); if (!f.active) f.required = false; miaRerender(); }
    miaDirty(); return;
  }
  if (t.dataset.bindList) { setPath(MIA.draft, t.dataset.bindList, t.value.split(',').map(s => s.trim()).filter(Boolean)); miaDirty(); return; }
  if (t.dataset.stageField !== undefined) {
    const s = MIA.draft.stages[+t.dataset.stageField];
    s.fields = (s.fields || []).filter(k => k !== t.value);
    if (t.checked) s.fields.push(t.value);
    miaDirty();
  }
});
document.addEventListener('change', e => { if (e.target.id === 'test-state') MIA.testState = e.target.value; if (e.target.id === 'test-segment') MIA.testSegment = e.target.value; });

function slugify(s) { return (s || 'item').toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 30) || 'item'; }
function uniqueKey(list, base) { let k = slugify(base), n = 2; while (list.some(x => x.key === k)) k = slugify(base) + '-' + n++; return k; }

async function miaWrite(path, body, okMsg) {
  try {
    MIA.data = await api('POST', path, body);
    MIA.draft = clone(MIA.data.config); MIA.dirty = false;
    toast(okMsg, 'ok', 'check');
    render(true);
  } catch (e) {
    if (e.message === 'versao_desatualizada') toast('Alguém salvou outra versão enquanto você editava. Descarte e abra de novo para não perder o que a outra pessoa fez.', 'warn', 'alert');
    else if (e.message === 'dados_invalidos') toast('Confira: ' + (e.data.detail || 'dados inválidos'), 'err', 'alert');
    else toast(errMsg(e), 'err', 'alert');
  }
}

document.addEventListener('click', async e => {
  const t = e.target.closest('button'); if (!t || !MIA.draft || !t.closest('.mia-page')) return;
  const ds = t.dataset, d = MIA.draft;
  if (ds.miaTab) { MIA.tab = ds.miaTab; miaRerender(); return; }
  if (ds.miaResetText) { d.texts[ds.miaResetText] = MIA.data.defaults.texts[ds.miaResetText]; miaDirty(); miaRerender(); return; }
  if (ds.miaMove) {
    const [list, idx] = ds.miaMove.split('.'), i = +idx, dir = +ds.dir;
    if (list === 'stages') { const j = i + dir; if (j < 0 || j >= d.stages.length) return; [d.stages[i], d.stages[j]] = [d.stages[j], d.stages[i]]; d.stages.forEach((s, k) => { s.order = k + 1; }); }
    else {
      const f = d.fields[i], same = d.fields.filter(x => x.group === f.group).sort((a, b) => a.order - b.order);
      const k = same.indexOf(f), other = same[k + dir]; if (!other) return;
      same.splice(k, 1); same.splice(k + dir, 0, f); same.forEach((x, n) => { x.order = n + 1; });
    }
    miaDirty(); miaRerender(); return;
  }
  if (ds.miaRemove) { const [list, i] = ds.miaRemove.split('.'); d[list].splice(+i, 1); miaDirty(); miaRerender(); return; }
  if (ds.miaAdd === 'segment') { d.segments.push({ key: uniqueKey(d.segments, 'segmento'), name: '', keywords: [], strategy: '', active: true }); miaDirty(); miaRerender(); return; }
  if (ds.miaAdd === 'stage') { d.stages.push({ key: uniqueKey(d.stages, 'coleta-extra'), name: 'Nova etapa de coleta', state: 'collecting', objective: '', instructions: '', fields: [], exit: '', order: d.stages.length + 1, active: true }); miaDirty(); miaRerender(); return; }
  if (ds.miaDiscard !== undefined) { MIA.draft = clone(MIA.data.config); MIA.dirty = false; render(true); return; }
  if (ds.miaSave !== undefined) {
    d.segments.forEach(s => { if (!s.key || /^segmento/.test(s.key)) s.key = uniqueKey(d.segments.filter(x => x !== s), s.name); });
    t.classList.add('loading');
    await miaWrite('/mia', { config: d, base_version: MIA.data.version, note: $('#mia-note')?.value || '' }, 'Versão salva. O bot passa a usar em até 30 segundos.');
    t.classList.remove('loading'); return;
  }
  if (ds.miaRestore !== undefined) {
    if (t.dataset.armed !== '1') { t.dataset.armed = '1'; t.textContent = 'Confirmar: restaurar o padrão?'; setTimeout(() => { if (t.isConnected) { t.dataset.armed = ''; t.innerHTML = icon('refresh') + 'Restaurar padrão'; } }, 4000); return; }
    await miaWrite('/mia/restore', { base_version: MIA.data.version }, 'Padrão restaurado (gravado como versão nova).'); return;
  }
  if (ds.miaRevert) { await miaWrite('/mia/revert', { version: +ds.miaRevert, base_version: MIA.data.version }, 'Voltou à versão ' + ds.miaRevert + ' (gravado como versão nova).'); return; }
  if (ds.miaSample !== undefined) {
    const r = await act(t, () => api('POST', '/mia/voice-sample', { voice: d.voice.name, text: $('#voice-text').value }));
    if (!r) return;
    const bytes = Uint8Array.from(atob(r.audio), c => c.charCodeAt(0));
    if (MIA.audioURL) URL.revokeObjectURL(MIA.audioURL);
    MIA.audioURL = URL.createObjectURL(new Blob([bytes], { type: r.mime }));
    const a = $('#voice-audio'); a.hidden = false; a.src = MIA.audioURL; a.play().catch(() => {});
    return;
  }
  if (ds.miaClearTest !== undefined) { MIA.test = []; miaRerender(); }
});

document.addEventListener('submit', async e => {
  if (e.target.id !== 'test-form') return;
  e.preventDefault();
  const ta = $('#test-text'), text = ta.value.trim(); if (!text) return;
  MIA.testState = $('#test-state').value; MIA.testSegment = $('#test-segment').value;
  const history = MIA.test.slice(-12).map(m => ({ from_client: m.from_client, text: m.text }));
  MIA.test.push({ from_client: true, text });
  miaRerender();
  const r = await act($('#test-send'), () => api('POST', '/mia/test', { config: MIA.draft, state: MIA.testState, segment: MIA.testSegment, message: text, history }));
  if (r) {
    const tn = r.turn;
    MIA.test.push({ from_client: false, text: tn.reply, meta: [`intent ${tn.intent}`, r.stage && 'etapa ' + r.stage, r.segment && 'segmento ' + r.segment,
      Object.keys(tn.fields || {}).length && 'campos ' + Object.keys(tn.fields).join(', '), Object.keys(tn.profile || {}).length && 'ficha ' + Object.keys(tn.profile).join(', ')].filter(Boolean).join(' · ') });
  }
  miaRerender();
  const m = $('#test-msgs'); if (m) m.scrollTop = m.scrollHeight;
  $('#test-text')?.focus();
});
document.addEventListener('keydown', e => {
  if (e.target.id === 'test-text' && e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); $('#test-form').requestSubmit(); }
});
window.addEventListener('beforeunload', e => { if (MIA.dirty) { e.preventDefault(); e.returnValue = ''; } });
