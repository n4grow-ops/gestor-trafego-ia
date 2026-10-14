package engine_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// guardPublisher é o fakePublisher com as leituras de período (proteções) e da conta (diagnóstico),
// e uma falha opcional ao pausar na Meta.
type guardPublisher struct {
	*fakePublisher
	gmu      sync.Mutex
	rng      meta.Insights
	rngErr   error
	rngCalls []string
	setErr   error

	total     meta.AdPerformance
	ads       []meta.AdPerformance
	active    []meta.ActiveAd
	accErr    error
	adsErr    error
	activeErr error
	reads     int
}

func (g *guardPublisher) CampaignInsightsRange(_ context.Context, _, campaignID string, since, until time.Time) (meta.Insights, error) {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	g.rngCalls = append(g.rngCalls, campaignID+" "+since.Format("2006-01-02")+".."+until.Format("2006-01-02"))
	return g.rng, g.rngErr
}

func (g *guardPublisher) SetStatus(ctx context.Context, token, campaignID, status string) error {
	g.gmu.Lock()
	err := g.setErr
	g.gmu.Unlock()
	if err != nil {
		return err
	}
	return g.fakePublisher.SetStatus(ctx, token, campaignID, status)
}

func (g *guardPublisher) AccountInsights(context.Context, string, string, time.Time, time.Time) (meta.AdPerformance, error) {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	g.reads++
	return g.total, g.accErr
}

func (g *guardPublisher) AdInsights(context.Context, string, string, time.Time, time.Time) ([]meta.AdPerformance, error) {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	return g.ads, g.adsErr
}

func (g *guardPublisher) ActiveAds(context.Context, string, string) ([]meta.ActiveAd, error) {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	return g.active, g.activeErr
}

func (g *guardPublisher) rangeCalls() int {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	return len(g.rngCalls)
}

func (g *guardPublisher) paused() int {
	g.fakePublisher.mu.Lock()
	defer g.fakePublisher.mu.Unlock()
	n := 0
	for _, s := range g.fakePublisher.statuses {
		if s == "C1:PAUSED" {
			n++
		}
	}
	return n
}

// newGuardHarness sobe o projeto publicado e pausado, com o publicador das proteções no lugar.
func newGuardHarness(t *testing.T) (*harness, *guardPublisher, *fakePubs) {
	t.Helper()
	h, pub, _ := publishWith(t, &meta.Funding{Status: 1, HasSource: true, AvailableCents: -1})
	gp := &guardPublisher{fakePublisher: pub}
	h.eng.Publisher = gp
	pubs := h.eng.Publications.(*fakePubs)
	// As publicações dos testes vivem em memória; os eventos das proteções apontam para a tabela de verdade.
	fp := pubs.latest()
	conn, ok, err := h.st.ActiveMeta(ctx, fp.OrgID)
	h.must(err)
	if !ok {
		t.Fatal("sem conexão Meta no teste")
	}
	_, err = h.pool.Exec(ctx, `INSERT INTO meta_publications (id, organization_id, draft_id, meta_connection_id, status, campaign_id)
		VALUES ($1, $2, $3, $4, 'paused', $5)`, fp.ID, fp.OrgID, fp.DraftID, conn.ID, fp.CampaignID)
	h.must(err)
	return h, gp, pubs
}

// activatedAt ajusta o "ativada em" da campanha (o store de verdade renova a cada ATIVAR).
func activatedAt(pubs *fakePubs, at time.Time) {
	pubs.mu.Lock()
	defer pubs.mu.Unlock()
	pubs.pubs[0].ActivatedAt = &at
}

func (h *harness) guardEvents() []store.GuardrailEvent {
	h.t.Helper()
	evs, err := h.st.GuardrailEvents(ctx, h.ident().OrgID, 20)
	h.must(err)
	return evs
}

func (h *harness) runGuardrail() {
	h.t.Helper()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobGuardrailCheck)))
	h.runJobs()
}

// activateWithRule ativa a campanha e o cliente escolhe a regra (frase curta, sem IA).
func (h *harness) activateWithRule(pubs *fakePubs, rules ...string) {
	h.t.Helper()
	h.say("ATIVAR")
	for _, r := range rules {
		h.say(r)
	}
	activatedAt(pubs, h.now.Add(-30*time.Hour))
}

