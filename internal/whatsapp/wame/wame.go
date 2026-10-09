// Package wame é o adaptador da Wame (wame.api.br). Serve tanto a instância oficial (Cloud API pela
// Wame) quanto a não oficial (QR). Webhook no formato "meta" (envelope da Cloud API). A key da
// instância vai no caminho; o token Bearer é opcional.
package wame

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type Client struct {
	BaseURL string
	Key     string
	Token   string
	// Official: instância da Cloud API. Visto e "digitando…" vão pela última mensagem recebida do chat.
	Official bool
	HTTP     *http.Client

	last sync.Map // chat → id da última mensagem recebida (oficial: visto e digitando)
}

func New(baseURL, key, token string) *Client {
	if baseURL == "" {
		baseURL = "https://server.wame.api.br"
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Key:     key,
		Token:   token,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) Name() string { return "wame" }

type change struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

type envelope struct {
	Entry []struct {
		Changes []change `json:"changes"`
	} `json:"entry"`
}

type media struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Caption  string `json:"caption"`
}

type message struct {
	From     string `json:"from"`
	ChatType string `json:"chat_type"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	FromMe   bool   `json:"from_me"`
	Text     struct {
		Body string `json:"body"`
	} `json:"text"`
	Image *media `json:"image"`
	Audio *media `json:"audio"`
	Video *media `json:"video"`
}

func changes(body []byte) ([]change, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("wame: webhook inválido: %w", err)
	}
	var out []change
	for _, e := range env.Entry {
		out = append(out, e.Changes...)
	}
	return out, nil
}

// ParseWebhook devolve a primeira mensagem de cliente do envelope. Grupo, eco nosso (from_me),
// status de entrega, presença e conexão ficam de fora.
func (c *Client) ParseWebhook(body []byte) (whatsapp.Inbound, bool, error) {
	chs, err := changes(body)
	if err != nil {
		return whatsapp.Inbound{}, false, err
	}
	for _, ch := range chs {
		if ch.Field != "messages" {
			continue
		}
		var v struct {
			Messages []message `json:"messages"`
		}
		if err := json.Unmarshal(ch.Value, &v); err != nil {
			return whatsapp.Inbound{}, false, fmt.Errorf("wame: mensagens inválidas: %w", err)
		}
		for _, m := range v.Messages {
			if in, ok := toInbound(m, body); ok {
				c.last.Store(in.ChatID, in.ExternalID)
				return in, true, nil
			}
		}
	}
	return whatsapp.Inbound{}, false, nil
}

func toInbound(m message, raw []byte) (whatsapp.Inbound, bool) {
	// A Cloud API não manda chat_type em conversa individual; só grupo vem marcado.
	if m.ID == "" || m.FromMe || m.ChatType == "group" || strings.Contains(m.From, "@") {
		return whatsapp.Inbound{}, false
	}
	chat := whatsapp.Digits(m.From)
	phone, err := whatsapp.NormalizePhone(chat)
	if err != nil {
		return whatsapp.Inbound{}, false
	}
	// Responde no número como o WhatsApp registrou; o telefone canônico casa com o cadastro.
	in := whatsapp.Inbound{ExternalID: m.ID, ChatID: chat, Phone: phone, Raw: raw}
	switch m.Type {
	case "text":
		in.Kind, in.Text = whatsapp.KindText, m.Text.Body
	case "audio":
		in.Kind = whatsapp.KindAudio
	case "image":
		in.Kind = whatsapp.KindImage
		if m.Image != nil {
			in.Text = m.Image.Caption
		}
	case "video":
		in.Kind = whatsapp.KindVideo
		if m.Video != nil {
			in.Text = m.Video.Caption
		}
	default:
		in.Kind = whatsapp.KindOther
	}
	return in, true
}

// ParseConnection lê o aviso de conexão ("open"/"close") do número.
func (c *Client) ParseConnection(body []byte) (whatsapp.ConnectionEvent, bool) {
	chs, err := changes(body)
	if err != nil {
		return whatsapp.ConnectionEvent{}, false
	}
	for _, ch := range chs {
		if ch.Field != "connection" {
			continue
		}
		var v struct {
			Connection struct {
				Status string `json:"status"`
				Code   int    `json:"code"`
				Reason string `json:"reason"`
			} `json:"connection"`
		}
		if json.Unmarshal(ch.Value, &v) != nil || v.Connection.Status == "" {
			return whatsapp.ConnectionEvent{}, false
		}
		return whatsapp.ConnectionEvent{
			Connected: v.Connection.Status == "open",
			Code:      v.Connection.Code,
			Reason:    v.Connection.Reason,
		}, true
	}
	return whatsapp.ConnectionEvent{}, false
}
