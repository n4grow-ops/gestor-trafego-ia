package store

import (
	"context"
	"encoding/json"
	"time"
)

// ActivePublications lista as campanhas ligadas de todas as organizações (para o resultado diário).
func (s *Store) ActivePublications(ctx context.Context) ([]Publication, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+publicationCols+` FROM meta_publications WHERE status = 'active' ORDER BY organization_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Publication
	for rows.Next() {
		p, err := scanPublication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CampaignResult é o resultado de uma campanha num dia.
type CampaignResult struct {
	PublicationID int64     `json:"publication_id"`
	Day           time.Time `json:"day"`
	SpendCents    int64     `json:"spend_cents"`
	Impressions   int64     `json:"impressions"`
	Reach         int64     `json:"reach"`
	Conversations int64     `json:"conversations"`
}

// SaveCampaignResult grava (ou atualiza) o resultado do dia.
func (s *Store) SaveCampaignResult(ctx context.Context, r CampaignResult) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO campaign_results (publication_id, day, spend_cents, impressions, reach, conversations)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (publication_id, day) DO UPDATE SET spend_cents = EXCLUDED.spend_cents, impressions = EXCLUDED.impressions,
			reach = EXCLUDED.reach, conversations = EXCLUDED.conversations`,
		r.PublicationID, r.Day.Format("2006-01-02"), r.SpendCents, r.Impressions, r.Reach, r.Conversations)
	return err
}

// SaveFunding guarda a última leitura do pagamento da conta conectada (o painel mostra).
func (s *Store) SaveFunding(ctx context.Context, orgID int64, funding any, at time.Time) error {
	raw, err := json.Marshal(funding)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE meta_connections SET funding = $2, funding_checked_at = $3
		WHERE organization_id = $1 AND status = 'active'`, orgID, string(raw), at)
	return err
}
