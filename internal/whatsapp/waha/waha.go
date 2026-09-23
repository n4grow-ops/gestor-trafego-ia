// Package waha é o adaptador do WAHA (WhatsApp HTTP API), motor GOWS. Só para desenvolvimento e
// testes: usa o protocolo do WhatsApp Web e não é oficial.
package waha

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type Client struct {
	BaseURL string
	APIKey  string
	Session string
	HTTP    *http.Client
}

func New(baseURL, apiKey, session string) *Client {
	if session == "" {
		session = "default"
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Session: session,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Name() string { return "waha" }

type webhookEvent struct {
	Event   string          `json:"event"`
	Session string          `json:"session"`
	Payload json.RawMessage `json:"payload"`
}

type message struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	Participant string `json:"participant"`
	Author      string `json:"author"`
	FromMe      bool   `json:"fromMe"`
	Body        string `json:"body"`
	HasMedia    bool   `json:"hasMedia"`
	Media       *struct {
		URL      string `json:"url"`
		Mimetype string `json:"mimetype"`
	} `json:"media"`
}

func (c *Client) ParseWebhook(body []byte) (whatsapp.Inbound, bool, error) {
	var ev webhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return whatsapp.Inbound{}, false, fmt.Errorf("waha: webhook inválido: %w", err)
	}
	if ev.Event != "message" {
		return whatsapp.Inbound{}, false, nil
	}
	var m message
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		return whatsapp.Inbound{}, false, fmt.Errorf("waha: mensagem inválida: %w", err)
	}
	if m.ID == "" || m.FromMe || !directChat(m.From) {
		return whatsapp.Inbound{}, false, nil
	}
	phone, ok := phoneFrom(m.From, m.Participant, m.Author)
	if !ok {
		// Remetente só com LID (sem telefone): não há como casar com o cadastro.
		return whatsapp.Inbound{}, false, nil
	}
	in := whatsapp.Inbound{ExternalID: m.ID, ChatID: m.From, Phone: phone, Raw: ev.Payload}
	mime := ""
	if m.Media != nil {
		mime = m.Media.Mimetype
	}
	switch {
	case !m.HasMedia:
		in.Kind, in.Text = whatsapp.KindText, m.Body
	case strings.HasPrefix(mime, "audio/"):
		in.Kind = whatsapp.KindAudio
	case strings.HasPrefix(mime, "image/"):
		// Em mídia, body é a legenda.
		in.Kind, in.Text = whatsapp.KindImage, m.Body
	default:
		in.Kind = whatsapp.KindOther
	}
	return in, true, nil
}

// directChat descarta grupos, status e canais.
func directChat(jid string) bool {
	for _, suffix := range []string{"@g.us", "@broadcast", "@newsletter"} {
		if strings.HasSuffix(jid, suffix) {
			return false
		}
	}
	return jid != ""
}

func phoneFrom(ids ...string) (string, bool) {
	for _, id := range ids {
		user, ok := strings.CutSuffix(id, "@c.us")
		if !ok {
			user, ok = strings.CutSuffix(id, "@s.whatsapp.net")
		}
		if !ok {
			continue
		}
		user, _, _ = strings.Cut(user, ":") // remove sufixo de dispositivo
		if p, err := whatsapp.NormalizePhone(user); err == nil {
			return p, true
		}
	}
	return "", false
}

func (c *Client) SendText(ctx context.Context, chatID, text string) error {
	body, err := json.Marshal(map[string]string{"session": c.Session, "chatId": chatID, "text": text})
	if err != nil {
		return fmt.Errorf("waha: %w", err)
	}
	res, err := c.do(ctx, http.MethodPost, "/api/sendText", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("waha /api/sendText: status %d", res.StatusCode)
	}
	return nil
}

// DownloadMedia baixa pela URL do evento, usando só o caminho sobre a BaseURL interna.
func (c *Client) DownloadMedia(ctx context.Context, in whatsapp.Inbound) ([]byte, string, error) {
	var m message
	if err := json.Unmarshal(in.Raw, &m); err != nil {
		return nil, "", fmt.Errorf("waha: %w", err)
	}
	if m.Media == nil || m.Media.URL == "" {
		return nil, "", errors.New("waha: mensagem sem URL de mídia")
	}
	u, err := url.Parse(m.Media.URL)
	if err != nil {
		return nil, "", fmt.Errorf("waha: URL de mídia inválida: %w", err)
	}
	res, err := c.do(ctx, http.MethodGet, u.RequestURI(), nil)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("waha mídia: status %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, "", fmt.Errorf("waha mídia: %w", err)
	}
	mime := strings.TrimSpace(strings.Split(m.Media.Mimetype, ";")[0])
	return data, mime, nil
}

func (c *Client) ChatIDForPhone(phone string) string { return phone + "@c.us" }

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("waha %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("waha %s: %w", path, err)
	}
	return res, nil
}
