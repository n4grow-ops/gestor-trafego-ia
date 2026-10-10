package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestCleanupJobPurgesAndReschedules(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.eng.EnsureCleanup(ctx))
	h.must(h.eng.EnsureCleanup(ctx))
	if n := h.pendingJobs(engine.JobCleanup); n != 1 {
		t.Fatalf("limpezas agendadas = %d", n)
	}
	_, err := h.pool.Exec(ctx, `INSERT INTO inbox_events (provider, external_id, payload, status, processed_at) VALUES ('wame', 'velho', '{}', 'done', now() - interval '40 days')`)
	h.must(err)
	b, _ := json.Marshal(map[string]string{})
	h.must(h.eng.HandleJob(ctx, store.Job{Kind: engine.JobCleanup, Payload: b}))
	if n := h.count(`SELECT count(*) FROM inbox_events`); n != 0 {
		t.Fatalf("evento velho ficou: %d", n)
	}
	if n := h.pendingJobs(engine.JobCleanup); n != 2 {
		t.Fatalf("próxima limpeza não agendada: %d", n)
	}
}
