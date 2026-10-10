package meta_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

func TestFindInstagramMediaPagesUntilShortcode(t *testing.T) {
	f, c := newFake(t)
	pages := 0
	f.resp["GET /IG1/media"] = func(r rec) (int, string) {
		pages++
		if !strings.Contains(r.Form.Get("fields"), "children{media_type,media_url,thumbnail_url}") ||
			!strings.Contains(r.Form.Get("fields"), "shortcode") {
			t.Errorf("campos = %q", r.Form.Get("fields"))
		}
		if r.Form.Get("after") == "" {
			return 200, `{"data":[{"id":"M1","shortcode":"OUTRO","media_type":"IMAGE","media_url":"https://x.cdninstagram.com/1.jpg"}],
				"paging":{"cursors":{"after":"CUR2"},"next":"https://graph.facebook.com/next"}}`
		}
		if r.Form.Get("after") != "CUR2" {
			t.Errorf("cursor = %q", r.Form.Get("after"))
		}
		return 200, `{"data":[{"id":"M2","permalink":"https://www.instagram.com/p/ALVO1/","media_type":"CAROUSEL_ALBUM","caption":"Pizza de sexta",
			"children":{"data":[{"media_type":"IMAGE","media_url":"https://x.cdninstagram.com/a.jpg"},
				{"media_type":"VIDEO","media_url":"https://x.fbcdn.net/b.mp4","thumbnail_url":"https://x.fbcdn.net/b.jpg"}]}}],
			"paging":{"cursors":{"after":"CUR3"}}}`
	}
	m, err := c.FindInstagramMedia(context.Background(), tok, "IG1", "ALVO1")
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 || m.ID != "M2" || m.MediaType != "CAROUSEL_ALBUM" || m.Caption != "Pizza de sexta" || len(m.Children) != 2 ||
		m.Children[1].MediaType != "VIDEO" || m.Children[1].MediaURL != "https://x.fbcdn.net/b.mp4" {
		t.Fatalf("páginas = %d post = %+v", pages, m)
	}
}

func TestFindInstagramMediaStopsAfterLimit(t *testing.T) {
	f, c := newFake(t)
	pages := 0
	f.resp["GET /IG1/media"] = func(r rec) (int, string) {
		pages++
		return 200, fmt.Sprintf(`{"data":[{"id":"M%d","shortcode":"X%d"}],"paging":{"cursors":{"after":"C%d"},"next":"n"}}`, pages, pages, pages)
	}
	_, err := c.FindInstagramMedia(context.Background(), tok, "IG1", "NUNCA")
	if !errors.Is(err, meta.ErrIGMediaNotFound) {
		t.Fatalf("erro = %v", err)
	}
	if pages != 4 { // 4 páginas de 50 = 200 posts
		t.Fatalf("páginas lidas = %d", pages)
	}
}

func TestFindInstagramMediaPermissionError(t *testing.T) {
	f, c := newFake(t)
	f.onCode("GET /IG1/media", 400, `{"error":{"message":"(#10) Application does not have permission for this action","code":10}}`)
	_, err := c.FindInstagramMedia(context.Background(), tok, "IG1", "A")
	if !meta.IsPermissionError(err) {
		t.Fatalf("erro de permissão não reconhecido: %v", err)
	}
	f.onCode("GET /IG1/media", 403, `{"error":{"message":"Missing permission instagram_basic","code":200}}`)
	if _, err := c.FindInstagramMedia(context.Background(), tok, "IG1", "A"); !meta.IsPermissionError(err) {
		t.Fatalf("código 200 não reconhecido: %v", err)
	}
	f.onCode("GET /IG1/media", 500, `{"error":{"message":"boom","code":2}}`)
	if _, err := c.FindInstagramMedia(context.Background(), tok, "IG1", "A"); err == nil || meta.IsPermissionError(err) {
		t.Fatalf("erro comum tratado como permissão: %v", err)
	}
	if _, err := c.FindInstagramMedia(context.Background(), tok, "", "A"); err == nil {
		t.Fatal("sem conta do Instagram devia falhar")
	}
}

func TestPublishInstagramPostAsAd(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	in := baseInput()
	in.InstagramActorID = "IG1"
	in.Creatives = []meta.PublishCreative{{Kind: "image", Placement: "feed", InstagramMediaID: "1789"}}
	in.Copies = append(in.Copies, meta.PublishCopy{Headline: "Outro", Text: "Texto 2", CTA: "WHATSAPP_MESSAGE"})
	res, err := c.Publish(context.Background(), tok, in)
	if err != nil {
		t.Fatal(err)
	}
	// o post já tem legenda: um anúncio por post, sem subir arquivo nem combinar com os textos
	if len(res.AdIDs) != 1 || len(f.calls("POST /act_9/adimages")) != 0 || len(f.calls("POST /act_9/advideos")) != 0 {
		t.Fatalf("anúncios = %v uploads = %d", res.AdIDs, len(f.calls("POST /act_9/adimages")))
	}
	cr := f.one("POST /act_9/adcreatives").Form
	if cr.Get("source_instagram_media_id") != "1789" || cr.Get("instagram_user_id") != "IG1" || cr.Get("object_id") != "P1" ||
		cr.Get("object_story_spec") != "" {
		t.Fatalf("criativo = %v", cr)
	}
	cta := decode(t, cr.Get("call_to_action"))
	if cta["type"] != "WHATSAPP_MESSAGE" {
		t.Fatalf("botão = %v", cta)
	}
	if !strings.Contains(strings.Join(res.Warnings, " "), "post do Instagram") {
		t.Fatalf("avisos = %v", res.Warnings)
	}

	// sem a conta do Instagram na conexão não dá para usar o post
	in.InstagramActorID = ""
	if _, err := c.Publish(context.Background(), tok, in); err == nil {
		t.Fatal("post do Instagram sem instagram_user_id aceito")
	}
}
