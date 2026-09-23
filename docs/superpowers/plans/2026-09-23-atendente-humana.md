# Atendente mais humana — plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a Atendente responde no WhatsApp como uma pessoa: visto azul, "digitando…"/"gravando áudio…", balões curtos, áudio quando o cliente manda áudio, e uma resposta só para mensagens seguidas.

**Architecture:** um pacote puro `internal/humanize` decide balões, atrasos e voz. O engine ganha um job `conversation.reply` agendado com atraso (debounce) e um envio "humano" encadeado (um job por balão, com presença antes). Voz sai por TTS do OpenRouter e `sendVoice` do WAHA. `HUMANIZE=false` mantém o fluxo atual intacto (os testes atuais rodam nesse modo).

**Tech Stack:** Go 1.x, pgx/Postgres (outbox com `run_after`), WAHA (GOWS), OpenRouter (`/chat/completions`, `/audio/speech`).

**Spec:** `docs/superpowers/specs/2026-09-23-atendente-humana-design.md`

## Global Constraints

- Valores, códigos e o resumo da proposta nunca saem da IA e sempre vão em texto (`sendJob` comum, sem voz).
- Mensagens fixas continuam em `internal/engine/messages.go`; ao reescrever, manter os trechos que os testes procuram (lista na Task 7).
- Aviso ao operador (`ConversationID == 0`) sai direto, sem presença nem atraso.
- Falha de presença ou de visto só vai para o log; nunca impede envio.
- Até 3 balões; partes a mais são unidas na última com `\n\n`.
- Digitação: `clamp((1,2 s + 35 ms × runas) × (0,85 + 0,3 × r), 1,5 s, 7 s)`. Pausa entre balões: `0,6 s + r × 0,8 s`.
- Voz só para resposta da Atendente, com texto de 1 a 600 runas, quando a última mensagem do lote foi áudio e há `TTS_MODEL`.
- TTS com prazo de 20 s; falhou, manda o mesmo texto em balões com "digitando…".
- Debounce padrão 4 s (`REPLY_DEBOUNCE_SECONDS`). Lote de no máximo 10 mensagens.
- `HUMANIZE` padrão `true`; `HUMANIZE=false` = comportamento de hoje.
- Testes de banco precisam de `TEST_DATABASE_URL` (README); `SKIP` conta como falha.
- Comentários e mensagens em português, no estilo do código atual.

## Review Focus

- Mensagem nova chega enquanto os balões da resposta anterior ainda estão na fila: o lote seguinte não pode incluir mensagens já respondidas nem cortar o histórico errado (teste na Task 6: `TestBatchIgnoresAlreadyAnswered`).
- Cliente em `needs_human` manda mensagens e depois o humano encerra: essas mensagens não podem virar lote da próxima resposta (Task 6: `TestNeedsHumanMarksReplied`).
- Proposta com código gerada numa conversa que veio de áudio: resumo sai em texto (Task 6: `TestProposalSummaryStaysTextAfterAudio`).
- TTS lento ou fora do ar: cliente recebe texto, não silêncio (Task 5: `TestVoiceFallsBackToText`).
- `SIM 123456` enviado junto com outra mensagem no mesmo lote: aprovação usa a última mensagem (Task 6: `TestApprovalUsesLastMessageOfBatch`).

---

## File Structure

- Create `internal/humanize/humanize.go` + `humanize_test.go` — regras puras (balões, atrasos, voz).
- Modify `internal/llm/llm.go`, `openrouter.go`, `fake.go`, `openrouter_test.go` — `Speaker`/`Speak`.
- Modify `internal/whatsapp/whatsapp.go`, `waha/waha.go`, `waha/waha_test.go` — presença, visto, voz.
- Create `internal/db/migrations/0005_replied_through.sql`.
- Modify `internal/store/queue.go` (Delay), `internal/store/conversation.go` (IDs, lote) + `internal/store/humanize_test.go`.
- Create `internal/engine/humanize.go` (envio humano, reply job) + `internal/engine/humanize_test.go`.
- Modify `internal/engine/engine.go`, `jobs.go`, `messages.go`, engine test fakes.
- Modify `internal/agent/attendant/attendant.go`, `prompt.go`, `attendant_test.go`.
- Modify `internal/config/config.go`, `config_test.go`, `cmd/worker/main.go`, `.env.example`, `docs/deploy-easypanel.md`, `internal/worker/worker_test.go` (fake).

---

### Task 1: Pacote `humanize`

**Files:**
- Create: `internal/humanize/humanize.go`
- Test: `internal/humanize/humanize_test.go`

**Interfaces:**
- Produces: `Split(reply string) []string`, `TypingDelay(text string, r float64) time.Duration`, `Gap(r float64) time.Duration`, `ShouldVoice(text string) bool`, `SpeechText(text string) string`, consts `MaxParts = 3`, `MaxVoiceRunes = 600`, `MinTyping`, `MaxTyping`.

- [ ] **Step 1: Write the failing test**

```go
package humanize_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/humanize"
)

func TestSplit(t *testing.T) {
	cases := map[string][]string{
		"":                               nil,
		"  oi  ":                         {"oi"},
		"Oi, Ana!\n\nQual a cidade?":     {"Oi, Ana!", "Qual a cidade?"},
		"a\n \nb\n\nc":                   {"a", "b", "c"},
		"a\n\nb\n\nc\n\nd\n\ne":          {"a", "b", "c\n\nd\n\ne"},
		"linha 1\nlinha 2":               {"linha 1\nlinha 2"},
	}
	for in, want := range cases {
		got := humanize.Split(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("Split(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestTypingDelayBounds(t *testing.T) {
	for _, r := range []float64{0, 0.5, 1} {
		if d := humanize.TypingDelay("ok", r); d != humanize.MinTyping && r == 0 {
			t.Errorf("texto curto com r=0: %v; want %v", d, humanize.MinTyping)
		}
		if d := humanize.TypingDelay(strings.Repeat("a", 2000), r); d != humanize.MaxTyping {
			t.Errorf("texto longo: %v; want %v", d, humanize.MaxTyping)
		}
	}
	// 100 runas, r=0.5: (1,2 s + 3,5 s) × 1,0 = 4,7 s
	if d := humanize.TypingDelay(strings.Repeat("é", 100), 0.5); d != 4700*time.Millisecond {
		t.Errorf("100 runas: %v; want 4.7s", d)
	}
}

func TestGap(t *testing.T) {
	if humanize.Gap(0) != 600*time.Millisecond || humanize.Gap(1) != 1400*time.Millisecond {
		t.Errorf("Gap fora dos limites: %v %v", humanize.Gap(0), humanize.Gap(1))
	}
}

func TestShouldVoice(t *testing.T) {
	if !humanize.ShouldVoice("Show! Em qual cidade fica a pizzaria?") {
		t.Error("resposta curta deveria virar áudio")
	}
	if humanize.ShouldVoice("   ") || humanize.ShouldVoice(strings.Repeat("a", humanize.MaxVoiceRunes+1)) {
		t.Error("vazio ou longo não vira áudio")
	}
}

func TestSpeechText(t *testing.T) {
	if got := humanize.SpeechText("Projeto *Pizzaria*\n\nQual o _público_?"); got != "Projeto Pizzaria. Qual o público?" {
		t.Errorf("SpeechText = %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/humanize/`
Expected: FAIL (`package humanize` não existe).

- [ ] **Step 3: Write minimal implementation**

