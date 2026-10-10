# WhatsApp pela Wame — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** trocar o WAHA próprio pela Wame (modo não oficial) atrás da interface `whatsapp.Provider`, com volta rápida por variável de ambiente.

**Architecture:**
- Novo adaptador `internal/whatsapp/wame` implementa `whatsapp.Provider` e uma interface nova e opcional, `whatsapp.ConnectionParser`.
- Um construtor único, `internal/whatsapp/build`, escolhe o provedor por `WHATSAPP_PROVIDER` e também monta os parsers de todos os provedores. O worker usa esses parsers para processar eventos antigos do inbox gravados pelo outro provedor.
- As identidades continuam no namespace `"waha"` (`Engine.IdentityProvider`), então os clientes atuais não viram clientes novos.

**Tech Stack:** Go 1.x, `net/http`, pgx, PostgreSQL (Supabase), Easypanel.

**Spec:** `docs/superpowers/specs/2026-10-09-plano-identidade-wame-testador-admin-design.md` (Bloco 1)

## Global Constraints

- **Base URL:** `https://server.wame.api.br`. Toda rota é `/{key}/...`, com a key da instância no caminho.
- **Autenticação:** `Authorization: Bearer <token>` só quando `WAME_TOKEN` estiver definido.
- **Webhook da Wame:**
  - `webhookFormat` = `meta`;
  - URL = `https://gestor-trafego-ia-api.x7qbj3.easypanel.host/webhooks/whatsapp?token=<WEBHOOK_SECRET>`, a mesma rota e o mesmo segredo do WAHA.
- **`WHATSAPP_PROVIDER`:** aceita `waha` (padrão) ou `wame`. Qualquer outro valor faz o processo falhar na partida.
- **Namespace das identidades:** continua `"waha"`. Nada de migration renomeando `whatsapp_identities.provider`.
- **Corte de produção:** o WAHA só é desligado 7 dias depois do corte. A volta rápida é `WHATSAPP_PROVIDER=waha` + redeploy.
- **Banco:** toda tabela nova tem `ENABLE ROW LEVEL SECURITY` (`TestEveryTableHasRowLevelSecurity`).
- **Idioma:** mensagens de log e erro em português, no estilo do código existente.

## Review Focus

1. **Mensagem de cliente antigo chegando pela Wame com o telefone sem o nono dígito.** Deve casar com o cadastro, via `NormalizePhone`. Teste na Task 1.
2. **Job de envio agendado antes do corte com `chat_id` `5511...@c.us`.** A Wame precisa receber só os dígitos. Teste na Task 2.
3. **Evento antigo do WAHA ainda pendente no inbox depois do corte.** O worker deve usar o parser do WAHA, não falhar. Teste na Task 3.
4. **Envelope com status de entrega, grupo, eco `from_me` ou `connection`.** Nunca vira mensagem de cliente. Testes nas Tasks 1 e 4.
5. **Mesma mensagem reenviada pela Wame.** Grava uma vez só (`ON CONFLICT` já existe). Teste na Task 4.

---

## File Structure

| Arquivo | Responsabilidade |
|---|---|
| `internal/whatsapp/whatsapp.go` | + `ConnectionEvent`, `ConnectionParser` |
| `internal/whatsapp/wame/wame.go` | cliente: `New`, `Name`, parse de mensagens e conexão |
| `internal/whatsapp/wame/send.go` | envios, presença, visto, download de mídia, `to` |
| `internal/whatsapp/wame/wame_test.go` | testes de parse |
| `internal/whatsapp/wame/send_test.go` | testes de envio com `httptest` |
| `internal/whatsapp/build/build.go` | escolhe o provedor pela config e monta os parsers |
| `internal/whatsapp/build/build_test.go` | testes da escolha |
| `internal/config/config.go` (+ test) | `WhatsAppProvider`, `WameBaseURL`, `WameKey`, `WameToken` |
| `internal/engine/engine.go` | campo `IdentityProvider` |
| `internal/worker/worker.go` (+ test) | campo `Parsers` |
| `internal/db/migrations/0011_whatsapp_status.sql` | tabela `whatsapp_status` |
| `internal/store/whatsapp_status.go` (+ test) | `SetWhatsAppStatus`, `WhatsAppStatus` |
| `internal/httpapi/httpapi.go` (+ test) | evento de conexão no webhook |
| `cmd/api/main.go`, `cmd/worker/main.go` | usam `build` |
| `internal/db/db_test.go` | conta 11 migrations |

---

### Task 1: Adaptador Wame — parse de mensagens e conexão

**Files:**
- Modify: `internal/whatsapp/whatsapp.go` (depois da interface `Provider`)
- Create: `internal/whatsapp/wame/wame.go`
- Test: `internal/whatsapp/wame/wame_test.go`

**Interfaces:**
- Produces:
  - `whatsapp.ConnectionEvent{Connected bool; Code int; Reason string}`
  - `whatsapp.ConnectionParser` com `ParseConnection(body []byte) (whatsapp.ConnectionEvent, bool)`
  - `wame.New(baseURL, key, token string) *wame.Client`
  - `(*Client).Name() string` retorna `"wame"`
  - `(*Client).ParseWebhook`
  - `(*Client).ParseConnection`

- [ ] **Step 1: Contrato de conexão em `whatsapp.go`** (logo após a interface `Provider`)

