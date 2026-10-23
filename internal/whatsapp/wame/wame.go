// Package wame é o adaptador da Wame (wame.api.br). Serve tanto a instância oficial (Cloud API pela
// Wame) quanto a não oficial (QR). Webhook no formato "meta" (envelope da Cloud API). A key da
// instância vai no caminho; o token Bearer é opcional.
package wame

import (
	"context"
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
	// MediaURL publica a imagem num link temporário. A conta oficial não aceita base64 e só manda imagem
	// por link; sem ele, SendImage falha na conta oficial.
	MediaURL func(ctx context.Context, data []byte, mime string) (string, error)
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
	Image       *media `json:"image"`
	Interactive *struct {
		Type     string `json:"type"`
		NfmReply struct {
			ResponseJSON string `json:"response_json"`
		} `json:"nfm_reply"`
	} `json:"interactive"`
	Audio    *media `json:"audio"`
	Video    *media `json:"video"`
	Document *media `json:"document"`
	Location *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Name      string  `json:"name"`
		Address   string  `json:"address"`
	} `json:"location"`
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

// ignoredTypes não são falas do cliente: reação, aviso do sistema, mensagem apagada ou que a API não lê.
var ignoredTypes = map[string]bool{"reaction": true, "system": true, "unsupported": true, "ephemeral": true, "revoked": true}

func toInbound(m message, raw []byte) (whatsapp.Inbound, bool) {
	// A Cloud API não manda chat_type em conversa individual; só grupo vem marcado.
	if m.ID == "" || m.FromMe || m.ChatType == "group" || strings.Contains(m.From, "@") || ignoredTypes[m.Type] {
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
	case "interactive":
		in.Kind = whatsapp.KindOther
		if m.Interactive != nil && m.Interactive.Type == "nfm_reply" {
			in.Kind, in.Text = whatsapp.KindFlow, m.Interactive.NfmReply.ResponseJSON
		}
	case "video":
		in.Kind = whatsapp.KindVideo
		if m.Video != nil {
			in.Text = m.Video.Caption
		}
	case "document":
		// Foto ou vídeo mandado como arquivo: vale como imagem ou vídeo (o download é o mesmo).
		in.Kind = whatsapp.KindOther
		if d := m.Document; d != nil {
			switch {
			case strings.HasPrefix(d.MimeType, "image/"):
				in.Kind, in.Text = whatsapp.KindImage, d.Caption
			case strings.HasPrefix(d.MimeType, "video/"):
				in.Kind, in.Text = whatsapp.KindVideo, d.Caption
			}
		}
	case "location":
		// Pino mandado pelo clipe do WhatsApp: vira texto para a Mia usar como local da campanha.
		in.Kind = whatsapp.KindOther
		if l := m.Location; l != nil && (l.Latitude != 0 || l.Longitude != 0) {
			in.Kind = whatsapp.KindText
			in.Text = fmt.Sprintf("[localização enviada pelo WhatsApp: pino %.6f,%.6f", l.Latitude, l.Longitude)
			if where := strings.TrimSpace(strings.Join([]string{l.Name, l.Address}, " ")); where != "" {
				in.Text += " (" + where + ")"
			}
			in.Text += "]"
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

// Split separa um envelope com várias mensagens num envelope por mensagem, para cada uma ganhar
// sua linha no inbox (o worker relê só a primeira de cada envelope). O resto passa intacto.
func (c *Client) Split(body []byte) [][]byte {
	var env map[string]json.RawMessage
	var entries []struct {
		ID      string `json:"id"`
		Changes []struct {
			Field string                     `json:"field"`
			Value map[string]json.RawMessage `json:"value"`
		} `json:"changes"`
	}
	if json.Unmarshal(body, &env) != nil || json.Unmarshal(env["entry"], &entries) != nil ||
		len(entries) != 1 || len(entries[0].Changes) != 1 || entries[0].Changes[0].Field != "messages" {
		return [][]byte{body}
	}
	ch := entries[0].Changes[0]
	var msgs []json.RawMessage
	if json.Unmarshal(ch.Value["messages"], &msgs) != nil || len(msgs) < 2 {
		return [][]byte{body}
	}
	parts := make([][]byte, 0, len(msgs))
	for _, m := range msgs {
		value := make(map[string]json.RawMessage, len(ch.Value))
		for k, v := range ch.Value {
			value[k] = v
		}
		value["messages"] = json.RawMessage("[" + string(m) + "]")
		entry := []map[string]any{{"id": entries[0].ID, "changes": []map[string]any{{"field": "messages", "value": value}}}}
		out := make(map[string]any, len(env))
		for k, v := range env {
			out[k] = v
		}
		out["entry"] = entry
		b, err := json.Marshal(out)
		if err != nil {
			return [][]byte{body}
		}
		parts = append(parts, b)
	}
	return parts
}
