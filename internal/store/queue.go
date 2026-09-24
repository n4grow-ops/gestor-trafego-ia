package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// Tentativas antes de um evento do inbox virar failed; também o padrão dos jobs.
const maxAttempts = 3

// JobSpec descreve um job a enfileirar. MaxAttempts 0 usa o padrão (3).
type JobSpec struct {
	Kind        string
	Payload     any
	MaxAttempts int
	Delay       time.Duration // espera antes de o job poder rodar
}

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
	return enqueue(ctx, s.pool, JobSpec{Kind: kind, Payload: payload})
}

func (s *Store) Enqueue(ctx context.Context, j JobSpec) error {
	return enqueue(ctx, s.pool, j)
}

func enqueue(ctx context.Context, q querier, j JobSpec) error {
	raw, err := json.Marshal(j.Payload)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	max := j.MaxAttempts
	if max <= 0 {
		max = maxAttempts
	}
	_, err = q.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload, max_attempts, run_after)
		VALUES ($1, $2, $3, now() + $4 * interval '1 millisecond')`, j.Kind, string(raw), max, j.Delay.Milliseconds())
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
		SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
		    last_error = $2, locked_until = NULL,
		    run_after = now() + attempts * interval '30 seconds', updated_at = now()
		WHERE id = $1 RETURNING status`, id, procErr.Error()).Scan(&status)
	return status == "failed", err
}

// SetStateAndEnqueue muda o estado da conversa e enfileira os jobs na mesma transação.
func (s *Store) SetStateAndEnqueue(ctx context.Context, convID int64, state string, f contract.Fields, jobs ...JobSpec) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := updateConversation(ctx, tx, convID, state, f); err != nil {
		return err
	}
	for _, j := range jobs {
		if err := enqueue(ctx, tx, j); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// HasPendingJob diz se existe job pendente do tipo com payload[field] = value.
func (s *Store) HasPendingJob(ctx context.Context, kind, field string, value int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbox_jobs
		WHERE kind = $1 AND status = 'pending' AND (payload->>$2)::bigint = $3)`, kind, field, value).Scan(&ok)
	return ok, err
}

// HasPendingKind diz se existe algum job pendente do tipo.
func (s *Store) HasPendingKind(ctx context.Context, kind string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbox_jobs WHERE kind = $1 AND status = 'pending')`, kind).Scan(&ok)
	return ok, err
}

// Classe da trava consultiva por conversa (a outra metade da chave é o id da conversa).
const conversationLockClass int32 = 727002

// LockConversation serializa o processamento de uma conversa entre workers.
// Segura uma conexão até a função devolvida ser chamada.
func (s *Store) LockConversation(ctx context.Context, convID int64) (func(), error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1, $2)`, conversationLockClass, int32(convID)); err != nil {
		conn.Release()
		return nil, err
	}
	return func() {
		conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1, $2)`, conversationLockClass, int32(convID))
		conn.Release()
	}, nil
}
