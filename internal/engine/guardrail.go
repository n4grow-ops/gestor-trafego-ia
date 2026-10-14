package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// Proteções escolhidas pelo cliente: teto de gasto por dia, custo máximo por conversa e pausa automática.
//
// Regras de ouro:
//   - Nada é ligado sem o cliente pedir (padrão desligado). A Mia oferece uma vez, depois do primeiro ATIVAR.
//   - Só pausa campanha de organização com regra explícita, e só com dados suficientes (gasto mínimo e janela
//     mínima, para não pausar por ruído). O teto diário é duro: gastar o teto já é dado suficiente.
//   - Idempotente e auditável: um evento por violação (guardrail_events), com motivo e números.
//   - O aviso respeita o silêncio das 21h às 8h (Brasília); a pausa em si acontece a qualquer hora.

const (
	// JobGuardrailCheck confere de hora em hora as campanhas no ar contra as regras dos clientes.
	JobGuardrailCheck = "guardrails.check"
	guardrailEvery    = time.Hour

	// Padrões do gasto e da janela mínimos para julgar o custo por conversa (Engine.GuardrailMin* trocam).
	defaultGuardrailMinSpendCents = 3000 // R$ 30
	defaultGuardrailMinWindow     = 24 * time.Hour
	// Quantos dias para trás o custo por conversa olha (a janela nunca começa antes da última ativação).
	guardrailLookbackDays = 7

	// Faixas aceitas nos comandos (centavos): fora delas o cliente é avisado em vez de gravar um valor sem sentido.
	minCostPerConvCents = 100      // R$ 1
	maxCostPerConvCents = 100000   // R$ 1.000
	minDailyCapCents    = 500      // R$ 5
	maxDailyCapCents    = 10000000 // R$ 100.000
)

// ---------- entendimento dos comandos do cliente (sem IA) ----------

type guardrailCmd int

const (
	gcNone guardrailCmd = iota
	gcSetCost
	gcSetCap
	gcClear
	gcAutoOn
	gcAutoOff
	gcShow
)

var guardrailSetRe = regexp.MustCompile(`^(limite|teto|custo maximo)(?: (de custo por conversa|de custo|de gasto|por conversa|por dia|diario))?(?: (?:de|em|para))? ?(?:r\$ ?)?(\d[\d.,]*)(?: reais| real)?(?: (por conversa|por dia|diario))?$`)

var guardrailWords = map[string]guardrailCmd{
	"sem limite": gcClear, "sem limites": gcClear, "sem teto": gcClear, "tirar limite": gcClear, "tirar o limite": gcClear,
	"tirar limites": gcClear, "tirar os limites": gcClear, "tirar teto": gcClear, "tirar o teto": gcClear,
	"remover limite": gcClear, "remover o limite": gcClear, "remover limites": gcClear, "remover teto": gcClear,
	"cancelar limite": gcClear, "cancelar o limite": gcClear, "desligar limite": gcClear, "desligar o limite": gcClear,
	"so avisar": gcAutoOff, "apenas avisar": gcAutoOff, "so me avise": gcAutoOff, "so avise": gcAutoOff, "so aviso": gcAutoOff,
	"nao pausar": gcAutoOff, "nao pausar sozinha": gcAutoOff, "nao pausar sozinho": gcAutoOff, "so alertar": gcAutoOff,
	"pausar sozinha": gcAutoOn, "pausar sozinho": gcAutoOn, "pausa automatica": gcAutoOn, "pausar automaticamente": gcAutoOn,
	"pode pausar sozinha": gcAutoOn, "pode pausar sozinho": gcAutoOn,
	"meu limite": gcShow, "meus limites": gcShow, "ver limite": gcShow, "ver limites": gcShow, "qual meu limite": gcShow,
	"qual o meu limite": gcShow, "meu teto": gcShow,
}