```go
// Package humanize guarda as regras que deixam a resposta com cara de gente: balões curtos,
// tempo de digitação e quando responder em áudio. Não faz I/O; a aleatoriedade vem de fora.
package humanize

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxParts      = 3
	MaxVoiceRunes = 600
	MinTyping     = 1500 * time.Millisecond
	MaxTyping     = 7 * time.Second
)

var blankLine = regexp.MustCompile(`\n[ \t]*\n`)

// Split corta a resposta em balões nas linhas em branco; o que passar de MaxParts vai no último.
func Split(reply string) []string {
	var parts []string
	for _, p := range blankLine.Split(strings.TrimSpace(reply), -1) {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > MaxParts {
		parts = append(parts[:MaxParts-1], strings.Join(parts[MaxParts-1:], "\n\n"))
	}
	return parts
}

// TypingDelay é quanto tempo mostrar "digitando…" antes de um balão. r em [0, 1] dá a variação.
func TypingDelay(text string, r float64) time.Duration {
	base := 1200*time.Millisecond + time.Duration(utf8.RuneCountInString(text))*35*time.Millisecond
	d := time.Duration(float64(base) * (0.85 + 0.3*r))
	return min(max(d, MinTyping), MaxTyping)
}

// Gap é a pausa entre dois balões seguidos.
func Gap(r float64) time.Duration {
	return 600*time.Millisecond + time.Duration(r*float64(800*time.Millisecond))
}

// ShouldVoice diz se o texto cabe numa mensagem de voz.
func ShouldVoice(text string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(text))
	return n > 0 && n <= MaxVoiceRunes
}

var markup = strings.NewReplacer("*", "", "_", "", "~", "")

// SpeechText prepara o texto para o TTS: sem marcação do WhatsApp e com os balões viram frases.
func SpeechText(text string) string {
	parts := Split(markup.Replace(text))
	for i, p := range parts {
		p = strings.Join(strings.Fields(p), " ")
		if !strings.ContainsAny(p[len(p)-1:], ".!?") {
			p += "."
		}
		parts[i] = p
	}
	return strings.Join(parts, " ")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/humanize/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/humanize
git commit -m "feat: pacote humanize (balões, tempo de digitação, voz)"
```

---

### Task 2: TTS no OpenRouter (`Speak`)

**Files:**
- Modify: `internal/llm/llm.go`, `internal/llm/openrouter.go`, `internal/llm/fake.go`
- Test: `internal/llm/openrouter_test.go`

**Interfaces:**
- Produces: `type Speech struct { Audio []byte; Mime string; Usage Usage }`, `type Speaker interface { Speak(ctx context.Context, model, voice, text string) (Speech, error) }`, `(*OpenRouter).Speak`, `Fake.Speech []byte`, `Fake.SpeechErr error`, `Fake.Spoken []string`, `(*Fake).Speak`.

- [ ] **Step 1: Write the failing test** (append to `internal/llm/openrouter_test.go`; reuse its imports, add `io` if missing)