```go
// ConnectionEvent é o aviso do provedor de que o número conectou ou caiu.
type ConnectionEvent struct {
	Connected bool
	Code      int
	Reason    string
}

// ConnectionParser é opcional: provedores que avisam a queda do número pelo webhook.
type ConnectionParser interface {
	ParseConnection(body []byte) (ConnectionEvent, bool)
}
```

- [ ] **Step 2: Testes de parse que falham** (`internal/whatsapp/wame/wame_test.go`)

```go
package wame_test

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

func envelope(field, value string) []byte {
	return []byte(`{"object":"wame","provider":"whatsapp","instance":"inst","official":false,"entry":[{"id":"wame.x","changes":[{"field":"` + field + `","value":` + value + `}]}]}`)
}

func msg(m string) []byte {
	return envelope("messages", `{"messaging_product":"whatsapp","metadata":{"display_phone_number":"5511971214993","phone_number_id":"inst"},"contacts":[{"profile":{"name":"Fulano"},"wa_id":"5511999998888"}],"messages":[`+m+`]}`)
}

var c = wame.New("https://server.wame.api.br", "chave", "")

func TestParseText(t *testing.T) {
	in, ok, err := c.ParseWebhook(msg(`{"from":"5511999998888","chat_type":"individual","id":"W1","timestamp":"1700000000","type":"text","text":{"body":"Olá!"}}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if in.ExternalID != "W1" || in.Phone != "5511999998888" || in.ChatID != "5511999998888" || in.Kind != whatsapp.KindText || in.Text != "Olá!" {
		t.Fatalf("inbound = %+v", in)
	}
}

func TestParseNormalizesNinthDigit(t *testing.T) {
	in, ok, _ := c.ParseWebhook(msg(`{"from":"551199998888","chat_type":"individual","id":"W2","type":"text","text":{"body":"oi"}}`))
	if !ok || in.Phone != "5511999998888" || in.ChatID != "551199998888" {
		t.Fatalf("phone=%q chat=%q ok=%v", in.Phone, in.ChatID, ok)
	}
}

func TestParseMedia(t *testing.T) {
	cases := []struct {
		m    string
		kind whatsapp.Kind
		text string
	}{
		{`{"from":"5511999998888","chat_type":"individual","id":"A","type":"audio","audio":{"id":"A","mime_type":"audio/ogg","voice":true}}`, whatsapp.KindAudio, ""},
		{`{"from":"5511999998888","chat_type":"individual","id":"I","type":"image","image":{"id":"I","mime_type":"image/jpeg","caption":"foto"}}`, whatsapp.KindImage, "foto"},
		{`{"from":"5511999998888","chat_type":"individual","id":"V","type":"video","video":{"id":"V","mime_type":"video/mp4","caption":"vid"}}`, whatsapp.KindVideo, "vid"},
		{`{"from":"5511999998888","chat_type":"individual","id":"S","type":"sticker","sticker":{"id":"S"}}`, whatsapp.KindOther, ""},
	}
	for _, tc := range cases {
		in, ok, err := c.ParseWebhook(msg(tc.m))
		if err != nil || !ok || in.Kind != tc.kind || in.Text != tc.text {
			t.Errorf("%s: in=%+v ok=%v err=%v", tc.m, in, ok, err)
		}
	}
}

func TestParseIgnores(t *testing.T) {
	ignored := [][]byte{
		msg(`{"from":"5511777776666","chat_type":"group","group_id":"123@g.us","id":"G","type":"text","text":{"body":"oi"}}`),
		msg(`{"from":"5511999998888","chat_type":"individual","id":"OUT","type":"text","text":{"body":"minha"},"from_me":true}`),
		envelope("messages", `{"messaging_product":"whatsapp","statuses":[{"id":"W1","status":"read","recipient_id":"5511999998888"}]}`),
		envelope("connection", `{"connection":{"status":"close","code":1004,"reason":"disconnected"}}`),
		envelope("presence", `{}`),
		msg(`{"from":"","chat_type":"individual","id":"X","type":"text","text":{"body":"sem remetente"}}`),
	}
	for i, body := range ignored {
		if _, ok, err := c.ParseWebhook(body); ok || err != nil {
			t.Errorf("caso %d: ok=%v err=%v", i, ok, err)
		}
	}
}

func TestParseInvalidJSON(t *testing.T) {
	if _, _, err := c.ParseWebhook([]byte("{nope")); err == nil {
		t.Fatal("esperava erro")
	}
}

func TestParseConnection(t *testing.T) {
	ev, ok := c.ParseConnection(envelope("connection", `{"connection":{"status":"close","code":1004,"reason":"disconnected"}}`))
	if !ok || ev.Connected || ev.Code != 1004 || ev.Reason != "disconnected" {
		t.Fatalf("close: %+v ok=%v", ev, ok)
	}
	ev, ok = c.ParseConnection(envelope("connection", `{"connection":{"status":"open","code":200}}`))
	if !ok || !ev.Connected {
		t.Fatalf("open: %+v ok=%v", ev, ok)
	}
	if _, ok := c.ParseConnection(msg(`{"from":"5511999998888","chat_type":"individual","id":"W","type":"text","text":{"body":"oi"}}`)); ok {
		t.Fatal("mensagem não é conexão")
	}
}

