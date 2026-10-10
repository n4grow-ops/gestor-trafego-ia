package store_test

import (
	"testing"
	"time"
)

// Eventos e jobs encerrados há mais de 30 dias saem; pendentes e recentes ficam.
func TestPurgeOld(t *testing.T) {
	st, pool := newStore(t)
	for _, q := range []string{
		`INSERT INTO inbox_events (provider, external_id, payload, status, processed_at) VALUES ('wame', 'velho', '{}', 'done', now() - interval '40 days')`,
		`INSERT INTO inbox_events (provider, external_id, payload, status, processed_at) VALUES ('wame', 'novo', '{}', 'done', now() - interval '2 days')`,
		`INSERT INTO inbox_events (provider, external_id, payload) VALUES ('wame', 'pendente', '{}')`,
		`INSERT INTO outbox_jobs (kind, payload, status, updated_at) VALUES ('x', '{}', 'done', now() - interval '40 days')`,
		`INSERT INTO outbox_jobs (kind, payload, status, updated_at) VALUES ('x', '{}', 'failed', now() - interval '40 days')`,
		`INSERT INTO outbox_jobs (kind, payload, status, updated_at) VALUES ('x', '{}', 'done', now())`,
		`INSERT INTO outbox_jobs (kind, payload) VALUES ('x', '{}')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	inbox, jobs, err := st.PurgeOld(ctx, 30*24*time.Hour)
	must(t, err)
	if inbox != 1 || jobs != 2 {
		t.Fatalf("apagados: inbox=%d jobs=%d", inbox, jobs)
	}
}

// A resposta do formulário de cadastro traz CPF e e-mail: depois de processada, o payload é apagado.
func TestRedactInbox(t *testing.T) {
	st, pool := newStore(t)
	var id int64
	must(t, pool.QueryRow(ctx, `INSERT INTO inbox_events (provider, external_id, payload) VALUES ('wame', 'f1', '{"cpf":"529.982.247-25"}') RETURNING id`).Scan(&id))
	must(t, st.RedactInbox(ctx, id))
	var payload string
	must(t, pool.QueryRow(ctx, `SELECT payload::text FROM inbox_events WHERE id = $1`, id).Scan(&payload))
	if payload != `{"redacted": true}` {
		t.Fatalf("payload = %s", payload)
	}
}