// A Mia oferece uma vez, depois do primeiro ATIVAR, e sem resposta nada é ligado.
func TestGuardrailOfferedOnceAndNothingTurnedOn(t *testing.T) {
	h, _, _ := newGuardHarness(t)
	h.say("ATIVAR")
	if got := h.sentText(); !strings.Contains(got, "LIMITE 15") || !strings.Contains(got, "TETO 100") || !strings.Contains(got, "não ligo nada sozinha") {
		t.Fatalf("sem a oferta: %q", got)
	}
	r, _, err := h.st.GetGuardrail(ctx, h.ident().OrgID)
	h.must(err)
	if r.Active() {
		t.Fatalf("oferecer não pode ligar regra: %+v", r)
	}
	h.say("PAUSAR")
	h.say("ATIVAR")
	if n := strings.Count(h.sentText(), "Uma coisa opcional"); n != 1 {
		t.Fatalf("oferta apareceu %d vezes", n)
	}
	if evs := h.guardEvents(); len(evs) != 0 {
		t.Fatalf("eventos sem regra: %+v", evs)
	}
}

func TestGuardrailCommandsSetAndClear(t *testing.T) {
	h, _, _ := newGuardHarness(t)
	h.say("ATIVAR")
	ai := h.att.Calls()
	org := h.ident().OrgID

	h.say("LIMITE 15")
	if got := h.lastClient(); !strings.Contains(got, "R$ 15,00 por conversa") || !strings.Contains(got, "pauso a campanha") ||
		!strings.Contains(got, "R$ 30,00") || !strings.Contains(got, "24 h") {
		t.Fatalf("confirmação do limite: %q", got)
	}
	h.say("Teto de R$ 100,50")
	if got := h.lastClient(); !strings.Contains(got, "R$ 100,50") {
		t.Fatalf("confirmação do teto: %q", got)
	}
	r, _, _ := h.st.GetGuardrail(ctx, org)
	if r.MaxCostPerConvCents == nil || *r.MaxCostPerConvCents != 1500 || r.DailyCapCents == nil || *r.DailyCapCents != 10050 || !r.AutoPause {
		t.Fatalf("regra = %+v", r)
	}
	h.say("meus limites")
	if got := h.lastClient(); !strings.Contains(got, "R$ 15,00") || !strings.Contains(got, "R$ 100,50") {
		t.Fatalf("resumo: %q", got)
	}
	h.say("só avisar")
	if r, _, _ = h.st.GetGuardrail(ctx, org); r.AutoPause {
		t.Fatal("só avisar não gravou")
	}
	h.say("limite 5000")
	if got := h.lastClient(); !strings.Contains(got, "não me parece certo") {
		t.Fatalf("valor fora da faixa: %q", got)
	}
	if r, _, _ = h.st.GetGuardrail(ctx, org); *r.MaxCostPerConvCents != 1500 {
		t.Fatalf("valor recusado não pode mudar a regra: %+v", r)
	}
	h.say("SEM LIMITE")
	if got := h.lastClient(); !strings.Contains(got, "tirei os limites") {
		t.Fatalf("limpar: %q", got)
	}
	if r, _, _ = h.st.GetGuardrail(ctx, org); r.Active() {
		t.Fatalf("regra continua: %+v", r)
	}
	if h.att.Calls() != ai {
		t.Fatal("comandos de limite não podem gastar IA")
	}
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action LIKE 'guardrail_%'`); n < 4 {
		t.Fatalf("auditoria = %d", n)
	}
}

// Regra violada com dados suficientes: pausa na Meta, avisa uma vez com os números e registra o evento.
func TestGuardrailPausesWhenCostRuleViolated(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.activateWithRule(pubs, "LIMITE 15")
	gp.rng = meta.Insights{SpendCents: 6000, Conversations: 3} // R$ 20 por conversa
	n := h.wa.count()

	h.runGuardrail()

	if gp.paused() != 1 || pubs.latest().Status != "paused" {
		t.Fatalf("não pausou: %v / %s", gp.statuses, pubs.latest().Status)
	}
	evs := h.guardEvents()
	if len(evs) != 1 {
		t.Fatalf("eventos = %+v", evs)
	}
	ev := evs[0]
	if ev.Rule != store.GuardrailMaxCost || ev.Action != "paused" || ev.CompletedAt == nil || ev.ThresholdCents != 1500 || ev.ObservedCents != 2000 ||
		ev.SpendCents != 6000 || ev.Conversations != 3 || !strings.Contains(ev.Reason, "R$ 20,00 por conversa") {
		t.Fatalf("evento = %+v", ev)
	}
	if h.wa.count() != n+1 {
		t.Fatalf("avisos = %d", h.wa.count()-n)
	}
	got := h.lastClient()
	for _, want := range []string{"Pausei a campanha", "R$ 60,00", "3 conversas", "R$ 20,00 por conversa", "R$ 15,00", "ATIVAR"} {
		if !strings.Contains(got, want) {
			t.Errorf("aviso sem %q: %q", want, got)
		}
	}
	if k := h.count(`SELECT count(*) FROM audit_events WHERE action = 'guardrail_paused'`); k != 1 {
		t.Fatalf("auditoria da pausa = %d", k)
	}

	// idempotente: rodar de novo não pausa nem avisa outra vez
	h.runGuardrail()
	h.runGuardrail()
	if gp.paused() != 1 || h.wa.count() != n+1 || len(h.guardEvents()) != 1 {
		t.Fatalf("repetiu: pausas=%d avisos=%d eventos=%d", gp.paused(), h.wa.count()-n, len(h.guardEvents()))
	}

	// o cliente religa com ATIVAR; na mesma janela a violação já foi tratada e não pausa de novo
	h.say("ATIVAR")
	if pubs.latest().Status != "active" {
		t.Fatalf("ATIVAR não religou: %s", pubs.latest().Status)
	}
	h.runGuardrail()
	if pubs.latest().Status != "active" || gp.paused() != 1 {
		t.Fatal("pausou de novo a mesma violação depois do ATIVAR")
	}
	// reativação nova (o store renova activated_at): janela mínima de novo, sem julgar com dado velho
	activatedAt(pubs, h.now)
	calls := gp.rangeCalls()
	h.runGuardrail()
	if pubs.latest().Status != "active" || gp.rangeCalls() != calls {
		t.Fatalf("julgou logo depois de religar: status=%s leituras=%d", pubs.latest().Status, gp.rangeCalls()-calls)
	}
}

func TestGuardrailKeepsRunningWhenRuleNotViolated(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.activateWithRule(pubs, "LIMITE 15")
	gp.rng = meta.Insights{SpendCents: 6000, Conversations: 5} // R$ 12 por conversa
	n := h.wa.count()
	h.runGuardrail()
	if gp.paused() != 0 || pubs.latest().Status != "active" || len(h.guardEvents()) != 0 || h.wa.count() != n {
		t.Fatalf("agiu sem violação: pausas=%d status=%s eventos=%d", gp.paused(), pubs.latest().Status, len(h.guardEvents()))
	}
}

func TestGuardrailInsufficientDataDoesNotPause(t *testing.T) {
	t.Run("pouco gasto", func(t *testing.T) {
		h, gp, pubs := newGuardHarness(t)
		h.activateWithRule(pubs, "LIMITE 15")
		gp.rng = meta.Insights{SpendCents: 2900, Conversations: 1} // R$ 29 por conversa, mas só R$ 29 gastos
		h.runGuardrail()
		if gp.paused() != 0 || len(h.guardEvents()) != 0 {
			t.Fatal("pausou por ruído (pouco gasto)")
		}
	})
	t.Run("janela curta nem consulta a Meta", func(t *testing.T) {
		h, gp, pubs := newGuardHarness(t)
		h.activateWithRule(pubs, "LIMITE 15")
		activatedAt(pubs, h.now.Add(-2*time.Hour))
		gp.rng = meta.Insights{SpendCents: 90000, Conversations: 1}
		h.runGuardrail()
		if gp.paused() != 0 || gp.rangeCalls() != 0 {
			t.Fatalf("julgou com 2 h no ar: pausas=%d leituras=%d", gp.paused(), gp.rangeCalls())
		}
	})
	t.Run("limites configuráveis", func(t *testing.T) {
		h, gp, pubs := newGuardHarness(t)
		h.eng.GuardrailMinSpendCents = 1000
		h.eng.GuardrailMinWindow = time.Hour
		h.activateWithRule(pubs, "LIMITE 15")
		activatedAt(pubs, h.now.Add(-2*time.Hour))
		gp.rng = meta.Insights{SpendCents: 2000, Conversations: 1} // R$ 20 por conversa
		h.runGuardrail()
		if gp.paused() != 1 {
			t.Fatal("com gasto mínimo R$ 10 e janela de 1 h, deveria pausar")
		}
	})
	t.Run("Meta sem resposta: nunca pausa", func(t *testing.T) {
		h, gp, pubs := newGuardHarness(t)
		h.activateWithRule(pubs, "LIMITE 15", "TETO 100")
		gp.rngErr = errors.New("meta fora do ar")
		h.runGuardrail()
		if gp.paused() != 0 || len(h.guardEvents()) != 0 || pubs.latest().Status != "active" {
			t.Fatal("agiu sem dados")
		}
	})
}

// Nunca pausa sem regra explícita do cliente, por pior que esteja o resultado.
func TestGuardrailNeverPausesWithoutExplicitRule(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.say("ATIVAR") // a Mia ofereceu; o cliente não respondeu
	activatedAt(pubs, h.now.Add(-100*time.Hour))
	gp.rng = meta.Insights{SpendCents: 9999999, Conversations: 0}
	h.runGuardrail()
	if gp.paused() != 0 || gp.rangeCalls() != 0 || pubs.latest().Status != "active" {
		t.Fatalf("pausou sem regra: pausas=%d leituras=%d", gp.paused(), gp.rangeCalls())
	}
	// regra tirada pelo cliente volta ao padrão desligado
	h.say("LIMITE 15")
	h.say("SEM LIMITE")
	h.runGuardrail()
	if gp.paused() != 0 || gp.rangeCalls() != 0 {
		t.Fatal("pausou depois de o cliente tirar a regra")
	}
}

func TestGuardrailDailyCapPausesAnytimeButNoticeWaitsForMorning(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.activateWithRule(pubs, "TETO 100")
	h.now = time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC) // 23h de Brasília
	activatedAt(pubs, h.now.Add(-30*time.Hour))
	gp.rng = meta.Insights{SpendCents: 10000}
	n := h.wa.count()

	h.runGuardrail()

	if gp.paused() != 1 || pubs.latest().Status != "paused" {
		t.Fatal("a pausa não espera o dia clarear")
	}
	if h.wa.count() != n {
		t.Fatalf("avisou de madrugada: %q", h.lastClient())
	}
	var secs float64
	h.must(h.pool.QueryRow(ctx, `SELECT extract(epoch FROM (run_after - now())) FROM outbox_jobs
		WHERE kind = 'whatsapp.send' AND status = 'pending' ORDER BY id DESC LIMIT 1`).Scan(&secs))
	if secs < 9*3600-120 || secs > 9*3600+120 {
		t.Fatalf("aviso agendado para daqui a %.0f s, esperado ~9 h (8h de Brasília)", secs)
	}
	// chega a hora: sai o aviso, uma vez
	_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE kind = 'whatsapp.send' AND status = 'pending'`)
	h.must(err)
	h.runJobs()
	got := h.lastClient()
	if h.wa.count() != n+1 || !strings.Contains(got, "Pausei a campanha") || !strings.Contains(got, "R$ 100,00") || !strings.Contains(got, "teto de gasto por dia") {
		t.Fatalf("aviso da manhã: %q", got)
	}
	if evs := h.guardEvents(); len(evs) != 1 || evs[0].Rule != store.GuardrailDailyCap {
		t.Fatalf("eventos = %+v", evs)
	}
	// o teto vale pelo dia: reativar no mesmo dia não pausa de novo
	h.say("ATIVAR")
	h.runGuardrail()
	if gp.paused() != 1 {
		t.Fatal("pausou duas vezes no mesmo dia")
	}
	// no dia seguinte vale de novo
	h.now = h.now.Add(24 * time.Hour)
	h.runGuardrail()
	if gp.paused() != 2 {
		t.Fatalf("no dia seguinte o teto deveria valer de novo: pausas=%d", gp.paused())
	}
}

