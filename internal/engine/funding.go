package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fundingText lê na Meta como a conta paga e diz ao cliente se o anúncio tem saldo para rodar.
// Vazio quando a Meta não responde (sem permissão de pagamento, fora do ar): a mensagem não inventa saldo.
func (e *Engine) fundingText(ctx context.Context, token, adAccountID string, dailyBRL float64) string {
	fr, ok := e.Publisher.(meta.FundingReader)
	if !ok || adAccountID == "" {
		return ""
	}
	f, err := fr.Funding(ctx, token, adAccountID)
	if err != nil {
		e.Log.Warn("pagamento da conta de anúncios não lido", "ad_account", adAccountID, "erro", err)
		return ""
	}
	return describeFunding(f, dailyBRL, adAccountID)
}

// describeFunding monta o aviso de saldo: quanto tem e quantos dias cobre, ou o que falta para o anúncio
// rodar, com o link de pagamento da conta.
func describeFunding(f meta.Funding, dailyBRL float64, adAccountID string) string {
	daily := int64(math.Round(dailyBRL * 100))
	brl := func(cents int64) string { return campaign.BRL(float64(cents) / 100) }
	var parts []string
	problem := false
	switch {
	case paymentPending(f.Status):
		parts, problem = append(parts, msgFundsPending), true
	case !f.HasSource:
		parts, problem = append(parts, msgFundsNone), true
	case f.Prepaid && f.AvailableCents >= 0:
		if daily > 0 && f.AvailableCents < daily {
			parts, problem = append(parts, fmt.Sprintf(msgFundsLow, brl(f.AvailableCents), brl(daily))), true
		} else if daily > 0 {
			days := f.AvailableCents / daily
			unit := "dias"
			if days == 1 {
				unit = "dia"
			}
			parts = append(parts, fmt.Sprintf(msgFundsPrepaid, brl(f.AvailableCents), days, unit, brl(daily)))
		} else {
			parts = append(parts, fmt.Sprintf(msgFundsPrepaidNoDaily, brl(f.AvailableCents)))
		}
	case f.Prepaid:
		parts = append(parts, fmt.Sprintf(msgFundsPrepaidUnknown, BillingURL(adAccountID)))
	case f.Source != "":
		parts = append(parts, fmt.Sprintf(msgFundsCard, f.Source))
	}
	if f.SpendCapCents > 0 {
		left := max(f.SpendCapCents-f.SpentCents, 0)
		if daily > 0 && left < daily {
			parts, problem = append(parts, fmt.Sprintf(msgFundsCapLow, brl(left), brl(daily))), true
		} else {
			parts = append(parts, fmt.Sprintf(msgFundsCap, brl(left)))
		}
	}
	if problem {
		parts = append(parts, fmt.Sprintf(msgFundsAdd, BillingURL(adAccountID)))
	}
	return join(parts...)
}

// dailyBudget lê o orçamento por dia da proposta aprovada (0 se não achar).
func (e *Engine) dailyBudget(ctx context.Context, draftID int64) float64 {
	data, err := e.pubs().PublishData(ctx, draftID)
	if err != nil {
		return 0
	}
	var prop contract.CampaignProposal
	if json.Unmarshal(data.Proposal, &prop) != nil {
		return 0
	}
	return prop.DailyBudgetBRL
}

// activationFunding: no ATIVAR, o saldo da conta conectada contra o orçamento da campanha ligada.
func (e *Engine) activationFunding(ctx context.Context, orgID int64, token string, targets []store.Publication) string {
	conn, ok, err := e.Store.ActiveMeta(ctx, orgID)
	if err != nil || !ok || len(targets) == 0 {
		return ""
	}
	var daily float64
	for _, p := range targets {
		daily += e.dailyBudget(ctx, p.DraftID)
	}
	return e.fundingText(ctx, token, conn.AdAccountID, daily)
}