```go
func TestSpeakSendsTextAndReturnsAudio(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/speech" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("rota/cabeçalho: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("MP3DATA"))
	}))
	defer srv.Close()
	o := llm.NewOpenRouter(srv.URL, "k")
	sp, err := o.Speak(context.Background(), "tts-model", "nova", "Oi, tudo bem?")
	if err != nil {
		t.Fatal(err)
	}
	if string(sp.Audio) != "MP3DATA" || sp.Mime != "audio/mpeg" || sp.Usage.Model != "tts-model" {
		t.Errorf("Speak = %q %q %+v", sp.Audio, sp.Mime, sp.Usage)
	}
	if got["model"] != "tts-model" || got["voice"] != "nova" || got["input"] != "Oi, tudo bem?" || got["response_format"] != "mp3" {
		t.Errorf("corpo = %v", got)
	}
}

func TestSpeakHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"sem crédito"}}`, http.StatusPaymentRequired)
	}))
	defer srv.Close()
	if _, err := llm.NewOpenRouter(srv.URL, "k").Speak(context.Background(), "m", "v", "oi"); err == nil {
		t.Fatal("esperava erro")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run Speak`
Expected: FAIL (`o.Speak undefined`).

- [ ] **Step 3: Write minimal implementation**

In `internal/llm/llm.go`, after `Provider`:

```go
// Speech é o áudio gerado por TTS.
type Speech struct {
	Audio []byte
	Mime  string
	Usage Usage
}

// Speaker transforma texto em voz.
type Speaker interface {
	Speak(ctx context.Context, model, voice, text string) (Speech, error)
}
```

In `internal/llm/openrouter.go`, after `Transcribe`:

```go
// Speak gera voz pelo endpoint de TTS compatível com OpenAI (/audio/speech), em MP3.
func (o *OpenRouter) Speak(ctx context.Context, model, voice, text string) (Speech, error) {
	payload, err := json.Marshal(map[string]string{"model": model, "voice": voice, "input": text, "response_format": "mp3"})
	if err != nil {
		return Speech{}, fmt.Errorf("llm: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/audio/speech", bytes.NewReader(payload))
	if err != nil {
		return Speech{}, fmt.Errorf("llm: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	res, err := o.HTTP.Do(req)
	if err != nil {
		return Speech{}, fmt.Errorf("%w: tts: %v", ErrTransient, err)
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		err := fmt.Errorf("llm: tts status %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			err = fmt.Errorf("%w: %v", ErrTransient, err)
		}
		return Speech{}, err
	}
	audio, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return Speech{}, fmt.Errorf("llm: tts: %w", err)
	}
	mime := strings.TrimSpace(strings.Split(res.Header.Get("Content-Type"), ";")[0])
	if !strings.HasPrefix(mime, "audio/") {
		mime = "audio/mpeg"
	}
	return Speech{Audio: audio, Mime: mime, Usage: Usage{Model: model}}, nil
}
```

In `internal/llm/fake.go`, add fields to `Fake` (after `Transcript`):

```go
	Speech     []byte   // devolvido por Speak
	SpeechErr  error    // se definido, Speak falha
	Spoken     []string // textos pedidos ao Speak
```

and the method:

```go
func (f *Fake) Speak(_ context.Context, model, _ string, text string) (Speech, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Spoken = append(f.Spoken, text)
	if f.SpeechErr != nil {
		return Speech{}, f.SpeechErr
	}
	return Speech{Audio: f.Speech, Mime: "audio/mpeg", Usage: Usage{Model: model}}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/llm/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/llm
git commit -m "feat: TTS pelo OpenRouter (Speak)"
```

---

### Task 3: WhatsApp — visto, presença e voz

**Files:**
- Modify: `internal/whatsapp/whatsapp.go`, `internal/whatsapp/waha/waha.go`
- Modify (fakes, para compilar): `internal/engine/engine_test.go`, `internal/worker/worker_test.go`
- Test: `internal/whatsapp/waha/waha_test.go`

**Interfaces:**
- Produces: `type Presence string`; `PresenceTyping`, `PresenceRecording`, `PresencePaused`; `Provider.MarkSeen(ctx, chatID string) error`, `Provider.SetPresence(ctx, chatID string, p Presence) error`, `Provider.SendVoice(ctx, chatID string, audio []byte, mime string) error`. Engine fake ganha `events []string` e `(*fakeWA).log() []string`.

- [ ] **Step 1: Write the failing test** (append to `internal/whatsapp/waha/waha_test.go`; add imports `encoding/base64`, `encoding/json`, `net/http`, `net/http/httptest` if missing)

```go
func TestHumanEndpoints(t *testing.T) {
	type call struct {
		Path string
		Body map[string]any
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			t.Errorf("sem X-Api-Key em %s", r.URL.Path)
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		calls = append(calls, call{r.URL.Path, b})
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := waha.New(srv.URL, "key", "default")
	ctx := context.Background()
	if err := c.MarkSeen(ctx, "5511@c.us"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPresence(ctx, "5511@c.us", whatsapp.PresenceRecording); err != nil {
		t.Fatal(err)
	}
	if err := c.SendVoice(ctx, "5511@c.us", []byte("MP3"), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("chamadas = %d", len(calls))
	}
	if calls[0].Path != "/api/sendSeen" || calls[0].Body["chatId"] != "5511@c.us" || calls[0].Body["session"] != "default" {
		t.Errorf("sendSeen = %+v", calls[0])
	}
	if calls[1].Path != "/api/default/presence" || calls[1].Body["presence"] != "recording" || calls[1].Body["chatId"] != "5511@c.us" {
		t.Errorf("presence = %+v", calls[1])
	}
	file, _ := calls[2].Body["file"].(map[string]any)
	if calls[2].Path != "/api/sendVoice" || calls[2].Body["convert"] != true ||
		file["mimetype"] != "audio/mpeg" || file["data"] != base64.StdEncoding.EncodeToString([]byte("MP3")) {
		t.Errorf("sendVoice = %+v", calls[2])
	}
}

func TestHumanEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := waha.New(srv.URL, "key", "").SendVoice(context.Background(), "x@c.us", []byte("a"), "audio/mpeg"); err == nil {
		t.Fatal("esperava erro")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/whatsapp/...`
Expected: FAIL (`c.MarkSeen undefined`).

- [ ] **Step 3: Write minimal implementation**

`internal/whatsapp/whatsapp.go` — fix the package comment (adapter is WAHA now) and add before `Provider`:

```go
// Presence é o que o cliente vê no topo da conversa.
type Presence string

const (
	PresenceTyping    Presence = "typing"
	PresenceRecording Presence = "recording"
	PresencePaused    Presence = "paused"
)
```

Add to `Provider`:

```go
	// MarkSeen marca as mensagens do chat como lidas (visto azul).
	MarkSeen(ctx context.Context, chatID string) error
	// SetPresence mostra "digitando…"/"gravando áudio…" ou limpa (paused).
	SetPresence(ctx context.Context, chatID string, p Presence) error
	// SendVoice envia mensagem de voz; o provedor converte o áudio para o formato do WhatsApp.
	SendVoice(ctx context.Context, chatID string, audio []byte, mime string) error
```

`internal/whatsapp/waha/waha.go` — replace `SendText` body with a shared `post` and add the three methods (add `encoding/base64` import):

```go
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
```

Engine fake (`internal/engine/engine_test.go`): add field `events []string` and `voices []sent` to `fakeWA`, and:

```go
func (f *fakeWA) MarkSeen(_ context.Context, chatID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "seen")
	return nil
}
func (f *fakeWA) SetPresence(_ context.Context, _ string, p whatsapp.Presence) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, string(p))
	return nil
}
func (f *fakeWA) SendVoice(_ context.Context, chatID string, audio []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("whatsapp fora do ar")
	}
	f.events = append(f.events, "voice")
	f.voices = append(f.voices, sent{chatID, string(audio)})
	return nil
}

// log devolve visto, presenças, voz e textos na ordem em que aconteceram.
func (f *fakeWA) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}
```

and in `SendText`, after appending to `f.sent`, add `f.events = append(f.events, "text:"+text)`.

Worker fake (`internal/worker/worker_test.go`): add no-op methods to its WA fake type (same receiver as its `ChatIDForPhone`):

```go
func (f *fakeWA) MarkSeen(context.Context, string) error                       { return nil }
func (f *fakeWA) SetPresence(context.Context, string, whatsapp.Presence) error { return nil }
func (f *fakeWA) SendVoice(context.Context, string, []byte, string) error      { return nil }
```

(Use the fake's real type name in that file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go vet ./... && go test ./internal/whatsapp/... ./internal/worker/ ./internal/engine/`
Expected: PASS (engine/worker só precisam compilar e continuar verdes).

- [ ] **Step 5: Commit**

```bash
git add internal/whatsapp internal/engine/engine_test.go internal/worker/worker_test.go
git commit -m "feat: WhatsApp com visto, presença e mensagem de voz (WAHA)"
```

---

### Task 4: Store — atraso de job e lote de mensagens

**Files:**
- Create: `internal/db/migrations/0005_replied_through.sql`
- Modify: `internal/store/queue.go`, `internal/store/conversation.go`
- Test: `internal/store/humanize_test.go`

**Interfaces:**
- Produces: `JobSpec.Delay time.Duration`; `Message.ID int64`; `(*Store).AddInbound(ctx, convID int64, kind, body string) (int64, error)`; `(*Store).LatestInboundID(ctx, convID int64) (int64, error)`; `(*Store).PendingInbound(ctx, convID, upTo int64, limit int) ([]Message, error)` (mais antigas primeiro, só `in`, `id > replied_through AND id <= upTo`); `(*Store).MarkReplied(ctx, convID, upTo int64) error`.

- [ ] **Step 1: Write the failing test** — look at `internal/store/conversation_test.go` for how it builds a store and a conversation (helper names), and reuse the same setup in this new file. Test body:

```go
package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestPendingInboundAndMarkReplied(t *testing.T) {
	st, convID := newConversation(t) // helper existente ou criado aqui com CreateOrganization/CreateMember/LinkPhone/GetOrCreateConversation
	a, err := st.AddInbound(ctx, convID, "text", "oi")
	must(t, err)
	must(t, st.AddMessage(ctx, convID, "out", "text", "olá!"))
	b, err := st.AddInbound(ctx, convID, "audio", "quero anunciar")
	must(t, err)
	c, err := st.AddInbound(ctx, convID, "text", "pizza")
	must(t, err)

	if latest, err := st.LatestInboundID(ctx, convID); err != nil || latest != c {
		t.Fatalf("LatestInboundID = %d, %v; want %d", latest, err, c)
	}
	got, err := st.PendingInbound(ctx, convID, b, 10)
	must(t, err)
	if len(got) != 2 || got[0].ID != a || got[1].ID != b || got[1].Kind != "audio" {
		t.Fatalf("PendingInbound até b = %+v", got)
	}
	must(t, st.MarkReplied(ctx, convID, b))
	must(t, st.MarkReplied(ctx, convID, a)) // não volta para trás
	got, err = st.PendingInbound(ctx, convID, c, 10)
	must(t, err)
	if len(got) != 1 || got[0].ID != c || got[0].Body != "pizza" {
		t.Fatalf("depois de MarkReplied = %+v", got)
	}
	if got, _ := st.PendingInbound(ctx, convID, c, 1); len(got) != 1 {
		t.Fatalf("limite 1 = %+v", got)
	}
}

func TestJobDelay(t *testing.T) {
	st, _ := newConversation(t)
	must(t, st.Enqueue(ctx, store.JobSpec{Kind: "x", Payload: map[string]int{}, Delay: time.Hour}))
	if _, ok, err := st.ClaimJob(ctx); err != nil || ok {
		t.Fatalf("job com atraso não pode ser pego agora: ok=%v err=%v", ok, err)
	}
	must(t, st.Enqueue(ctx, store.JobSpec{Kind: "y", Payload: map[string]int{}}))
	if j, ok, err := st.ClaimJob(ctx); err != nil || !ok || j.Kind != "y" {
		t.Fatalf("job sem atraso: %+v ok=%v err=%v", j, ok, err)
	}
}
```

If `newConversation`/`must`/`ctx` don't exist in `store_test`, add them in this file:

```go
func newConversation(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st := store.New(testdb.New(t))
	org, err := st.CreateOrganization(ctx, "Org", "restaurante", 100, []string{"OUTCOME_ENGAGEMENT"})
	must(t, err)
	mem, err := st.CreateMember(ctx, org, "Ana", "owner")
	must(t, err)
	must(t, st.LinkPhone(ctx, "waha", "5511988887777", mem))
	ident, err := st.FindIdentity(ctx, "waha", "5511988887777")
	must(t, err)
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	return st, conv.ID
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'PendingInbound|JobDelay'`
Expected: FAIL (`st.AddInbound undefined`).

- [ ] **Step 3: Write minimal implementation**

`internal/db/migrations/0005_replied_through.sql`:

```sql
-- Atendente humana: até qual mensagem de entrada a conversa já foi respondida (debounce em lote).
ALTER TABLE conversations ADD COLUMN replied_through bigint NOT NULL DEFAULT 0;
```

`internal/store/queue.go` — `JobSpec` gains `Delay time.Duration // espera antes de o job poder rodar` (import `time`), and the insert in `enqueue` becomes:

```go
	_, err = q.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload, max_attempts, run_after)
		VALUES ($1, $2, $3, now() + $4 * interval '1 millisecond')`, j.Kind, string(raw), max, j.Delay.Milliseconds())
```

`internal/store/conversation.go` — `Message` gains `ID int64` as first field; `RecentMessages` selects and scans `id` too:

```go
	rows, err := s.pool.Query(ctx, `SELECT id, direction, kind, body FROM (
		SELECT id, direction, kind, body FROM messages WHERE conversation_id = $1 ORDER BY id DESC LIMIT $2
	) t ORDER BY id`, convID, limit)
	...
		err := r.Scan(&m.ID, &m.Direction, &m.Kind, &m.Body)
```

and new methods after `AddMessage`:

```go
// AddInbound grava a mensagem do cliente e devolve o id (âncora do debounce).
func (s *Store) AddInbound(ctx context.Context, convID int64, kind, body string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, direction, kind, body)
		VALUES ($1, 'in', $2, $3) RETURNING id`, convID, kind, body).Scan(&id)
	return id, err
}

// LatestInboundID é o id da mensagem de entrada mais nova da conversa (0 se não houver).
func (s *Store) LatestInboundID(ctx context.Context, convID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM messages WHERE conversation_id = $1 AND direction = 'in'`, convID).Scan(&id)
	return id, err
}

