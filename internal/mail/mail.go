// Package mail manda e-mail de aviso à equipe pela API da Resend (resend.com).
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Resend manda e-mail por https://api.resend.com/emails. From precisa ser de um domínio verificado lá.
type Resend struct {
	Key     string
	From    string
	BaseURL string
	HTTP    *http.Client
}

func NewResend(key, from string) *Resend {
	return &Resend{Key: key, From: from, BaseURL: "https://api.resend.com", HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Send manda o mesmo texto para todos os destinos, num e-mail só.
func (r *Resend) Send(ctx context.Context, to []string, subject, text string) error {
	if len(to) == 0 {
		return nil
	}
	b, err := json.Marshal(map[string]any{"from": r.From, "to": to, "subject": subject, "text": text})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+"/emails", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("resend: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2<<10))
		return fmt.Errorf("resend: status %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
