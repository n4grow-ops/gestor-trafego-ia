package engine_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// shieldPub é o Publisher dos testes com as leituras de saúde da conta (o que o escudo lê da Meta).
type shieldPub struct {
	*fakePublisher
	smu       sync.Mutex
	health    meta.AccountHealth
	healthErr error
	fund      meta.Funding
	page      meta.PageAccess
	tok       meta.TokenInfo
	biz       string
	reads     int
}

func newShieldPub(base *fakePublisher) *shieldPub {
	return &shieldPub{fakePublisher: base,
		health: meta.AccountHealth{ID: "act_1", Name: "Loja", Currency: "BRL", Status: 1, BusinessID: "B1"},
		fund:   meta.Funding{Status: 1, HasSource: true, Source: "Visa *1234", AvailableCents: -1},
		page:   meta.PageAccess{Found: true, Published: true, Tasks: []string{"ADVERTISE"}, InstagramID: "IG1"},
		tok:    meta.TokenInfo{Valid: true, Scopes: []string{"ads_management", "ads_read", "pages_show_list", "pages_read_engagement"}},
		biz:    "verified"}
}

func (p *shieldPub) AccountHealth(context.Context, string, string) (meta.AccountHealth, error) {
	p.smu.Lock()
	defer p.smu.Unlock()
	p.reads++
	return p.health, p.healthErr
}

func (p *shieldPub) Funding(context.Context, string, string) (meta.Funding, error) {
	p.smu.Lock()
	defer p.smu.Unlock()
	return p.fund, nil
}

func (p *shieldPub) PageAccess(context.Context, string, string) (meta.PageAccess, error) {
	p.smu.Lock()
	defer p.smu.Unlock()
	return p.page, nil
}

func (p *shieldPub) TokenInfo(context.Context, string) (meta.TokenInfo, error) {
	p.smu.Lock()
	defer p.smu.Unlock()
	return p.tok, nil
}

func (p *shieldPub) BusinessVerification(context.Context, string, string) (string, error) {
	p.smu.Lock()
	defer p.smu.Unlock()
	return p.biz, nil
}

func (p *shieldPub) setStatus(status, reason int) {
	p.smu.Lock()
	defer p.smu.Unlock()
	p.health.Status, p.health.DisableReason = status, reason
}

func newShieldHarness(t *testing.T) (*harness, *fakePubs, *shieldPub) {
	h, pubs, base := newPublishHarness(t, "owner")
	sp := newShieldPub(base)
	h.eng.Publisher = sp
	h.connectMetaIG()
	return h, pubs, sp
}

func (h *harness) shieldRow() (state, notified string) {
	h.t.Helper()
	h.must(h.pool.QueryRow(ctx, `SELECT state, notified_state FROM shield_account_state`).Scan(&state, &notified))
	return
}

func (h *harness) clientMessages(contains string) int {
	n := 0
	for _, m := range h.wa.to(chatID) {
		if strings.Contains(m, contains) {
			n++
		}
	}
	return n
}

// ---------- Mia: aprovação e publicação ----------

func TestShieldApprovalSaysAllClear(t *testing.T) {
	h, _, _ := newShieldHarness(t)
	h.say("SIM " + h.reachApproval())
	if msg := h.lastClient(); !strings.Contains(msg, "tudo certo") || !strings.Contains(msg, "Campanha aprovada") {
		t.Fatalf("aprovação sem o resumo do escudo: %q", msg)
	}
	var cached int
	h.must(h.pool.QueryRow(ctx, `SELECT count(*) FROM shield_account_state WHERE report IS NOT NULL`).Scan(&cached))
	if cached != 1 {
		t.Fatal("relatório não ficou guardado para o painel")
	}
}

func TestShieldApprovalListsPointsToFix(t *testing.T) {
	h, _, sp := newShieldHarness(t)
	sp.fund = meta.Funding{Status: 1, AvailableCents: -1} // sem forma de pagamento
	sp.biz = "not_verified"
	h.say("SIM " + h.reachApproval())
	msg := h.lastClient()
	for _, want := range []string{"um ponto para resolver", "Pagamento", "Como resolver"} {
		if !strings.Contains(msg, want) {
			t.Errorf("resumo sem %q: %q", want, msg)
		}
	}
	// verificação do Business não vai para a conversa (fica só no painel)
	if strings.Contains(msg, "Verificação do Business") {
		t.Errorf("resumo com a verificação do Business: %q", msg)
	}
}

// Só a verificação do Business pendente: a Mia diz que está tudo certo.
func TestShieldApprovalIgnoresBusinessVerification(t *testing.T) {
	h, _, sp := newShieldHarness(t)
	sp.biz = "not_verified"
	h.say("SIM " + h.reachApproval())
	if msg := h.lastClient(); strings.Contains(msg, "Business") || !strings.Contains(msg, "tudo certo") {
		t.Fatalf("aprovação = %q", msg)
	}
}

