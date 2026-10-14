package meta_test

import (
	"context"
	"testing"
	"time"
)

func TestCampaignInsights(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /C1/insights", `{"data":[{"spend":"12.34","impressions":"1500","reach":"900",
		"actions":[{"action_type":"link_click","value":"30"},{"action_type":"onsite_conversion.messaging_conversation_started_7d","value":"7"}]}]}`)
	got, err := c.CampaignInsights(context.Background(), tok, "C1", time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.SpendCents != 1234 || got.Impressions != 1500 || got.Reach != 900 || got.Conversations != 7 {
		t.Fatalf("%+v", got)
	}
	if tr := f.one("GET /C1/insights").Form.Get("time_range"); tr != `{"since":"2026-10-13","until":"2026-10-13"}` {
		t.Errorf("time_range = %s", tr)
	}
}