func TestGuardrailNotifyOnlyDoesNotPause(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.activateWithRule(pubs, "LIMITE 15", "só avisar")
	gp.rng = meta.Insights{SpendCents: 6000, Conversations: 3}
	n := h.wa.count()
	h.runGuardrail()
	if gp.paused() != 0 || pubs.latest().Status != "active" {
		t.Fatal("pausou mesmo com 'só avisar'")
	}
	got := h.lastClient()
	if h.wa.count() != n+1 || !strings.Contains(got, "passou do limite") || !strings.Contains(got, "continua no ar") || !strings.Contains(got, "PAUSAR") {
		t.Fatalf("alerta: %q", got)
	}
	if evs := h.guardEvents(); len(evs) != 1 || evs[0].Action != "notified" {
		t.Fatalf("eventos = %+v", evs)
	}
	h.runGuardrail()
	if h.wa.count() != n+1 {
		t.Fatal("repetiu o alerta")
	}
}

// Se a Meta falha na hora de pausar, o evento fica aberto e a próxima conferência termina o trabalho, sem avisar duas vezes.
func TestGuardrailRetriesPauseWhenMetaFails(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.activateWithRule(pubs, "LIMITE 15")
	gp.rng = meta.Insights{SpendCents: 6000, Conversations: 3}
	gp.setErr = errors.New("meta fora do ar")
	n := h.wa.count()

	h.runGuardrail()
	evs := h.guardEvents()
	if len(evs) != 1 || evs[0].CompletedAt != nil || pubs.latest().Status != "active" || h.wa.count() != n {
		t.Fatalf("falha na pausa: eventos=%+v status=%s", evs, pubs.latest().Status)
	}

	gp.setErr = nil
	h.runGuardrail()
	evs = h.guardEvents()
	if len(evs) != 1 || evs[0].Action != "paused" || gp.paused() != 1 || pubs.latest().Status != "paused" || h.wa.count() != n+1 {
		t.Fatalf("retomada: eventos=%+v pausas=%d status=%s avisos=%d", evs, gp.paused(), pubs.latest().Status, h.wa.count()-n)
	}
}

