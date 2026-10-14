package meta

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Leituras de insights por período: servem às proteções escolhidas pelo cliente (limite por conversa e
// teto de gasto) e ao diagnóstico grátis da conta. Só leitura: nada aqui altera campanha, conjunto ou anúncio.

// RangeInsightsReader lê o resultado de uma campanha somado num período (datas inclusivas).
type RangeInsightsReader interface {
	CampaignInsightsRange(ctx context.Context, token, campaignID string, since, until time.Time) (Insights, error)
}

// AdPerformance é o resultado de um anúncio (ou da conta inteira) no período, em centavos da moeda da conta.
type AdPerformance struct {
	AdID          string
	AdName        string
	CampaignName  string
	SpendCents    int64
	Impressions   int64
	Reach         int64
	Conversations int64   // conversas iniciadas por mensagem
	Frequency     float64 // vezes que cada pessoa viu, em média (0 = a Meta não informou)
}

// ActiveAd é um anúncio com status efetivo ACTIVE.
type ActiveAd struct {
	ID           string
	Name         string
	CampaignName string
}

// AccountReader lê o desempenho da conta de anúncios para o diagnóstico.
type AccountReader interface {
	// AccountInsights é o total da conta no período (zero quando a Meta não devolve linha: sem entrega).
	AccountInsights(ctx context.Context, token, accountID string, since, until time.Time) (AdPerformance, error)
	// AdInsights é o resultado por anúncio, só dos que tiveram entrega no período.
	AdInsights(ctx context.Context, token, accountID string, since, until time.Time) ([]AdPerformance, error)
	// ActiveAds lista os anúncios ligados agora (têm ou não entrega).
	ActiveAds(ctx context.Context, token, accountID string) ([]ActiveAd, error)
}

var (
	_ RangeInsightsReader = (*Client)(nil)
	_ AccountReader       = (*Client)(nil)
)

const (
	insightsPageSize = "200"
	insightsMaxPages = 5 // até 1000 anúncios
)

type rawAction struct {
	Type  string `json:"action_type"`
	Value string `json:"value"`
}

type rawInsight struct {
	AdID         string      `json:"ad_id"`
	AdName       string      `json:"ad_name"`
	CampaignName string      `json:"campaign_name"`
	Spend        string      `json:"spend"`
	Impressions  string      `json:"impressions"`
	Reach        string      `json:"reach"`
	Frequency    string      `json:"frequency"`
	Actions      []rawAction `json:"actions"`
}

func (r rawInsight) perf() AdPerformance {
	p := AdPerformance{AdID: r.AdID, AdName: r.AdName, CampaignName: r.CampaignName}
	if v, err := strconv.ParseFloat(r.Spend, 64); err == nil {
		p.SpendCents = int64(math.Round(v * 100))
	}
	p.Impressions, _ = strconv.ParseInt(r.Impressions, 10, 64)
	p.Reach, _ = strconv.ParseInt(r.Reach, 10, 64)
	if v, err := strconv.ParseFloat(r.Frequency, 64); err == nil && v > 0 {
		p.Frequency = v
	}
	for _, a := range r.Actions {
		if a.Type == conversationAction {
			if v, err := strconv.ParseFloat(a.Value, 64); err == nil {
				p.Conversations += int64(v)
			}
		}
	}
	return p
}

func timeRange(since, until time.Time) string {
	tr, _ := json.Marshal(map[string]string{"since": since.Format("2006-01-02"), "until": until.Format("2006-01-02")})
	return string(tr)
}

// CampaignInsightsRange soma o resultado da campanha de since até until (inclusive).
func (c *Client) CampaignInsightsRange(ctx context.Context, token, campaignID string, since, until time.Time) (Insights, error) {
	q := url.Values{"time_range": {timeRange(since, until)}, "fields": {"spend,impressions,reach,actions"}, "level": {"campaign"}}
	var out struct {
		Data []rawInsight `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(campaignID)+"/insights", token, q, &out); err != nil {
		return Insights{}, err
	}
	var in Insights
	for _, r := range out.Data {
		p := r.perf()
		in.SpendCents += p.SpendCents
		in.Impressions += p.Impressions
		in.Reach += p.Reach
		in.Conversations += p.Conversations
	}
	return in, nil
}

func accountPath(accountID string) string {
	accountID = strings.TrimSpace(accountID)
	if !strings.HasPrefix(accountID, "act_") {
		accountID = "act_" + accountID
	}
	return "/" + url.PathEscape(accountID)
}

func (c *Client) AccountInsights(ctx context.Context, token, accountID string, since, until time.Time) (AdPerformance, error) {
	q := url.Values{"time_range": {timeRange(since, until)}, "level": {"account"},
		"fields": {"spend,impressions,reach,frequency,actions"}}
	var out struct {
		Data []rawInsight `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, accountPath(accountID)+"/insights", token, q, &out); err != nil {
		return AdPerformance{}, err
	}
	var total AdPerformance
	for _, r := range out.Data {
		p := r.perf()
		total.SpendCents += p.SpendCents
		total.Impressions += p.Impressions
		total.Reach += p.Reach
		total.Conversations += p.Conversations
		if p.Frequency > total.Frequency {
			total.Frequency = p.Frequency
		}
	}
	return total, nil
}

// pageAfter percorre as páginas da Graph API pelo cursor "after", até maxPages.
func (c *Client) pageAfter(ctx context.Context, token, path string, q url.Values, each func(raw json.RawMessage) error) error {
	var after string
	for range insightsMaxPages {
		if after != "" {
			q.Set("after", after)
		}
		var page struct {
			Data   []json.RawMessage `json:"data"`
			Paging struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := c.call(ctx, http.MethodGet, path, token, q, &page); err != nil {
			return err
		}
		for _, raw := range page.Data {
			if err := each(raw); err != nil {
				return err
			}
		}
		if page.Paging.Next == "" || page.Paging.Cursors.After == "" {
			return nil
		}
		after = page.Paging.Cursors.After
	}
	return nil
}

func (c *Client) AdInsights(ctx context.Context, token, accountID string, since, until time.Time) ([]AdPerformance, error) {
	q := url.Values{"time_range": {timeRange(since, until)}, "level": {"ad"}, "limit": {insightsPageSize},
		"fields": {"ad_id,ad_name,campaign_name,spend,impressions,reach,frequency,actions"}}
	var out []AdPerformance
	err := c.pageAfter(ctx, token, accountPath(accountID)+"/insights", q, func(raw json.RawMessage) error {
		var r rawInsight
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		out = append(out, r.perf())
		return nil
	})
	return out, err
}

func (c *Client) ActiveAds(ctx context.Context, token, accountID string) ([]ActiveAd, error) {
	q := url.Values{"limit": {insightsPageSize}, "effective_status": {`["ACTIVE"]`}, "fields": {"id,name,campaign{name}"}}
	var out []ActiveAd
	err := c.pageAfter(ctx, token, accountPath(accountID)+"/ads", q, func(raw json.RawMessage) error {
		var r struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Campaign struct {
				Name string `json:"name"`
			} `json:"campaign"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		out = append(out, ActiveAd{ID: r.ID, Name: r.Name, CampaignName: r.Campaign.Name})
		return nil
	})
	return out, err
}
