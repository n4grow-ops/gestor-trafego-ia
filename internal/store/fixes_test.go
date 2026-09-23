package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestJobMaxAttemptsPerJob(t *testing.T) {
	st, _ := newStore(t)
	must(t, st.Enqueue(ctx, store.JobSpec{Kind: "k", Payload: nil, MaxAttempts: 1}))
	job, ok, err := st.ClaimJob(ctx)
	must(t, err)
	if !ok {
		t.Fatal("sem job")
	}
	failed, err := st.FinishJob(ctx, job.ID, errors.New("fora"))
	must(t, err)
	if !failed {
		t.Fatal("com MaxAttempts 1, a primeira falha é definitiva")
	}
}

func TestSetStateAndEnqueueMany(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	c, err := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, err)
	must(t, st.SetStateAndEnqueue(ctx, c.ID, store.StateNeedsHuman, c.Fields,
		store.JobSpec{Kind: "a"}, store.JobSpec{Kind: "b", MaxAttempts: 8}))
	var n, max int
	must(t, pool.QueryRow(ctx, `SELECT count(*), max(max_attempts) FROM outbox_jobs`).Scan(&n, &max))
	if n != 2 || max != 8 {
		t.Errorf("jobs = %d max_attempts = %d", n, max)
	}
}

func TestSaveProposalRequiresAwaitingProposal(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	c, err := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, err)
	_, err = st.SaveProposal(ctx, store.ProposalRecord{
		OrgID: ident.OrgID, ConversationID: c.ID, ProposalHash: "h", CodeHash: "c",
		ExpiresAt: time.Now().Add(time.Hour), Send: store.JobSpec{Kind: "whatsapp.send"},
		Proposal: contract.CampaignProposal{Objective: "x"},
	})
	if !errors.Is(err, store.ErrStateChanged) {
		t.Fatalf("err = %v", err)
	}
	var drafts int
	must(t, pool.QueryRow(ctx, `SELECT count(*) FROM campaign_drafts`).Scan(&drafts))
	if drafts != 0 {
		t.Errorf("rascunho gravado mesmo com estado errado")
	}
}

func TestLockConversationSerializes(t *testing.T) {
	st, _ := newStore(t)
	unlock, err := st.LockConversation(ctx, 42)
	must(t, err)
	done := make(chan struct{})
	go func() {
		unlock2, err := st.LockConversation(ctx, 42)
		if err == nil {
			unlock2()
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("segunda trava não esperou a primeira")
	case <-time.After(300 * time.Millisecond):
	}
	other, err := st.LockConversation(ctx, 43)
	must(t, err)
	other()
	unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("segunda trava não foi liberada")
	}
}