func TestGuardrailOpenEventCancelledWhenClientRemovesRule(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.activateWithRule(pubs, "LIMITE 15")
	gp.rng = meta.Insights{SpendCents: 6000, Conversations: 3}
	gp.setErr = errors.New("meta fora do ar")
	h.runGuardrail()
	h.say("SEM LIMITE")
	gp.setErr = nil
	h.runGuardrail()
	evs := h.guardEvents()
	if len(evs) != 1 || evs[0].Action != "cancelled" || gp.paused() != 0 || pubs.latest().Status != "active" {
		t.Fatalf("regra removida: eventos=%+v pausas=%d status=%s", evs, gp.paused(), pubs.latest().Status)
	}
}

// Duas regras violadas ao mesmo tempo: uma pausa, um aviso.
func TestGuardrailTwoRulesViolatedPausesOnce(t *testing.T) {
	h, gp, pubs := newGuardHarness(t)
	h.activateWithRule(pubs, "LIMITE 15", "TETO 100")
	gp.rng = meta.Insights{SpendCents: 12000, Conversations: 2}
	n := h.wa.count()
	h.runGuardrail()
	if gp.paused() != 1 || h.wa.count() != n+1 || len(h.guardEvents()) != 1 {
		t.Fatalf("pausas=%d avisos=%d eventos=%d", gp.paused(), h.wa.count()-n, len(h.guardEvents()))
	}
}

