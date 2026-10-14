package meta_test

import (
	"context"
	"testing"
	"time"
)

var (
	since = time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	until = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
)

func TestCampaignInsightsRangeSumsAndSendsPeriod(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /C1/insights", `{"data":[
		{"spend":"10.50","impressions":"1000","reach":"800","actions":[{"action_type":"onsite_conversion.messaging_conversation_started_7d","value":"2"}]},
		{"spend":"4.50","impressions":"500","reach":"400","actions":[{"action_type":"link_click","value":"9"},{"action_type":"onsite_conversion.messaging_conversation_started_7d","value":"1"}]}]}`)
	got, err := c.CampaignInsightsRange(context.Background(), tok, "C1", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendCents != 1500 || got.Impressions != 1500 || got.Reach != 1200 || got.Conversations != 3 {
		t.Fatalf("%+v", got)
	}
	if tr := f.one("GET /C1/insights").Form.Get("time_range"); tr != `{"since":"2026-09-14","until":"2026-10-13"}` {
		t.Errorf("time_range = %s", tr)
	}
}

func TestAccountInsightsWithoutRowsIsZero(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9/insights", `{"data":[]}`)
	got, err := c.AccountInsights(context.Background(), tok, "act_9", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendCents != 0 || got.Impressions != 0 || got.Conversations != 0 || got.Frequency != 0 {
		t.Fatalf("conta sem entrega deveria vir zerada: %+v", got)
	}
}

func TestAccountInsightsReadsTotals(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9/insights", `{"data":[{"spend":"812.40","impressions":"90000","reach":"30000","frequency":"3.0",
		"actions":[{"action_type":"onsite_conversion.messaging_conversation_started_7d","value":"41"}]}]}`)
	got, err := c.AccountInsights(context.Background(), tok, "9", since, until) // sem o prefixo act_
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendCents != 81240 || got.Reach != 30000 || got.Conversations != 41 || got.Frequency != 3.0 {
		t.Fatalf("%+v", got)
	}
	if q := f.one("GET /act_9/insights").Form; q.Get("level") != "account" {
		t.Errorf("level = %s", q.Get("level"))
	}
}

func TestAdInsightsFollowsPages(t *testing.T) {
	f, c := newFake(t)
	f.resp["GET /act_9/insights"] = func(r rec) (int, string) {
		if r.Form.Get("after") == "CUR1" {
			return 200, `{"data":[{"ad_id":"A3","ad_name":"Vídeo 2","campaign_name":"Camp","spend":"5.00","impressions":"100","frequency":"1.2"}]}`
		}
		return 200, `{"data":[
			{"ad_id":"A1","ad_name":"Foto 1","campaign_name":"Camp","spend":"20.00","impressions":"2000","frequency":"4.5","actions":[{"action_type":"onsite_conversion.messaging_conversation_started_7d","value":"4"}]},
			{"ad_id":"A2","ad_name":"Foto 2","campaign_name":"Camp","spend":"30.00","impressions":"3000"}],
			"paging":{"cursors":{"after":"CUR1"},"next":"https://graph.facebook.com/proxima"}}`
	}
	got, err := c.AdInsights(context.Background(), tok, "act_9", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].AdID != "A1" || got[0].Conversations != 4 || got[0].Frequency != 4.5 || got[1].Conversations != 0 || got[2].AdName != "Vídeo 2" {
		t.Fatalf("%+v", got)
	}
	if n := len(f.calls("GET /act_9/insights")); n != 2 {
		t.Errorf("chamadas = %d", n)
	}
}

func TestActiveAdsAsksOnlyActive(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9/ads", `{"data":[{"id":"A1","name":"Foto 1","campaign":{"name":"Camp"}},{"id":"A2","name":"Foto 2"}]}`)
	got, err := c.ActiveAds(context.Background(), tok, "act_9")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].CampaignName != "Camp" || got[1].Name != "Foto 2" {
		t.Fatalf("%+v", got)
	}
	if es := f.one("GET /act_9/ads").Form.Get("effective_status"); es != `["ACTIVE"]` {
		t.Errorf("effective_status = %s", es)
	}
}

func TestInsightsRangeGraphErrorIsReturned(t *testing.T) {
	f, c := newFake(t)
	f.onCode("GET /act_9/insights", 400, `{"error":{"message":"sem permissão","code":200}}`)
	if _, err := c.AccountInsights(context.Background(), tok, "act_9", since, until); err == nil {
		t.Fatal("erro da Meta deveria subir")
	}
	if _, err := c.AdInsights(context.Background(), tok, "act_9", since, until); err == nil {
		t.Fatal("erro da Meta deveria subir")
	}
}
