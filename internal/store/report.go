package store

import (
	"context"
	"time"
)

// DailyStats é o resumo do relatório diário para o operador.
type DailyStats struct {
	NewContacts     int     // telefones novos
	Registered      int     // cadastros concluídos
	PendingWithLink int     // cadastros pendentes que já receberam link (total, não só na janela)
	Inbound         int     // mensagens recebidas
	Proposals       int     // propostas geradas
	Approved        int     // propostas aprovadas
	Creatives       int     // anúncios (imagens e vídeos) recebidos
	CostUSD         float64 // custo de IA
}

func (s *Store) DailyStats(ctx context.Context, since time.Time) (DailyStats, error) {
	var d DailyStats
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM whatsapp_identities WHERE created_at >= $1),
		(SELECT count(*) FROM organizations WHERE registration_status = 'complete' AND registered_at >= $1),
		(SELECT count(DISTINCT o.id) FROM organizations o
			JOIN members m ON m.organization_id = o.id
			JOIN whatsapp_identities w ON w.member_id = m.id
			JOIN signup_links l ON l.identity_id = w.id
			WHERE o.registration_status = 'pending'),
		(SELECT count(*) FROM messages WHERE direction = 'in' AND created_at >= $1),
		(SELECT count(*) FROM campaign_drafts WHERE created_at >= $1),
		(SELECT count(*) FROM campaign_drafts WHERE approved_at >= $1),
		(SELECT count(*) FROM creatives WHERE created_at >= $1),
		(SELECT coalesce(sum(cost_usd), 0)::float8 FROM llm_usage WHERE created_at >= $1)`, since).
		Scan(&d.NewContacts, &d.Registered, &d.PendingWithLink, &d.Inbound, &d.Proposals, &d.Approved, &d.Creatives, &d.CostUSD)
	return d, err
}
