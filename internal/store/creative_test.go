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
