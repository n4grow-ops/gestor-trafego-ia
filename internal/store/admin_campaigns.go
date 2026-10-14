package store

import (
	"context"
	"encoding/json"
	"time"
)

// AdminCampaign é uma campanha publicada na Meta como o painel mostra.
type AdminCampaign struct {
	ID               int64           `json:"id"`
	OrgID            int64           `json:"org_id"`
	OrgName          string          `json:"org_name"`
	ProjectName      string          `json:"project_name"`
	Status           string          `json:"status"`
	CampaignID       string          `json:"campaign_id"`
	Error            string          `json:"error"`
	CreatedAt        time.Time       `json:"created_at"`
	ActivatedAt      *time.Time      `json:"activated_at"`
	DailyBudgetBRL   float64         `json:"daily_budget_brl"`
	AdAccountID      string          `json:"ad_account_id"`
	Funding          json.RawMessage `json:"-"`
	FundingCheckedAt *time.Time      `json:"funding_checked_at"`
	Spend7dCents     int64           `json:"spend_7d_cents"`
	Conversations7d  int64           `json:"conversations_7d"`
	// Preenchidos pela api.
	FundingFlag string `json:"funding_flag"`
	AdsManager  string `json:"ads_manager_url"`
}

// AdminCampaigns lista as campanhas (status vazio = todas), da mais nova para a mais antiga.
func (s *Store) AdminCampaigns(ctx context.Context, status string) ([]AdminCampaign, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.id, p.organization_id, coalesce(nullif(o.trade_name, ''), o.name), coalesce(pr.name, ''),
			p.status, p.campaign_id, p.error, p.created_at, p.activated_at,
			coalesce((d.proposal->>'daily_budget_brl')::float8, 0), coalesce(mc.ad_account_id, ''), mc.funding, mc.funding_checked_at,
			(SELECT coalesce(sum(r.spend_cents), 0) FROM campaign_results r WHERE r.publication_id = p.id AND r.day > current_date - 7)::bigint,
			(SELECT coalesce(sum(r.conversations), 0) FROM campaign_results r WHERE r.publication_id = p.id AND r.day > current_date - 7)::bigint
		FROM meta_publications p
		JOIN organizations o ON o.id = p.organization_id
		LEFT JOIN projects pr ON pr.id = p.project_id
		LEFT JOIN campaign_drafts d ON d.id = p.draft_id
		LEFT JOIN meta_connections mc ON mc.id = p.meta_connection_id
		WHERE ($1 = '' OR p.status = $1)
		ORDER BY p.created_at DESC LIMIT 300`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminCampaign{}
	for rows.Next() {
		var c AdminCampaign
		if err := rows.Scan(&c.ID, &c.OrgID, &c.OrgName, &c.ProjectName, &c.Status, &c.CampaignID, &c.Error, &c.CreatedAt, &c.ActivatedAt,
			&c.DailyBudgetBRL, &c.AdAccountID, &c.Funding, &c.FundingCheckedAt, &c.Spend7dCents, &c.Conversations7d); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