// PendingInbound devolve as mensagens de entrada ainda sem resposta até upTo, as limit mais novas,
// da mais antiga para a mais nova.
func (s *Store) PendingInbound(ctx context.Context, convID, upTo int64, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, direction, kind, body FROM (
		SELECT m.id, m.direction, m.kind, m.body FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND m.direction = 'in' AND m.id > c.replied_through AND m.id <= $2
		ORDER BY m.id DESC LIMIT $3
	) t ORDER BY id`, convID, upTo, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var m Message
		err := r.Scan(&m.ID, &m.Direction, &m.Kind, &m.Body)
		return m, err
	})
}

// MarkReplied avança a marca de respondido; nunca volta para trás.
func (s *Store) MarkReplied(ctx context.Context, convID, upTo int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET replied_through = greatest(replied_through, $2) WHERE id = $1`, convID, upTo)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ ./internal/db/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/0005_replied_through.sql internal/store
git commit -m "feat: store com atraso de job e lote de mensagens sem resposta"
```

---

### Task 5: Engine — envio humano (balões, digitando, voz)

**Files:**
- Create: `internal/engine/humanize.go`
- Modify: `internal/engine/engine.go` (campos, `send`, novo `speak`), `internal/engine/jobs.go` (`JobSend`)
- Test: `internal/engine/humanize_test.go`

**Interfaces:**
- Consumes: Task 1 (`humanize.*`), Task 2 (`llm.Speaker`, `Fake.Speak`), Task 3 (`Presence`, `MarkSeen`, `SetPresence`, `SendVoice`, `fakeWA.log`), Task 4 (`JobSpec.Delay`).
- Produces: `Engine` fields `Humanize bool`, `Debounce time.Duration`, `Voice llm.Speaker`, `TTSModel string`, `TTSVoice string`, `Sleep func(context.Context, time.Duration) error`, `Rand func() float64`; `SendJob` fields `OrgID int64 json:"org_id,omitempty"`, `Human bool json:"human,omitempty"`, `Voice bool json:"voice,omitempty"`, `Next []string json:"next,omitempty"`; `(*Engine).speak(ctx, ident store.Identity, conv store.Conversation, text string) error`; context helpers `withVoice(ctx) context.Context`, `voiceWanted(ctx) bool`; test helpers `newHumanHarness(t)`, `(*harness).drain()`.

- [ ] **Step 1: Write the failing test** (`internal/engine/humanize_test.go`)

```go
package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// newHumanHarness liga o modo humano sem esperas reais; tts guarda o TTS falso.
func newHumanHarness(t *testing.T) (*harness, *llm.Fake) {
	h := newHarness(t, "owner")
	tts := &llm.Fake{Speech: []byte("MP3")}
	h.eng.Humanize = true
	h.eng.Debounce = 0
	h.eng.Voice = tts
	h.eng.TTSModel = "tts"
	h.eng.TTSVoice = "nova"
	h.eng.Sleep = func(context.Context, time.Duration) error { return nil }
	h.eng.Rand = func() float64 { return 0.5 }
	return h, tts
}

// drain roda os jobs pendentes (inclusive os agendados para depois) até a fila esvaziar.
func (h *harness) drain() {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		h.must(func() error { _, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE status = 'pending'`); return err }())
		job, ok, err := h.st.ClaimJob(ctx)
		h.must(err)
		if !ok {
			return
		}
		failed, err := h.st.FinishJob(ctx, job.ID, h.eng.HandleJob(ctx, job))
		h.must(err)
		if failed {
			h.must(h.eng.OnJobFailed(ctx, job))
		}
	}
	h.t.Fatal("fila não esvaziou")
}

func TestHumanReplyInBubblesWithTyping(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"Pizza grande"},"reply":"Show, Ana!\n\nPra quem é a pizza e em qual cidade?"}`}
	h.say("quero anunciar pizza")
	if h.wa.count() != 0 {
		t.Fatal("nada sai antes do debounce")
	}
	h.drain()
	want := []string{"seen", "typing", "text:Show, Ana!", "paused", "typing", "text:Pra quem é a pizza e em qual cidade?", "paused"}
	if got := h.wa.log(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("eventos = %q\nwant %q", got, want)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out'`); n != 2 {
		t.Fatalf("mensagens de saída = %d; want 2", n)
	}
}

func TestAudioGetsVoiceReply(t *testing.T) {
	h, tts := newHumanHarness(t)
	h.att.Transcript = "quero anunciar pizza"
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"Pizza"},"reply":"Show! Em qual cidade?"}`}
	h.sayAudio()
	h.drain()
	want := []string{"seen", "recording", "voice", "paused"}
	if got := h.wa.log(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("eventos = %q; want %q", got, want)
	}
	if len(tts.Spoken) != 1 || tts.Spoken[0] != "Show! Em qual cidade?" {
		t.Fatalf("TTS recebeu %q", tts.Spoken)
	}
	if !strings.Contains(h.att.Requests[0].Messages[1].Content, "Formato da resposta: áudio") {
		t.Error("Atendente deveria saber que a resposta vira áudio")
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out' AND kind = 'voice'`); n != 1 {
		t.Fatalf("voz registrada = %d", n)
	}
}

