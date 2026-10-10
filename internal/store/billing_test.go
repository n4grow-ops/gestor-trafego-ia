package store_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

var brt = time.FixedZone("BRT", -3*3600)

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 12, 0, 0, 0, brt) }

type billingFixture struct {
	pool *pgxpool.Pool
	orgs map[string]int64
	subs map[string]int64
}

// seedBilling monta uma base conhecida para outubro de 2026.
func seedBilling(t *testing.T) (*store.Store, billingFixture) {
	t.Helper()
	st, pool := newStore(t)
	f := billingFixture{pool: pool, orgs: map[string]int64{}, subs: map[string]int64{}}
	type sub struct {
		key, plan, status string
		started, updated  time.Time
	}
	for _, s := range []sub{
		{"A", "starter", "active", day(10, 1), day(10, 1)},
		{"B", "pro", "active", day(8, 10), day(8, 10)},
		{"C", "starter", "past_due", day(7, 1), day(10, 4)},
		{"D", "free", "active", day(10, 2), day(10, 2)},
		{"E", "pro", "canceled", day(6, 1), day(10, 8)},
		{"F", "starter", "trialing", day(9, 20), day(9, 20)},
		{"G", "pro", "active", day(10, 3), day(10, 3)},
	} {
		org, err := st.CreateOrganization(ctx, "Cliente "+s.key, "", 50, []string{"OUTCOME_ENGAGEMENT"})
		must(t, err)
		f.orgs[s.key] = org
		var id int64
		must(t, pool.QueryRow(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, started_at, updated_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, org, s.plan, s.status, s.started, s.updated).Scan(&id))
		f.subs[s.key] = id
	}
	type pay struct {
		key            string
		amount         int
		status, method string
		at             time.Time
	}
	for _, p := range []pay{
		{"A", 9700, "paid", "pix", day(9, 5)}, // mês anterior (série)
		{"A", 9700, "paid", "pix", day(10, 5)},
		{"B", 29700, "paid", "card", day(10, 6)},
		{"C", 9700, "pending", "boleto", day(10, 3)},
		{"C", 9700, "failed", "card", day(10, 2)},
	} {
		var paidAt *time.Time
		if p.status == "paid" {
			paidAt = &p.at
		}
		_, err := pool.Exec(ctx, `INSERT INTO payments (subscription_id, amount_cents, method, status, provider, paid_at, created_at)
			VALUES ($1, $2, $3, $4, 'manual', $5, $6)`, f.subs[p.key], p.amount, p.method, p.status, paidAt, p.at)
		must(t, err)
	}
	for _, u := range []struct {
		cost float64
		at   time.Time
	}{{2.0, day(10, 7)}, {0.5, day(10, 9)}, {1.0, day(9, 15)}, {9.9, day(11, 1)}} {
		_, err := pool.Exec(ctx, `INSERT INTO llm_usage (organization_id, agent, model, input_tokens, output_tokens, cost_usd, created_at)
			VALUES ($1, 'attendant', 'm', 1, 1, $2, $3)`, f.orgs["A"], u.cost, u.at)
		must(t, err)
	}
	return st, f
}