func TestGuardrailCheckReschedulesWithoutPilingUp(t *testing.T) {
	h, _, _ := newGuardHarness(t)
	for range 4 {
		h.must(h.eng.HandleJob(ctx, jobOf(engine.JobGuardrailCheck)))
	}
	if n := h.pendingJobs(engine.JobGuardrailCheck); n > 2 {
		t.Fatalf("a cadeia de conferências cresceu: %d pendentes", n)
	}
	h.must(h.eng.EnsureGuardrailCheck(ctx))
	if n := h.pendingJobs(engine.JobGuardrailCheck); n > 2 {
		t.Fatalf("Ensure duplicou: %d", n)
	}
}

func TestGuardrailCommandOnlyWithCampaignAndLeavesOthersToTheAssistant(t *testing.T) {
	h := newHarness(t, "owner") // sem campanha na Meta
	h.att.Responses = []string{turnOffer}
	h.say("limite 15")
	if r, ok, _ := h.st.GetGuardrail(ctx, h.ident().OrgID); ok || r.Active() {
		t.Fatalf("gravou limite sem campanha: %+v", r)
	}
	if h.att.Calls() == 0 {
		t.Fatal("sem campanha, a frase deveria seguir para a Atendente")
	}
}

// ---------- diagnóstico grátis ----------

