package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

func TestWhatsAppStatus(t *testing.T) {
	st := store.New(testdb.New(t))
	ctx := context.Background()
	if _, err := st.WhatsAppStatus(ctx, "wame"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sem estado: %v", err)
	}
	changed, err := st.SetWhatsAppStatus(ctx, "wame", whatsapp.ConnectionEvent{Connected: false, Code: 1004, Reason: "disconnected"})
	if err != nil || !changed {
		t.Fatalf("primeira queda: changed=%v err=%v", changed, err)
	}
	changed, _ = st.SetWhatsAppStatus(ctx, "wame", whatsapp.ConnectionEvent{Connected: false, Code: 1004})
	if changed {
		t.Fatal("queda repetida não é mudança")
	}
	s, _ := st.WhatsAppStatus(ctx, "wame")
	if s.Reason != "" || s.Code != 1004 {
		t.Fatalf("motivo deve acompanhar o último aviso: %+v", s)
	}
	changed, _ = st.SetWhatsAppStatus(ctx, "wame", whatsapp.ConnectionEvent{Connected: true, Code: 200})
	s, err = st.WhatsAppStatus(ctx, "wame")
	if !changed || err != nil || !s.Connected || s.Code != 200 || s.UpdatedAt.IsZero() {
		t.Fatalf("volta: changed=%v s=%+v err=%v", changed, s, err)
	}
}

func TestWhatsAppStats(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "c")
	must(t, err)
	must(t, st.AddMessage(ctx, conv.ID, "in", "text", "oi"))
	must(t, st.AddMessage(ctx, conv.ID, "out", "text", "olá"))
	must(t, st.AddMessage(ctx, conv.ID, "out", "text", "tudo bem?"))
	_, err = pool.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload, status, last_error) VALUES ('whatsapp.send', $1, 'failed', 'sessão FAILED')`,
		`{"conversation_id":`+itoa(conv.ID)+`,"text":"faz seu cadastro: https://x.io/c/AbCdEfGhIjKl"}`)
	must(t, err)
	_, err = st.SetWhatsAppStatus(ctx, "wame", whatsapp.ConnectionEvent{Connected: true})
	must(t, err)
	w, err := st.WhatsAppStats(ctx)
	must(t, err)
	if len(w.Statuses) != 1 || !w.Statuses[0].Connected {
		t.Fatalf("estado = %+v", w.Statuses)
	}
	if len(w.Days) != 7 || w.Days[6].In != 1 || w.Days[6].Out != 2 {
		t.Fatalf("dias = %+v", w.Days)
	}
	if len(w.Failed) != 1 || w.Failed[0].OrgName != "Pizzaria Boa" || w.Failed[0].Preview != "faz seu cadastro: https://x.io/c/[link]" {
		t.Fatalf("falhas = %+v", w.Failed)
	}
}