// parseGuardrailCommand reconhece só a frase curta e exata ("LIMITE 15", "TETO 100", "SEM LIMITE"...). Frase solta
// que fale de limite no meio de outro assunto segue para a Atendente.
func parseGuardrailCommand(text string) (cmd guardrailCmd, cents int64) {
	s := strings.TrimRight(normalizeName(strings.ReplaceAll(text, "*", "")), " .!?")
	if c, ok := guardrailWords[s]; ok {
		return c, 0
	}
	m := guardrailSetRe.FindStringSubmatch(s)
	if m == nil {
		return gcNone, 0
	}
	cents, ok := parseMoneyCents(m[3])
	if !ok {
		return gcSetCost, -1 // frase de limite com valor que não dá para ler: o cliente é avisado
	}
	qual := m[2] + " " + m[4]
	perConv := strings.Contains(qual, "conversa") || strings.Contains(qual, "custo")
	perDay := strings.Contains(qual, "dia") || strings.Contains(qual, "gasto")
	if !perConv && (m[1] == "teto" || (m[1] == "limite" && perDay)) {
		return gcSetCap, cents
	}
	return gcSetCost, cents
}

var thousandsRe = regexp.MustCompile(`^\d{1,3}(\.\d{3})+$`)

// parseMoneyCents lê "15", "15,50", "15.50" e "1.500,00" em centavos.
func parseMoneyCents(s string) (int64, bool) {
	s = strings.Trim(s, ".,")
	switch {
	case strings.Contains(s, ","):
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	case thousandsRe.MatchString(s):
		s = strings.ReplaceAll(s, ".", "")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || v > 1e7 {
		return 0, false
	}
	return int64(math.Round(v * 100)), true
}

// guardrailCommand trata os comandos de proteção. handled=false segue o fluxo normal.
// Só vale com campanha na Meta e nas etapas livres da conversa: "limite 50" no meio do briefing é orçamento.
func (e *Engine) guardrailCommand(ctx context.Context, ident store.Identity, conv store.Conversation, text string) (bool, error) {
	cmd, cents := parseGuardrailCommand(text)
	if cmd == gcNone {
		return false, nil
	}
	if conv.State != store.StateIdle && conv.State != store.StateCollecting {
		return false, nil
	}
	if _, err := e.pubs().LatestPublication(ctx, ident.OrgID); errors.Is(err, store.ErrNotFound) {
		return false, nil // sem campanha na Meta ainda: a Atendente conduz
	} else if err != nil {
		return true, err
	}
	if !ident.CanApprove() {
		e.audit(ctx, ident.OrgID, member(ident), "guardrail_refused", map[string]any{"text": text})
		return true, e.send(ctx, conv, msgCampaignNotAllowed)
	}
	noteAction(ctx, "limite do cliente (sem IA)")
	rule, _, err := e.Store.GetGuardrail(ctx, ident.OrgID)
	if err != nil {
		return true, err
	}
	switch cmd {
	case gcSetCost, gcSetCap:
		rname, lo, hi := store.GuardrailMaxCost, int64(minCostPerConvCents), int64(maxCostPerConvCents)
		if cmd == gcSetCap {
			rname, lo, hi = store.GuardrailDailyCap, minDailyCapCents, maxDailyCapCents
		}
		if cents < lo || cents > hi {
			return true, e.send(ctx, conv, msgGuardrailBadValue)
		}
		if err := e.Store.SetGuardrailLimit(ctx, ident.OrgID, rname, cents); err != nil {
			return true, err
		}
		// Quem define um limite quer a pausa automática, salvo se já pediu só aviso antes.
		e.audit(ctx, ident.OrgID, member(ident), "guardrail_set", map[string]any{"rule": rname, "cents": cents, "auto_pause": !rule.Active() || rule.AutoPause})
		act := msgGuardrailActPause
		if rule.Active() && !rule.AutoPause {
			act = msgGuardrailActNotify
		}
		if cmd == gcSetCap {
			return true, e.send(ctx, conv, fmt.Sprintf(msgGuardrailCapSet, brlCents(cents), act))
		}
		return true, e.send(ctx, conv, fmt.Sprintf(msgGuardrailCostSet, brlCents(cents), act, brlCents(e.guardrailMinSpend()), durationText(e.guardrailMinWindow())))
	case gcClear:
		if err := e.Store.ClearGuardrail(ctx, ident.OrgID); err != nil {
			return true, err
		}
		e.audit(ctx, ident.OrgID, member(ident), "guardrail_cleared", map[string]any{})
		return true, e.send(ctx, conv, msgGuardrailCleared)
	case gcAutoOn, gcAutoOff:
		if !rule.Active() {
			return true, e.send(ctx, conv, msgGuardrailNoLimit)
		}
		if err := e.Store.SetGuardrailAutoPause(ctx, ident.OrgID, cmd == gcAutoOn); err != nil {
			return true, err
		}
		e.audit(ctx, ident.OrgID, member(ident), "guardrail_auto_pause", map[string]any{"auto_pause": cmd == gcAutoOn})
		if cmd == gcAutoOn {
			return true, e.send(ctx, conv, msgGuardrailAutoOn)
		}
		return true, e.send(ctx, conv, msgGuardrailAutoOff)
	case gcShow:
		return true, e.send(ctx, conv, e.guardrailSummary(rule))
	}
	return false, nil
}

func (e *Engine) guardrailSummary(r store.GuardrailRule) string {
	if !r.Active() {
		return msgGuardrailNone
	}
	var lines []string
	if r.MaxCostPerConvCents != nil {
		lines = append(lines, "• Custo máximo por conversa: "+brlCents(*r.MaxCostPerConvCents))
	}
	if r.DailyCapCents != nil {
		lines = append(lines, "• Teto de gasto por dia: "+brlCents(*r.DailyCapCents))
	}
	how := "eu pauso a campanha sozinha e te aviso"
	if !r.AutoPause {
		how = "eu só te aviso (a campanha continua no ar)"
	}
	return "Seus limites:\n" + strings.Join(lines, "\n") + "\n\nSe passar, " + how + ". Pra tirar: *SEM LIMITE*."
}

// durationText: "24 h", "2 dias".
func durationText(d time.Duration) string {
	h := int(d.Hours())
	if h >= 48 && h%24 == 0 {
		return fmt.Sprintf("%d dias", h/24)
	}
	return fmt.Sprintf("%d h", max(h, 1))
}

// guardrailOffer é a oferta única, anexada à resposta do primeiro ATIVAR. Vazio = já ofereceu ou o cliente já definiu.
func (e *Engine) guardrailOffer(ctx context.Context, orgID int64) string {
	first, err := e.Store.MarkGuardrailOffered(ctx, orgID)
	if err != nil {
		e.Log.Warn("oferta de limite não registrada", "org_id", orgID, "erro", err)
		return ""
	}
	if !first {
		return ""
	}
	e.audit(ctx, orgID, "system", "guardrail_offered", map[string]any{})
	return msgGuardrailOffer
}

func (e *Engine) guardrailMinSpend() int64 {
	if e.GuardrailMinSpendCents > 0 {
		return e.GuardrailMinSpendCents
	}
	return defaultGuardrailMinSpendCents
}

func (e *Engine) guardrailMinWindow() time.Duration {
	if e.GuardrailMinWindow > 0 {
		return e.GuardrailMinWindow
	}
	return defaultGuardrailMinWindow
}

// ---------- avaliação das regras (pura) ----------

// guardrailData é o que se sabe da campanha na hora de julgar.
type guardrailData struct {
	Now         time.Time
	ActivatedAt *time.Time
	// Gasto de hoje (dia de Brasília) e o resultado desde o início da janela do custo por conversa.
	TodaySpendCents int64
	HaveToday       bool
	WindowSince     time.Time // primeiro dia da janela do custo
	WindowSpend     int64
	WindowConvs     int64
	HaveWindow      bool
}

// guardrailViolation é uma regra do cliente que foi passada, com os números e a frase explicando.
type guardrailViolation struct {
	Rule           string
	WindowStart    time.Time // chave do evento: uma violação por janela
	ThresholdCents int64
	ObservedCents  int64
	SpendCents     int64
	Conversations  int64
	Reason         string
}

// evaluateGuardrails devolve as regras violadas com dados suficientes. Sem regra do cliente, sem dado ou com
// pouco dado, devolve vazio: nunca age por palpite.
func evaluateGuardrails(rule store.GuardrailRule, d guardrailData, minSpend int64, minWindow time.Duration) []guardrailViolation {
	var out []guardrailViolation
	if limit := rule.DailyCapCents; limit != nil && d.HaveToday && d.TodaySpendCents >= *limit {
		out = append(out, guardrailViolation{Rule: store.GuardrailDailyCap, WindowStart: brtMidnight(d.Now),
			ThresholdCents: *limit, ObservedCents: d.TodaySpendCents, SpendCents: d.TodaySpendCents,
			Reason: fmt.Sprintf("Hoje ela já gastou %s, e o teto de gasto por dia que você escolheu é %s.", brlCents(d.TodaySpendCents), brlCents(*limit))})
	}
	if cost := rule.MaxCostPerConvCents; cost != nil && d.HaveWindow && d.ActivatedAt != nil &&
		d.Now.Sub(*d.ActivatedAt) >= minWindow && d.WindowSpend >= minSpend {
		since := d.WindowSince.Format("02/01")
		switch {
		case d.WindowConvs == 0 && d.WindowSpend > *cost:
			out = append(out, guardrailViolation{Rule: store.GuardrailMaxCost, WindowStart: *d.ActivatedAt,
				ThresholdCents: *cost, ObservedCents: d.WindowSpend, SpendCents: d.WindowSpend,
				Reason: fmt.Sprintf("Desde %s ela gastou %s e ainda não gerou nenhuma conversa, e o limite que você escolheu é %s por conversa.",
					since, brlCents(d.WindowSpend), brlCents(*cost))})
		case d.WindowConvs > 0 && d.WindowSpend/d.WindowConvs > *cost:
			per := d.WindowSpend / d.WindowConvs
			out = append(out, guardrailViolation{Rule: store.GuardrailMaxCost, WindowStart: *d.ActivatedAt,
				ThresholdCents: *cost, ObservedCents: per, SpendCents: d.WindowSpend, Conversations: d.WindowConvs,
				Reason: fmt.Sprintf("Desde %s ela gastou %s e gerou %s: dá %s por conversa, e o limite que você escolheu é %s.",
					since, brlCents(d.WindowSpend), conversationsText(d.WindowConvs), brlCents(per), brlCents(*cost))})
		}
	}
	return out
}

func conversationsText(n int64) string {
	if n == 1 {
		return "1 conversa"
	}
	return fmt.Sprintf("%d conversas", n)
}

// brtMidnight é a meia-noite de Brasília do dia de t.
func brtMidnight(t time.Time) time.Time {
	y, m, d := t.In(brt).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, brt)
}

