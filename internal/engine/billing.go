package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// billingAsk reconhece o pedido de pagar a Meta: colocar saldo, cadastrar cartão, pagamento pendente.
var billingAsk = regexp.MustCompile(`(?i)\b((colocar|coloco|coloca|adicionar|adiciono|incluir|cadastrar|cadastro|depositar|deposito|recarregar|botar|trocar|mudar)\s+((o|um|meu|minha|de|mais|novo|nova)\s+)*(saldo|cart[aã]o|cr[eé]dito|fundos|forma\s+de\s+pagamento)|(faltou|falta|sem|acabou(\s+o)?)\s+saldo|pagar\s+((o|os|a|as|meu|minha|meus)\s+)*(an[uú]ncios?|meta|facebook|conta)|pagamento\s+(pendente|recusado|n[aã]o\s+passou))\b`)

// isBillingCommand diz se a mensagem é o pedido direto de pagamento (frase curta, fora do atendimento humano).
func isBillingCommand(state, text string) bool {
	return state != store.StateNeedsHuman && len(strings.Fields(text)) <= 12 && billingAsk.MatchString(text)
}

// BillingURL é a página de pagamento da conta de anúncios na Meta (cartão e saldo pré-pago).
func BillingURL(adAccountID string) string {
	return "https://business.facebook.com/billing_hub/payment_settings/?asset_id=" + strings.TrimPrefix(adAccountID, "act_")
}

// billingText é o link de pagamento da conta com o passo a passo de cartão e Pix.
func billingText(adAccountID, accountName string) string {
	return fmt.Sprintf(msgBilling, accountName, BillingURL(adAccountID))
}

// sendBillingLink manda o link de pagamento da conta conectada; sem conta, primeiro o link de conexão.
func (e *Engine) sendBillingLink(ctx context.Context, ident store.Identity, conv store.Conversation, lead string) error {
	conn, ok, err := e.Store.ActiveMeta(ctx, ident.OrgID)
	if err != nil {
		return err
	}
	if !ok {
		if e.MetaConnectBaseURL == "" {
			return e.send(ctx, conv, join(lead, msgBillingNoAccount))
		}
		return e.sendMetaLink(ctx, ident, conv, join(lead, msgBillingConnectFirst))
	}
	noteAction(ctx, "mandou link de pagamento da Meta")
	e.audit(ctx, ident.OrgID, member(ident), "billing_link_sent", map[string]any{"conversation_id": conv.ID})
	return e.send(ctx, conv, join(lead, billingText(conn.AdAccountID, conn.AdAccountName)))
}
