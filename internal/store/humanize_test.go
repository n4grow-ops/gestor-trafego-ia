package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func newConversation(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	return st, conv.ID
}

func TestPendingInboundAndMarkReplied(t *testing.T) {
	st, convID := newConversation(t)
	a, err := st.AddInbound(ctx, convID, "text", "oi")
	must(t, err)
	must(t, st.AddMessage(ctx, convID, "out", "text", "olá!"))
	b, err := st.AddInbound(ctx, convID, "audio", "quero anunciar")
	must(t, err)
	c, err := st.AddInbound(ctx, convID, "text", "pizza")
	must(t, err)

	if latest, err := st.LatestInboundID(ctx, convID); err != nil || latest != c {
		t.Fatalf("LatestInboundID = %d, %v; want %d", latest, err, c)
	}
	got, err := st.PendingInbound(ctx, convID, b, 10)
	must(t, err)
	if len(got) != 2 || got[0].ID != a || got[1].ID != b || got[1].Kind != "audio" {
		t.Fatalf("PendingInbound até b = %+v", got)
	}
	must(t, st.MarkReplied(ctx, convID, b))
	must(t, st.MarkReplied(ctx, convID, a)) // não volta para trás
	got, err = st.PendingInbound(ctx, convID, c, 10)
	must(t, err)
	if len(got) != 1 || got[0].ID != c || got[0].Body != "pizza" {
		t.Fatalf("depois de MarkReplied = %+v", got)
	}
	must(t, st.MarkReplied(ctx, convID, 0))
	if got, _ := st.PendingInbound(ctx, convID, c, 1); len(got) != 1 || got[0].ID != c {
		t.Fatalf("limite 1 deve trazer a mais nova: %+v", got)
	}
}

func TestJobDelay(t *testing.T) {
	st, _ := newConversation(t)
	must(t, st.Enqueue(ctx, store.JobSpec{Kind: "x", Payload: map[string]int{}, Delay: time.Hour}))
	if _, ok, err := st.ClaimJob(ctx); err != nil || ok {
		t.Fatalf("job com atraso não pode ser pego agora: ok=%v err=%v", ok, err)
	}
	must(t, st.Enqueue(ctx, store.JobSpec{Kind: "y", Payload: map[string]int{}}))
	if j, ok, err := st.ClaimJob(ctx); err != nil || !ok || j.Kind != "y" {
		t.Fatalf("job sem atraso: %+v ok=%v err=%v", j, ok, err)
	}
}
