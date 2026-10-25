package engine_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/instagram"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fakePosts é o fakeIG com os posts recentes e os posts da Página (engine.PostSource).
type fakePosts struct {
	*fakeIG
	recent      []instagram.Choice
	recentCalls []string // igUser|page
	page        map[string]instagram.Post
}

func (f *fakePosts) Recent(_ context.Context, token, igUserID, pageID string, n int) ([]instagram.Choice, error) {
	f.recentCalls = append(f.recentCalls, token+"|"+igUserID+"|"+pageID)
	return f.recent[:min(n, len(f.recent))], nil
}

func (f *fakePosts) Facebook(ctx context.Context, token, pageID, postID string, max int, sink instagram.Sink) (instagram.Post, error) {
	p, ok := f.page[pageID+"|"+postID]
	if !ok {
		return instagram.Post{}, instagram.ErrUnavailable
	}
	return f.deliver(ctx, p, max, sink)
}

func newPostsHarness(t *testing.T) (*harness, *fakePublisher, *fakePosts) {
	h, _, pub, ig, _ := newIGHarness(t)
	fp := &fakePosts{fakeIG: ig, page: map[string]instagram.Post{}}
	h.eng.Instagram = fp
	h.connectMetaIG()
	day := time.Date(2026, 10, 20, 15, 0, 0, 0, time.UTC)
	fp.recent = []instagram.Choice{
		{Source: instagram.SourceInstagram, ID: "1789", Permalink: "https://www.instagram.com/p/MEU1/", Caption: "Pizza de sexta com borda grátis", Kind: "image", Created: day},
		{Source: instagram.SourceFacebook, ID: "P1_555", Permalink: "https://www.facebook.com/lojapage/posts/pfbid0abc", Caption: "Rodízio no sábado", Kind: "video", Created: day.Add(-24 * time.Hour)},
	}
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Permalink: "https://www.instagram.com/p/MEU1/", Items: []instagram.Item{igSquare}}
	fp.page["P1|P1_555"] = instagram.Post{MediaID: "P1_555", Permalink: "https://www.facebook.com/lojapage/posts/pfbid0abc",
		Caption: "Rodízio no sábado", Items: []instagram.Item{igVertical}}
	return h, pub, fp
}

func TestPostsListedOnlyWhenAsked(t *testing.T) {
	h, _, fp := newPostsHarness(t)
	h.say("SIM " + h.reachApproval())
	if len(fp.recentCalls) != 0 {
		t.Fatal("listou os posts sem o cliente pedir")
	}
	h.say("quero usar um post que já tenho no insta")
	msg := h.lastClient()
	for _, want := range []string{"Achei estes posts seus:", "*1.* 📸 Instagram · 20/10 · foto", "“Pizza de sexta com borda grátis”",
		"https://www.instagram.com/p/MEU1/", "*2.* 📘 Facebook · 19/10 · vídeo", "*POST 2*"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("lista sem %q:\n%s", want, msg)
		}
	}
	if len(fp.recentCalls) != 1 || fp.recentCalls[0] != "token-do-cliente|IG1|P1" {
		t.Fatalf("chamadas = %v", fp.recentCalls)
	}
}

func TestPostsPickInstagramUsesLinkFlow(t *testing.T) {
	h, _, _ := newPostsHarness(t)
	h.say("SIM " + h.reachApproval())
	h.say("POSTS")
	h.say("o 1")
	if msg := h.lastClient(); msg != "Deixa comigo, vou buscar o post 1 🔎" {
		t.Fatalf("resposta = %q", msg)
	}
	h.runKind(engine.JobInstagramFetch)
	list := h.creativeRows()
	if len(list) != 1 || list[0].Origin != store.OriginInstagramOwn || list[0].IGMediaID != "1789" || list[0].RightsPending {
		t.Fatalf("criativos = %+v", list)
	}
	if !strings.Contains(h.lastClient(), "Peguei seu post ✅") {
		t.Fatalf("aviso = %q", h.lastClient())
	}
}

func TestPostsPickFacebookPublishesPagePost(t *testing.T) {
	h, pub, _ := newPostsHarness(t)
	h.say("SIM " + h.reachApproval())
	h.say("dá pra impulsionar um post da minha página?")
	h.say("post 2")
	h.runKind(engine.JobInstagramFetch)
	list := h.creativeRows()
	if len(list) != 1 || list[0].Origin != store.OriginFacebookOwn || list[0].FBPostID != "P1_555" || list[0].IGMediaID != "" ||
		list[0].SourceURL != "https://www.facebook.com/lojapage/posts/pfbid0abc" || list[0].Kind != "video" {
		t.Fatalf("criativos = %+v", list)
	}
	h.say("pronto")
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 1 {
		t.Fatalf("Publish = %d", pub.calls())
	}
	in := pub.inputs[0]
	if len(in.Creatives) != 1 || in.Creatives[0].FacebookPostID != "P1_555" || in.Creatives[0].InstagramMediaID != "" || len(in.Creatives[0].Data) != 0 {
		t.Fatalf("criativos = %+v", in.Creatives)
	}
}

func TestPostsPickInvalidAndStale(t *testing.T) {
	h, _, _ := newPostsHarness(t)
	h.say("SIM " + h.reachApproval())
	h.say("posts")
	h.say("post 7")
	if msg := h.lastClient(); msg != "Não tem post 7 na lista. Responde um número de 1 a 2 (ex.: *POST 1*)." {
		t.Fatalf("resposta = %q", msg)
	}
	if h.pendingJobs(engine.JobInstagramFetch) != 0 {
		t.Fatal("agendou busca com número fora da lista")
	}
	// lista velha: o número sozinho não vale mais, "post 1" ainda vale
	_, err := h.pool.Exec(ctx, `UPDATE campaign_drafts SET post_choices = jsonb_set(post_choices, '{at}', '"2020-01-01T00:00:00Z"')`)
	h.must(err)
	h.say("1")
	if h.pendingJobs(engine.JobInstagramFetch) != 0 {
		t.Fatal("número sozinho valeu com a lista vencida")
	}
	h.say("post 1")
	if h.pendingJobs(engine.JobInstagramFetch) != 1 {
		t.Fatalf("jobs = %d", h.pendingJobs(engine.JobInstagramFetch))
	}
}
