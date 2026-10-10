package store_test

import (
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestMiaVersions(t *testing.T) {
	st, _ := newStore(t)
	if _, _, err := st.MiaCurrent(ctx); !errors.Is(err, mia.ErrNoConfig) {
		t.Fatalf("vazio: %v", err)
	}
	seeded, err := st.EnsureMiaSeed(ctx, mia.Defaults())
	if err != nil || !seeded {
		t.Fatalf("semente: %v %v", seeded, err)
	}
	if again, _ := st.EnsureMiaSeed(ctx, mia.Defaults()); again {
		t.Fatal("semente só uma vez")
	}
	c, v, err := st.MiaCurrent(ctx)
	if err != nil || v != 1 || c.Texts[mia.TextFirstMessage] != mia.Defaults().Texts[mia.TextFirstMessage] {
		t.Fatalf("v1 = %d %v", v, err)
	}

	c.Texts[mia.TextFirstMessage] = "Oi! Sou a Mia 2.0."
	v2, err := st.SaveMia(ctx, c, 1, "dono@meuanuncio.io", "nova saudação")
	if err != nil || v2 != 2 {
		t.Fatalf("salvar: %d %v", v2, err)
	}
	// Edição aberta na versão 1 não sobrescreve a 2.
	if _, err := st.SaveMia(ctx, c, 1, "marina@meuanuncio.io", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflito: %v", err)
	}
	bad := mia.Defaults()
	bad.Texts[mia.TextReminder1] = "sem link"
	if _, err := st.SaveMia(ctx, bad, 2, "x", ""); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("inválida: %v", err)
	}
	// Desfazer = gravar de novo a versão 1 como versão 3.
	old, err := st.MiaVersionConfig(ctx, 1)
	must(t, err)
	if v3, err := st.SaveMia(ctx, old, 2, "dono@meuanuncio.io", "desfeito: volta à versão 1"); err != nil || v3 != 3 {
		t.Fatalf("desfazer: %d %v", v3, err)
	}
	c, v, _ = st.MiaCurrent(ctx)
	if v != 3 || c.Texts[mia.TextFirstMessage] != mia.Defaults().Texts[mia.TextFirstMessage] {
		t.Fatalf("depois de desfazer: v%d %q", v, c.Texts[mia.TextFirstMessage])
	}
	hist, err := st.MiaVersions(ctx, 10)
	if err != nil || len(hist) != 3 || hist[0].Version != 3 || hist[1].CreatedBy != "dono@meuanuncio.io" || hist[1].Note != "nova saudação" {
		t.Fatalf("histórico = %+v %v", hist, err)
	}
	if _, err := st.MiaVersionConfig(ctx, 99); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("versão inexistente: %v", err)
	}
}

func TestConversationSnapshots(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, _ := st.GetOrCreateConversation(ctx, ident.ID, "c")
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateCollecting, contract.Fields{Offer: contract.Str("pizza")}))
	snaps, err := st.ConversationSnapshots(ctx)
	if err != nil || len(snaps) != 1 || snaps[0].State != "collecting" || *snaps[0].Fields.Offer != "pizza" {
		t.Fatalf("%+v %v", snaps, err)
	}
}
