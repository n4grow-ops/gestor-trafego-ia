package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

func TestMediaLinks(t *testing.T) {
	st := store.New(testdb.New(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	a, err := st.SaveMediaLink(ctx, store.MediaLink{Kind: "preview", RefID: 8, Bucket: "previews", Path: "d/8-feed.jpg", Mime: "image/jpeg", URL: "u1", URLUntil: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.SaveMediaLink(ctx, store.MediaLink{Kind: "preview", RefID: 8, Bucket: "previews", Path: "d/8-story.jpg", Mime: "image/jpeg", URL: "u2", URLUntil: now.Add(40 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// mesmo caminho: atualiza em vez de duplicar
	if again, err := st.SaveMediaLink(ctx, store.MediaLink{Kind: "preview", RefID: 8, Bucket: "previews", Path: "d/8-feed.jpg", URL: "u1b", URLUntil: now.Add(2 * time.Hour)}); err != nil || again != a {
		t.Fatalf("upsert: id=%d err=%v", again, err)
	}
	list, err := st.MediaLinksFor(ctx, "preview", 8)
	if err != nil || len(list) != 2 || list[0].URL != "u1b" || list[1].ID != b {
		t.Fatalf("lista = %+v err=%v", list, err)
	}
	exp, err := st.MediaLinksExpiring(ctx, now.Add(7*24*time.Hour), 10)
	if err != nil || len(exp) != 1 || exp[0].ID != a {
		t.Fatalf("vencendo = %+v err=%v", exp, err)
	}
	if err := st.RenewMediaLink(ctx, a, "novo", now.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	l, err := st.GetMediaLink(ctx, a)
	if err != nil || l.URL != "novo" || l.Path != "d/8-feed.jpg" {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := st.GetMediaLink(ctx, 99999); err != store.ErrNotFound {
		t.Fatalf("inexistente: %v", err)
	}
}
