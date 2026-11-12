package store_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func notifyJobs(t *testing.T, pool *pgxpool.Pool, event string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT payload::text FROM outbox_jobs WHERE kind = 'notify.deliver' AND payload->>'event' = $1 ORDER BY id`, event)
	must(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		must(t, rows.Scan(&s))
		out = append(out, s)
	}
	return out
}

func subscribe(t *testing.T, st *store.Store, events ...string) {
	t.Helper()
	_, err := st.CreateNotifyDestination(ctx, store.NotifyDestinationInput{Name: "celular", Kind: "pushcut",
		URL: "https://api.pushcut.io/x/notifications/V", Events: events, Active: true})
	must(t, err)
}

// Venda aprovada no Asaas avisa uma vez só: o cartão manda CONFIRMED e depois RECEIVED.
func TestAsaasPaidEmitsSaleOnce(t *testing.T) {
	st, pool := newStore(t)
	subscribe(t, st, "venda.nova")
	ident := seedIdentity(t, st, "owner", "5511988887777")
	must(t, st.ChooseBillingPlan(ctx, ident.OrgID, "starter"))
	must(t, st.SetBillingCheckout(ctx, ident.OrgID, "card", "sub_1", ""))
	pay := &store.AsaasPayment{ID: "pay_1", Subscription: "sub_1", Value: 97, BillingType: "CREDIT_CARD", DueDate: "2026-11-12"}
	for i, ev := range []string{"PAYMENT_CREATED", "PAYMENT_CONFIRMED", "PAYMENT_RECEIVED"} {
		_, err := st.ApplyAsaasEvent(ctx, store.AsaasEvent{ID: "evt_" + string(rune('a'+i)), Event: ev, Payment: pay}, nil)
		must(t, err)
	}
	jobs := notifyJobs(t, pool, "venda.nova")
	if len(jobs) != 1 {
		t.Fatalf("avisos de venda = %d", len(jobs))
	}
	for _, want := range []string{`"valor": "R$ 97,00"`, `"whatsapp": "5511988887777"`, `"empresa": "Pizzaria Boa"`, `"origem": "asaas · card"`} {
		if !strings.Contains(jobs[0], want) {
			t.Errorf("payload sem %s: %s", want, jobs[0])
		}
	}
}

func TestRegistrationEmitsSignup(t *testing.T) {
	st, pool := newStore(t)
	subscribe(t, st, "cadastro.novo")
	ident := pendingIdentity(t, st, "owner", "5511988887777")
	must(t, st.CreateSignupLink(ctx, ident.ID, "hash-ok", time.Now().Add(time.Hour)))
	_, err := st.CompleteRegistration(ctx, store.Registration{TokenHash: "hash-ok", Email: "ana@mail.com", Form: regForm(),
		PlanCode: "free", TermsVersion: "2026-09-23"})
	must(t, err)
	jobs := notifyJobs(t, pool, "cadastro.novo")
	if len(jobs) != 1 || !strings.Contains(jobs[0], `"nome": "Ana Souza"`) || !strings.Contains(jobs[0], `"origem": "cadastro"`) {
		t.Fatalf("cadastro = %q", jobs)
	}
}

func TestEmitWithoutDestinationIsNoop(t *testing.T) {
	st, pool := newStore(t)
	must(t, st.EmitNotify(ctx, "venda.nova", map[string]string{"nome": "x"}))
	if n := notifyJobs(t, pool, "venda.nova"); len(n) != 0 {
		t.Fatalf("job sem destino: %v", n)
	}
	if got := store.FormatBRL(123456789); got != "R$ 1.234.567,89" {
		t.Fatalf("FormatBRL = %s", got)
	}
}
