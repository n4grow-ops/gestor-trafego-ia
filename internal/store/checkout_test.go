package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/paylink"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestCheckoutLinkLifecycle(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	link := store.NewCheckoutLink{OrgID: ident.OrgID, AsaasSub: "sub_1", AsaasPayment: "pay_1", AmountCents: 39700,
		DueDate: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), ExpiresAt: time.Now().Add(72 * time.Hour)}
	_, link.TokenHash, _ = paylink.New()
	// Sem assinatura não há link.
	if err := st.CreateCheckoutLink(ctx, link); !errors.Is(err, store.ErrNoSubscription) {
		t.Fatalf("sem assinatura: %v", err)
	}
	must(t, st.ChooseBillingPlan(ctx, ident.OrgID, "starter"))
	must(t, st.CreateCheckoutLink(ctx, link))
	// Mesmo hash não entra duas vezes.
	if err := st.CreateCheckoutLink(ctx, link); err == nil {
		t.Fatal("hash repetido aceito")
	}
	got, err := st.CheckoutByHash(ctx, link.TokenHash)
	must(t, err)
	if got.OrgID != ident.OrgID || got.PlanName != "Intermediário" || got.AmountCents != 39700 || got.AsaasPayment != "pay_1" ||
		got.DueDate.Format("2006-01-02") != "2026-11-02" || got.Paid || !got.Usable(time.Now()) {
		t.Fatalf("link = %+v", got)
	}
	if _, err := st.CheckoutByHash(ctx, paylink.Hash("outro")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("desconhecido: %v", err)
	}
	// Recusas: bloqueia na terceira (max 3) e o desbloqueio só vale depois do prazo.
	for i := 1; i <= 3; i++ {
		locked, err := st.CheckoutCardFailed(ctx, got.ID, 3, time.Hour)
		must(t, err)
		if locked != (i == 3) {
			t.Fatalf("recusa %d: bloqueado = %v", i, locked)
		}
	}
	must(t, st.CheckoutUnlock(ctx, got.ID))
	if l, _ := st.CheckoutByHash(ctx, link.TokenHash); !l.Locked(time.Now()) {
		t.Fatal("desbloqueou antes do prazo")
	}
	must(t, st.CheckoutCardPaid(ctx, got.ID))
	if l, _ := st.CheckoutByHash(ctx, link.TokenHash); !l.Paid || l.Locked(time.Now()) || l.FailedAttempts != 0 || l.Usable(time.Now()) {
		t.Fatalf("pago = %+v", l)
	}
	exp, err := st.LatestCheckoutExpiry(ctx, ident.OrgID, "sub_1")
	must(t, err)
	if exp.Sub(link.ExpiresAt).Abs() > time.Second {
		t.Fatalf("validade = %v", exp)
	}
	if _, err := st.LatestCheckoutExpiry(ctx, ident.OrgID, "sub_x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sem link: %v", err)
	}
	// O link é apagado junto com a organização (nada órfão).
	var n int
	must(t, pool.QueryRow(ctx, `SELECT count(*) FROM checkout_links WHERE token_hash = $1`, link.TokenHash).Scan(&n))
	if n != 1 {
		t.Fatalf("links = %d", n)
	}
}

func TestCheckoutLinkPaidByWebhook(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	must(t, st.ChooseBillingPlan(ctx, ident.OrgID, "starter"))
	must(t, st.SetBillingCheckout(ctx, ident.OrgID, "pix", "sub_1", ""))
	_, hash, _ := paylink.New()
	must(t, st.CreateCheckoutLink(ctx, store.NewCheckoutLink{OrgID: ident.OrgID, TokenHash: hash, AsaasSub: "sub_1", AsaasPayment: "pay_1",
		AmountCents: 39700, DueDate: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}))
	_, err := st.ApplyAsaasEvent(ctx, store.AsaasEvent{ID: "evt_1", Event: "PAYMENT_RECEIVED", Payment: &store.AsaasPayment{ID: "pay_1",
		Subscription: "sub_1", Value: 397, BillingType: "PIX", DueDate: "2026-11-02"}}, nil)
	must(t, err)
	if l, _ := st.CheckoutByHash(ctx, hash); !l.Paid {
		t.Fatal("webhook pago não aparece no link")
	}
}

func TestCheckoutHolderPrefill(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	_, err := pool.Exec(ctx, `UPDATE members SET cpf = '52998224725', email = 'ana@x.com' WHERE organization_id = $1`, ident.OrgID)
	must(t, err)
	h, err := st.CheckoutHolder(ctx, ident.OrgID)
	must(t, err)
	if h.Name != "Ana" || h.CpfCnpj != "52998224725" || h.Email != "ana@x.com" || h.Phone != "11988887777" {
		t.Fatalf("titular = %+v", h)
	}
}
