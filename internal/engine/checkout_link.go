package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/asaas"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/paylink"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// checkoutGrace: o link vale até o vencimento + 3 dias (e nunca menos de 3 dias a partir de agora).
const checkoutGrace = 72 * time.Hour

// checkoutPayments é o que o checkout próprio precisa do Asaas além do BillingProvider. O asaas.Client
// tem; o fake dos testes antigos não, e aí a Mia segue com o link do Asaas.
type checkoutPayments interface {
	GetPayment(ctx context.Context, paymentID string) (asaas.Payment, error)
	FirstPayment(ctx context.Context, subID string) (asaas.Payment, error)
}

var errNoOwnCheckout = errors.New("engine: checkout próprio desligado")

// CheckoutURL cria o link /pagar/{token} da cobrança paymentID (pay_...) da assinatura subID (sub_...).
// Um token novo a cada chamada; o anterior continua valendo até expirar. Só o hash vai para o banco.
func (e *Engine) CheckoutURL(ctx context.Context, orgID int64, subID, paymentID string) (string, error) {
	cp, ok := e.Billing.(checkoutPayments)
	if e.CheckoutBaseURL == "" || !ok {
		return "", errNoOwnCheckout
	}
	p, err := cp.GetPayment(ctx, paymentID)
	if err != nil {
		return "", err
	}
	return e.checkoutLink(ctx, orgID, subID, p)
}

func (e *Engine) checkoutLink(ctx context.Context, orgID int64, subID string, p asaas.Payment) (string, error) {
	due, err := time.ParseInLocation("2006-01-02", p.DueDate, brt)
	if err != nil {
		return "", fmt.Errorf("engine: vencimento da cobrança ilegível: %q", p.DueDate)
	}
	// Fim do dia do vencimento + 3 dias, em São Paulo.
	expires := due.AddDate(0, 0, 1).Add(checkoutGrace)
	if min := e.now().Add(checkoutGrace); expires.Before(min) {
		expires = min
	}
	raw, hash, err := paylink.New()
	if err != nil {
		return "", err
	}
	if err := e.Store.CreateCheckoutLink(ctx, store.NewCheckoutLink{OrgID: orgID, TokenHash: hash, AsaasSub: subID,
		AsaasPayment: p.ID, AmountCents: int64(math.Round(p.Value * 100)), DueDate: due, ExpiresAt: expires,
		Method: map[bool]string{true: "card", false: "pix"}[p.BillingType == asaas.BillingCard]}); err != nil {
		return "", err
	}
	return strings.TrimRight(e.CheckoutBaseURL, "/") + "/" + raw, nil
}

// ownCheckoutLink troca o link do Asaas pelo nosso quando o checkout próprio está ligado. Qualquer falha
// devolve o link do Asaas (fallback): o cliente nunca fica sem como pagar.
func (e *Engine) ownCheckoutLink(ctx context.Context, orgID int64, subID, paymentID, fallback string) string {
	cp, ok := e.Billing.(checkoutPayments)
	if e.CheckoutBaseURL == "" || !ok || subID == "" {
		return fallback
	}
	var link string
	var err error
	if paymentID == "" {
		var p asaas.Payment
		if p, err = cp.FirstPayment(ctx, subID); err == nil {
			link, err = e.checkoutLink(ctx, orgID, subID, p)
		}
	} else {
		link, err = e.CheckoutURL(ctx, orgID, subID, paymentID)
	}
	if err != nil {
		e.Log.Warn("checkout próprio indisponível; segue o link do Asaas", "org_id", orgID, "erro", err)
		return fallback
	}
	return link
}

// refreshCheckoutLink: ao reenviar o link pendente, um link nosso que vence em menos de um dia é trocado por
// um novo (o antigo expira e o cliente cairia na página de link vencido). Link do Asaas fica como está.
func (e *Engine) refreshCheckoutLink(ctx context.Context, orgID int64, bs store.BillingState) string {
	base := strings.TrimRight(e.CheckoutBaseURL, "/")
	if base == "" || bs.ProviderRef == "" || !strings.HasPrefix(bs.CheckoutURL, base+"/") {
		return bs.CheckoutURL
	}
	exp, err := e.Store.LatestCheckoutExpiry(ctx, orgID, bs.ProviderRef)
	if err == nil && exp.After(e.now().Add(24*time.Hour)) {
		return bs.CheckoutURL
	}
	link := e.ownCheckoutLink(ctx, orgID, bs.ProviderRef, "", bs.CheckoutURL)
	if link != bs.CheckoutURL {
		if err := e.Store.SetCheckoutURL(ctx, orgID, link); err != nil {
			e.Log.Warn("link de pagamento novo não gravado", "org_id", orgID, "erro", err)
		}
	}
	return link
}
