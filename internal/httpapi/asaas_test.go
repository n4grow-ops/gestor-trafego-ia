package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

type asaasEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	h     *httpapi.AsaasWebhook
	orgID int64
}

// newAsaasEnv: organização com assinatura pending já criada no Asaas (sub_1, plano starter).
func newAsaasEnv(t *testing.T, token string) *asaasEnv {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()
	org, err := st.CreateOrganization(ctx, "Pizzaria Boa", "restaurante", 100, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, provider, provider_ref, pending_plan, pending_method, checkout_url)
		VALUES ($1, 'starter', 'pending', 'asaas', 'sub_1', 'starter', 'pix', 'https://asaas/i/1')`, org); err != nil {
		t.Fatal(err)
	}
	return &asaasEnv{t: t, pool: pool, orgID: org, h: &httpapi.AsaasWebhook{Store: st, Token: token, Log: discard}}
}

func (e *asaasEnv) post(token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/asaas", strings.NewReader(body))
	if token != "" {
		req.Header.Set("asaas-access-token", token)
	}
	mux := http.NewServeMux()
	e.h.Register(mux)
	mux.ServeHTTP(rec, req)
	return rec
}

func (e *asaasEnv) sub() (status string, pastDue *time.Time, end *time.Time) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT status, past_due_since, current_period_end FROM subscriptions WHERE organization_id = $1`, e.orgID).
		Scan(&status, &pastDue, &end); err != nil {
		e.t.Fatal(err)
	}
	return
}

