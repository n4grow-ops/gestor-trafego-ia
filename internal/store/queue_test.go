package store_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestInboxDeduplicates(t *testing.T) {
	st, _ := newStore(t)
	ok, err := st.InsertInbox(ctx, "openwa", "e1", []byte(`{"a":1}`))
	must(t, err)
	dup, err := st.InsertInbox(ctx, "openwa", "e1", []byte(`{"a":2}`))
	must(t, err)
	if !ok || dup {
		t.Errorf("ok = %v dup = %v", ok, dup)
	}
}

func TestInboxClaimOrderAndLock(t *testing.T) {
	st, _ := newStore(t)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{"n":1}`))
	st.InsertInbox(ctx, "openwa", "e2", []byte(`{"n":2}`))

	ev, ok, err := st.ClaimInbox(ctx)
	must(t, err)
	if !ok || ev.Provider != "openwa" || ev.Attempts != 1 || string(ev.Payload) != `{"n": 1}` {
		t.Fatalf("1º claim = %+v %s", ev, ev.Payload)
	}
	ev2, ok, err := st.ClaimInbox(ctx)
	must(t, err)
	if !ok || ev2.ID == ev.ID {
		t.Fatalf("2º claim pegou o mesmo evento: %+v", ev2)
	}
	if _, ok, _ := st.ClaimInbox(ctx); ok {
		t.Fatal("os dois estão travados; não deveria haver claim")
	}
	must(t, st.FinishInbox(ctx, ev.ID, nil))
	must(t, st.FinishInbox(ctx, ev2.ID, nil))
}

func TestInboxFailureRetriesThenFails(t *testing.T) {
	st, pool := newStore(t)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{}`))
	unlock := func() { pool.Exec(ctx, `UPDATE inbox_events SET locked_until = now() - interval '1 second'`) }

	for attempt := 1; attempt <= 3; attempt++ {
		ev, ok, err := st.ClaimInbox(ctx)
		must(t, err)
		if !ok || ev.Attempts != attempt {
			t.Fatalf("tentativa %d: ok=%v ev=%+v", attempt, ok, ev)
		}
		must(t, st.FinishInbox(ctx, ev.ID, errors.New("falhou")))
		if _, ok, _ := st.ClaimInbox(ctx); ok {
			t.Fatalf("tentativa %d: evento voltou antes do fim da trava", attempt)
		}
		unlock()
	}
	var status, lastErr string
	must(t, pool.QueryRow(ctx, `SELECT status, last_error FROM inbox_events`).Scan(&status, &lastErr))
	if status != "failed" || lastErr != "falhou" {
		t.Errorf("status = %s last_error = %s", status, lastErr)
	}
	if _, ok, _ := st.ClaimInbox(ctx); ok {
		t.Error("evento failed não pode voltar")
	}
}

func TestJobsRunAfterAndFailure(t *testing.T) {
	st, pool := newStore(t)
	must(t, st.EnqueueJob(ctx, "k", map[string]int{"x": 1}))
	job, ok, err := st.ClaimJob(ctx)
	must(t, err)
	if !ok || job.Kind != "k" || job.Attempts != 1 {
		t.Fatalf("job = %+v", job)
	}
	var payload map[string]int
	must(t, json.Unmarshal(job.Payload, &payload))
	if payload["x"] != 1 {
		t.Errorf("payload = %s", job.Payload)
	}

	failed, err := st.FinishJob(ctx, job.ID, errors.New("fora"))
	must(t, err)
	if failed {
		t.Fatal("1ª falha não é definitiva")
	}
	if _, ok, _ := st.ClaimJob(ctx); ok {
		t.Fatal("job com run_after futuro não deveria ser pego")
	}
	for i := 2; i <= 3; i++ {
		pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() - interval '1 second'`)
		job, ok, err = st.ClaimJob(ctx)
		must(t, err)
		if !ok || job.Attempts != i {
			t.Fatalf("tentativa %d: %+v", i, job)
		}
		failed, err = st.FinishJob(ctx, job.ID, errors.New("fora"))
		must(t, err)
	}
	if !failed {
		t.Fatal("3ª falha deveria ser definitiva")
	}
}

func TestJobDone(t *testing.T) {
	st, _ := newStore(t)
	must(t, st.EnqueueJob(ctx, "k", nil))
	job, _, _ := st.ClaimJob(ctx)
	failed, err := st.FinishJob(ctx, job.ID, nil)
	must(t, err)
	if failed {
		t.Error("sucesso não é falha")
	}
	if _, ok, _ := st.ClaimJob(ctx); ok {
		t.Error("job concluído voltou")
	}
}

func TestSetStateAndEnqueue(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	c, err := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, err)
	f := contract.Fields{Offer: contract.Str("Pizza")}
	must(t, st.SetStateAndEnqueue(ctx, c.ID, store.StateAwaitingProposal, f, store.JobSpec{Kind: "manager.propose", Payload: map[string]int64{"conversation_id": c.ID}}))

	got, err := st.GetConversation(ctx, c.ID)
	must(t, err)
	if got.State != store.StateAwaitingProposal || *got.Fields.Offer != "Pizza" {
		t.Errorf("conversa = %+v", got)
	}
	job, ok, err := st.ClaimJob(ctx)
	must(t, err)
	if !ok || job.Kind != "manager.propose" {
		t.Errorf("job = %+v", job)
	}
	if err := st.SetStateAndEnqueue(ctx, 999, store.StateIdle, f, store.JobSpec{Kind: "k"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("conversa inexistente: %v", err)
	}
	if _, ok, _ := st.ClaimJob(ctx); ok {
		t.Error("job da transação que falhou não deveria existir")
	}
}