func TestVoiceFallsBackToText(t *testing.T) {
	h, tts := newHumanHarness(t)
	tts.SpeechErr = errors.New("tts fora")
	h.att.Transcript = "oi"
	h.att.Responses = []string{`{"intent":"question","reply":"Oi! Tudo certo?"}`}
	h.sayAudio()
	h.drain()
	want := []string{"seen", "recording", "paused", "typing", "text:Oi! Tudo certo?", "paused"}
	if got := h.wa.log(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("eventos = %q; want %q", got, want)
	}
}

func TestFixedMessageNeverVoice(t *testing.T) {
	h, tts := newHumanHarness(t)
	h.att.Transcript = "oi"
	h.att.Errors = []error{errors.New("modelo fora")}
	h.sayAudio()
	h.drain()
	if len(tts.Spoken) != 0 || !strings.Contains(h.wa.last().Text, "problema técnico") {
		t.Fatalf("mensagem fixa foi para voz: spoken=%q last=%q", tts.Spoken, h.wa.last().Text)
	}
}

func TestOperatorNoteHasNoPresence(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"human","reply":"ok"}`}
	h.say("quero falar com uma pessoa")
	h.drain()
	for _, ev := range h.wa.log() {
		if ev == "typing" && len(h.wa.to(operatorChat)) == 0 {
			t.Fatal("aviso ao operador não saiu")
		}
	}
	if len(h.wa.to(operatorChat)) != 1 || h.conv().State != store.StateNeedsHuman {
		t.Fatalf("operador = %q estado = %s", h.wa.to(operatorChat), h.conv().State)
	}
}
```

Also add to `engine_test.go` next to `say`:

```go
func (h *harness) sayAudio() {
	h.t.Helper()
	h.seq++
	h.wa.media, h.wa.mime = []byte("OGG"), "audio/ogg"
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: fmt.Sprintf("m%d", h.seq), ChatID: chatID, Phone: phone, Kind: whatsapp.KindAudio}))
}
```

(If `resilience_test.go` already has an equivalent audio helper, reuse it instead and use its name in the tests above.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run 'Human|Voice|Fixed|Operator'`
Expected: FAIL (`h.eng.Humanize undefined`).

- [ ] **Step 3: Write minimal implementation**

`internal/engine/engine.go` — add fields to `Engine` (after `SignupDailyLimit`):

```go

	// Atendente humana: debounce, visto, presença, balões e voz. Sem Voice, só texto.
	Humanize bool
	Debounce time.Duration
	Voice    llm.Speaker
	TTSModel string
	TTSVoice string
	Sleep    func(context.Context, time.Duration) error // espera; trocada nos testes
	Rand     func() float64                             // variação dos tempos; trocada nos testes
```

`SendJob` gains:

```go
	OrgID int64    `json:"org_id,omitempty"`
	Human bool     `json:"human,omitempty"` // com visto/presença e tempo de digitação
	Voice bool     `json:"voice,omitempty"` // tenta mandar como mensagem de voz
	Next  []string `json:"next,omitempty"`  // balões seguintes, enviados em jobs encadeados
```

Replace `send`:

```go
// send manda uma mensagem fixa. No modo humano ela vai em balões com "digitando…", nunca em voz.
func (e *Engine) send(ctx context.Context, conv store.Conversation, text string) error {
	if e.Humanize {
		return e.Store.Enqueue(ctx, humanJob(0, conv, text, false))
	}
	if err := e.WA.SendText(ctx, conv.ChatID, text); err != nil {
		return err
	}
	return e.Store.AddMessage(ctx, conv.ID, "out", "text", text)
}

// speak manda a resposta da Atendente; vira voz quando o cliente falou por áudio.
func (e *Engine) speak(ctx context.Context, ident store.Identity, conv store.Conversation, text string) error {
	if !e.Humanize {
		return e.send(ctx, conv, text)
	}
	voice := e.Voice != nil && voiceWanted(ctx) && humanize.ShouldVoice(text)
	return e.Store.Enqueue(ctx, humanJob(ident.OrgID, conv, text, voice))
}
```

In `converse` (and `requestProposal`), replace every `e.send(ctx, conv, turn.Reply)` with `e.speak(ctx, ident, conv, turn.Reply)`. `requestProposal` gets `ident store.Identity` as second parameter and calls `e.speak(ctx, ident, conv, reply)`; update its two call sites in `converse`. The `IntentReject` branch: `if reply == turn.Reply { return e.speak(...) }` else `e.send(ctx, conv, reply)`. Fixed messages (`msgOverBudget`, `listText`, etc.) keep `e.send`.

`internal/engine/humanize.go`:

```go
package engine

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/humanize"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const ttsTimeout = 20 * time.Second

type voiceKey struct{}

// withVoice marca que o cliente falou por áudio neste lote.
func withVoice(ctx context.Context) context.Context { return context.WithValue(ctx, voiceKey{}, true) }

func voiceWanted(ctx context.Context) bool { v, _ := ctx.Value(voiceKey{}).(bool); return v }

// humanJob monta o primeiro envio humano; os outros balões seguem em Next.
func humanJob(orgID int64, conv store.Conversation, text string, voice bool) store.JobSpec {
	s := SendJob{OrgID: orgID, ConversationID: conv.ID, ChatID: conv.ChatID, Text: text, Human: true, Voice: voice}
	if !voice {
		s = splitJob(s)
	}
	return store.JobSpec{Kind: JobSend, Payload: s, MaxAttempts: sendAttempts}
}

func splitJob(s SendJob) SendJob {
	parts := humanize.Split(s.Text)
	if len(parts) == 0 {
		parts = []string{s.Text}
	}
	s.Voice, s.Text, s.Next = false, parts[0], parts[1:]
	return s
}

// sendHuman envia um balão com presença; se houver mais, agenda o próximo depois de uma pausa.
func (e *Engine) sendHuman(ctx context.Context, s SendJob) error {
	if s.Voice {
		ok, err := e.sendVoice(ctx, s)
		if err != nil || ok {
			return err
		}
		s = splitJob(s) // sem voz: mesmo texto em balões
	}
	e.presence(ctx, s.ChatID, whatsapp.PresenceTyping)
	if err := e.sleep(ctx, humanize.TypingDelay(s.Text, e.rand())); err != nil {
		return err
	}
	if err := e.WA.SendText(ctx, s.ChatID, s.Text); err != nil {
		return err
	}
	e.presence(ctx, s.ChatID, whatsapp.PresencePaused)
	if err := e.Store.AddMessage(ctx, s.ConversationID, "out", "text", s.Text); err != nil {
		return err
	}
	if len(s.Next) == 0 {
		return nil
	}
	next := s
	next.Text, next.Next = s.Next[0], s.Next[1:]
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobSend, Payload: next, MaxAttempts: sendAttempts, Delay: humanize.Gap(e.rand())})
}

// sendVoice devolve ok=false quando a voz não pôde ser gerada; o chamador manda em texto.
func (e *Engine) sendVoice(ctx context.Context, s SendJob) (bool, error) {
	if e.Voice == nil {
		return false, nil
	}
	e.presence(ctx, s.ChatID, whatsapp.PresenceRecording)
	start := time.Now()
	tctx, cancel := context.WithTimeout(ctx, ttsTimeout)
	sp, err := e.Voice.Speak(tctx, e.TTSModel, e.TTSVoice, humanize.SpeechText(s.Text))
	cancel()
	e.recordUsage(ctx, s.OrgID, "tts", sp.Usage)
	if err != nil {
		e.Log.Error("tts falhou, indo em texto", "conversation_id", s.ConversationID, "erro", err)
		e.presence(ctx, s.ChatID, whatsapp.PresencePaused)
		return false, nil
	}
	if wait := humanize.TypingDelay(s.Text, e.rand()) - time.Since(start); wait > 0 {
		if err := e.sleep(ctx, wait); err != nil {
			return false, err
		}
	}
	if err := e.WA.SendVoice(ctx, s.ChatID, sp.Audio, sp.Mime); err != nil {
		return false, err
	}
	e.presence(ctx, s.ChatID, whatsapp.PresencePaused)
	return true, e.Store.AddMessage(ctx, s.ConversationID, "out", "voice", s.Text)
}