// brtDay é o dia de Brasília de t como data pura (o formato que a Meta pede no período).
func brtDay(t time.Time) time.Time {
	y, m, d := t.In(brt).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// guardrailWindowSince é o primeiro dia da janela do custo: o dia da última ativação, no máximo 7 dias atrás.
func guardrailWindowSince(now time.Time, activatedAt time.Time) time.Time {
	since := brtDay(activatedAt)
	if floor := brtDay(now).AddDate(0, 0, -(guardrailLookbackDays - 1)); since.Before(floor) {
		since = floor
	}
	return since
}

// quietDelay adia o aviso para as 8h quando cai entre 21h e 8h de Brasília. Fora disso, sai na hora.
func quietDelay(now time.Time) time.Duration {
	t := now.In(brt)
	y, m, d := t.Date()
	switch {
	case t.Hour() >= 21:
		return time.Date(y, m, d+1, 8, 0, 0, 0, brt).Sub(now)
	case t.Hour() < 8:
		return time.Date(y, m, d, 8, 0, 0, 0, brt).Sub(now)
	}
	return 0
}

// ---------- o job de hora em hora ----------

// EnsureGuardrailCheck garante a conferência agendada (chamado quando o worker sobe).
func (e *Engine) EnsureGuardrailCheck(ctx context.Context) error {
	pending, err := e.Store.HasPendingKind(ctx, JobGuardrailCheck)
	if err != nil || pending {
		return err
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobGuardrailCheck, Payload: map[string]string{}, Delay: 5 * time.Minute})
}

