package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrPaidSubscription: o cliente já paga (assinatura ativa ou em atraso); teste grátis não substitui isso.
var ErrPaidSubscription = errors.New("store: cliente com assinatura paga")

// MaxTrialDays é o teste grátis mais longo que o painel libera de uma vez.
const MaxTrialDays = 90

// GrantTrial libera o sistema por days dias a partir de now, sem cobrança (status trialing até
// current_period_end). days = 0 encerra o teste em andamento. Sem assinatura, nasce uma no plano visível
// mais barato: é o plano que aparece se o cliente quiser pagar depois. Devolve o fim do teste.
func (s *Store) GrantTrial(ctx context.Context, orgID int64, days int, now time.Time) (time.Time, error) {
	if days < 0 || days > MaxTrialDays {
		return time.Time{}, fmt.Errorf("%w: dias entre 0 e %d", ErrInvalidInput, MaxTrialDays)
	}
	end := now.Add(time.Duration(days) * 24 * time.Hour)
	if days == 0 {
		tag, err := s.pool.Exec(ctx, `UPDATE subscriptions SET current_period_end = $2, updated_at = now()
			WHERE organization_id = $1 AND status = 'trialing'`, orgID, end)
		if err == nil && tag.RowsAffected() == 0 {
			return end, ErrNoSubscription
		}
		return end, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)`, orgID).Scan(&exists); err != nil {
		return end, err
	}
	if !exists {
		return end, ErrNotFound
	}
	var status string
	err := s.pool.QueryRow(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, current_period_end)
		VALUES ($1, (SELECT code FROM plans WHERE available AND visible ORDER BY price_cents, code LIMIT 1), 'trialing', $2)
		ON CONFLICT (organization_id) DO UPDATE SET status = 'trialing', current_period_end = $2, past_due_since = NULL, updated_at = now()
			WHERE subscriptions.status NOT IN ('active', 'past_due')
		RETURNING status`, orgID, end).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return end, ErrPaidSubscription
	}
	return end, err
}
