package llm_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
)

var ctx = context.Background()

const okBody = `{"model":"m-1","choices":[{"message":{"content":"{\"ok\":true}"}}],"usage":{"prompt_tokens":12,"completion_tokens":3,"cost":0.0004}}`

func client(url string) *llm.OpenRouter {
	c := llm.NewOpenRouter(url, "chave")
	c.Backoff = []time.Duration{0, 0}
	return c
}

func TestCompleteSendsJSONModeAndParsesUsage(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	resp, err := client(srv.URL).Complete(ctx, llm.Request{Model: "m", Messages: []llm.Message{{Role: "user", Content: "oi"}}, JSON: true, MaxTokens: 50})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != `{"ok":true}` {
		t.Errorf("content = %q", resp.Content)
	}
	want := llm.Usage{Model: "m-1", InputTokens: 12, OutputTokens: 3, CostUSD: 0.0004}
	if resp.Usage != want {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if auth != "Bearer chave" || path != "/chat/completions" {
		t.Errorf("auth = %q path = %q", auth, path)
	}
	if rf, _ := got["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Errorf("response_format = %v", got["response_format"])
	}
	if u, _ := got["usage"].(map[string]any); u["include"] != true {
		t.Errorf("usage = %v", got["usage"])
	}
	if got["max_tokens"] != float64(50) || got["model"] != "m" {
		t.Errorf("body = %v", got)
	}
}

func TestCompleteRetriesTransient(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			w.WriteHeader(500)
		case 2:
			w.WriteHeader(429)
		default:
			io.WriteString(w, okBody)
		}
	}))
	defer srv.Close()

	if _, err := client(srv.URL).Complete(ctx, llm.Request{Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 {
		t.Errorf("chamadas = %d", n.Load())
	}
}

func TestCompleteGivesUpAfterRetries(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	_, err := client(srv.URL).Complete(ctx, llm.Request{Model: "m"})
	if !errors.Is(err, llm.ErrTransient) {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 3 {
		t.Errorf("chamadas = %d", n.Load())
	}
}

func TestCompleteTreatsErrorBodyAsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":{"message":"provider caiu"}}`)
	}))
	defer srv.Close()

	if _, err := client(srv.URL).Complete(ctx, llm.Request{Model: "m"}); !errors.Is(err, llm.ErrTransient) {
		t.Fatalf("err = %v", err)
	}
}

func TestCompleteDoesNotRetryClientError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"modelo inválido"}}`)
	}))
	defer srv.Close()

	_, err := client(srv.URL).Complete(ctx, llm.Request{Model: "m"})
	if err == nil || errors.Is(err, llm.ErrTransient) {
		t.Fatalf("err = %v", err)
	}
	if n.Load() != 1 {
		t.Errorf("chamadas = %d", n.Load())
	}
}

func TestTranscribeSendsAudioPart(t *testing.T) {
	var got struct {
		Messages []struct {
			Content []struct {
				Type       string `json:"type"`
				InputAudio struct {
					Data   string `json:"data"`
					Format string `json:"format"`
				} `json:"input_audio"`
			} `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"model":"t","choices":[{"message":{"content":"quero anunciar"}}],"usage":{}}`)
	}))
	defer srv.Close()

	resp, err := client(srv.URL).Transcribe(ctx, "t", []byte("abc"), "ogg")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "quero anunciar" {
		t.Errorf("content = %q", resp.Content)
	}
	parts := got.Messages[0].Content
	if len(parts) != 2 || parts[1].Type != "input_audio" || parts[1].InputAudio.Format != "ogg" ||
		parts[1].InputAudio.Data != base64.StdEncoding.EncodeToString([]byte("abc")) {
		t.Errorf("parts = %+v", parts)
	}
}

func TestStripFences(t *testing.T) {
	for in, want := range map[string]string{
		"```json\n{\"a\":1}\n```": `{"a":1}`,
		"```\n{\"a\":1}```":       `{"a":1}`,
		"  {\"a\":1} ":            `{"a":1}`,
	} {
		if got := llm.StripFences(in); got != want {
			t.Errorf("StripFences(%q) = %q", in, got)
		}
	}
}

func TestFakeReturnsResponsesInOrder(t *testing.T) {
	f := &llm.Fake{Responses: []string{"a"}, Errors: []error{nil, llm.ErrTransient}}
	if r, err := f.Complete(ctx, llm.Request{Model: "x"}); err != nil || r.Content != "a" || r.Usage.Model != "x" {
		t.Fatalf("1ª: %v %v", r, err)
	}
	if _, err := f.Complete(ctx, llm.Request{}); !errors.Is(err, llm.ErrTransient) {
		t.Fatalf("2ª: %v", err)
	}
	if _, err := f.Complete(ctx, llm.Request{}); err == nil {
		t.Fatal("3ª sem resposta deveria falhar")
	}
	if f.Calls() != 3 || len(f.Requests) != 3 {
		t.Errorf("calls = %d", f.Calls())
	}
}

func TestSpeakSendsTextAndReturnsAudio(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/speech" || r.Header.Get("Authorization") != "Bearer chave" {
			t.Errorf("rota/cabeçalho: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "MP3DATA")
	}))
	defer srv.Close()
	sp, err := client(srv.URL).Speak(ctx, "tts-model", "nova", "Oi, tudo bem?")
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
	_, err := client(srv.URL).Speak(ctx, "m", "v", "oi")
	if err == nil || errors.Is(err, llm.ErrTransient) {
		t.Fatalf("402 deve falhar sem ser temporário: %v", err)
	}
}
