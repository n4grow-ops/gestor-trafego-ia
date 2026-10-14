package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	// JobDailyResults manda às 8h o resultado de ontem de cada campanha ligada.
	JobDailyResults = "results.daily"
	resultsHour     = 8
	TplDailyResults = "resultado_diario"
	// JobFundingUpdate relê o pagamento de uma organização (botão "atualizar" do painel).
	JobFundingUpdate = "meta.funding"
)

// EnsureDailyResults garante o resultado diário agendado (chamado quando o worker sobe).
func (e *Engine) EnsureDailyResults(ctx context.Context) error {
	pending, err := e.Store.HasPendingKind(ctx, JobDailyResults)
	if err != nil || pending {
		return err
	}
	return e.enqueueDaily(ctx, JobDailyResults, resultsHour)
}

// orgResult junta o resultado do dia das campanhas de uma organização.
type orgResult struct {
	orgID        int64
	lines        []string
	spend, convs int64
	dailyCents   int64
}

// dailyResults lê os insights de ontem das campanhas ligadas, grava e manda o resumo ao dono.
func (e *Engine) dailyResults(ctx context.Context) error {
	defer func() {
		if err := e.enqueueDaily(ctx, JobDailyResults, resultsHour); err != nil {
			e.Log.Error("próximo resultado diário não agendado", "erro", err)
		}
	}()
	ir, ok := e.Publisher.(meta.InsightsReader)
	if !ok || e.MetaTokenKey == nil {
		return nil
	}
	pubs, err := e.pubs().ActivePublications(ctx)
	if err != nil {
		return err
	}
	y, m, d := e.now().In(brt).AddDate(0, 0, -1).Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	byOrg := map[int64]*orgResult{}
	var order []int64
	tokens := map[int64]string{}
	for _, p := range pubs {
		tok, ok := tokens[p.OrgID]
		if !ok {
			_, sealed, err := e.pubs().MetaTokenSealed(ctx, p.OrgID)
			if err != nil {
				continue
			}
			raw, err := e.MetaTokenKey.Open(sealed)
			if err != nil {
				continue
			}
			tok = string(raw)
			tokens[p.OrgID] = tok
		}
		in, err := ir.CampaignInsights(ctx, tok, p.CampaignID, day)
		if err != nil {
			e.Log.Warn("resultado da campanha não lido", "campaign_id", p.CampaignID, "erro", err)
			continue
		}
		if err := e.Store.SaveCampaignResult(ctx, store.CampaignResult{PublicationID: p.ID, Day: day, SpendCents: in.SpendCents,
			Impressions: in.Impressions, Reach: in.Reach, Conversations: in.Conversations}); err != nil {
			e.Log.Warn("resultado não gravado", "publication_id", p.ID, "erro", err)
		}
		r := byOrg[p.OrgID]
		if r == nil {
			r = &orgResult{orgID: p.OrgID}
			byOrg[p.OrgID] = r
			order = append(order, p.OrgID)
		}
		name := "Campanha"
		if data, err := e.pubs().PublishData(ctx, p.DraftID); err == nil {
			if data.ProjectName != "" {
				name = data.ProjectName
			}
			var prop struct {
				Daily float64 `json:"daily_budget_brl"`
			}
			if json.Unmarshal(data.Proposal, &prop) == nil {
				r.dailyCents += int64(prop.Daily * 100)
			}
		}
		r.spend += in.SpendCents
		r.convs += in.Conversations
		r.lines = append(r.lines, fmt.Sprintf("• *%s*: %s", name, resultLine(in.SpendCents, in.Conversations)))
	}
	for _, orgID := range order {
		r := byOrg[orgID]
		if r.spend == 0 {
			continue // dia sem gasto: nada a contar
		}
		if err := e.sendResults(ctx, r, tokens[orgID], day.Format("02/01")); err != nil {
			e.Log.Error("resultado diário não enviado", "org_id", orgID, "erro", err)
		}
	}
	return nil
}

func brlCents(c int64) string { return campaign.BRL(float64(c) / 100) }

func resultLine(spend, convs int64) string {
	switch convs {
	case 0:
		return brlCents(spend) + " investidos · nenhuma conversa ainda"
	case 1:
		return brlCents(spend) + " investidos · 1 conversa · " + brlCents(spend) + " por conversa"
	}
	return fmt.Sprintf("%s investidos · %d conversas · %s por conversa", brlCents(spend), convs, brlCents(spend/convs))
}

