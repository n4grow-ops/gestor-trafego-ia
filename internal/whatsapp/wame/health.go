package wame

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// ignoredHealth são avisos que não afetam o envio de mensagens (chamadas por SIP, que o bot não usa).
var ignoredHealth = map[int]bool{138024: true, 138025: true}

// Health lê /instance e devolve o healthStatus da Meta. Só a instância oficial tem essa leitura.
func (c *Client) Health(ctx context.Context) (whatsapp.Health, bool, error) {
	if !c.Official {
		return whatsapp.Health{}, false, nil
	}
	res, err := c.do(ctx, http.MethodGet, "/instance", nil)
	if err != nil {
		return whatsapp.Health{}, true, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return whatsapp.Health{}, true, fmt.Errorf("wame /instance: status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseHealth(body)
}

func parseHealth(body []byte) (whatsapp.Health, bool, error) {
	var v struct {
		Instance struct {
			Meta struct {
				Phone struct {
					HealthStatus *struct {
						CanSendMessage string `json:"canSendMessage"`
						Entities       []struct {
							EntityType     string `json:"entityType"`
							CanSendMessage string `json:"canSendMessage"`
							Errors         []struct {
								Code             int    `json:"code"`
								Description      string `json:"description"`
								PossibleSolution string `json:"possibleSolution"`
							} `json:"errors"`
						} `json:"entities"`
					} `json:"healthStatus"`
				} `json:"phone"`
			} `json:"meta"`
		} `json:"instance"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return whatsapp.Health{}, true, fmt.Errorf("wame /instance: %w", err)
	}
	hs := v.Instance.Meta.Phone.HealthStatus
	if hs == nil {
		return whatsapp.Health{}, false, nil
	}
	h := whatsapp.Health{Blocked: hs.CanSendMessage == "BLOCKED"}
	for _, e := range hs.Entities {
		for _, er := range e.Errors {
			if ignoredHealth[er.Code] {
				continue
			}
			h.Problems = append(h.Problems, whatsapp.HealthProblem{Entity: e.EntityType, Code: er.Code,
				Description: er.Description, Solution: er.PossibleSolution})
		}
	}
	return h, true, nil
}
