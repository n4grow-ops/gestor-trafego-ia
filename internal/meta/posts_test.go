package meta_test

import (
	"context"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

func TestRecentPagePostsUsesPageTokenAndReadsMedia(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /P1", `{"access_token":"PAGETOKEN"}`)
	f.resp["GET /P1/published_posts"] = func(r rec) (int, string) {
		if r.Form.Get("limit") != "6" || !strings.Contains(r.Form.Get("fields"), "subattachments") {
			t.Errorf("consulta = %v", r.Form)
		}
		return 200, `{"data":[
			{"id":"P1_1","message":"Rodízio","permalink_url":"https://www.facebook.com/p/1","created_time":"2026-10-20T14:03:11+0000",
			 "attachments":{"data":[{"media_type":"video","media":{"image":{"src":"https://x.fbcdn.net/t.jpg"},"source":"https://video.xx.fbcdn.net/v.mp4"}}]}},
			{"id":"P1_2","message":"Álbum","permalink_url":"https://www.facebook.com/p/2","created_time":"2026-10-19T10:00:00+0000",
			 "attachments":{"data":[{"media_type":"album","subattachments":{"data":[
				{"media_type":"photo","media":{"image":{"src":"https://x.fbcdn.net/a.jpg"}}},
				{"media_type":"photo","media":{"image":{"src":"https://x.fbcdn.net/b.jpg"}}}]}}]}},
			{"id":"P1_3","message":"Só texto","permalink_url":"https://www.facebook.com/p/3"},
			{"id":"P1_4","permalink_url":"https://www.facebook.com/p/4","full_picture":"https://x.fbcdn.net/f.jpg"}]}`
	}
	list, err := c.RecentPagePosts(context.Background(), tok, "P1", 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("posts = %+v", list)
	}
	if list[0].ID != "P1_1" || len(list[0].Items) != 1 || list[0].Items[0] != (meta.PagePostItem{Kind: "video", URL: "https://video.xx.fbcdn.net/v.mp4"}) ||
		list[0].Created.Day() != 20 {
		t.Fatalf("vídeo = %+v", list[0])
	}
	if len(list[1].Items) != 2 || list[1].Items[1].URL != "https://x.fbcdn.net/b.jpg" {
		t.Fatalf("álbum = %+v", list[1])
	}
	if list[2].Items[0] != (meta.PagePostItem{Kind: "image", URL: "https://x.fbcdn.net/f.jpg"}) {
		t.Fatalf("só full_picture = %+v", list[2])
	}
}

func TestPagePostRefusesOtherPage(t *testing.T) {
	f, c := newFake(t)
	if _, err := c.PagePost(context.Background(), tok, "P1", "P2_9"); err == nil {
		t.Fatal("aceitou post de outra página")
	}
	if len(f.reqs) != 0 {
		t.Fatalf("chamou a Meta: %v", f.reqs)
	}
}

func TestPublishPagePostAsAd(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	in := baseInput()
	in.InstagramActorID = "IG1"
	in.Creatives = []meta.PublishCreative{{Kind: "image", Placement: "feed", FacebookPostID: "P1_555"}}
	in.Copies = append(in.Copies, meta.PublishCopy{Headline: "Outro", Text: "Texto 2", CTA: "WHATSAPP_MESSAGE"})
	res, err := c.Publish(context.Background(), tok, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AdIDs) != 1 || len(f.calls("POST /act_9/adimages")) != 0 {
		t.Fatalf("anúncios = %v", res.AdIDs)
	}
	cr := f.one("POST /act_9/adcreatives").Form
	if cr.Get("object_story_id") != "P1_555" || cr.Get("instagram_user_id") != "IG1" || cr.Get("object_story_spec") != "" ||
		decode(t, cr.Get("call_to_action"))["type"] != "WHATSAPP_MESSAGE" {
		t.Fatalf("criativo = %v", cr)
	}
}

func TestPublishPagePostFallsBackWithoutButton(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.resp["POST /act_9/adcreatives"] = func(r rec) (int, string) {
		if r.Form.Get("call_to_action") != "" {
			return 400, `{"error":{"message":"Invalid parameter","type":"OAuthException","code":100}}`
		}
		return 200, `{"id":"CR1"}`
	}
	in := baseInput()
	in.Creatives = []meta.PublishCreative{{Kind: "image", Placement: "feed", FacebookPostID: "P1_555"}}
	res, err := c.Publish(context.Background(), tok, in)
	if err != nil {
		t.Fatal(err)
	}
	calls := f.calls("POST /act_9/adcreatives")
	if len(res.AdIDs) != 1 || len(calls) != 3 || calls[2].Form.Get("object_story_id") != "P1_555" || calls[2].Form.Get("instagram_user_id") != "" {
		t.Fatalf("tentativas = %d anúncios = %v", len(calls), res.AdIDs)
	}
}
