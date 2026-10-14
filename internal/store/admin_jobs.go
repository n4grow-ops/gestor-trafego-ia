package store

import (
	"context"
	"time"
)

// FailedJob é uma tarefa que esgotou as tentativas, como o painel mostra (sem o texto das mensagens).
type FailedJob struct {
	ID             int64     `json:"id"`
	Kind           string    `json:"kind"`
	OrgID          *int64    `json:"org_id"`
	OrgName        string    `json:"org_name"`
	ConversationID *int64    `json:"conversation_id"`
	Attempts       int       `json:"attempts"`
	Error          string    `json:"error"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// FailedJobs lista as tarefas que falharam desde since, da mais recente para a mais antiga.
func (s *Store) FailedJobs(ctx context.Context, since time.Time) ([]FailedJob, error) {
	rows, err := s.pool.Query(ctx, `SELECT j.id, j.kind, o.id, coalesce(nullif(o.trade_name, ''), o.name, ''),
			c.id, j.attempts, coalesce(j.last_error, ''), j.created_at, j.updated_at
		FROM outbox_jobs j
		LEFT JOIN conversations c ON c.id = CASE WHEN j.payload->>'conversation_id' ~ '^[0-9]+$' THEN (j.payload->>'conversation_id')::bigint END
		LEFT JOIN whatsapp_identities w ON w.id = c.identity_id
		LEFT JOIN members m ON m.id = w.member_id
		LEFT JOIN organizations o ON o.id = coalesce(m.organization_id,
			CASE WHEN j.payload->>'org_id' ~ '^[0-9]+$' THEN (j.payload->>'org_id')::bigint END)
		WHERE j.status = 'failed' AND j.updated_at >= $1
		ORDER BY j.updated_at DESC LIMIT 200`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FailedJob{}
	for rows.Next() {
		var f FailedJob
		if err := rows.Scan(&f.ID, &f.Kind, &f.OrgID, &f.OrgName, &f.ConversationID, &f.Attempts, &f.Error, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.Error = truncateRunes(RedactSecrets(f.Error), 300)
		out = append(out, f)
	}
	return out, rows.Err()
}

// RetryJob devolve a tarefa que falhou para a fila, com as tentativas zeradas. ErrNotFound se ela não
// está mais em falha.
func (s *Store) RetryJob(ctx context.Context, id int64) (string, error) {
	var kind string
	err := s.pool.QueryRow(ctx, `UPDATE outbox_jobs SET status = 'pending', attempts = 0, run_after = now(),
		locked_until = NULL, last_error = NULL, updated_at = now()
		WHERE id = $1 AND status = 'failed' RETURNING kind`, id).Scan(&kind)
	return kind, notFound(err)
}

// DiscardJob encerra a tarefa que falhou sem rodar de novo; o erro fica com a marca de quem descartou.
func (s *Store) DiscardJob(ctx context.Context, id int64, by string) (string, error) {
	var kind string
	err := s.pool.QueryRow(ctx, `UPDATE outbox_jobs SET status = 'done', locked_until = NULL, updated_at = now(),
		last_error = 'descartado pela equipe (' || $2 || '): ' || coalesce(last_error, '')
		WHERE id = $1 AND status = 'failed' RETURNING kind`, id, by).Scan(&kind)
	return kind, notFound(err)
}