func TestName(t *testing.T) {
	if c.Name() != "wame" {
		t.Fatal(c.Name())
	}
}
```

- [ ] **Step 3: Rodar e ver falhar**

Run: `go test ./internal/whatsapp/wame/ -run 'TestParse|TestName' -v`
Expected: FAIL de compilação (`undefined: wame.New`).

- [ ] **Step 4: Implementar `internal/whatsapp/wame/wame.go`**

```go
// Package wame é o adaptador da Wame (wame.api.br), modo não oficial por QR. Webhook no formato
// "meta" (envelope da Cloud API). A key da instância vai no caminho; o token Bearer é opcional.
package wame

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type Client struct {
	BaseURL string
	Key     string
	Token   string
	HTTP    *http.Client
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

type envelope struct {
	Entry []struct {
		Changes []struct {
			Field string          `json:"field"`
			Value json.RawMessage `json:"value"`
		} `json:"changes"`
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

type messagesValue struct {
	Messages []message `json:"messages"`
}

func (c *Client) changes(body []byte) ([]struct {
	Field string
	Value json.RawMessage
}, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("wame: webhook inválido: %w", err)
	}
	var out []struct {
		Field string
		Value json.RawMessage
	}
	for _, e := range env.Entry {
		for _, ch := range e.Changes {
			out = append(out, struct {
				Field string
				Value json.RawMessage
			}{ch.Field, ch.Value})
		}
	}
	return out, nil
}

// ParseWebhook devolve a primeira mensagem de cliente do envelope. Grupo, eco nosso (from_me),
// status de entrega, presença e conexão ficam de fora.
func (c *Client) ParseWebhook(body []byte) (whatsapp.Inbound, bool, error) {
	chs, err := c.changes(body)
	if err != nil {
		return whatsapp.Inbound{}, false, err
	}
	for _, ch := range chs {
		if ch.Field != "messages" {
			continue
		}
		var v messagesValue
		if err := json.Unmarshal(ch.Value, &v); err != nil {
			return whatsapp.Inbound{}, false, fmt.Errorf("wame: mensagens inválidas: %w", err)
		}
		for _, m := range v.Messages {
			if in, ok := toInbound(m, body); ok {
				return in, true, nil
			}
		}
	}
	return whatsapp.Inbound{}, false, nil
}

func toInbound(m message, raw []byte) (whatsapp.Inbound, bool) {
	if m.ID == "" || m.FromMe || m.ChatType != "individual" {
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
	chs, err := c.changes(body)
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
```

Obs.: o `ChatID` guarda os dígitos como chegaram (`551199998888` no teste do nono dígito) e o `Phone` é o canônico. É o mesmo comportamento do WAHA, que responde no número como o WhatsApp registrou.

- [ ] **Step 5: Rodar e ver passar**

Run: `go test ./internal/whatsapp/... -v`
Expected: PASS em todos, inclusive os testes antigos de `whatsapp` e `waha`. Nesta task o `Client` ainda não implementa `Provider` por inteiro; nenhum código o usa como `Provider` até a Task 2, então compila.

- [ ] **Step 6: Commit**

```bash
git add internal/whatsapp/whatsapp.go internal/whatsapp/wame/wame.go internal/whatsapp/wame/wame_test.go
git commit -m "feat(wame): parse do webhook (mensagens e conexão) no formato meta"
```

---

### Task 2: Adaptador Wame — envios, presença, visto e mídia

**Files:**
- Create: `internal/whatsapp/wame/send.go`
- Test: `internal/whatsapp/wame/send_test.go`

**Interfaces:**
- Consumes: `wame.Client` (Task 1)
- Produces: `*wame.Client` satisfaz `whatsapp.Provider` e `whatsapp.ConnectionParser`

- [ ] **Step 1: Testes que falham** (`send_test.go`)

```go
package wame_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

var (
	_ whatsapp.Provider         = (*wame.Client)(nil)
	_ whatsapp.ConnectionParser = (*wame.Client)(nil)
)

type call struct {
	Method, Path, Auth string
	Body               map[string]any
}

func server(t *testing.T, status int, reply string) (*wame.Client, *[]call) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		calls = append(calls, call{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), body})
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return wame.New(srv.URL, "KEY", "TOK"), &calls
}

func TestSendText(t *testing.T) {
	c, calls := server(t, 200, `{"status":200}`)
	if err := c.SendText(context.Background(), "5511999998888@c.us", "oi"); err != nil {
		t.Fatal(err)
	}
	got := (*calls)[0]
	if got.Method != "POST" || got.Path != "/KEY/message/text" || got.Auth != "Bearer TOK" {
		t.Fatalf("%+v", got)
	}
	if got.Body["to"] != "5511999998888" || got.Body["text"] != "oi" {
		t.Fatalf("body %+v", got.Body)
	}
}

func TestNoTokenNoHeader(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { auth = r.Header.Get("Authorization") }))
	defer srv.Close()
	if err := wame.New(srv.URL, "KEY", "").SendText(context.Background(), "5511999998888", "oi"); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("auth = %q", auth)
	}
}

func TestSendErrors(t *testing.T) {
	c, _ := server(t, 401, `{"code":"token_invalid"}`)
	if err := c.SendText(context.Background(), "5511999998888", "oi"); err == nil {
		t.Fatal("esperava erro em 401")
	}
}

