package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fundingText lê na Meta como a conta paga e diz ao cliente se o anúncio tem saldo para rodar.
// Vazio quando a Meta não responde (sem permissão de pagamento, fora do ar): a mensagem não inventa saldo.
func (e *Engine) fundingText(ctx context.Context, orgID int64, token, adAccountID string, dailyBRL float64) string {
	fr, ok := e.Publisher.(meta.FundingReader)
	if !ok || adAccountID == "" {
		return ""
	}
	f, err := fr.Funding(ctx, token, adAccountID)
	if err != nil {
		e.Log.Warn("pagamento da conta de anúncios não lido", "ad_account", adAccountID, "erro", err)
		return ""
	}
	e.cacheFunding(ctx, orgID, f)
	text, _ := describeFunding(f, dailyBRL, adAccountID)
	return text
}

// describeFunding monta o aviso de saldo: quanto tem e quantos dias cobre, ou o que falta para o anúncio
// rodar, com o link de pagamento da conta.
func describeFunding(f meta.Funding, dailyBRL float64, adAccountID string) (string, bool) {
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
	return join(parts...), problem
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
	return e.fundingText(ctx, orgID, token, conn.AdAccountID, daily)
}

// paidAsk: o cliente avisa que pagou ou colocou saldo.
var paidAsk = regexp.MustCompile(`(?i)^\W*(j[aá]\s+)?(paguei|fiz o pagamento|fiz o pix|pix feito|depositei|coloquei (o |um )?saldo|adicionei (o |meu |um )?(cart[aã]o|saldo)|cadastrei (o |meu )?cart[aã]o|t[aá] pago|est[aá] pago)\b`)

// isPaidCommand: aviso curto de pagamento, fora do atendimento humano.
func isPaidCommand(state, text string) bool {
	return state != store.StateNeedsHuman && len(strings.Fields(text)) <= 10 && paidAsk.MatchString(text)
}

// paidCheck confere o pagamento na Meta depois do "paguei" e responde com o estado real da conta.
func (e *Engine) paidCheck(ctx context.Context, ident store.Identity, conv store.Conversation) error {
	conn, ok, err := e.Store.ActiveMeta(ctx, ident.OrgID)
	if err != nil {
		return err
	}
	fr, canRead := e.Publisher.(meta.FundingReader)
	if !ok || !canRead || e.MetaTokenKey == nil {
		return e.sendBillingLink(ctx, ident, conv, "")
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, ident.OrgID)
	if err != nil {
		return e.send(ctx, conv, msgPaidUnknown)
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		return e.send(ctx, conv, msgPaidUnknown)
	}
	f, err := fr.Funding(ctx, string(token), conn.AdAccountID)
	if err != nil {
		e.Log.Warn("pagamento não conferido", "org_id", ident.OrgID, "erro", err)
		return e.send(ctx, conv, msgPaidUnknown)
	}
	e.cacheFunding(ctx, ident.OrgID, f)
	paused, _ := e.pubs().ListPublications(ctx, ident.OrgID, "paused")
	var daily float64
	for _, p := range paused {
		daily += e.dailyBudget(ctx, p.DraftID)
	}
	text, problem := describeFunding(f, daily, conn.AdAccountID)
	e.audit(ctx, ident.OrgID, member(ident), "payment_checked", map[string]any{"ok": !problem})
	if problem {
		return e.send(ctx, conv, join(msgPaidPending, text))
	}
	reply := []string{msgPaidOK, text}
	if len(paused) > 0 {
		reply = append(reply, msgPaidActivate)
	}
	return e.send(ctx, conv, join(reply...))
}

// FundingView é a leitura do pagamento guardada para o painel, com o resumo pronto.
type FundingView struct {
	meta.Funding
	Flag string `json:"flag"` // ok, sem_pagamento, pendente, saldo_baixo, limite_baixo
}

// FundingFlag resume o pagamento contra o orçamento por dia das campanhas.
func FundingFlag(f meta.Funding, dailyCents int64) string {
	switch {
	case paymentPending(f.Status):
		return "pendente"
	case !f.HasSource:
		return "sem_pagamento"
	case f.Prepaid && f.AvailableCents >= 0 && dailyCents > 0 && f.AvailableCents < dailyCents:
		return "saldo_baixo"
	case f.SpendCapCents > 0 && dailyCents > 0 && f.SpendCapCents-f.SpentCents < dailyCents:
		return "limite_baixo"
	}
	return "ok"
}

// cacheFunding guarda a leitura para o painel (falha só vai pro log).
func (e *Engine) cacheFunding(ctx context.Context, orgID int64, f meta.Funding) {
	if err := e.Store.SaveFunding(ctx, orgID, f, e.now()); err != nil {
		e.Log.Warn("pagamento não guardado para o painel", "org_id", orgID, "erro", err)
	}
}
