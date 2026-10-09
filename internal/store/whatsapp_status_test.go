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
