package store

import "context"

// CampaignQuota é o uso de campanhas do mês (fuso de São Paulo) contra o limite do plano.
type CampaignQuota struct {
	Plan string // nome do plano ("" = sem assinatura)
	Max  int    // 0 = sem limite
	Used int    // campanhas novas aprovadas no mês (projetos distintos)
	Edit bool   // o projeto já teve campanha aprovada: ajuste dela não gasta campanha
}

// Blocked diz se uma campanha nova passaria do limite do plano.
func (q CampaignQuota) Blocked() bool { return !q.Edit && q.Max > 0 && q.Used >= q.Max }

// CampaignQuota conta as campanhas aprovadas no mês da organização. Sem assinatura ativa (cliente
// trazido pela equipe) ou organização liberada (organizations.unlimited) não há limite. projectID nil =
// conversa sem projeto ativo.
func (s *Store) CampaignQuota(ctx context.Context, orgID int64, projectID *int64) (CampaignQuota, error) {
	var q CampaignQuota
	var plan *string
	var max *int
	var unlimited bool
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT p.name FROM subscriptions sub JOIN plans p ON p.code = sub.plan_code
			WHERE sub.organization_id = $1 AND sub.status IN ('active', 'trialing') ORDER BY sub.id DESC LIMIT 1),
		(SELECT p.max_campaigns_month FROM subscriptions sub JOIN plans p ON p.code = sub.plan_code
			WHERE sub.organization_id = $1 AND sub.status IN ('active', 'trialing') ORDER BY sub.id DESC LIMIT 1),
		(SELECT count(DISTINCT coalesce(project_id, -id)) FROM campaign_drafts
			WHERE organization_id = $1 AND status = 'approved'
			  AND approved_at >= date_trunc('month', now() AT TIME ZONE 'America/Sao_Paulo') AT TIME ZONE 'America/Sao_Paulo'),
		$2::bigint IS NOT NULL AND EXISTS (SELECT 1 FROM campaign_drafts WHERE project_id = $2 AND status = 'approved'),
		coalesce((SELECT unlimited FROM organizations WHERE id = $1), false)`,
		orgID, projectID).Scan(&plan, &max, &q.Used, &q.Edit, &unlimited)
	if err != nil {
		return q, err
	}
	if plan != nil {
		q.Plan = *plan
	}
	if max != nil && !unlimited {
		q.Max = *max
	}
	return q, nil
}
