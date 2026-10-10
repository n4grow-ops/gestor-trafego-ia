package store_test

import (
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestCreatives(t *testing.T) {
	f := seedDraft(t)
	if _, err := f.st.LatestApprovedDraft(ctx, f.conv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sem aprovação ainda: %v", err)
	}
	must(t, f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0))
	d, err := f.st.LatestApprovedDraft(ctx, f.conv.ID)
	must(t, err)
	if d.ID != f.draftID {
		t.Fatalf("rascunho aprovado = %+v", d)
	}
	for i, kind := range []string{"image", "video"} {
		_, err := f.st.AddCreative(ctx, store.Creative{OrgID: f.ident.OrgID, DraftID: d.ID, ProjectID: d.ProjectID,
			Kind: kind, Path: "org/x/" + kind, Mime: kind + "/x", Size: int64(100 + i)})
		must(t, err)
	}
	n, err := f.st.CountCreatives(ctx, d.ID)
	must(t, err)
	if n != 2 {
		t.Fatalf("criativos = %d", n)
	}
	must(t, f.st.UpdateConversation(ctx, f.conv.ID, store.StateAwaitingCreatives, f.conv.Fields))
	c, _ := f.st.GetConversation(ctx, f.conv.ID)
	if c.State != store.StateAwaitingCreatives {
		t.Fatalf("estado = %s", c.State)
	}
}

func TestCreativeOriginAndRights(t *testing.T) {
	f := seedDraft(t)
	must(t, f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0))
	add := func(c store.Creative) int64 {
		t.Helper()
		c.OrgID, c.DraftID, c.Kind, c.Mime = f.ident.OrgID, f.draftID, "image", "image/jpeg"
		id, err := f.st.AddCreative(ctx, c)
		must(t, err)
		return id
	}
	add(store.Creative{Path: "up.jpg"})
	add(store.Creative{Path: "own.jpg", Origin: store.OriginInstagramOwn, IGMediaID: "1789", SourceURL: "https://www.instagram.com/p/A/"})
	add(store.Creative{Path: "pub1.jpg", Origin: store.OriginInstagramPublic, RightsPending: true,
		SourceURL: "https://www.instagram.com/p/B/", SourceCaption: "Legenda do post"})
	add(store.Creative{Path: "pub2.jpg", Origin: store.OriginInstagramPublic, RightsPending: true, SourceURL: "https://www.instagram.com/p/B/"})
	list, err := f.st.ListCreatives(ctx, f.draftID)
	must(t, err)
	if len(list) != 4 || list[0].Origin != store.OriginUpload || list[0].RightsPending ||
		list[1].Origin != store.OriginInstagramOwn || list[1].IGMediaID != "1789" ||
		!list[2].RightsPending || list[2].SourceCaption != "Legenda do post" || list[2].SourceURL != "https://www.instagram.com/p/B/" {
		t.Fatalf("lista = %+v", list)
	}
	if _, err := f.st.AddCreative(ctx, store.Creative{OrgID: f.ident.OrgID, DraftID: f.draftID, Kind: "image", Path: "x", Origin: "outro"}); err == nil {
		t.Fatal("origem inválida aceita")
	}

	// SÓ REFERÊNCIA: apaga os pendentes e devolve o que precisa para o Storage e as referências
	gone, err := f.st.DeletePendingCreatives(ctx, f.draftID)
	must(t, err)
	if len(gone) != 2 || gone[0].Path != "pub1.jpg" || gone[0].SourceCaption != "Legenda do post" {
		t.Fatalf("apagados = %+v", gone)
	}
	if n, _ := f.st.CountCreatives(ctx, f.draftID); n != 2 {
		t.Fatalf("restaram %d", n)
	}

	// É MEU: confirma os pendentes
	add(store.Creative{Path: "pub3.jpg", Origin: store.OriginInstagramPublic, RightsPending: true})
	n, err := f.st.ConfirmCreativeRights(ctx, f.draftID)
	must(t, err)
	if n != 1 {
		t.Fatalf("confirmados = %d", n)
	}
	list, err = f.st.ListCreatives(ctx, f.draftID)
	must(t, err)
	for _, c := range list {
		if c.RightsPending {
			t.Fatalf("ainda pendente: %+v", c)
		}
	}
}