func TestBillingReport(t *testing.T) {
	st, _ := seedBilling(t)
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, brt)
	r, err := st.Billing(ctx, month, 5.0)
	must(t, err)

	// MRR: ativos e inadimplentes (starter 97 + pro 297 + starter 97 em atraso + pro 297).
	if r.MRRCents != 78800 || r.PayingSubs != 4 || r.ARPACents != 19700 {
		t.Errorf("MRR = %d, pagantes = %d, ARPA = %d", r.MRRCents, r.PayingSubs, r.ARPACents)
	}
	if r.NewSubs != 3 || r.NewPaying != 2 || r.Canceled != 1 {
		t.Errorf("novos = %d, novos pagantes = %d, cancelados = %d", r.NewSubs, r.NewPaying, r.Canceled)
	}
	if r.ReceivedCents != 39400 || r.PaidCount != 2 || r.TicketCents != 19700 {
		t.Errorf("recebido = %d em %d, ticket = %d", r.ReceivedCents, r.PaidCount, r.TicketCents)
	}
	if r.PendingCents != 9700 || r.PendingCount != 1 || r.FailedPayments != 1 {
		t.Errorf("pendente = %d (%d), falhas = %d", r.PendingCents, r.PendingCount, r.FailedPayments)
	}
	if r.PastDueSubs != 1 || r.PastDueMRRCents != 9700 || math.Abs(r.DelinquencyRate-9700.0/78800) > 1e-9 {
		t.Errorf("inadimplência: %d assinaturas, %d, taxa %f", r.PastDueSubs, r.PastDueMRRCents, r.DelinquencyRate)
	}
	// Previsão: recebido + pendente do mês + assinaturas pagas sem cobrança no mês (G, pro).
	if r.ForecastCents != 39400+9700+29700 {
		t.Errorf("previsão = %d", r.ForecastCents)
	}
	if math.Abs(r.AICostUSD-2.5) > 1e-9 || r.AICostCents != 1250 || r.MarginCents != 38150 {
		t.Errorf("custo IA = %f US$ = %d, margem = %d", r.AICostUSD, r.AICostCents, r.MarginCents)
	}
	if math.Abs(r.MarginPct-38150.0/39400) > 1e-9 {
		t.Errorf("margem %% = %f", r.MarginPct)
	}

	plans := map[string]store.PlanBreakdown{}
	for _, p := range r.Plans {
		plans[p.Code] = p
	}
	if p := plans["starter"]; p.Active != 1 || p.PastDue != 1 || p.Trialing != 1 || p.MRRCents != 19400 {
		t.Errorf("starter = %+v", p)
	}
	if p := plans["pro"]; p.Active != 2 || p.Canceled != 1 || p.MRRCents != 59400 {
		t.Errorf("pro = %+v", p)
	}
	if p := plans["free"]; p.Active != 1 || p.MRRCents != 0 {
		t.Errorf("free = %+v", p)
	}

	if len(r.Series) != 6 || r.Series[0].Month != "2026-05" || r.Series[5].Month != "2026-10" {
		t.Fatalf("série = %+v", r.Series)
	}
	if r.Series[4].ReceivedCents != 9700 || r.Series[4].AICostCents != 500 || r.Series[5].ReceivedCents != 39400 || r.Series[5].AICostCents != 1250 {
		t.Errorf("série set/out = %+v %+v", r.Series[4], r.Series[5])
	}
	if len(r.Recent) != 5 || r.Recent[0].OrgName != "Cliente B" || r.Recent[0].AmountCents != 29700 {
		t.Errorf("pagamentos recentes (mais novo primeiro) = %+v", r.Recent)
	}
}

func TestRecordManualPayment(t *testing.T) {
	st, f := seedBilling(t)
	paid, err := st.RecordManualPayment(ctx, store.ManualPayment{OrgID: f.orgs["G"], AmountCents: 29700, Method: "pix",
		Status: "paid", Date: day(10, 10), Note: "pix do dia 10", RecordedBy: "dono@meuanuncio.io"})
	must(t, err)
	var status, provider, note, by string
	var paidAt *time.Time
	var created time.Time
	must(t, f.pool.QueryRow(ctx, `SELECT status, provider, paid_at, created_at, note, recorded_by FROM payments WHERE id = $1`, paid).
		Scan(&status, &provider, &paidAt, &created, &note, &by))
	if status != "paid" || provider != "manual" || paidAt == nil || !paidAt.Equal(day(10, 10)) || !created.Equal(day(10, 10)) ||
		note != "pix do dia 10" || by != "dono@meuanuncio.io" {
		t.Fatalf("pago: %s %s %v %v %q %q", status, provider, paidAt, created, note, by)
	}
	pend, err := st.RecordManualPayment(ctx, store.ManualPayment{OrgID: f.orgs["A"], AmountCents: 100, Method: "boleto", Status: "pending", Date: day(10, 11)})
	must(t, err)
	must(t, f.pool.QueryRow(ctx, `SELECT paid_at FROM payments WHERE id = $1`, pend).Scan(&paidAt))
	if paidAt != nil {
		t.Fatal("pendente não tem data de pagamento")
	}

	bad := []store.ManualPayment{
		{OrgID: f.orgs["A"], AmountCents: 0, Method: "pix", Status: "paid", Date: day(10, 1)},
		{OrgID: f.orgs["A"], AmountCents: 100, Method: "cheque", Status: "paid", Date: day(10, 1)},
		{OrgID: f.orgs["A"], AmountCents: 100, Method: "pix", Status: "talvez", Date: day(10, 1)},
		{OrgID: f.orgs["A"], AmountCents: 100, Method: "pix", Status: "paid"},
		{OrgID: f.orgs["A"], AmountCents: 100, Method: "pix", Status: "paid", Date: day(10, 1), Note: string(make([]byte, 201))},
	}
	for i, p := range bad {
		if _, err := st.RecordManualPayment(ctx, p); !errors.Is(err, store.ErrInvalidInput) {
			t.Errorf("caso %d: %v", i, err)
		}
	}
	org, _ := st.CreateOrganization(ctx, "Sem assinatura", "", 50, []string{"OUTCOME_ENGAGEMENT"})
	if _, err := st.RecordManualPayment(ctx, store.ManualPayment{OrgID: org, AmountCents: 100, Method: "pix", Status: "paid", Date: day(10, 1)}); !errors.Is(err, store.ErrNoSubscription) {
		t.Fatalf("sem assinatura: %v", err)
	}
}
