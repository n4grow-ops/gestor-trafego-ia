package worker_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/worker"
)

var ctx = context.Background()

type fakeWA struct {
	mu    sync.Mutex
	parse func([]byte) (whatsapp.Inbound, bool, error)
	sent  []string
}

func (f *fakeWA) Name() string { return "openwa" }
func (f *fakeWA) ParseWebhook(b []byte) (whatsapp.Inbound, bool, error) {
	return f.parse(b)
}
func (f *fakeWA) SendText(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeWA) DownloadMedia(context.Context, whatsapp.Inbound) ([]byte, string, error) {
	return nil, "", nil
}
func (f *fakeWA) ChatIDForPhone(p string) string { return p + "@c.us" }

func setup(t *testing.T, wa *fakeWA) (*worker.Worker, *store.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := &engine.Engine{Store: st, WA: wa, Log: log}
	return &worker.Worker{Store: st, WA: wa, Engine: eng, Log: log}, st, pool
}

func eventStatus(t *testing.T, pool *pgxpool.Pool) (string, int, string) {
	t.Helper()
	var status string
	var attempts int
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM inbox_events`).Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if lastErr == nil {
		return status, attempts, ""
	}
	return status, attempts, *lastErr
}

func TestTickProcessesInboxEvent(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) {
		return whatsapp.Inbound{ExternalID: "e1", ChatID: "5511911112222@c.us", Phone: "5511911112222", Kind: whatsapp.KindText, Text: "oi"}, true, nil
	}}
	w, st, pool := setup(t, wa)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{"event":"onMessage"}`))

	did, err := w.Tick(ctx)
	if err != nil || !did {
		t.Fatalf("did = %v err = %v", did, err)
	}
	if status, _, _ := eventStatus(t, pool); status != "done" {
		t.Errorf("status = %s", status)
	}
	if len(wa.sent) != 1 || !strings.Contains(wa.sent[0], "não está liberado") {
		t.Errorf("enviado = %v", wa.sent)
	}
}

func TestTickIgnoredEventIsDone(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { return whatsapp.Inbound{}, false, nil }}
	w, st, pool := setup(t, wa)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{}`))
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := eventStatus(t, pool); status != "done" || len(wa.sent) != 0 {
		t.Errorf("status = %s enviado = %v", status, wa.sent)
	}
}

func TestTickRecoversPanic(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { panic("quebrou") }}
	w, st, pool := setup(t, wa)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{}`))
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	status, attempts, lastErr := eventStatus(t, pool)
	if status != "pending" || attempts != 1 || !strings.Contains(lastErr, "panic") {
		t.Errorf("status = %s attempts = %d last_error = %q", status, attempts, lastErr)
	}
}

func TestTickRunsJobsAndFlagsFailure(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { return whatsapp.Inbound{}, false, nil }}
	w, st, pool := setup(t, wa)
	st.EnqueueJob(ctx, engine.JobSend, engine.SendJob{ChatID: "x@c.us", Text: "olá"})
	pool.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload) VALUES ('desconhecido', '{}')`)

	for i := 0; i < 2; i++ {
		if _, err := w.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(wa.sent) != 1 || wa.sent[0] != "olá" {
		t.Errorf("enviado = %v", wa.sent)
	}
	var lastErr string
	pool.QueryRow(ctx, `SELECT last_error FROM outbox_jobs WHERE kind = 'desconhecido'`).Scan(&lastErr)
	if !strings.Contains(lastErr, "desconhecido") {
		t.Errorf("last_error = %q", lastErr)
	}
}

func TestTickWithoutWork(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { return whatsapp.Inbound{}, false, nil }}
	w, _, _ := setup(t, wa)
	did, err := w.Tick(ctx)
	if err != nil || did {
		t.Fatalf("did = %v err = %v", did, err)
	}
}
