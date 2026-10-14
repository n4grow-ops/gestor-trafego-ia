package store

import (
	"context"
	"time"
)

// ReportPref é a preferência do relatório do dono. Sem linha = padrão: só texto, sem convite feito.
type ReportPref struct {
	Audio   bool
	Offered bool
}

// GetReportPref lê a preferência da organização (padrão se nunca definida).
func (s *Store) GetReportPref(ctx context.Context, orgID int64) (ReportPref, error) {
	var p ReportPref
	err := s.pool.QueryRow(ctx, `SELECT coalesce((SELECT audio FROM report_prefs WHERE organization_id = $1), false),
		coalesce((SELECT offered_at IS NOT NULL FROM report_prefs WHERE organization_id = $1), false)`, orgID).Scan(&p.Audio, &p.Offered)
	return p, err
}

// SetReportAudio liga ou desliga o áudio. Quem responde ao convite também conta como convidado.
func (s *Store) SetReportAudio(ctx context.Context, orgID int64, audio bool) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO report_prefs (organization_id, audio, offered_at) VALUES ($1, $2, now())
		ON CONFLICT (organization_id) DO UPDATE SET audio = EXCLUDED.audio, updated_at = now(),
			offered_at = coalesce(report_prefs.offered_at, now())`, orgID, audio)
	return err
}

// MarkReportOffered registra o convite para o áudio. Devolve true só na primeira vez (atômico).
func (s *Store) MarkReportOffered(ctx context.Context, orgID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO report_prefs (organization_id, offered_at) VALUES ($1, now())
		ON CONFLICT (organization_id) DO UPDATE SET offered_at = now(), updated_at = now()
		WHERE report_prefs.offered_at IS NULL`, orgID)
	return tag.RowsAffected() == 1, err
}

// DayTotals soma gasto e conversas das campanhas da organização num dia. ok=false: sem resultado gravado.
func (s *Store) DayTotals(ctx context.Context, orgID int64, day time.Time) (spendCents, conversations int64, ok bool, err error) {
	var n int64
	err = s.pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(r.spend_cents), 0)::bigint, coalesce(sum(r.conversations), 0)::bigint
		FROM campaign_results r JOIN meta_publications p ON p.id = r.publication_id
		WHERE p.organization_id = $1 AND r.day = $2`, orgID, day.Format("2006-01-02")).Scan(&n, &spendCents, &conversations)
	return spendCents, conversations, n > 0, err
}