func (e *asaasEnv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func payEvent(id, event string) string {
	return `{"id":"` + id + `","event":"` + event + `","payment":{"id":"pay_1","customer":"cus_1","subscription":"sub_1","value":397,
		"billingType":"PIX","status":"RECEIVED","dueDate":"2026-10-31","paymentDate":"2026-10-31","invoiceUrl":"https://asaas/i/1","externalReference":"org-1"}}`
}

func TestAsaasWebhookDisabledWithoutToken(t *testing.T) {
	e := newAsaasEnv(t, "")
	if rec := e.post("", payEvent("evt_1", "PAYMENT_RECEIVED")); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestAsaasWebhookRejectsWrongToken(t *testing.T) {
	e := newAsaasEnv(t, "segredo")
	for _, tok := range []string{"", "errado", "segred"} {
		if rec := e.post(tok, payEvent("evt_1", "PAYMENT_RECEIVED")); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: code = %d", tok, rec.Code)
		}
	}
	if s, _, _ := e.sub(); s != "pending" || e.count(`SELECT count(*) FROM asaas_events`) != 0 {
		t.Fatalf("nada deveria mudar: %s", s)
	}
}

func TestAsaasPaymentReceivedActivatesAndNotifiesOnce(t *testing.T) {
	e := newAsaasEnv(t, "segredo")
	rec := e.post("segredo", payEvent("evt_1", "PAYMENT_RECEIVED"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "applied") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	status, pastDue, end := e.sub()
	if status != "active" || pastDue != nil || end == nil || end.In(time.FixedZone("BRT", -3*3600)).Format("2006-01-02") != "2026-11-30" {
		t.Fatalf("assinatura = %s %v %v", status, pastDue, end)
	}
	if n := e.count(`SELECT count(*) FROM payments WHERE provider = 'asaas' AND provider_ref = 'pay_1' AND status = 'paid'
		AND amount_cents = 39700 AND method = 'pix' AND paid_at IS NOT NULL`); n != 1 {
		t.Fatalf("pagamento = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM outbox_jobs WHERE kind = $1`, engine.JobBillingNotify); n != 1 {
		t.Fatalf("aviso = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM subscriptions WHERE organization_id = $1 AND checkout_url IS NULL AND pending_plan IS NULL`, e.orgID); n != 1 {
		t.Fatal("link e escolha pendentes deveriam sair")
	}

	// Mesmo evento reenviado pelo Asaas: 200 sem efeito.
	rec = e.post("segredo", payEvent("evt_1", "PAYMENT_RECEIVED"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("repetido: %d %s", rec.Code, rec.Body)
	}
	// Outro evento do mesmo pagamento (CONFIRMED depois do RECEIVED): sem segundo aviso nem segunda linha.
	e.post("segredo", payEvent("evt_2", "PAYMENT_CONFIRMED"))
	if n := e.count(`SELECT count(*) FROM outbox_jobs WHERE kind = $1`, engine.JobBillingNotify); n != 1 {
		t.Fatalf("aviso repetido = %d", n)
	}
	if n := e.count(`SELECT count(*) FROM payments`); n != 1 {
		t.Fatalf("pagamentos = %d", n)
	}
	// CREATED atrasado não volta o pagamento para pendente.
	e.post("segredo", payEvent("evt_3", "PAYMENT_CREATED"))
	if n := e.count(`SELECT count(*) FROM payments WHERE status = 'paid'`); n != 1 {
		t.Fatal("pagamento voltou para pendente")
	}
}

func TestAsaasPaymentOverdueSetsPastDue(t *testing.T) {
	e := newAsaasEnv(t, "segredo")
	// Primeira cobrança vencida sem pagar continua pending: past_due liberaria o sistema.
	e.post("segredo", payEvent("evt_0", "PAYMENT_OVERDUE"))
	if s, _, _ := e.sub(); s != "pending" {
		t.Fatalf("sem pagamento = %s", s)
	}
	e.post("segredo", payEvent("evt_1", "PAYMENT_RECEIVED"))
	e.post("segredo", strings.Replace(payEvent("evt_2", "PAYMENT_OVERDUE"), `"pay_1"`, `"pay_2"`, 1))
	status, pastDue, _ := e.sub()
	if status != "past_due" || pastDue == nil {
		t.Fatalf("assinatura = %s %v", status, pastDue)
	}
	first := *pastDue
	e.post("segredo", strings.Replace(payEvent("evt_3", "PAYMENT_OVERDUE"), `"pay_1"`, `"pay_3"`, 1))
	if _, again, _ := e.sub(); again == nil || !again.Equal(first) {
		t.Fatalf("past_due_since mudou: %v -> %v", first, again)
	}
	// Avisos: pago + 3 vencidos (o primeiro, sem pagamento, também avisa com o link).
	if n := e.count(`SELECT count(*) FROM outbox_jobs WHERE kind = $1 AND payload->>'kind' = 'overdue'`, engine.JobBillingNotify); n != 3 {
		t.Fatalf("avisos de vencido = %d", n)
	}
}

func TestAsaasSubscriptionDeletedCancels(t *testing.T) {
	e := newAsaasEnv(t, "segredo")
	e.post("segredo", payEvent("evt_1", "PAYMENT_RECEIVED"))
	rec := e.post("segredo", `{"id":"evt_9","event":"SUBSCRIPTION_DELETED","subscription":{"id":"sub_1","externalReference":"org-1"}}`)
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	if s, _, end := e.sub(); s != "canceled" || end == nil {
		t.Fatalf("assinatura = %s %v", s, end)
	}
}

func TestAsaasUnknownEventOnlyRecorded(t *testing.T) {
	e := newAsaasEnv(t, "segredo")
	rec := e.post("segredo", `{"id":"evt_x","event":"PAYMENT_BANK_SLIP_VIEWED","payment":{"id":"pay_1","subscription":"sub_1"}}`)
	if rec.Code != 200 || e.count(`SELECT count(*) FROM asaas_events WHERE id = 'evt_x'`) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if s, _, _ := e.sub(); s != "pending" || e.count(`SELECT count(*) FROM payments`) != 0 {
		t.Fatal("evento desconhecido não tem efeito")
	}
}

func TestAsaasPixRenewalSendsLink(t *testing.T) {
	e := newAsaasEnv(t, "segredo")
	// Primeira cobrança criada (ainda pending): o link já foi no checkout, sem aviso.
	e.post("segredo", payEvent("evt_0", "PAYMENT_CREATED"))
	e.post("segredo", payEvent("evt_1", "PAYMENT_RECEIVED"))
	renew := strings.NewReplacer(`"pay_1"`, `"pay_2"`, `"2026-10-31"`, `"2026-11-30"`, `"https://asaas/i/1"`, `"https://asaas/i/2"`)
	e.post("segredo", renew.Replace(payEvent("evt_2", "PAYMENT_CREATED")))
	if n := e.count(`SELECT count(*) FROM outbox_jobs WHERE kind = $1 AND payload->>'kind' = 'due'
		AND payload->>'invoice_url' = 'https://asaas/i/2' AND payload->>'due_date' = '2026-11-30'`, engine.JobBillingNotify); n != 1 {
		t.Fatalf("aviso da mensalidade = %d", n)
	}
	// Cartão renova sozinho: sem aviso.
	card := strings.NewReplacer(`"pay_1"`, `"pay_3"`, `"PIX"`, `"CREDIT_CARD"`)
	e.post("segredo", card.Replace(payEvent("evt_3", "PAYMENT_CREATED")))
	if n := e.count(`SELECT count(*) FROM outbox_jobs WHERE kind = $1 AND payload->>'kind' = 'due'`, engine.JobBillingNotify); n != 1 {
		t.Fatalf("cartão não deveria avisar: %d", n)
	}
}
