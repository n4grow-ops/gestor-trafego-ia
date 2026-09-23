package store_test

import (
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestConversationLifecycle(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")

	c1, err := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, err)
	if c1.State != store.StateIdle || c1.ChatID != "a@c.us" || c1.Fields.HasData() {
		t.Errorf("nova conversa = %+v", c1)
	}
	c2, err := st.GetOrCreateConversation(ctx, ident.ID, "b@c.us")
	must(t, err)
	if c2.ID != c1.ID || c2.ChatID != "b@c.us" {
		t.Errorf("segunda = %+v", c2)
	}
	c3, err := st.GetOrCreateConversation(ctx, ident.ID, "")
	must(t, err)
	if c3.ChatID != "b@c.us" {
		t.Errorf("chat_id vazio sobrescreveu: %+v", c3)
	}

	f := contract.Fields{Offer: contract.Str("Pizza"), DailyBudgetBRL: contract.Num(50), Media: []string{"m1"}}
	must(t, st.UpdateConversation(ctx, c1.ID, store.StateCollecting, f))
	got, err := st.GetConversation(ctx, c1.ID)
	must(t, err)
	if got.State != store.StateCollecting || *got.Fields.Offer != "Pizza" || *got.Fields.DailyBudgetBRL != 50 || got.Fields.Media[0] != "m1" {
		t.Errorf("depois do update = %+v", got)
	}
	if err := st.UpdateConversation(ctx, 999, store.StateIdle, contract.Fields{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update inexistente: %v", err)
	}
	if _, err := st.GetConversation(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get inexistente: %v", err)
	}
}

func TestRecentMessagesOldestFirst(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	c, err := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, err)
	must(t, st.AddMessage(ctx, c.ID, "in", "text", "m1"))
	must(t, st.AddMessage(ctx, c.ID, "out", "text", "m2"))
	must(t, st.AddMessage(ctx, c.ID, "in", "audio", "m3"))

	msgs, err := st.RecentMessages(ctx, c.ID, 2)
	must(t, err)
	want := []store.Message{{Direction: "out", Kind: "text", Body: "m2"}, {Direction: "in", Kind: "audio", Body: "m3"}}
	if len(msgs) != 2 || msgs[0] != want[0] || msgs[1] != want[1] {
		t.Errorf("msgs = %+v", msgs)
	}
}
