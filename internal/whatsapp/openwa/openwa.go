// Package openwa é o adaptador do open-wa (EASY API). Só para desenvolvimento e testes:
// usa o WhatsApp Web e não é oficial.
package openwa

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Name() string { return "openwa" }

type envelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type message struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	Author string `json:"author"`
	ChatID string `json:"chatId"`
	Sender struct {
		ID string `json:"id"`
	} `json:"sender"`
	Type       string `json:"type"`
	Body       string `json:"body"`
	Caption    string `json:"caption"`
	FromMe     bool   `json:"fromMe"`
	IsGroupMsg bool   `json:"isGroupMsg"`
}

func (c *Client) ParseWebhook(body []byte) (whatsapp.Inbound, bool, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return whatsapp.Inbound{}, false, fmt.Errorf("openwa: webhook inválido: %w", err)
	}
	if env.Event != "onMessage" {
		return whatsapp.Inbound{}, false, nil
	}
	var m message
	if err := json.Unmarshal(env.Data, &m); err != nil {
		return whatsapp.Inbound{}, false, fmt.Errorf("openwa: mensagem inválida: %w", err)
	}
	if m.ID == "" || m.FromMe || m.IsGroupMsg || strings.HasSuffix(m.From, "@g.us") || m.From == "status@broadcast" {
		return whatsapp.Inbound{}, false, nil
	}
	phone, ok := phoneFrom(m.From, m.Sender.ID, m.Author, m.ChatID)
	if !ok {
		// Remetente só com LID (sem telefone): não há como casar com o cadastro.
		return whatsapp.Inbound{}, false, nil
	}
	in := whatsapp.Inbound{ExternalID: m.ID, ChatID: m.From, Phone: phone, Raw: env.Data}
	switch m.Type {
	case "chat":
		in.Kind, in.Text = whatsapp.KindText, m.Body
	case "ptt", "audio":
		in.Kind = whatsapp.KindAudio
	case "image":
		// Em mídia, body traz a miniatura em base64; o texto do cliente é a legenda.
		in.Kind, in.Text = whatsapp.KindImage, m.Caption
	default:
		in.Kind = whatsapp.KindOther
	}
	return in, true, nil
}

func phoneFrom(ids ...string) (string, bool) {
	for _, id := range ids {
		if user, ok := strings.CutSuffix(id, "@c.us"); ok {
			if p, err := whatsapp.NormalizePhone(user); err == nil {
				return p, true
			}
		}
	}
	return "", false
}

type result struct {
	Success  bool            `json:"success"`
	Response json.RawMessage `json:"response"`
}

func (c *Client) SendText(ctx context.Context, chatID, text string) error {
	res, err := c.call(ctx, "/sendText", map[string]any{"to": chatID, "content": text})
	if err != nil {
		return err
	}
	if !res.Success {
		return errors.New("openwa /sendText: envio recusado")
	}
	return nil
}

func (c *Client) DownloadMedia(ctx context.Context, in whatsapp.Inbound) ([]byte, string, error) {
	res, err := c.call(ctx, "/decryptMedia", map[string]any{"message": in.Raw})
	if err != nil {
		return nil, "", err
	}
	var dataURL string
	if !res.Success || json.Unmarshal(res.Response, &dataURL) != nil {
		return nil, "", errors.New("openwa /decryptMedia: mídia indisponível")
	}
	prefix, b64, ok := strings.Cut(dataURL, ";base64,")
	if !ok {
		return nil, "", errors.New("openwa /decryptMedia: data URL inválida")
	}
	mime := strings.TrimSpace(strings.Split(strings.TrimPrefix(prefix, "data:"), ";")[0])
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", fmt.Errorf("openwa /decryptMedia: %w", err)
	}
	return data, mime, nil
}

func (c *Client) ChatIDForPhone(phone string) string { return phone + "@c.us" }

func (c *Client) call(ctx context.Context, path string, args any) (result, error) {
	body, err := json.Marshal(map[string]any{"args": args})
	if err != nil {
		return result{}, fmt.Errorf("openwa %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return result{}, fmt.Errorf("openwa %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_key", c.APIKey)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return result{}, fmt.Errorf("openwa %s: %w", path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return result{}, fmt.Errorf("openwa %s: %w", path, err)
	}
	if res.StatusCode != http.StatusOK {
		return result{}, fmt.Errorf("openwa %s: status %d", path, res.StatusCode)
	}
	var out result
	if err := json.Unmarshal(raw, &out); err != nil {
		return result{}, fmt.Errorf("openwa %s: resposta inválida: %w", path, err)
	}
	return out, nil
}
