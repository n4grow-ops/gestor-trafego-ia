package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// Tentativas antes de um evento ou job virar failed.
const maxAttempts = 3

// Event é um webhook recebido, guardado cru para o worker processar.
type Event struct {
	ID       int64
	Provider string
	Payload  []byte
	Attempts int
}

// Job é trabalho assíncrono do outbox.
type Job struct {
	ID       int64
	Kind     string
	Payload  []byte
	Attempts int
}

// InsertInbox grava o webhook; devolve false se o evento já existia.
func (s *Store) InsertInbox(ctx context.Context, provider, externalID string, payload []byte) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO inbox_events (provider, external_id, payload) VALUES ($1, $2, $3)
		ON CONFLICT (provider, external_id) DO NOTHING`, provider, externalID, string(payload))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimInbox pega o evento pendente mais antigo e o trava por 5 minutos.
func (s *Store) ClaimInbox(ctx context.Context) (Event, bool, error) {
	var e Event
	err := s.pool.QueryRow(ctx, `UPDATE inbox_events
		SET attempts = attempts + 1, locked_until = now() + interval '5 minutes'
		WHERE id = (
			SELECT id FROM inbox_events
			WHERE status = 'pending' AND (locked_until IS NULL OR locked_until < now())
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, provider, payload, attempts`).Scan(&e.ID, &e.Provider, &e.Payload, &e.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	return e, true, nil
}

// FinishInbox fecha o evento; em falha, ele volta em 30 s ou vira failed na última tentativa.
func (s *Store) FinishInbox(ctx context.Context, id int64, procErr error) error {
	if procErr == nil {
		_, err := s.pool.Exec(ctx, `UPDATE inbox_events SET status = 'done', processed_at = now(), locked_until = NULL WHERE id = $1`, id)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE inbox_events
		SET status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
		    last_error = $3, locked_until = now() + interval '30 seconds'
		WHERE id = $1`, id, maxAttempts, procErr.Error())
	return err
}

func (s *Store) EnqueueJob(ctx context.Context, kind string, payload any) error {
	return enqueue(ctx, s.pool, kind, payload)
}

func enqueue(ctx context.Context, q querier, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_, err = q.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload) VALUES ($1, $2)`, kind, string(raw))
	return err
}

// ClaimJob pega o job pronto mais antigo e o trava por 5 minutos.
func (s *Store) ClaimJob(ctx context.Context) (Job, bool, error) {
	var j Job
	err := s.pool.QueryRow(ctx, `UPDATE outbox_jobs
		SET attempts = attempts + 1, locked_until = now() + interval '5 minutes', updated_at = now()
		WHERE id = (
			SELECT id FROM outbox_jobs
			WHERE status = 'pending' AND run_after <= now() AND (locked_until IS NULL OR locked_until < now())
			ORDER BY run_after, id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, kind, payload, attempts`).Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

// FinishJob fecha o job; em falha, reagenda com espera crescente. failed=true quando esgotou.
func (s *Store) FinishJob(ctx context.Context, id int64, procErr error) (bool, error) {
	if procErr == nil {
		_, err := s.pool.Exec(ctx, `UPDATE outbox_jobs SET status = 'done', locked_until = NULL, updated_at = now() WHERE id = $1`, id)
		return false, err
	}
	var status string
	err := s.pool.QueryRow(ctx, `UPDATE outbox_jobs
		SET status = CASE WHEN attempts >= $2 THEN 'failed' ELSE 'pending' END,
		    last_error = $3, locked_until = NULL,
		    run_after = now() + attempts * interval '30 seconds', updated_at = now()
		WHERE id = $1 RETURNING status`, id, maxAttempts, procErr.Error()).Scan(&status)
	return status == "failed", err
}

// SetStateAndEnqueue muda o estado da conversa e enfileira o job na mesma transação.
func (s *Store) SetStateAndEnqueue(ctx context.Context, convID int64, state string, f contract.Fields, kind string, payload any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := updateConversation(ctx, tx, convID, state, f); err != nil {
		return err
	}
	if err := enqueue(ctx, tx, kind, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