func TestVoiceImagePresenceSeen(t *testing.T) {
	c, calls := server(t, 200, `{}`)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(c.SendVoice(ctx, "5511999998888", []byte("AUD"), "audio/mpeg"))
	must(c.SendImage(ctx, "5511999998888", []byte("IMG"), "image/jpeg", "prévia"))
	must(c.SetPresence(ctx, "5511999998888", whatsapp.PresenceTyping))
	must(c.SetPresence(ctx, "5511999998888", whatsapp.PresenceRecording))
	must(c.SetPresence(ctx, "5511999998888", whatsapp.PresencePaused))
	must(c.MarkSeen(ctx, "5511999998888"))
	want := []struct {
		path string
		key  string
		val  any
	}{
		{"/KEY/message/base64/audio", "base64", "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString([]byte("AUD"))},
		{"/KEY/message/base64/image", "caption", "prévia"},
		{"/KEY/message/presence", "status", "composing"},
		{"/KEY/message/presence", "status", "recording"},
		{"/KEY/message/presence", "status", "paused"},
		{"/KEY/chat?id=5511999998888&action=markRead&value=true", "", nil},
	}
	for i, w := range want {
		got := (*calls)[i]
		if got.Path != w.path {
			t.Errorf("%d: path %q, want %q", i, got.Path, w.path)
		}
		if w.key != "" && got.Body[w.key] != w.val {
			t.Errorf("%d: %s = %v, want %v", i, w.key, got.Body[w.key], w.val)
		}
	}
	if (*calls)[1].Body["base64"] != "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString([]byte("IMG")) {
		t.Errorf("imagem: %v", (*calls)[1].Body["base64"])
	}
	if (*calls)[5].Method != "PATCH" {
		t.Errorf("visto: método %s", (*calls)[5].Method)
	}
}

func TestDownloadMedia(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		w.Header().Set("Content-Type", "audio/ogg; codecs=opus")
		io.WriteString(w, "OGG")
	}))
	defer srv.Close()
	c := wame.New(srv.URL, "KEY", "")
	in, ok, _ := c.ParseWebhook(msg(`{"from":"5511999998888","chat_type":"individual","id":"A1","type":"audio","audio":{"id":"A1","url":"https://evil.example/x","mime_type":"audio/ogg"}}`))
	if !ok {
		t.Fatal("parse")
	}
	data, mime, err := c.DownloadMedia(context.Background(), in)
	if err != nil || string(data) != "OGG" || mime != "audio/ogg" {
		t.Fatalf("data=%q mime=%q err=%v", data, mime, err)
	}
	if path != "/KEY/message/A1/media?format=binary" {
		t.Fatalf("path = %q (não pode seguir a URL do payload)", path)
	}
}

func TestChatIDForPhone(t *testing.T) {
	if got := wame.New("", "K", "").ChatIDForPhone("5511999998888"); got != "5511999998888" {
		t.Fatal(got)
	}
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test ./internal/whatsapp/wame/ -v`
Expected: FAIL (`c.SendText undefined`).

- [ ] **Step 3: Implementar `internal/whatsapp/wame/send.go`**

```go
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
	return c.send(ctx, http.MethodPost, "/message/presence", map[string]any{"to": to(chatID), "status": status})
}

// SendVoice manda o áudio em base64; a Wame entrega como mensagem de voz (PTT, ogg/opus).
func (c *Client) SendVoice(ctx context.Context, chatID string, audio []byte, mime string) error {
	return c.send(ctx, http.MethodPost, "/message/base64/audio", map[string]any{"to": to(chatID), "base64": dataURI(mime, audio)})
}

