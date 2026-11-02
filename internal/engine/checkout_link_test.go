package engine_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/asaas"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fakeCheckoutBilling é o fakeBilling com as cobranças que o checkout próprio lê.
type fakeCheckoutBilling struct {
	*fakeBilling
}

func (f fakeCheckoutBilling) FirstPayment(_ context.Context, subID string) (asaas.Payment, error) {
	return asaas.Payment{ID: "pay_" + subID, Status: "PENDING", BillingType: asaas.BillingPix, Value: 397, DueDate: "2026-09-22"}, nil
}
func (f fakeCheckoutBilling) GetPayment(_ context.Context, id string) (asaas.Payment, error) {
	return asaas.Payment{ID: id, Status: "PENDING", BillingType: asaas.BillingPix, Value: 147, DueDate: "2026-11-30"}, nil
}

var ownLinkRe = regexp.MustCompile(`https://cadastro\.meuanuncio\.io/pagar/[0-9A-Za-z]{24}`)

func TestCheckoutOwnLinkReplacesAsaasPage(t *testing.T) {
	h, _ := newBillingHarness(t)
	fb := &fakeBilling{}
	h.eng.Billing = fakeCheckoutBilling{fb}
	h.eng.CheckoutBaseURL = "https://cadastro.meuanuncio.io/pagar"
	h.must(h.st.SetRegistrationStatus(ctx, mustOrg(h), "complete"))

	h.say("oi")
	h.say("2")
	h.say("pix")
	h.say("meu cpf é 529.982.247-25")
	got := h.wa.last().Text
	link := ownLinkRe.FindString(got)
	if link == "" || strings.Contains(got, "asaas.com") {
		t.Fatalf("mensagem sem o checkout próprio: %q", got)
	}
	var amount int64
	var payment string
	var expires time.Time
	h.must(h.pool.QueryRow(ctx, `SELECT amount_cents, asaas_payment, expires_at FROM checkout_links`).Scan(&amount, &payment, &expires))
	var method string
	h.must(h.pool.QueryRow(ctx, `SELECT method FROM checkout_links`).Scan(&method))
	if method != "pix" {
		t.Errorf("forma = %q", method)
	}
	if amount != 39700 || payment != "pay_sub_1" {
		t.Fatalf("link gravado: %d %s", amount, payment)
	}
	// Vence no fim do dia 22/09 + 3 dias (São Paulo).
	if want := time.Date(2026, 9, 26, 3, 0, 0, 0, time.UTC); !expires.Equal(want) {
		t.Errorf("validade = %v, want %v", expires.UTC(), want)
	}
	// O token cru não vai para o banco.
	var n int
	h.must(h.pool.QueryRow(ctx, `SELECT count(*) FROM checkout_links WHERE token_hash = $1`, link[strings.LastIndexByte(link, '/')+1:]).Scan(&n))
	if n != 0 {
		t.Fatal("token cru gravado")
	}
	// Reenvio com o link ainda válido: o mesmo link, sem criar outro.
	h.say("e agora?")
	if got := h.wa.last().Text; !strings.Contains(got, link) {
		t.Fatalf("reenvio = %q", got)
	}
	h.must(h.pool.QueryRow(ctx, `SELECT count(*) FROM checkout_links`).Scan(&n))
	if n != 1 {
		t.Fatalf("links = %d", n)
	}
	// Link perto de vencer: o reenvio troca por um novo.
	_, err := h.pool.Exec(ctx, `UPDATE checkout_links SET expires_at = $1`, time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC))
	h.must(err)
	h.say("cadê o link?")
	got = h.wa.last().Text
	if nl := ownLinkRe.FindString(got); nl == "" || nl == link {
		t.Fatalf("reenvio não trocou o link vencendo: %q", got)
	}
}

func TestCheckoutOwnLinkOnPixRenewal(t *testing.T) {
	h, _ := newBillingHarness(t)
	h.eng.Billing = fakeCheckoutBilling{&fakeBilling{}}
	h.eng.CheckoutBaseURL = "https://cadastro.meuanuncio.io/pagar"
	h.say("oi")
	_, err := h.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, provider, provider_ref)
		VALUES ($1, 'free', 'active', 'asaas', 'sub_9') ON CONFLICT (organization_id) DO UPDATE SET provider_ref = 'sub_9', status = 'active'`, mustOrg(h))
	h.must(err)
	payload, err := json.Marshal(store.BillingNotice{OrgID: mustOrg(h), Kind: store.BillingNoticeDue, PlanName: "Básico",
		InvoiceURL: "https://asaas/i/2", DueDate: "2026-11-30", PaymentID: "pay_2", SubRef: "sub_9"})
	h.must(err)
	h.must(h.eng.HandleJob(ctx, store.Job{Kind: engine.JobBillingNotify, Payload: payload}))
	if got := h.wa.last().Text; ownLinkRe.FindString(got) == "" || strings.Contains(got, "asaas/i/2") {
		t.Fatalf("aviso = %q", got)
	}
}

func TestCheckoutFallsBackToAsaasLink(t *testing.T) {
	// Provedor sem as cobranças (fake antigo) com a URL ligada: segue o link do Asaas, sem quebrar.
	h, _ := newBillingHarness(t)
	h.eng.CheckoutBaseURL = "https://cadastro.meuanuncio.io/pagar"
	h.say("oi")
	payload, err := json.Marshal(store.BillingNotice{OrgID: mustOrg(h), Kind: store.BillingNoticeOverdue, PlanName: "Básico",
		InvoiceURL: "https://asaas/i/3", PaymentID: "pay_3", SubRef: "sub_3"})
	h.must(err)
	h.must(h.eng.HandleJob(ctx, store.Job{Kind: engine.JobBillingNotify, Payload: payload}))
	if got := h.wa.last().Text; !strings.Contains(got, "https://asaas/i/3") {
		t.Fatalf("aviso = %q", got)
	}
}