func diagJob(h *harness, force bool) store.Job {
	conv := h.conv()
	raw := `{"org_id":` + itoa(h.ident().OrgID) + `,"conversation_id":` + itoa(conv.ID)
	if force {
		raw += `,"force":true`
	}
	return store.Job{Kind: engine.JobAccountDiagnostic, Payload: []byte(raw + `}`)}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func richAccount(gp *guardPublisher) {
	gp.total = meta.AdPerformance{SpendCents: 80000, Impressions: 90000, Reach: 30000, Conversations: 40, Frequency: 4.1}
	gp.ads = []meta.AdPerformance{
		{AdID: "A1", AdName: "Vídeo depoimento", SpendCents: 30000, Impressions: 40000, Conversations: 30, Frequency: 2.0},
		{AdID: "A2", AdName: "Foto promoção", SpendCents: 30000, Impressions: 40000, Conversations: 10, Frequency: 3.0},
		{AdID: "A3", AdName: "Carrossel", SpendCents: 20000, Impressions: 10000, Conversations: 0, Frequency: 1.1},
	}
	gp.active = []meta.ActiveAd{{ID: "A1", Name: "Vídeo depoimento"}, {ID: "A9", Name: "Anúncio parado"}}
}

func TestDiagnosticDeliveredOnceAndReadOnly(t *testing.T) {
	h, gp, _ := newGuardHarness(t)
	richAccount(gp)
	n := h.wa.count()
	statusesBefore := len(gp.statuses)

	h.must(h.eng.HandleJob(ctx, diagJob(h, false)))
	h.runJobs()
	if h.wa.count() == n {
		t.Fatal("diagnóstico não foi enviado")
	}
	for _, want := range []string{"últimos 30 dias", "só leitura", "R$ 800,00", "Conversas iniciadas: 40", "Melhor anúncio: *Vídeo depoimento*",
		"Pior anúncio: *Carrossel*", "Anúncio parado", "O que eu notei"} {
		if !strings.Contains(strings.Join(h.wa.to(chatID)[n:], "\n"), want) {
			t.Errorf("diagnóstico sem %q", want)
		}
	}
	if len(gp.statuses) != statusesBefore || gp.calls() != 1 {
		t.Fatalf("o diagnóstico alterou algo na Meta: %v", gp.statuses)
	}
	sent := h.wa.count()
	h.must(h.eng.HandleJob(ctx, diagJob(h, false)))
	h.runJobs()
	if h.wa.count() != sent {
		t.Fatal("a mesma conexão recebeu o diagnóstico duas vezes")
	}
	h.must(h.eng.HandleJob(ctx, diagJob(h, true))) // a pedido do cliente refaz
	h.runJobs()
	if h.wa.count() == sent {
		t.Fatal("pedido do cliente deveria refazer o diagnóstico")
	}
}

func TestDiagnosticWithoutDataIsHonest(t *testing.T) {
	h, _, _ := newGuardHarness(t) // conta sem nenhuma entrega
	n := h.wa.count()
	h.must(h.eng.HandleJob(ctx, diagJob(h, false)))
	h.runJobs()
	got := strings.Join(h.wa.to(chatID)[n:], "\n")
	if !strings.Contains(got, "não encontrei nenhum anúncio com entrega") || strings.Contains(got, "R$") {
		t.Fatalf("diagnóstico sem dados: %q", got)
	}
}

func TestDiagnosticMetaFailureRetriesLater(t *testing.T) {
	h, gp, _ := newGuardHarness(t)
	richAccount(gp)
	n := h.wa.count()
	gp.accErr = errors.New("sem permissão ads_read")
	if err := h.eng.HandleJob(ctx, diagJob(h, false)); err == nil {
		t.Fatal("falha da Meta deveria voltar ao outbox")
	}
	h.runJobs()
	if h.wa.count() != n {
		t.Fatal("enviou diagnóstico sem ler a conta")
	}
	gp.accErr = nil
	h.must(h.eng.HandleJob(ctx, diagJob(h, false)))
	h.runJobs()
	if h.wa.count() == n {
		t.Fatal("depois de a Meta voltar, o diagnóstico deveria sair")
	}
}

func TestDiagnosticWithoutActiveAdsListStillDelivers(t *testing.T) {
	h, gp, _ := newGuardHarness(t)
	richAccount(gp)
	gp.activeErr = errors.New("sem permissão")
	n := h.wa.count()
	h.must(h.eng.HandleJob(ctx, diagJob(h, false)))
	h.runJobs()
	got := strings.Join(h.wa.to(chatID)[n:], "\n")
	if !strings.Contains(got, "R$ 800,00") || strings.Contains(got, "sem nenhuma exibição") {
		t.Fatalf("%q", got)
	}
}

func TestDiagnosticCommand(t *testing.T) {
	h, gp, _ := newGuardHarness(t)
	richAccount(gp)
	ai := h.att.Calls()
	h.say("DIAGNÓSTICO")
	if got := h.lastClient(); !strings.Contains(got, "Vou olhar os últimos 30 dias") || h.pendingJobs(engine.JobAccountDiagnostic) != 1 {
		t.Fatalf("pedido: %q (jobs %d)", got, h.pendingJobs(engine.JobAccountDiagnostic))
	}
	h.say("diagnóstico")
	if got := h.lastClient(); !strings.Contains(got, "Já estou olhando") || h.pendingJobs(engine.JobAccountDiagnostic) != 1 {
		t.Fatalf("repetido: %q", got)
	}
	h.runKind(engine.JobAccountDiagnostic)
	if got := h.lastClient(); !strings.Contains(got, "Conversas iniciadas: 40") {
		t.Fatalf("resumo: %q", got)
	}
	if h.att.Calls() != ai {
		t.Fatal("o comando não pode gastar IA")
	}
}

func TestDiagnosticCommandWithoutMetaSendsConnectLink(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.say("diagnóstico grátis")
	if got := h.sentText(); !strings.Contains(got, "conecte ela primeiro") || !strings.Contains(got, metaBase+"/") {
		t.Fatalf("%q", got)
	}
}

// Depois de conectar a conta (confirmação enviada), o diagnóstico é agendado e entregue sozinho.
func TestDiagnosticScheduledAfterMetaConnected(t *testing.T) {
	h, gp, _ := newGuardHarness(t)
	richAccount(gp)
	h.eng.Humanize = true
	h.eng.Debounce = 0
	h.eng.Rand = func() float64 { return 0.5 }
	conv := h.conv()
	h.must(h.st.Enqueue(ctx, engine.MetaConnectedJob(conv.ID, conv.ChatID, engine.MetaSummary{AccountID: "act_1", AccountName: "Loja",
		Currency: "BRL", Timezone: "America/Sao_Paulo", AccountStatus: 1, PageName: "Loja Page", PageWhatsApp: true})))
	h.drain()
	text := h.sentText()
	if !strings.Contains(text, "Loja") || !strings.Contains(text, "últimos 30 dias") || !strings.Contains(text, "Conversas iniciadas: 40") {
		t.Fatalf("diagnóstico não chegou depois da conexão: %q", text)
	}
	if n := strings.Count(text, "Foi só leitura"); n != 1 {
		t.Fatalf("diagnóstico enviado %d vezes", n)
	}
	if n := h.count(`SELECT count(*) FROM account_diagnostics WHERE summary IS NOT NULL`); n != 1 {
		t.Fatalf("resumo guardado %d vezes", n)
	}
}
