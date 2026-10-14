package meta

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// AdReview é o resultado da revisão da Meta para um anúncio.
type AdReview struct {
	ID              string
	EffectiveStatus string   // ACTIVE, PAUSED, PENDING_REVIEW, IN_PROCESS, DISAPPROVED, WITH_ISSUES…
	Reasons         []string // motivos da reprovação (ad_review_feedback), quando há
}

// Rejected: a Meta recusou o anúncio.
func (r AdReview) Rejected() bool { return r.EffectiveStatus == "DISAPPROVED" }

// InReview: o anúncio ainda está em análise.
func (r AdReview) InReview() bool {
	return r.EffectiveStatus == "PENDING_REVIEW" || r.EffectiveStatus == "IN_PROCESS"
}

// AdReviewer lê a revisão dos anúncios publicados.
type AdReviewer interface {
	AdsReview(ctx context.Context, token string, adIDs []string) ([]AdReview, error)
}

func (c *Client) AdsReview(ctx context.Context, token string, adIDs []string) ([]AdReview, error) {
	if len(adIDs) == 0 {
		return nil, nil
	}
	var out map[string]struct {
		EffectiveStatus string                       `json:"effective_status"`
		Feedback        map[string]map[string]string `json:"ad_review_feedback"`
	}
	q := url.Values{"ids": {strings.Join(adIDs, ",")}, "fields": {"effective_status,ad_review_feedback"}}
	if err := c.call(ctx, http.MethodGet, "/", token, q, &out); err != nil {
		return nil, err
	}
	res := make([]AdReview, 0, len(adIDs))
	for _, id := range adIDs {
		a, ok := out[id]
		if !ok {
			continue
		}
		r := AdReview{ID: id, EffectiveStatus: a.EffectiveStatus}
		seen := map[string]bool{}
		for _, group := range []string{"global", "placement_specific"} {
			keys := make([]string, 0, len(a.Feedback[group]))
			for k := range a.Feedback[group] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if v := strings.TrimSpace(a.Feedback[group][k]); v != "" && !seen[v] {
					seen[v] = true
					r.Reasons = append(r.Reasons, v)
				}
			}
		}
		res = append(res, r)
	}
	return res, nil
}

var _ AdReviewer = (*Client)(nil)
