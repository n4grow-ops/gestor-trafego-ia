package instagram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

type fakePostGraph struct {
	ig    []meta.IGMedia
	igErr error
	fb    []meta.PagePost
	fbErr error
}

func (g *fakePostGraph) FindInstagramMedia(context.Context, string, string, string) (meta.IGMedia, error) {
	return meta.IGMedia{}, meta.ErrIGMediaNotFound
}

func (g *fakePostGraph) RecentInstagramMedia(context.Context, string, string, int) ([]meta.IGMedia, error) {
	return g.ig, g.igErr
}

func (g *fakePostGraph) RecentPagePosts(context.Context, string, string, int) ([]meta.PagePost, error) {
	return g.fb, g.fbErr
}

func (g *fakePostGraph) PagePost(context.Context, string, string, string) (meta.PagePost, error) {
	return meta.PagePost{}, errors.New("não usado")
}

func TestRecentMergesNewestFirst(t *testing.T) {
	day := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	g := &fakePostGraph{
		ig: []meta.IGMedia{{ID: "I1", Permalink: "https://www.instagram.com/p/A/", MediaType: "CAROUSEL_ALBUM", Created: day},
			{ID: "I2", Permalink: "https://www.instagram.com/p/B/", MediaType: "VIDEO", Created: day.Add(-48 * time.Hour)}},
		fb: []meta.PagePost{{ID: "P1_1", Permalink: "https://www.facebook.com/p/1", Created: day.Add(-24 * time.Hour),
			Items: []meta.PagePostItem{{Kind: "image", URL: "https://x.fbcdn.net/a.jpg"}}}},
	}
	f := New(g)
	list, err := f.Recent(context.Background(), "tok", "IG1", "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "I1" || list[0].Kind != "album" || list[1].ID != "P1_1" || list[1].Source != SourceFacebook {
		t.Fatalf("lista = %+v", list)
	}

	// uma conta falhando não derruba a outra; as duas falhando por permissão = ErrPermission
	g.igErr = errors.New("fora")
	g.ig = nil
	if list, err := f.Recent(context.Background(), "tok", "IG1", "P1", 6); err != nil || len(list) != 1 {
		t.Fatalf("lista = %+v erro = %v", list, err)
	}
	g.fb, g.fbErr = nil, &permErr{}
	if _, err := f.Recent(context.Background(), "tok", "IG1", "P1", 6); err == nil {
		t.Fatal("sem erro com as duas contas falhando")
	}
	// sem Instagram vinculado: só a Página
	g.igErr, g.fbErr = nil, nil
	g.fb = []meta.PagePost{{ID: "P1_9", Permalink: "https://www.facebook.com/p/9", Items: []meta.PagePostItem{{Kind: "video", URL: "https://x.fbcdn.net/v.mp4"}}}}
	if list, err := f.Recent(context.Background(), "tok", "", "P1", 6); err != nil || len(list) != 1 || list[0].Kind != "video" {
		t.Fatalf("lista = %+v erro = %v", list, err)
	}
}

type permErr struct{}

func (*permErr) Error() string { return "sem permissão" }