// presence e markSeen são cosméticos: falha só vai para o log.
func (e *Engine) presence(ctx context.Context, chatID string, p whatsapp.Presence) {
	if err := e.WA.SetPresence(ctx, chatID, p); err != nil {
		e.Log.Warn("falha na presença", "presenca", p, "erro", err)
	}
}

func (e *Engine) markSeen(ctx context.Context, chatID string) {
	if err := e.WA.MarkSeen(ctx, chatID); err != nil {
		e.Log.Warn("falha ao marcar visto", "erro", err)
	}
}

func (e *Engine) sleep(ctx context.Context, d time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (e *Engine) rand() float64 {
	if e.Rand != nil {
		return e.Rand()
	}
	return rand.Float64()
}
```

`internal/engine/jobs.go`, `case JobSend:` after unmarshal:

```go
		if s.Human {
			return e.sendHuman(ctx, s)
		}
```

Note: `recordUsage` with `OrgID == 0` would fail on FK; guard in `sendVoice`: call `e.recordUsage` only `if s.OrgID != 0`.

These tests also need Task 6's debounce (`HandleInbound` enqueuing `conversation.reply`, `MarkSeen`). Implement Task 6 Steps 3 before running Task 5 Step 4, or temporarily run only after Task 6. **Execution order: do Task 5 Step 3, then Task 6 Steps 1–3, then run both test sets together.**

- [ ] **Step 4: Run test to verify it passes** (after Task 6 Step 3)

Run: `go test ./internal/engine/`
Expected: PASS (novos e antigos).

- [ ] **Step 5: Commit** (junto com a Task 6)

---

### Task 6: Engine — debounce e resposta em lote

**Files:**
- Modify: `internal/engine/engine.go` (`HandleInbound`, `converse`, `history`), `internal/engine/jobs.go`, `internal/engine/humanize.go`
- Modify: `internal/agent/attendant/attendant.go` (`Input.Voice`, `userPrompt`), `internal/agent/attendant/attendant_test.go`
- Test: `internal/engine/humanize_test.go`

**Interfaces:**
- Consumes: Task 4 (`AddInbound`, `LatestInboundID`, `PendingInbound`, `MarkReplied`, `Message.ID`), Task 5.
- Produces: `JobReply = "conversation.reply"`, `type ReplyJob struct { ConversationID int64 json:"conversation_id"; MessageID int64 json:"message_id" }`, `(*Engine).dispatch(ctx, ident, conv, text, last string, batch []int64) error`, `attendant.Input.Voice bool`.

- [ ] **Step 1: Write the failing test** (append to `internal/engine/humanize_test.go`)

```go
func TestBurstGetsOneReply(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{turnOffer}
	h.say("oi")
	h.say("quero anunciar")
	h.say("pizza grande")
	h.drain()
	if h.att.Calls() != 1 {
		t.Fatalf("Atendente chamada %d vezes; want 1", h.att.Calls())
	}
	user := h.att.Requests[0].Messages[1].Content
	if !strings.Contains(user, "<cliente>oi\nquero anunciar\npizza grande</cliente>") {
		t.Fatalf("lote não chegou junto:\n%s", user)
	}
	if strings.Contains(user, "Histórico recente") {
		t.Error("mensagens do lote não podem aparecer também no histórico")
	}
}

func TestBatchIgnoresAlreadyAnswered(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{turnOffer, `{"intent":"provide_info","fields":{"location":"Campinas"},"reply":"Anotado!"}`}
	h.say("quero anunciar pizza")
	// roda só o reply: os balões ficam na fila, sem registrar a saída ainda
	h.runKind("conversation.reply")
	h.say("em Campinas")
	h.drain()
	if h.att.Calls() != 2 {
		t.Fatalf("chamadas = %d", h.att.Calls())
	}
	user := h.att.Requests[1].Messages[1].Content
	if !strings.Contains(user, "Mensagem atual do cliente:\n<cliente>em Campinas</cliente>") {
		t.Fatalf("segundo lote errado:\n%s", user)
	}
	if !strings.Contains(user, "Cliente: <cliente>quero anunciar pizza</cliente>") {
		t.Fatalf("histórico perdeu a primeira mensagem:\n%s", user)
	}
}

func TestNeedsHumanMarksReplied(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"human","reply":"ok"}`, `{"intent":"question","reply":"Oi de novo!"}`}
	h.say("quero uma pessoa")
	h.drain()
	h.say("alô?") // durante o atendimento humano: fica sem resposta automática
	h.drain()
	ident, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	h.must(h.st.ResetConversation(ctx, ident.ID))
	h.say("voltei")
	h.drain()
	if user := h.att.Requests[1].Messages[1].Content; !strings.Contains(user, "Mensagem atual do cliente:\n<cliente>voltei</cliente>") {
		t.Fatalf("lote depois do humano:\n%s", user)
	}
}

func TestApprovalUsesLastMessageOfBatch(t *testing.T) {
	h, _ := newHumanHarness(t)
	code := h.reachApproval() // helper de approval_test.go que deixa a conversa em awaiting_approval e devolve o código
	h.say("gostei")
	h.say("SIM " + code)
	h.drain()
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "Campanha aprovada") {
		t.Fatalf("estado = %s último = %q", h.conv().State, h.wa.last().Text)
	}
}

func TestProposalSummaryStaysTextAfterAudio(t *testing.T) {
	h, tts := newHumanHarness(t)
	code := h.reachApproval()
	if code == "" || len(h.wa.voices) != 0 {
		t.Fatal("resumo com código não pode ir em voz")
	}
	_ = tts
}

func TestHumanizeOffKeepsDirectSend(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	if h.wa.count() != 1 || h.pendingJobs("conversation.reply") != 0 {
		t.Fatal("sem HUMANIZE a resposta sai na hora, como antes")
	}
}
```

Helpers to add in `humanize_test.go`:

```go
// runKind roda só os jobs pendentes de um tipo.
func (h *harness) runKind(kind string) {
	h.t.Helper()
	h.must(func() error {
		_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = CASE WHEN kind = $1 THEN now() ELSE now() + interval '1 hour' END WHERE status = 'pending'`, kind)
		return err
	}())
	for {
		job, ok, err := h.st.ClaimJob(ctx)
		h.must(err)
		if !ok {
			return
		}
		_, err = h.st.FinishJob(ctx, job.ID, h.eng.HandleJob(ctx, job))
		h.must(err)
	}
}
```

For `reachApproval`: read `approval_test.go`; it already has a flow that brings the conversation to `awaiting_approval` and extracts the code from the summary (lines ~60-80). Extract that flow into `func (h *harness) reachApproval() string` in `approval_test.go` (calling `h.drain()` when `h.eng.Humanize` is true, and running the propose job the way the existing test does), make the existing test use it, and use it here. For `TestProposalSummaryStaysTextAfterAudio`, make `reachApproval` start with `h.sayAudio()` when a `viaAudio bool` parameter is true: signature `reachApproval(viaAudio bool) string`; update the calls above to `h.reachApproval(false)` and `h.reachApproval(true)`.

Attendant test (append to `internal/agent/attendant/attendant_test.go`, reuse its fake setup):

```go
func TestPromptSaysVoiceFormat(t *testing.T) {
	if p := userPromptForTest(attendant.Input{Message: "oi", Voice: true}); !strings.Contains(p, "Formato da resposta: áudio") {
		t.Fatalf("prompt sem formato de áudio:\n%s", p)
	}
	if p := userPromptForTest(attendant.Input{Message: "oi"}); !strings.Contains(p, "Formato da resposta: texto") {
		t.Fatalf("prompt sem formato de texto:\n%s", p)
	}
}
```

where `userPromptForTest` runs `Agent.Reply` with an `llm.Fake` returning `{"intent":"other","reply":"ok"}` and returns `fake.Requests[0].Messages[1].Content`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ ./internal/agent/attendant/`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