// SendImage manda a imagem em base64 com legenda.
func (c *Client) SendImage(ctx context.Context, chatID string, image []byte, mime, caption string) error {
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
		return fmt.Errorf("wame %s: status %d: %s", strings.Split(path, "?")[0], res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/"+url.PathEscape(c.Key)+path, body)
	if err != nil {
		return nil, fmt.Errorf("wame %s: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wame %s: %w", strings.Split(path, "?")[0], err)
	}
	return res, nil
}
```

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./internal/whatsapp/... -v && go vet ./internal/whatsapp/...`
Expected: PASS, sem avisos do vet.

- [ ] **Step 5: Commit**

```bash
git add internal/whatsapp/wame/send.go internal/whatsapp/wame/send_test.go
git commit -m "feat(wame): envios de texto, voz e imagem, presença, visto e download de mídia"
```

---

### Task 3: Escolha do provedor, config, engine e worker

**Files:**
- Modify: `internal/config/config.go` (struct, `Load`, `Require`), `internal/config/config_test.go`
- Create: `internal/whatsapp/build/build.go`, `internal/whatsapp/build/build_test.go`
- Modify: `internal/engine/engine.go` (struct `Engine` e `HandleInbound`)
- Modify: `internal/worker/worker.go` (struct `Worker` e `inbox`), `internal/worker/worker_test.go`
- Modify: `cmd/api/main.go`, `cmd/worker/main.go`

**Interfaces:**
- Consumes: `wame.New`, `waha.New`
- Produces:
  - `config.Config` ganha `WhatsAppProvider`, `WameBaseURL`, `WameKey`, `WameToken`
  - `build.Provider(cfg config.Config) (whatsapp.Provider, error)`
  - `build.Parsers(cfg config.Config) map[string]whatsapp.Provider`
  - `engine.Engine.IdentityProvider string`
  - `worker.Worker.Parsers map[string]whatsapp.Provider`

- [ ] **Step 1: Testes de config que falham** (adicionar em `config_test.go`)

```go
func TestLoadWhatsAppProvider(t *testing.T) {
	c := config.Load(env(map[string]string{}))
	if c.WhatsAppProvider != "waha" || c.WameBaseURL != "https://server.wame.api.br" {
		t.Fatalf("padrões: %q %q", c.WhatsAppProvider, c.WameBaseURL)
	}
	c = config.Load(env(map[string]string{"WHATSAPP_PROVIDER": "wame", "WAME_KEY": "k", "WAME_TOKEN": "t", "WAME_BASE_URL": "https://x/"}))
	if c.WhatsAppProvider != "wame" || c.WameKey != "k" || c.WameToken != "t" || c.WameBaseURL != "https://x" {
		t.Fatalf("%+v", c)
	}
	if err := c.Require("WAME_KEY"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Implementar a config**

Em `Config`, depois de `WAHASession`:

```go
	// Provedor do WhatsApp: "waha" (padrão) ou "wame".
	WhatsAppProvider string
	WameBaseURL      string
	WameKey          string
	WameToken        string
```

Em `Load`, depois de `WAHASession`:

```go
		WhatsAppProvider:  orDefault(strings.ToLower(strings.TrimSpace(getenv("WHATSAPP_PROVIDER"))), "waha"),
		WameBaseURL:       strings.TrimRight(orDefault(getenv("WAME_BASE_URL"), "https://server.wame.api.br"), "/"),
		WameKey:           getenv("WAME_KEY"),
		WameToken:         getenv("WAME_TOKEN"),
```

Em `Require`, no mapa `values`:

```go
		"WAME_KEY":           c.WameKey,
```

Run: `go test ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 3: Testes do `build` que falham** (`internal/whatsapp/build/build_test.go`)

```go
package build_test

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/build"
)

func TestProvider(t *testing.T) {
	cases := []struct {
		cfg     config.Config
		name    string
		wantErr bool
	}{
		{config.Config{WhatsAppProvider: "waha", WAHAURL: "http://waha:3000", WAHAAPIKey: "k"}, "waha", false},
		{config.Config{WhatsAppProvider: "waha"}, "", true},
		{config.Config{WhatsAppProvider: "wame", WameKey: "k", WameBaseURL: "https://server.wame.api.br"}, "wame", false},
		{config.Config{WhatsAppProvider: "wame"}, "", true},
		{config.Config{WhatsAppProvider: "zap"}, "", true},
	}
	for _, tc := range cases {
		p, err := build.Provider(tc.cfg)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v", tc.cfg.WhatsAppProvider, err)
			continue
		}
		if err == nil && p.Name() != tc.name {
			t.Errorf("%s: nome %q", tc.cfg.WhatsAppProvider, p.Name())
		}
	}
}

func TestParsersAlwaysHasBoth(t *testing.T) {
	ps := build.Parsers(config.Config{})
	if ps["waha"] == nil || ps["wame"] == nil {
		t.Fatalf("parsers: %v", ps)
	}
}
```

- [ ] **Step 4: Implementar `internal/whatsapp/build/build.go`**

```go
// Package build escolhe o adaptador de WhatsApp pela configuração.
package build

import (
	"fmt"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/waha"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

// Provider devolve o adaptador ativo; falha se faltar configuração.
func Provider(cfg config.Config) (whatsapp.Provider, error) {
	switch cfg.WhatsAppProvider {
	case "waha":
		if err := cfg.Require("WAHA_URL", "WAHA_API_KEY"); err != nil {
			return nil, err
		}
		return waha.New(cfg.WAHAURL, cfg.WAHAAPIKey, cfg.WAHASession), nil
	case "wame":
		if err := cfg.Require("WAME_KEY"); err != nil {
			return nil, err
		}
		return wame.New(cfg.WameBaseURL, cfg.WameKey, cfg.WameToken), nil
	default:
		return nil, fmt.Errorf("config: WHATSAPP_PROVIDER inválido %q (use waha ou wame)", cfg.WhatsAppProvider)
	}
}

// Parsers lê eventos de qualquer provedor já usado: na troca, o inbox ainda pode ter eventos do outro.
// Só o parse é usado, então não precisa de credencial.
func Parsers(cfg config.Config) map[string]whatsapp.Provider {
	return map[string]whatsapp.Provider{
		"waha": waha.New(cfg.WAHAURL, cfg.WAHAAPIKey, cfg.WAHASession),
		"wame": wame.New(cfg.WameBaseURL, cfg.WameKey, cfg.WameToken),
	}
}
```

Run: `go test ./internal/whatsapp/build/ -v`
Expected: PASS.

- [ ] **Step 5: Engine — namespace das identidades**

No struct `Engine`, logo abaixo de `WA whatsapp.Provider`:

```go
	// IdentityProvider é o namespace de whatsapp_identities. Fica "waha" mesmo com a Wame,
	// para os clientes atuais continuarem cadastrados. Vazio = WA.Name().
	IdentityProvider string
```

Novo método, perto de `now()`:

```go
func (e *Engine) identityProvider() string {
	if e.IdentityProvider != "" {
		return e.IdentityProvider
	}
	return e.WA.Name()
}
```

Em `HandleInbound`, trocar as três ocorrências de `e.WA.Name()` (FindIdentity, log e AutoSignup) por `e.identityProvider()`.

Teste em `internal/engine/engine_test.go`. Use o harness existente: o `fakeWA` tem `Name()` = `"openwa"` e o helper liga telefones com `"openwa"`.

```go
func TestIdentityProviderOverridesWAName(t *testing.T) {
	h := newHarness(t)
	h.eng.IdentityProvider = "openwa"
	h.wa.name = "wame" // se fakeWA não tiver o campo name, adicionar: Name() devolve name quando não vazio
	phone := h.client(t) // cliente já cadastrado no namespace "openwa"
	h.say(phone, "oi")
	if h.wa.lastTextTo(phone) == msgUnknown {
		t.Fatal("cliente cadastrado tratado como desconhecido")
	}
}
```

Os nomes do harness (`newHarness`, `h.client`, `h.say`, `lastTextTo`) devem ser confirmados em `engine_test.go` antes de escrever. Adapte aos helpers reais, mantendo a asserção: com `WA.Name()` diferente de `IdentityProvider`, o cliente já ligado em `IdentityProvider` não recebe `msgUnknown` nem ganha cadastro novo.

Run: `go test ./internal/engine/ -run TestIdentityProvider -v`
Expected: PASS.

- [ ] **Step 6: Worker — parser pelo provedor do evento**

No struct `Worker`:

```go
	// Parsers lê eventos gravados por outro provedor (troca WAHA ↔ Wame). Sem entrada, usa WA.
	Parsers map[string]whatsapp.Provider
```

Em `inbox`, trocar `in, keep, err := w.WA.ParseWebhook(ev.Payload)` por:

```go
		parser := w.WA
		if p, ok := w.Parsers[ev.Provider]; ok && p != nil {
			parser = p
		}
		in, keep, err := parser.ParseWebhook(ev.Payload)
```

Teste em `worker_test.go`: grave no inbox um evento com `provider` `"legado"`; registre em `Parsers["legado"]` um fake que devolve um `Inbound`; confirme que o engine recebeu a mensagem e que o fake `WA` (`openwa`) não foi usado para o parse. Siga o padrão dos testes existentes do arquivo, que já usam `testdb` e o `fakeWA` local.

Run: `go test ./internal/worker/ -v`
Expected: PASS.

- [ ] **Step 7: Mains**

`cmd/worker/main.go`:
- `Require` passa a exigir só `"DATABASE_URL", "OPENROUTER_API_KEY", "ATTENDANT_MODEL", "MANAGER_MODEL", "TRANSCRIBE_MODEL"`; as variáveis do WhatsApp quem valida é o `build`.
- Trocar `wa := waha.New(...)` por:

```go
	wa, err := build.Provider(cfg)
	if err != nil {
		return err
	}
```

- No `engine.Engine{...}`, adicionar `IdentityProvider: "waha",` com o comentário `// namespace histórico das identidades; não muda com a Wame`.
- `w := &worker.Worker{Store: st, WA: wa, Parsers: build.Parsers(cfg), Engine: eng, Log: log}`.
- No log de início, adicionar `"whatsapp", wa.Name()`.
- Remover o import de `waha` e adicionar `.../internal/whatsapp/build`.

`cmd/api/main.go`: no `httpapi.Webhook`, trocar `WA: waha.New(...)` por:

```go
	wa, err := build.Provider(cfg)
	if err != nil {
		return err
	}
```

Declarar antes do `webhook` e usar `WA: wa`. Ajustar imports.

Run: `go build ./... && go vet ./...`
Expected: sem erros.

- [ ] **Step 8: Suíte inteira e commit**

Run: `go test ./...` (com `TEST_DATABASE_URL` definido, ver README)
Expected: PASS.

```bash
git add internal/config internal/whatsapp/build internal/engine/engine.go internal/engine/engine_test.go internal/worker cmd
git commit -m "feat(whatsapp): WHATSAPP_PROVIDER escolhe WAHA ou Wame; identidades mantêm o namespace waha"
```

---

### Task 4: Estado da conexão pelo webhook

**Files:**
- Create: `internal/db/migrations/0011_whatsapp_status.sql`
- Create: `internal/store/whatsapp_status.go`, `internal/store/whatsapp_status_test.go`
- Modify: `internal/httpapi/httpapi.go` (`Webhook.ServeHTTP`), `internal/httpapi/httpapi_test.go`
- Modify: `internal/db/db_test.go` (10 → 11 migrations)

**Interfaces:**
- Consumes: `whatsapp.ConnectionParser`, `whatsapp.ConnectionEvent` (Task 1)
- Produces:
  - `(*store.Store).SetWhatsAppStatus(ctx, provider string, ev whatsapp.ConnectionEvent) (changed bool, err error)`
  - `(*store.Store).WhatsAppStatus(ctx, provider string) (store.WhatsAppStatus, error)`, com `WhatsAppStatus{Provider string; Connected bool; Code int; Reason string; UpdatedAt time.Time}`
  - O admin (Bloco 3) lê esse estado.

- [ ] **Step 1: Migration `0011_whatsapp_status.sql`**

```sql
-- Último estado da conexão do número do bot, avisado pelo provedor (webhook "connection").
CREATE TABLE whatsapp_status (
  provider text PRIMARY KEY,
  connected boolean NOT NULL,
  code int,
  reason text,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE whatsapp_status ENABLE ROW LEVEL SECURITY;
```

Em `internal/db/db_test.go`, trocar `want 10` e a comparação para 11.

- [ ] **Step 2: Teste do store que falha** (`whatsapp_status_test.go`)

```go
package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

func TestWhatsAppStatus(t *testing.T) {
	st := store.New(testdb.New(t))
	ctx := context.Background()
	if _, err := st.WhatsAppStatus(ctx, "wame"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sem estado: %v", err)
	}
	changed, err := st.SetWhatsAppStatus(ctx, "wame", whatsapp.ConnectionEvent{Connected: false, Code: 1004, Reason: "disconnected"})
	if err != nil || !changed {
		t.Fatalf("primeira queda: changed=%v err=%v", changed, err)
	}
	changed, _ = st.SetWhatsAppStatus(ctx, "wame", whatsapp.ConnectionEvent{Connected: false, Code: 1004})
	if changed {
		t.Fatal("queda repetida não é mudança")
	}
	changed, _ = st.SetWhatsAppStatus(ctx, "wame", whatsapp.ConnectionEvent{Connected: true, Code: 200})
	s, err := st.WhatsAppStatus(ctx, "wame")
	if !changed || err != nil || !s.Connected || s.Code != 200 {
		t.Fatalf("volta: changed=%v s=%+v err=%v", changed, s, err)
	}
}
```

- [ ] **Step 3: Implementar `internal/store/whatsapp_status.go`**

```go
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type WhatsAppStatus struct {
	Provider  string
	Connected bool
	Code      int
	Reason    string
	UpdatedAt time.Time
}

// SetWhatsAppStatus grava o estado; changed=true quando conectado/desconectado mudou (ou é o primeiro).
func (s *Store) SetWhatsAppStatus(ctx context.Context, provider string, ev whatsapp.ConnectionEvent) (bool, error) {
	var before *bool
	err := s.pool.QueryRow(ctx, `SELECT connected FROM whatsapp_status WHERE provider = $1`, provider).Scan(&before)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO whatsapp_status (provider, connected, code, reason, updated_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), now())
		ON CONFLICT (provider) DO UPDATE SET connected = EXCLUDED.connected, code = EXCLUDED.code,
		  reason = EXCLUDED.reason, updated_at = now()`, provider, ev.Connected, ev.Code, ev.Reason)
	if err != nil {
		return false, err
	}
	return before == nil || *before != ev.Connected, nil
}