// scheduleGuardrailNext agenda a próxima hora. Já havendo outro job futuro (retomada depois de queda), não duplica.
func (e *Engine) scheduleGuardrailNext(ctx context.Context) error {
	n, err := e.Store.CountPendingKind(ctx, JobGuardrailCheck)
	if err != nil {
		return err
	}
	if n > 1 { // este job (ainda pendente, rodando) e mais um
		return nil
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobGuardrailCheck, Payload: map[string]string{}, Delay: guardrailEvery})
}

// guardrailCheck lê o gasto e as conversas das campanhas no ar e pausa (ou avisa) quando uma regra escolhida
// pelo cliente foi violada. Falha numa campanha não impede as outras; só erro de agendamento volta ao outbox.
func (e *Engine) guardrailCheck(ctx context.Context) error {
	if err := e.guardrailRun(ctx); err != nil {
		e.Log.Error("conferência das proteções falhou", "erro", err)
	}
	return e.scheduleGuardrailNext(ctx)
}

func (e *Engine) guardrailRun(ctx context.Context) error {
	ir, ok := e.Publisher.(meta.RangeInsightsReader)
	if !ok || e.MetaTokenKey == nil {
		return nil
	}
	rules, err := e.Store.ActiveGuardrails(ctx)
	if err != nil {
		return err
	}
	tokens := map[int64]string{}
	tokenOf := func(orgID int64) (string, bool) {
		if t, ok := tokens[orgID]; ok {
			return t, t != ""
		}
		tokens[orgID] = ""
		_, sealed, err := e.pubs().MetaTokenSealed(ctx, orgID)
		if err != nil {
			return "", false
		}
		raw, err := e.MetaTokenKey.Open(sealed)
		if err != nil {
			return "", false
		}
		tokens[orgID] = string(raw)
		return tokens[orgID], true
	}
	// Antes de olhar de novo, termina o que ficou pela metade (ex.: a Meta estava fora do ar na hora da pausa).
	e.guardrailFinishOpen(ctx, rules, tokenOf)
	if len(rules) == 0 {
		return nil
	}

	pubs, err := e.pubs().ActivePublications(ctx)
	if err != nil {
		return err
	}
	for _, p := range pubs {
		rule, ok := rules[p.OrgID]
		if !ok {
			continue // sem regra explícita do cliente: nunca pausa
		}
		token, ok := tokenOf(p.OrgID)
		if !ok {
			continue
		}
		e.guardrailPublication(ctx, ir, token, p, rule)
	}
	return nil
}