// sendResults manda o resumo do dia ao dono, com o saldo da conta (pré-paga) ou o problema de pagamento.
func (e *Engine) sendResults(ctx context.Context, r *orgResult, token, date string) error {
	conv, err := e.Store.OwnerConversation(ctx, r.orgID)
	if err != nil {
		return err
	}
	parts := []string{fmt.Sprintf(msgResultsHead, date) + "\n" + strings.Join(r.lines, "\n")}
	if len(r.lines) > 1 {
		parts = append(parts, "Total: "+resultLine(r.spend, r.convs))
	}
	if conn, ok, err := e.Store.ActiveMeta(ctx, r.orgID); err == nil && ok {
		if fr, ok := e.Publisher.(meta.FundingReader); ok {
			if f, err := fr.Funding(ctx, token, conn.AdAccountID); err == nil {
				e.cacheFunding(ctx, r.orgID, f)
				text, problem := describeFunding(f, float64(r.dailyCents)/100, conn.AdAccountID)
				if problem || (f.Prepaid && f.AvailableCents >= 0) {
					parts = append(parts, text)
				}
			}
		}
	}
	cost := "-"
	if r.convs > 0 {
		cost = brlCents(r.spend / r.convs)
	}
	tpl := &whatsapp.Template{Name: TplDailyResults, Params: []string{templateParam(date), templateParam(brlCents(r.spend)),
		templateParam(fmt.Sprint(r.convs)), templateParam(cost)}}
	e.audit(ctx, r.orgID, "system", "results_sent", map[string]any{"spend_cents": r.spend, "conversations": r.convs})
	return e.Store.Enqueue(ctx, systemSendJob(conv.ID, conv.ChatID, join(parts...), tpl))
}

// FundingUpdateJob relê o pagamento de uma organização (pedido pelo painel).
type FundingUpdateJob struct {
	OrgID int64 `json:"org_id"`
}

func (e *Engine) fundingUpdate(ctx context.Context, raw []byte) error {
	var j FundingUpdateJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("engine: payload de pagamento inválido: %w", err)
	}
	fr, ok := e.Publisher.(meta.FundingReader)
	if !ok || e.MetaTokenKey == nil {
		return nil
	}
	conn, ok, err := e.Store.ActiveMeta(ctx, j.OrgID)
	if err != nil || !ok {
		return err
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, j.OrgID)
	if err != nil {
		return nil
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		return nil
	}
	f, err := fr.Funding(ctx, string(token), conn.AdAccountID)
	if err != nil {
		return err
	}
	e.cacheFunding(ctx, j.OrgID, f)
	return nil
}

// JobTeamPause pausa uma campanha a pedido da equipe (painel). Ativar continua só com o cliente.
const JobTeamPause = "campaign.pause"

// TeamPauseJob é o pedido de pausa da equipe.
type TeamPauseJob struct {
	PublicationID int64  `json:"publication_id"`
	By            string `json:"by"`
}

func (e *Engine) teamPause(ctx context.Context, raw []byte) error {
	var j TeamPauseJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("engine: payload de pausa inválido: %w", err)
	}
	if !e.publishOn() {
		return nil
	}
	p, err := e.pubs().GetPublication(ctx, j.PublicationID)
	if err != nil {
		return err
	}
	if p.Status != "active" {
		return nil // já pausada, reprovada ou com erro
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, p.OrgID)
	if err != nil {
		return err
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		return err
	}
	if err := e.Publisher.SetStatus(ctx, string(token), p.CampaignID, "PAUSED"); err != nil {
		return err
	}
	if err := e.pubs().SetPublicationStatus(ctx, p.ID, "paused"); err != nil {
		return err
	}
	e.audit(ctx, p.OrgID, "system", "campaign_paused_by_team", map[string]any{"publication_id": p.ID, "campaign_id": p.CampaignID, "by": j.By})
	name := "sua campanha"
	if data, err := e.pubs().PublishData(ctx, p.DraftID); err == nil && data.ProjectName != "" {
		name = "a campanha *" + data.ProjectName + "*"
	}
	conv, err := e.Store.OwnerConversation(ctx, p.OrgID)
	if err != nil {
		return nil
	}
	return e.Store.Enqueue(ctx, systemSendJob(conv.ID, conv.ChatID, fmt.Sprintf(msgTeamPaused, name), nil))
}