`attendant.Input` gains `Voice bool // a resposta vai virar mensagem de voz`. In `userPrompt`, before "Histórico recente":

```go
	if in.Voice {
		b.WriteString("Formato da resposta: áudio (fale como numa mensagem de voz: sem listas, sem emoji, sem asterisco, uma parte só)\n")
	} else {
		b.WriteString("Formato da resposta: texto\n")
	}
```

`internal/engine/engine.go`:

1. Constants: add `JobReply = "conversation.reply"`, `maxBatch = 10`, `defaultDebounce = 4 * time.Second`.
2. `ReplyJob` struct (see Interfaces).
3. In `HandleInbound`, replace from `if err := e.Store.AddMessage(ctx, conv.ID, "in", string(in.Kind), text); err != nil {` to the end of the function with:

```go
	if e.Humanize {
		id, err := e.Store.AddInbound(ctx, conv.ID, string(in.Kind), text)
		if err != nil {
			return err
		}
		if conv.State == store.StateNeedsHuman {
			return e.Store.MarkReplied(ctx, conv.ID, id) // humano atende; o bot não junta isso depois
		}
		// Debounce: quem responde é o job da mensagem mais nova, depois de um tempo sem mensagens.
		return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobReply, Payload: ReplyJob{ConversationID: conv.ID, MessageID: id}, Delay: e.debounce()})
	}
	if err := e.Store.AddMessage(ctx, conv.ID, "in", string(in.Kind), text); err != nil {
		return err
	}
	return e.dispatch(ctx, ident, conv, text, text, nil)
}

// dispatch decide a resposta pelo estado da conversa. last é a última mensagem (aprovação);
// batch são os ids do lote, fora do histórico (nil = só a última mensagem gravada).
func (e *Engine) dispatch(ctx context.Context, ident store.Identity, conv store.Conversation, text, last string, batch []int64) error {
	switch conv.State {
	case store.StateNeedsHuman:
		return nil
	case store.StateAwaitingProposal:
		return e.send(ctx, conv, msgBusy)
	case store.StateAwaitingApproval:
		if code, ok := campaign.ParseApproval(last); ok {
			return e.approve(ctx, ident, conv, code)
		}
	}
	return e.converse(ctx, ident, conv, text, batch)
}

func (e *Engine) debounce() time.Duration {
	if e.Debounce < 0 {
		return 0
	}
	return e.Debounce
}
```

(Config passes the default 4 s; tests set `Debounce = 0`.)

4. `converse(ctx, ident, conv, text string, batch []int64)`: call `e.history(ctx, conv.ID, batch)` and set `Voice: e.Voice != nil && voiceWanted(ctx)` in `attendant.Input`.

5. `history`:

```go
// history devolve as últimas mensagens sem as do lote atual (batch nil = sem a última gravada).
func (e *Engine) history(ctx context.Context, convID int64, batch []int64) ([]attendant.Turn, error) {
	msgs, err := e.Store.RecentMessages(ctx, convID, historySize+max(len(batch), 1))
	if err != nil {
		return nil, err
	}
	if batch == nil && len(msgs) > 0 {
		msgs = msgs[:len(msgs)-1] // a última é a mensagem atual
	}
	skip := make(map[int64]bool, len(batch))
	for _, id := range batch {
		skip[id] = true
	}
	turns := make([]attendant.Turn, 0, len(msgs))
	for _, m := range msgs {
		if !skip[m.ID] {
			turns = append(turns, attendant.Turn{FromClient: m.Direction == "in", Text: m.Body})
		}
	}
	if len(turns) > historySize {
		turns = turns[len(turns)-historySize:]
	}
	return turns, nil
}
```

6. `internal/engine/humanize.go` — the reply job:

```go
// reply responde o lote de mensagens ainda sem resposta, se esta for a mensagem mais nova.
func (e *Engine) reply(ctx context.Context, r ReplyJob) error {
	unlock, err := e.Store.LockConversation(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	latest, err := e.Store.LatestInboundID(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	if latest != r.MessageID {
		return nil // chegou mensagem nova: o job dela responde tudo
	}
	msgs, err := e.Store.PendingInbound(ctx, r.ConversationID, r.MessageID, maxBatch)
	if err != nil || len(msgs) == 0 {
		return err
	}
	conv, err := e.Store.GetConversation(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	if ident.Status != "blocked" && conv.State != store.StateNeedsHuman {
		e.markSeen(ctx, conv.ChatID)
		texts := make([]string, len(msgs))
		ids := make([]int64, len(msgs))
		for i, m := range msgs {
			texts[i], ids[i] = m.Body, m.ID
		}
		last := msgs[len(msgs)-1]
		if last.Kind == string(whatsapp.KindAudio) {
			ctx = withVoice(ctx)
		}
		if err := e.dispatch(ctx, ident, conv, strings.Join(texts, "\n"), last.Body, ids); err != nil {
			return err
		}
	}
	return e.Store.MarkReplied(ctx, conv.ID, r.MessageID)
}
```

(add `strings` import). `jobs.go` `HandleJob`:

```go
	case JobReply:
		var r ReplyJob
		if err := json.Unmarshal(job.Payload, &r); err != nil {
			return fmt.Errorf("engine: payload de resposta inválido: %w", err)
		}
		return e.reply(ctx, r)
```

`OnJobFailed` for `JobReply`: send `msgRetryLater` so the client is not left without answer:

```go
	case JobReply:
		var r ReplyJob
		if err := json.Unmarshal(job.Payload, &r); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		conv, err := e.Store.GetConversation(ctx, r.ConversationID)
		if err != nil {
			return err
		}
		if err := e.Store.MarkReplied(ctx, conv.ID, r.MessageID); err != nil {
			return err
		}
		return e.Store.Enqueue(ctx, sendJob(conv.ID, conv.ChatID, msgRetryLater))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go vet ./... && go test ./...`
Expected: PASS, sem SKIP de banco.

- [ ] **Step 5: Commit** (Tasks 5 e 6 juntas)

```bash
git add internal/engine internal/agent/attendant
git commit -m "feat: resposta humana com debounce, visto, digitando, balões e voz"
```

---

### Task 7: Tom de conversa (prompt e mensagens fixas)

**Files:**
- Modify: `internal/agent/attendant/prompt.go`, `internal/engine/messages.go`

- [ ] **Step 1: Prompt.** Em `prompt.go`, trocar a linha "Seu papel: conversar com o cliente em português do Brasil, de forma simples, curta e cordial; ..." e a regra de `reply` por:

```text
Seu papel: conversar com o cliente em português do Brasil como uma pessoa da equipe conversaria no WhatsApp; entender o que ele quer; coletar os dados da campanha e lembrar os dados do negócio.

Jeito de falar:
- Frases curtas, tom de conversa, como alguém digitando no celular. Nada de linguagem de robô ("Registrei sua solicitação", "Como posso ajudar?", "Prezado").
- Chame o cliente pelo nome quando souber, sem repetir o nome em toda mensagem.
- Espelhe o cliente: se ele escreve informal, responda informal; se é formal, seja educado sem exagero.
- No máximo 1 emoji, e só se o cliente também usar.
- Não comece duas respostas seguidas do mesmo jeito. Reaja ao que ele disse antes de perguntar ("Pizza artesanal, boa!").
- Pode separar a resposta em até 3 mensagens curtas com uma linha em branco entre elas, como balões no WhatsApp.
```

and the `reply:` rule becomes:

```text
reply: sua resposta ao cliente, com no máximo 3 frases curtas no total. Não pergunte o que já está na memória do negócio ou nos dados coletados. Se faltarem dados, pergunte um ou dois por vez. Se todos os dados estiverem completos, diga que o especialista vai montar a proposta e que ela chega em instantes. Se "Formato da resposta" for áudio, escreva como se fala: uma parte só, sem listas, sem emoji, sem asterisco.
```

- [ ] **Step 2: Mensagens fixas.** Em `messages.go`, reescrever mantendo os trechos entre colchetes (os testes procuram por eles):

```go
	msgUnknown        = "Oi! Este número ainda [não está liberado] para o seu contato. Fala com o responsável pelo serviço pra cadastrar seu telefone, tá?"
	msgBusy           = "Tô [montando sua proposta] com o especialista, já já ela chega aqui."
	msgRetryLater     = "Opa, tive um [problema técnico] aqui. Me manda de novo daqui a pouquinho?"
	msgHuman          = "Beleza! Vou chamar uma [pessoa da nossa equipe] pra continuar com você."
	msgOverBudget     = "O limite de investimento por dia da sua conta é %s. Qual valor até esse limite fica bom pra você?"
	msgApproved       = "[Campanha aprovada]! Anotei sua aprovação. Assim que a conexão com o Facebook estiver ativa, ela é publicada pausada pra uma última conferência."
	msgCodeInvalid    = "Hmm, esse código [não confere]. Responde SIM seguido do código de 6 dígitos que está na proposta."
	msgCodeExpired    = "Esse código [expirou]. Vou pedir uma proposta atualizada pro especialista, com código novo."
	msgCodeLocked     = "Foram [muitas tentativas] com código errado. Vou gerar uma proposta nova, com outro código."
	msgNotAllowed     = "Seu contato [não tem permissão] pra aprovar gastos desta conta. Pede pro responsável aprovar, tá?"
	msgNoDraft        = "Não achei nenhuma proposta esperando aprovação. Quer criar uma campanha?"
	msgRejected       = "Tranquilo, [descartei] essa proposta. Quando quiser, é só me contar o que anunciar."
	msgProposalFailed = "Não consegui montar uma [proposta segura] com esses dados. Vou chamar alguém da equipe pra te ajudar."
	msgAudioFailed    = "Não consegui ouvir seu [áudio] agora. Consegue me mandar por escrito?"

	msgDailyLimit             = "Você chegou no [limite de mensagens] de hoje. Amanhã a gente continua de onde parou!"
	msgProjectResumedComplete = "[Retomei] o projeto *%s*. Já tenho todos os dados. Quer que eu gere a proposta?"
	msgProjectNotFound        = "[Não encontrei] o projeto \"%s\"."
	msgProjectAmbiguous       = "Achei mais de um projeto parecido: %s. [Qual deles]?"
```

(Os colchetes só marcam o trecho protegido; **não** escrever colchetes no código.) `msgOperator`, `msgUnsupported` e os demais ficam iguais.

- [ ] **Step 3: Run tests**

Run: `go test ./...`
Expected: PASS. Se um teste quebrar por texto, ajustar a mensagem para conter o trecho que ele procura (não mudar o teste).

- [ ] **Step 4: Commit**

```bash
git add internal/agent/attendant/prompt.go internal/engine/messages.go
git commit -m "feat: tom de conversa na Atendente e nas mensagens fixas"
```

---

### Task 8: Configuração, worker e documentação

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`, `cmd/worker/main.go`, `.env.example`, `docs/deploy-easypanel.md`

**Interfaces:**
- Produces: `Config.Humanize bool`, `Config.ReplyDebounce time.Duration`, `Config.VoiceReplies bool`, `Config.TTSModel string`, `Config.TTSVoice string`.

- [ ] **Step 1: Write the failing test** (append to `config_test.go`)

```go
func TestHumanizeDefaults(t *testing.T) {
	c := config.Load(func(string) string { return "" })
	if !c.Humanize || c.ReplyDebounce != 4*time.Second || !c.VoiceReplies || c.TTSModel != "" || c.TTSVoice != "nova" {
		t.Fatalf("padrões = %+v", c)
	}
	env := map[string]string{"HUMANIZE": "false", "REPLY_DEBOUNCE_SECONDS": "7", "VOICE_REPLIES": "false", "TTS_MODEL": "m", "TTS_VOICE": "alloy"}
	c = config.Load(func(k string) string { return env[k] })
	if c.Humanize || c.ReplyDebounce != 7*time.Second || c.VoiceReplies || c.TTSModel != "m" || c.TTSVoice != "alloy" {
		t.Fatalf("lidos = %+v", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL.

- [ ] **Step 3: Implement**

`Config` gains (import `time`):

```go

	// Atendente humana.
	Humanize      bool
	ReplyDebounce time.Duration
	VoiceReplies  bool
	TTSModel      string
	TTSVoice      string
```

`Load` gains:

```go
		Humanize:          getenv("HUMANIZE") != "false",
		ReplyDebounce:     time.Duration(positiveInt(getenv("REPLY_DEBOUNCE_SECONDS"), 4)) * time.Second,
		VoiceReplies:      getenv("VOICE_REPLIES") != "false",
		TTSModel:          getenv("TTS_MODEL"),
		TTSVoice:          orDefault(getenv("TTS_VOICE"), "nova"),
```

`cmd/worker/main.go`, in the `engine.Engine` literal:

```go

		Humanize: cfg.Humanize,
		Debounce: cfg.ReplyDebounce,
		TTSModel: cfg.TTSModel,
		TTSVoice: cfg.TTSVoice,
	}
	if cfg.VoiceReplies && cfg.TTSModel != "" {
		eng.Voice = router // voz só com modelo de TTS configurado
	}
```

and log `"humano", cfg.Humanize, "voz", eng.Voice != nil` in "worker iniciado".

`.env.example` — add:

```
# Atendente humana
HUMANIZE=true
REPLY_DEBOUNCE_SECONDS=4
VOICE_REPLIES=true
TTS_MODEL=            # modelo de TTS do OpenRouter; vazio = responde só em texto
TTS_VOICE=nova
```

`docs/deploy-easypanel.md` — seção "Atendente humana": variáveis acima; WAHA >= 2026.6.1; desligar auto presence do WAHA se estiver ligado; como testar (mandar texto → visto, digitando, balões; mandar áudio → gravando, voz).

- [ ] **Step 4: Run tests**

Run: `go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config cmd/worker .env.example docs/deploy-easypanel.md
git commit -m "feat: configuração da atendente humana e voz no worker"
```

---

### Task 9: Validação ao vivo (depois da revisão final)

- [ ] Conferir versão do WAHA e auto presence no Easypanel (MCP `easypanel`, só leitura).
- [ ] Escolher `TTS_MODEL`/`TTS_VOICE` na lista de TTS do OpenRouter e gerar um áudio de teste local com `Speak` (custo de centavos) para ouvir a voz em pt-BR.
- [ ] Deploy só com confirmação do usuário (produção usa `feat/etapa-2`).
