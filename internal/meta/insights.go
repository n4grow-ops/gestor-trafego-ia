package meta

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Insights é o resultado de uma campanha num dia (valores em centavos da moeda da conta).
type Insights struct {
	SpendCents    int64
	Impressions   int64
	Reach         int64
	Conversations int64 // conversas iniciadas por mensagem
}

// conversationAction é a ação da Meta para "conversa por mensagem iniciada".
const conversationAction = "onsite_conversion.messaging_conversation_started_7d"

// InsightsReader lê o resultado diário das campanhas.
type InsightsReader interface {
	CampaignInsights(ctx context.Context, token, campaignID string, day time.Time) (Insights, error)
}

func (c *Client) CampaignInsights(ctx context.Context, token, campaignID string, day time.Time) (Insights, error) {
	d := day.Format("2006-01-02")
	tr, _ := json.Marshal(map[string]string{"since": d, "until": d})
	q := url.Values{"time_range": {string(tr)}, "fields": {"spend,impressions,reach,actions"}, "level": {"campaign"}}
	var out struct {
		Data []struct {
			Spend       string `json:"spend"`
			Impressions string `json:"impressions"`
			Reach       string `json:"reach"`
			Actions     []struct {
				Type  string `json:"action_type"`
				Value string `json:"value"`
			} `json:"actions"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(campaignID)+"/insights", token, q, &out); err != nil {
		return Insights{}, err
	}
	var in Insights
	for _, r := range out.Data {
		if v, err := strconv.ParseFloat(r.Spend, 64); err == nil {
			in.SpendCents += int64(math.Round(v * 100))
		}
		n, _ := strconv.ParseInt(r.Impressions, 10, 64)
		in.Impressions += n
		n, _ = strconv.ParseInt(r.Reach, 10, 64)
		in.Reach += n
		for _, a := range r.Actions {
			if a.Type == conversationAction {
				if v, err := strconv.ParseFloat(a.Value, 64); err == nil {
					in.Conversations += int64(v)
				}
			}
		}
	}
	return in, nil
}

var _ InsightsReader = (*Client)(nil)