func (e *Engine) guardrailPublication(ctx context.Context, ir meta.RangeInsightsReader, token string, p store.Publication, rule store.GuardrailRule) {
	now := e.now()
	today := brtDay(now)
	d := guardrailData{Now: now, ActivatedAt: p.ActivatedAt}
	// Só pergunta à Meta o que a regra precisa: o custo só vale depois da janela mínima.
	if rule.MaxCostPerConvCents != nil && p.ActivatedAt != nil && now.Sub(*p.ActivatedAt) >= e.guardrailMinWindow() {
		d.WindowSince = guardrailWindowSince(now, *p.ActivatedAt)
		in, err := ir.CampaignInsightsRange(ctx, token, p.CampaignID, d.WindowSince, today)
		if err != nil {
			e.Log.Warn("proteção: resultado da campanha não lido", "campaign_id", p.CampaignID, "erro", err)
			return // sem dado não há decisão
		}
		d.WindowSpend, d.WindowConvs, d.HaveWindow = in.SpendCents, in.Conversations, true
		if d.WindowSince.Equal(today) {
			d.TodaySpendCents, d.HaveToday = in.SpendCents, true
		}
	}
	if rule.DailyCapCents != nil && !d.HaveToday {
		in, err := ir.CampaignInsightsRange(ctx, token, p.CampaignID, today, today)
		if err != nil {
			e.Log.Warn("proteção: gasto de hoje não lido", "campaign_id", p.CampaignID, "erro", err)
			return
		}
		d.TodaySpendCents, d.HaveToday = in.SpendCents, true
	}
	for _, v := range evaluateGuardrails(rule, d, e.guardrailMinSpend(), e.guardrailMinWindow()) {
		ev, _, err := e.Store.OpenGuardrailEvent(ctx, store.GuardrailEvent{OrgID: p.OrgID, PublicationID: p.ID, Rule: v.Rule,
			WindowStart: v.WindowStart, ThresholdCents: v.ThresholdCents, ObservedCents: v.ObservedCents, SpendCents: v.SpendCents,
			Conversations: v.Conversations, Reason: v.Reason, AutoPause: rule.AutoPause})
		if err != nil {
			e.Log.Error("proteção: evento não registrado", "publication_id", p.ID, "erro", err)
			return
		}
		if ev.CompletedAt != nil {
			continue // essa violação já foi tratada (pausada ou avisada): não repete
		}
		paused, err := e.guardrailComplete(ctx, token, p, ev, rule.AutoPause)
		if err != nil {
			e.Log.Error("proteção: não concluída; tenta de novo na próxima hora", "publication_id", p.ID, "erro", err)
			return
		}
		if paused {
			return // campanha parada: as outras regras ficam para quando ela voltar
		}
	}
}