func (s *Store) WhatsAppStatus(ctx context.Context, provider string) (WhatsAppStatus, error) {
	var w WhatsAppStatus
	var code *int
	var reason *string
	err := s.pool.QueryRow(ctx, `SELECT provider, connected, code, reason, updated_at FROM whatsapp_status WHERE provider = $1`,
		provider).Scan(&w.Provider, &w.Connected, &code, &reason, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	if code != nil {
		w.Code = *code
	}
	if reason != nil {
		w.Reason = *reason
	}
	return w, err
}
```

Confira que `ErrNotFound` existe em `internal/store` (é usado em `identity_test.go`) e reutilize-o.

Run: `go test ./internal/store/ -run TestWhatsAppStatus -v && go test ./internal/db/ -v`
Expected: PASS.

- [ ] **Step 4: Teste do webhook que falha** (adicionar em `httpapi_test.go`)

O webhook com a Wame recebe aviso de conexão: grava o estado, responde `{"status":"connection"}` e não grava no inbox. Mensagem duplicada responde `duplicate`.

```go
func TestWebhookWameConnectionAndDuplicate(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool)
	wh := &httpapi.Webhook{Store: st, WA: wame.New("http://nao-usado", "k", ""), Secret: "segredo", Log: discard}
	conn := `{"object":"wame","entry":[{"changes":[{"field":"connection","value":{"connection":{"status":"close","code":1004,"reason":"disconnected"}}}]}]}`
	rec := post(wh, "/webhooks/whatsapp?token=segredo", conn)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "connection") {
		t.Fatalf("conexão: %d %s", rec.Code, rec.Body)
	}
	s, err := st.WhatsAppStatus(context.Background(), "wame")
	if err != nil || s.Connected {
		t.Fatalf("estado: %+v %v", s, err)
	}
	m := `{"object":"wame","entry":[{"changes":[{"field":"messages","value":{"messages":[{"from":"5511999998888","chat_type":"individual","id":"W1","type":"text","text":{"body":"oi"}}]}}]}]}`
	if rec := post(wh, "/webhooks/whatsapp?token=segredo", m); !strings.Contains(rec.Body.String(), "queued") {
		t.Fatalf("1ª: %s", rec.Body)
	}
	if rec := post(wh, "/webhooks/whatsapp?token=segredo", m); !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("2ª: %s", rec.Body)
	}
}
```

Use os helpers `post`, `discard` e os imports que o arquivo já tem; acrescente `wame`, `context` e `strings` se faltarem.

- [ ] **Step 5: Implementar no `Webhook.ServeHTTP`**

Logo depois de ler o `body` e antes de `h.WA.ParseWebhook(body)`:

```go
	if cp, ok := h.WA.(whatsapp.ConnectionParser); ok {
		if ev, isConn := cp.ParseConnection(body); isConn {
			changed, err := h.Store.SetWhatsAppStatus(r.Context(), h.WA.Name(), ev)
			if err != nil {
				h.Log.Error("falha ao gravar estado do WhatsApp", "erro", err)
				http.Error(w, "erro interno", http.StatusInternalServerError)
				return
			}
			if changed && !ev.Connected {
				h.Log.Warn("WhatsApp do bot desconectado", "provider", h.WA.Name(), "codigo", ev.Code, "motivo", ev.Reason)
			} else if changed {
				h.Log.Info("WhatsApp do bot conectado", "provider", h.WA.Name())
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"connection"}`)
			return
		}
	}
```

O alerta ativo (admin e e-mail) entra no Bloco 3, que lê `whatsapp_status`. Aviso por WhatsApp não serve aqui, porque é justamente o WhatsApp que caiu.

Run: `go test ./internal/httpapi/ -v`
Expected: PASS, com os testes antigos do WAHA intactos.

- [ ] **Step 6: Suíte, guarda-regressão e commit**

Run: `go test ./... && go vet ./...`
Expected: PASS.

```bash
git add internal/db internal/store/whatsapp_status.go internal/store/whatsapp_status_test.go internal/httpapi
git commit -m "feat(whatsapp): grava a queda e a volta do número pelo webhook de conexão"
```

Depois: `graphify update .`

---

### Task 5: Corte em produção (manual, com o usuário)

Esta task não tem código. Cada passo depende de credencial do usuário ou mexe em produção.

- [ ] **Step 1: Instância na Wame (usuário).** Criar a conta, plano Pro e uma instância no painel `dash.wame.api.br`. Salvar `WAME_KEY` (e `WAME_TOKEN`, se ligar "Require access token") em `C:\Claude Code\Jarvis - N4G\.secrets\wame.env`.

- [ ] **Step 2: Configurar o webhook da instância**

```bash
curl -X PUT "https://server.wame.api.br/$WAME_KEY/instance" \
  -H "Content-Type: application/json" ${WAME_TOKEN:+-H "Authorization: Bearer $WAME_TOKEN"} \
  -d '{"allowWebhook":true,"allowNumber":"all","webhookFormat":"meta",
       "webhookMessage":"https://gestor-trafego-ia-api.x7qbj3.easypanel.host/webhooks/whatsapp?token='"$WEBHOOK_SECRET"'",
       "webhookConnection":"https://gestor-trafego-ia-api.x7qbj3.easypanel.host/webhooks/whatsapp?token='"$WEBHOOK_SECRET"'",
       "webhookMessageFromMe":"","webhookGroup":"","webhookQrCode":"","webhookHistory":""}'
```

- [ ] **Step 3: Conectar o número do bot (5511971214993).** Pegar o QR com `POST /{key}/instance` ou o código de pareamento com `POST /{key}/instance/pairing-code` `{"phoneNumber":"5511971214993"}`. Antes, desconectar a sessão do WAHA, porque o mesmo número não pode ficar nos dois.

- [ ] **Step 4: Publicar a branch** na api e no worker com `WHATSAPP_PROVIDER=wame`, `WAME_KEY` e `WAME_TOKEN`. As variáveis `WAHA_*` ficam, para a volta rápida.

- [ ] **Step 5: Checklist ao vivo com um celular de teste**
  - texto recebido e respondido;
  - "digitando…" visível;
  - visto azul;
  - áudio recebido e transcrito;
  - resposta em voz;
  - foto recebida para o anúncio;
  - prévia em imagem enviada;
  - lembrete agendado antes do corte (`…@c.us`) entregue;
  - desconectar o aparelho no celular faz `whatsapp_status` virar `connected=false`.

- [ ] **Step 6: Volta rápida, se algum item falhar.** `WHATSAPP_PROVIDER=waha` + redeploy + reconectar o QR no WAHA.

- [ ] **Step 7: Registrar.** Memória do projeto e hub do vault com o resultado. Desligar o WAHA 7 dias depois do corte estável.

---

## Ajuste de 2026-10-09: a instância criada é OFICIAL (Cloud API pela Wame)

Verificado em `GET /{key}/instance`:

- `official: true`, plataforma `CLOUD_API`, coexistência com o app WhatsApp Business (`isOnBizApp: true`);
- número **+55 49 9191-1891**, nome verificado "Meu Anuncio", BM "Ana Moura" (verificada);
- limite `TIER_250` (250 contatos novos por dia);
- `healthStatus`:
  - **WABA `BLOCKED`**;
  - número `LIMITED` ("display name has not been approved yet");
- webhooks ainda vazios.

O que muda nas tasks:

- **Task 2:**
  - `MarkSeen` e `SetPresence(typing)` no oficial usam `POST /{key}/message/read` com `{"messageId": <última mensagem recebida do chat>, "typing": <bool>}`. O `Client` guarda em memória (sync.Map) a última `ExternalID` por chat, preenchida no `ParseWebhook`. O worker parseia e envia no mesmo processo.
  - `PresenceRecording` vira `typing`, porque o oficial não tem "gravando áudio".
  - `PresencePaused` vira no-op.
  - Sem mensagem conhecida do chat, `MarkSeen` e `SetPresence` não fazem nada (sem erro).
  - O `Client` ganha o campo `Official bool`, lido de `WAME_OFFICIAL=true`. Com `false`, mantém as rotas do modo QR (`/message/presence` e `PATCH /chat`).
- **Task 4:** o webhook de conexão continua, mas no oficial quase não dispara.
- **Task nova — janela de 24 h e modelos (templates):**
  - Fora de 24 h desde a última mensagem do cliente, texto livre é recusado pela Meta. Os envios iniciados pelo sistema passam a usar `POST /{key}/message/template`:
    - lembrete de cadastro de 24 h;
    - lembrete de aceite do convite;
    - relatório diário;
    - avisos ao operador.
  - Os modelos são criados por `POST /{key}/templates` (categoria UTILITY, pt_BR) e aprovados pela Meta.
  - O engine decide texto ou modelo pelo horário da última mensagem recebida da conversa.
- **Task 5 (corte):**
  - `BOT_WHATSAPP_NUMBER=5549991911891`;
  - webhook da instância com `PUT /{key}/instance`;
  - **antes**, resolver o `BLOCKED` da WABA (forma de pagamento no Gerenciador do WhatsApp) e acompanhar a aprovação do nome de exibição.
