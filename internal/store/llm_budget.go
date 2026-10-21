package store

import "context"

// LLMSpent diz quanto a chave (pela impressão) já gastou; 0 se nunca gastou.
func (s *Store) LLMSpent(ctx context.Context, keyID string) (float64, error) {
	var usd float64
	err := s.pool.QueryRow(ctx, `SELECT coalesce((SELECT spent_usd FROM llm_budget WHERE key_id = $1), 0)::float8`, keyID).Scan(&usd)
	return usd, err
}

// AddLLMSpend soma o custo de uma chamada ao gasto da chave (atômico entre api e worker).
func (s *Store) AddLLMSpend(ctx context.Context, keyID string, usd float64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO llm_budget (key_id, spent_usd) VALUES ($1, $2)
		ON CONFLICT (key_id) DO UPDATE SET spent_usd = llm_budget.spent_usd + EXCLUDED.spent_usd, updated_at = now()`, keyID, usd)
	return err
}
