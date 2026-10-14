package wame

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

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// to aceita o chat no formato antigo do WAHA (…@c.us) e manda só os dígitos para a Wame.
func to(chatID string) string {
	for _, suffix := range []string{"@c.us", "@s.whatsapp.net"} {
		if user, ok := strings.CutSuffix(chatID, suffix); ok {
			user, _, _ = strings.Cut(user, ":")
			return user
		}
	}
	return chatID
}

func dataURI(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func (c *Client) SendText(ctx context.Context, chatID, text string) error {
	return c.send(ctx, http.MethodPost, "/message/text", map[string]any{"to": to(chatID), "text": text})
}

// MarkSeen marca o chat como lido (visto azul).
func (c *Client) MarkSeen(ctx context.Context, chatID string) error {
	if c.Official {
		return c.read(ctx, chatID, false)
	}
	q := url.Values{"id": {to(chatID)}, "action": {"markRead"}, "value": {"true"}}
	return c.send(ctx, http.MethodPatch, "/chat?"+q.Encode(), nil)
}

var presence = map[whatsapp.Presence]string{
	whatsapp.PresenceTyping:    "composing",
	whatsapp.PresenceRecording: "recording",
	whatsapp.PresencePaused:    "paused",
}

func (c *Client) SetPresence(ctx context.Context, chatID string, p whatsapp.Presence) error {
	status, ok := presence[p]
	if !ok {
		return fmt.Errorf("wame: presença desconhecida %q", p)
	}
	if c.Official {
		// A Cloud API só tem "digitando…" (some sozinho em ~25 s ou com a resposta); sem "gravando".
		if p == whatsapp.PresencePaused {
			return nil
		}
		return c.read(ctx, chatID, true)
	}
	return c.send(ctx, http.MethodPost, "/message/presence", map[string]any{"to": to(chatID), "status": status})
}

// read marca como lida a última mensagem recebida do chat e, com typing, mostra "digitando…".
// Sem mensagem conhecida (ex.: worker reiniciado) não há o que marcar.
func (c *Client) read(ctx context.Context, chatID string, typing bool) error {
	id, ok := c.last.Load(to(chatID))
	if !ok {
		return nil
	}
	return c.send(ctx, http.MethodPost, "/message/read", map[string]any{"messageId": id, "typing": typing})
}

// SendVoice manda o áudio em base64; a Wame converte para ogg/opus e entrega como mensagem de voz.
func (c *Client) SendVoice(ctx context.Context, chatID string, audio []byte, mime string) error {
	return c.send(ctx, http.MethodPost, "/message/base64/audio", map[string]any{"to": to(chatID), "base64": dataURI(mime, audio)})
}

// SendImage manda a imagem com legenda: em base64 na não oficial, por link (MediaURL) na oficial.
func (c *Client) SendImage(ctx context.Context, chatID string, image []byte, mime, caption string) error {
	if c.Official {
		if c.MediaURL == nil {
			return errors.New("wame: conta oficial sem link de mídia configurado; imagem não enviada")
		}
		link, err := c.MediaURL(ctx, image, mime)
		if err != nil {
			return fmt.Errorf("wame: publicar imagem: %w", err)
		}
		return c.send(ctx, http.MethodPost, "/message/image", map[string]any{"to": to(chatID), "url": link, "caption": caption})
	}
	return c.send(ctx, http.MethodPost, "/message/base64/image", map[string]any{"to": to(chatID), "base64": dataURI(mime, image), "caption": caption})
}

// DownloadMedia baixa pela rota da própria instância, pelo id da mensagem; a URL do payload é ignorada.
func (c *Client) DownloadMedia(ctx context.Context, in whatsapp.Inbound) ([]byte, string, error) {
	if in.ExternalID == "" {
		return nil, "", errors.New("wame: mensagem sem id")
	}
	res, err := c.do(ctx, http.MethodGet, "/message/"+url.PathEscape(in.ExternalID)+"/media?format=binary", nil)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("wame mídia: status %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, "", fmt.Errorf("wame mídia: %w", err)
	}
	mime := strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
	return data, mime, nil
}

func (c *Client) ChatIDForPhone(phone string) string { return phone }

// SendFlow manda o botão que abre o formulário publicado (navegação estática, sem endpoint de dados).
func (c *Client) SendFlow(ctx context.Context, chatID string, f whatsapp.Flow) error {
	mode := "published"
	if f.Draft {
		mode = "draft"
	}
	return c.send(ctx, http.MethodPost, "/message/flow", map[string]any{
		"to": to(chatID), "flowId": f.ID, "flowToken": f.Token, "flowCta": f.CTA, "body": f.Body,
		"header": f.Header, "footer": f.Footer, "screen": f.Screen, "flowAction": "navigate", "mode": mode,
	})
}

// NeedsTemplates: só a instância oficial (Cloud API) tem a janela de 24 h.
func (c *Client) NeedsTemplates() bool { return c.Official }

// SendTemplate manda um modelo aprovado (pt_BR). A Meta recusa variável vazia, com quebra de
// linha, tab ou mais de 4 espaços seguidos, então os valores são limpos antes.
func (c *Client) SendTemplate(ctx context.Context, chatID string, t whatsapp.Template) error {
	body := map[string]any{"to": to(chatID), "name": t.Name, "language": "pt_BR"}
	if len(t.Params) > 0 {
		params := make([]map[string]string, len(t.Params))
		for i, p := range t.Params {
			params[i] = map[string]string{"type": "text", "text": templateParam(p)}
		}
		body["components"] = []map[string]any{{"type": "body", "parameters": params}}
	}
	return c.send(ctx, http.MethodPost, "/message/template", body)
}

// maxParam fica abaixo do limite da Meta para uma variável; acima disso o modelo é recusado.
const maxParam = 900

func templateParam(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "-"
	}
	if r := []rune(s); len(r) > maxParam {
		s = string(r[:maxParam-1]) + "…"
	}
	return s
}

func (c *Client) send(ctx context.Context, method, path string, payload any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("wame: %w", err)
		}
		body = bytes.NewReader(b)
	}
	res, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("wame %s: status %d: %s", route(path), res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func route(path string) string { return strings.Split(path, "?")[0] }

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/"+url.PathEscape(c.Key)+path, body)
	if err != nil {
		return nil, fmt.Errorf("wame %s: %w", route(path), err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wame %s: %w", route(path), err)
	}
	return res, nil
}
