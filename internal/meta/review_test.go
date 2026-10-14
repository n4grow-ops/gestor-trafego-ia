package meta_test

import (
	"context"
	"testing"
)

func TestAdsReview(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /", `{"A1":{"effective_status":"DISAPPROVED","ad_review_feedback":{"global":{"ADULT_CONTENT":"Seu anúncio não segue as políticas de conteúdo adulto."}}},
		"A2":{"effective_status":"PENDING_REVIEW"}}`)
	got, err := c.AdsReview(context.Background(), tok, []string{"A1", "A2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Rejected() || got[0].Reasons[0] != "Seu anúncio não segue as políticas de conteúdo adulto." || !got[1].InReview() {
		t.Fatalf("%+v", got)
	}
	if fields := f.one("GET /").Form.Get("ids"); fields != "A1,A2" {
		t.Errorf("ids = %q", fields)
	}
}