// guardrailComplete pausa a campanha (se for pausa automática), fecha o evento e avisa o cliente, uma vez.
// Devolve paused=true quando a campanha ficou pausada por esta violação.
func (e *Engine) guardrailComplete(ctx context.Context, token string, p store.Publication, ev store.GuardrailEvent, autoPause bool) (bool, error) {
	action := "notified"
	if autoPause {
		if p.Status == "active" {
			if err := e.Publisher.SetStatus(ctx, token, p.CampaignID, "PAUSED"); err != nil {
				return false, fmt.Errorf("pausa na Meta: %w", err)
			}
			if err := e.pubs().SetPublicationStatus(ctx, p.ID, "paused"); err != nil && !errors.Is(err, store.ErrNotFound) {
				return false, err
			}
		}
		action = "paused"
	}
	notice := e.guardrailNotice(ctx, p, ev, action)
	done, err := e.Store.CompleteGuardrailEvent(ctx, ev.ID, action, notice)
	if err != nil {
		return false, err
	}
	if done {
		e.audit(ctx, p.OrgID, "system", "guardrail_"+action, map[string]any{"event_id": ev.ID, "publication_id": p.ID,
			"campaign_id": p.CampaignID, "rule": ev.Rule, "threshold_cents": ev.ThresholdCents, "observed_cents": ev.ObservedCents,
			"spend_cents": ev.SpendCents, "conversations": ev.Conversations, "reason": ev.Reason})
	}
	return action == "paused", nil
}

// guardrailNotice monta o aviso ao dono (nil se não houver conversa para mandar). Entre 21h e 8h espera as 8h.
func (e *Engine) guardrailNotice(ctx context.Context, p store.Publication, ev store.GuardrailEvent, action string) *store.JobSpec {
	conv, err := e.Store.OwnerConversation(ctx, p.OrgID)
	if err != nil {
		e.Log.Warn("proteção: sem conversa do dono para avisar", "org_id", p.OrgID, "erro", err)
		return nil
	}
	name := "sua campanha"
	if data, err := e.pubs().PublishData(ctx, p.DraftID); err == nil && strings.TrimSpace(data.ProjectName) != "" {
		name = data.ProjectName
	}
	text := fmt.Sprintf(msgGuardrailPaused, name, ev.Reason)
	short := fmt.Sprintf("pausei a campanha %s: %s", name, ev.Reason)
	if action != "paused" {
		text = fmt.Sprintf(msgGuardrailAlert, name, ev.Reason)
		short = fmt.Sprintf("a campanha %s passou do limite: %s", name, ev.Reason)
	}
	job := systemSendJob(conv.ID, conv.ChatID, text, &whatsapp.Template{Name: TplGuardrail, Params: []string{templateParam(short)}})
	job.Delay = quietDelay(e.now())
	return &job
}

// guardrailFinishOpen retoma os eventos que ficaram abertos. Regra tirada pelo cliente cancela; campanha que já
// não está no ar (o cliente pausou, a Meta reprovou) fecha sem aviso.
func (e *Engine) guardrailFinishOpen(ctx context.Context, rules map[int64]store.GuardrailRule, tokenOf func(int64) (string, bool)) {
	open, err := e.Store.OpenGuardrailEvents(ctx)
	if err != nil {
		e.Log.Warn("proteção: eventos abertos não lidos", "erro", err)
		return
	}
	for _, ev := range open {
		p, err := e.pubs().GetPublication(ctx, ev.PublicationID)
		if errors.Is(err, store.ErrNotFound) {
			_, _ = e.Store.CompleteGuardrailEvent(ctx, ev.ID, "skipped", nil)
			continue
		}
		if err != nil {
			continue
		}
		rule, ok := rules[ev.OrgID]
		switch {
		case !ok:
			_, _ = e.Store.CompleteGuardrailEvent(ctx, ev.ID, "cancelled", nil) // o cliente tirou a regra
		case p.Status != "active":
			_, _ = e.Store.CompleteGuardrailEvent(ctx, ev.ID, "skipped", nil)
		default:
			token, ok := tokenOf(ev.OrgID)
			if !ok {
				continue
			}
			if _, err := e.guardrailComplete(ctx, token, p, ev, rule.AutoPause); err != nil {
				e.Log.Warn("proteção: retomada falhou", "event_id", ev.ID, "erro", err)
			}
		}
	}
}