func TestShieldApprovalWithoutConnectionStaysQuiet(t *testing.T) {
	h, _, base := newPublishHarness(t, "owner")
	h.eng.Publisher = newShieldPub(base)
	h.say("SIM " + h.reachApproval())
	if msg := h.lastClient(); strings.Contains(msg, "Conferi") {
		t.Fatalf("sem conta conectada não há o que conferir: %q", msg)
	}
}

// A Meta recusaria (conta desativada): para antes de criar a campanha, explica e, resolvido, "tentar de novo" publica.
func TestShieldBlocksPublishUntilFixed(t *testing.T) {
	h, pubs, sp := newShieldHarness(t)
	h.readyProject()
	sp.setStatus(2, 1) // desativada por política
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if sp.calls() != 0 || pubs.latest().ID != 0 {
		t.Fatalf("publicou com a conta desativada: calls=%d pub=%+v", sp.calls(), pubs.latest())
	}
	msg := h.lastClient()
	for _, want := range []string{"Antes de publicar", "recusaria", "desativou", "1. ", "tentar de novo"} {
		if !strings.Contains(msg, want) {
			t.Errorf("mensagem sem %q: %q", want, msg)
		}
	}
	if h.count(`SELECT count(*) FROM audit_events WHERE action = 'shield_publish_blocked'`) != 1 {
		t.Fatal("bloqueio não foi registrado")
	}
	sp.setStatus(1, 0)
	h.say("tentar de novo")
	h.runKind(engine.JobMetaPublish)
	if sp.calls() != 1 || pubs.latest().Status != "paused" {
		t.Fatalf("não publicou depois de resolver: calls=%d status=%s", sp.calls(), pubs.latest().Status)
	}
}

// Atenção não bloqueia: publica normalmente; a verificação do Business não entra na mensagem.
func TestShieldAttentionDoesNotBlockButIsMentioned(t *testing.T) {
	h, pubs, sp := newShieldHarness(t)
	sp.biz = "not_verified"
	h.readyProject()
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if sp.calls() != 1 || pubs.latest().Status != "paused" {
		t.Fatalf("calls=%d status=%s", sp.calls(), pubs.latest().Status)
	}
	if msg := h.lastClient(); !strings.Contains(msg, "Prontinho") || strings.Contains(msg, "Verificação do Business") {
		t.Fatalf("mensagem = %q", msg)
	}
}

func TestShieldKeepsCurrencyRefusalBehavior(t *testing.T) {
	h, pubs, sp := newShieldHarness(t)
	sp.health.Currency = "USD"
	_, err := h.pool.Exec(ctx, `UPDATE meta_connections SET currency = 'USD'`)
	h.must(err)
	h.readyProject()
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if p := pubs.latest(); p.Status != "failed" || !strings.Contains(p.Error, "USD") {
		t.Fatalf("publicação = %+v", p)
	}
	if sp.calls() != 0 {
		t.Fatal("publicou em conta que não é BRL")
	}
}

// ---------- reprovação ----------

func TestRejectionIsExplainedInSimpleWords(t *testing.T) {
	h, _, base := newPublishHarness(t, "owner")
	sp := newShieldPub(base)
	h.eng.Publisher = sp
	h.eng.CopyFix = func(headline, text, segment string) (string, string, bool) {
		return strings.ReplaceAll(headline, "garantido", "possível"), text, true
	}
	h.connectMetaIG()
	h.readyProject()
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	base.reviews = []meta.AdReview{{ID: "A1", EffectiveStatus: "DISAPPROVED", Reasons: []string{"Image contains too much text."}}}
	h.runKind(engine.JobAdsReview)
	msg := h.lastClient()
	for _, want := range []string{"reprovou", "Image contains too much text.", "Em português simples", "texto demais", "O que mudar", "PRONTO"} {
		if !strings.Contains(msg, want) {
			t.Errorf("mensagem sem %q: %q", want, msg)
		}
	}
}

// ---------- job de saúde da conta ----------

func TestShieldHealthIsScheduledOnce(t *testing.T) {
	h, _, _ := newShieldHarness(t)
	h.must(h.eng.EnsureShieldHealth(ctx))
	h.must(h.eng.EnsureShieldHealth(ctx))
	if n := h.pendingJobs(engine.JobShieldHealth); n != 1 {
		t.Fatalf("jobs agendados = %d", n)
	}
	// Sem as leituras da Meta no Publisher, não agenda nada.
	h2, _, _ := newPublishHarness(t, "owner")
	h2.must(h2.eng.EnsureShieldHealth(ctx))
	if h2.pendingJobs(engine.JobShieldHealth) != 0 {
		t.Fatal("agendou sem leitura da Meta")
	}
}

func (h *harness) healthRun() {
	h.t.Helper()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobShieldHealth)))
	h.runJobs()
}

