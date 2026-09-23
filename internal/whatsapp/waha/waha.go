// Package waha é o adaptador do WAHA (WhatsApp HTTP API), motor GOWS. Só para desenvolvimento e
// testes: usa o protocolo do WhatsApp Web e não é oficial.
package waha

import (
	"bytes"
	"context"
	"encoding/base64"
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
	// Dados do motor GOWS: com remetente em LID, o telefone vem em SenderAlt.
	Data struct {
		Info struct {
			SenderAlt string `json:"SenderAlt"`
		} `json:"Info"`
	} `json:"_data"`
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
	phone, ok := phoneFrom(m.From, m.Participant, m.Author, m.Data.Info.SenderAlt)
	if !ok {
		// Remetente só com LID (sem telefone): não há como casar com o cadastro.
		return whatsapp.Inbound{}, false, nil
	}
	chatID := m.From
	if strings.HasSuffix(chatID, "@lid") {
		// Responde no chat do telefone, com o número como o WhatsApp registrou.
		if user, ok := jidUser(m.Data.Info.SenderAlt); ok {
			chatID = user + "@c.us"
		}
	}
	in := whatsapp.Inbound{ExternalID: m.ID, ChatID: chatID, Phone: phone, Raw: ev.Payload}
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
		user, ok := jidUser(id)
		if !ok {
			continue
		}
		if p, err := whatsapp.NormalizePhone(user); err == nil {
			return p, true
		}
	}
	return "", false
}

// jidUser extrai os dígitos de um JID de telefone (@c.us ou @s.whatsapp.net), sem o sufixo de dispositivo.
func jidUser(id string) (string, bool) {
	user, ok := strings.CutSuffix(id, "@c.us")
	if !ok {
		user, ok = strings.CutSuffix(id, "@s.whatsapp.net")
	}
	if !ok {
		return "", false
	}
	user, _, _ = strings.Cut(user, ":")
	return user, user != ""
}

func (c *Client) SendText(ctx context.Context, chatID, text string) error {
	return c.post(ctx, "/api/sendText", map[string]any{"session": c.Session, "chatId": chatID, "text": text})
}

func (c *Client) MarkSeen(ctx context.Context, chatID string) error {
	return c.post(ctx, "/api/sendSeen", map[string]any{"session": c.Session, "chatId": chatID})
}

func (c *Client) SetPresence(ctx context.Context, chatID string, p whatsapp.Presence) error {
	return c.post(ctx, "/api/"+url.PathEscape(c.Session)+"/presence", map[string]any{"chatId": chatID, "presence": string(p)})
}

// SendVoice manda o áudio em base64 e pede ao WAHA a conversão para OGG/Opus (mensagem de voz).
func (c *Client) SendVoice(ctx context.Context, chatID string, audio []byte, mime string) error {
	return c.post(ctx, "/api/sendVoice", map[string]any{
		"session": c.Session, "chatId": chatID, "convert": true,
		"file": map[string]string{"mimetype": mime, "data": base64.StdEncoding.EncodeToString(audio)},
	})
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("waha: %w", err)
	}
	res, err := c.do(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("waha %s: status %d", path, res.StatusCode)
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