// Conta que piora: o cliente é avisado uma vez, com o passo a passo, e a equipe também. Repetir a checagem
// não repete o aviso; voltar ao normal zera e um novo problema avisa de novo.
func TestShieldHealthWarnsOncePerChange(t *testing.T) {
	h, _, sp := newShieldHarness(t)
	h.say("oi") // abre a conversa
	h.now = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	before := h.clientMessages("O que fazer agora")

	h.healthRun() // conta saudável
	if st, nt := h.shieldRow(); st != "ok" || nt != "ok" || h.clientMessages("O que fazer agora") != before {
		t.Fatalf("conta saudável: state=%s notified=%s", st, nt)
	}

	sp.setStatus(7, 0) // análise de risco
	h.healthRun()
	if h.clientMessages("O que fazer agora") != before+1 {
		t.Fatalf("sem aviso ao cliente: %v", h.wa.to(chatID))
	}
	msg := h.lastClient()
	for _, want := range []string{"análise de risco", "Qualidade da Conta", "1. ", "Eu não consigo reverter"} {
		if !strings.Contains(msg, want) {
			t.Errorf("aviso sem %q: %q", want, msg)
		}
	}
	if st, nt := h.shieldRow(); st != "restrita" || nt != "restrita" {
		t.Fatalf("estado = %s/%s", st, nt)
	}

	for range 3 { // checagens seguintes com a mesma situação
		h.now = h.now.Add(4 * time.Hour)
		if h.now.Hour() >= 21 { // fica dentro do dia para não confundir com o silêncio
			h.now = h.now.Add(-4 * time.Hour)
		}
		h.healthRun()
	}
	if h.clientMessages("O que fazer agora") != before+1 {
		t.Fatalf("repetiu o aviso: %d", h.clientMessages("O que fazer agora")-before)
	}
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action = 'shield_account_alert'`); n != 1 {
		t.Fatalf("auditorias = %d", n)
	}

	sp.setStatus(1, 0) // volta ao normal: em silêncio
	h.healthRun()
	if st, nt := h.shieldRow(); st != "ok" || nt != "ok" || h.clientMessages("O que fazer agora") != before+1 {
		t.Fatalf("normalizou: %s/%s", st, nt)
	}

	sp.setStatus(3, 0) // pagamento recusado: novo aviso
	h.healthRun()
	if h.clientMessages("O que fazer agora") != before+2 || !strings.Contains(h.lastClient(), "Pagar agora") {
		t.Fatalf("sem novo aviso de pagamento: %q", h.lastClient())
	}
}

// Nunca das 21h às 8h (Brasília): o aviso fica para as 8h e sai uma vez só.
func TestShieldHealthRespectsQuietHours(t *testing.T) {
	h, _, sp := newShieldHarness(t)
	h.say("oi")
	before := h.clientMessages("O que fazer agora")
	sp.setStatus(2, 3) // desativada por risco de pagamento

	h.now = time.Date(2026, 10, 15, 2, 30, 0, 0, time.UTC) // 23h30 em Brasília
	h.healthRun()
	if h.clientMessages("O que fazer agora") != before {
		t.Fatal("avisou o cliente de madrugada")
	}
	if st, nt := h.shieldRow(); st != "pagamento" || nt != "ok" {
		t.Fatalf("estado guardado = %s/%s (o aviso adiado não pode ficar como dado)", st, nt)
	}
	// A próxima rodada é logo depois das 8h, não só daqui a 4 h.
	var secs float64
	h.must(h.pool.QueryRow(ctx, `SELECT extract(epoch FROM max(run_after) - now()) FROM outbox_jobs WHERE kind = $1 AND status = 'pending'`,
		engine.JobShieldHealth).Scan(&secs))
	if want := (8*time.Hour + 35*time.Minute).Seconds(); secs < want-60 || secs > want+60 {
		t.Fatalf("próxima rodada em %.0f s, quer ~%.0f s (8h05 de Brasília)", secs, want)
	}

	h.now = time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC) // 3h em Brasília: ainda silêncio
	h.healthRun()
	if h.clientMessages("O que fazer agora") != before {
		t.Fatal("avisou às 3h")
	}

	h.now = time.Date(2026, 10, 15, 11, 5, 0, 0, time.UTC) // 8h05 em Brasília
	h.healthRun()
	if h.clientMessages("O que fazer agora") != before+1 {
		t.Fatalf("não avisou de manhã: %v", h.wa.to(chatID))
	}
	h.now = h.now.Add(4 * time.Hour)
	h.healthRun()
	if h.clientMessages("O que fazer agora") != before+1 {
		t.Fatal("repetiu o aviso depois das 8h")
	}
}

// Token vencido ou erro da Meta não derrubam o job nem geram aviso de conta.
func TestShieldHealthIgnoresReadErrors(t *testing.T) {
	h, _, sp := newShieldHarness(t)
	h.say("oi")
	sp.healthErr = context.DeadlineExceeded
	before := h.clientMessages("O que fazer agora")
	h.healthRun()
	if h.clientMessages("O que fazer agora") != before || h.pendingJobs(engine.JobShieldHealth) != 1 {
		t.Fatal("erro de leitura virou aviso ou parou o ciclo")
	}
}
