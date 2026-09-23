package llm

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
)

// OpenRouter fala com a API compatível com OpenAI do OpenRouter.
type OpenRouter struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	Backoff []time.Duration // espera antes de cada nova tentativa
}

func NewOpenRouter(baseURL, apiKey string) *OpenRouter {
	return &OpenRouter{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 90 * time.Second},
		Backoff: []time.Duration{time.Second, 3 * time.Second},
	}
}

type orAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

type orPart struct {
	Type       string   `json:"type"`
	Text       string   `json:"text,omitempty"`
	InputAudio *orAudio `json:"input_audio,omitempty"`
}

type orMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type orFormat struct {
	Type string `json:"type"`
}

type orUsageOpt struct {
	Include bool `json:"include"`
}

type orRequest struct {
	Model          string      `json:"model"`
	Messages       []orMessage `json:"messages"`
	ResponseFormat *orFormat   `json:"response_format,omitempty"`
	MaxTokens      int         `json:"max_tokens,omitempty"`
	Usage          orUsageOpt  `json:"usage"`
}

type orResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		Cost             float64 `json:"cost"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (o *OpenRouter) Complete(ctx context.Context, req Request) (Response, error) {
	msgs := make([]orMessage, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = orMessage{Role: m.Role, Content: m.Content}
	}
	body := orRequest{Model: req.Model, Messages: msgs, MaxTokens: req.MaxTokens, Usage: orUsageOpt{Include: true}}
	if req.JSON {
		body.ResponseFormat = &orFormat{Type: "json_object"}
	}
	return o.do(ctx, body)
}

func (o *OpenRouter) Transcribe(ctx context.Context, model string, audio []byte, format string) (Response, error) {
	body := orRequest{Model: model, Usage: orUsageOpt{Include: true}, Messages: []orMessage{{
		Role: "user",
		Content: []orPart{
			{Type: "text", Text: "Transcreva este áudio em português do Brasil. Responda somente com a transcrição, sem comentários."},
			{Type: "input_audio", InputAudio: &orAudio{Data: base64.StdEncoding.EncodeToString(audio), Format: format}},
		},
	}}}
	return o.do(ctx, body)
}

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

func (o *OpenRouter) do(ctx context.Context, body orRequest) (Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("llm: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt <= len(o.Backoff); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(o.Backoff[attempt-1]):
			}
		}
		resp, err := o.once(ctx, payload)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !errors.Is(err, ErrTransient) {
			return Response{}, err
		}
	}
	return Response{}, lastErr
}

func (o *OpenRouter) once(ctx context.Context, payload []byte) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("llm: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := o.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return Response{}, fmt.Errorf("%w: lendo resposta: %v", ErrTransient, err)
	}
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		return Response{}, fmt.Errorf("%w: status %d", ErrTransient, res.StatusCode)
	}
	if res.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("llm: status %d: %s", res.StatusCode, truncate(string(raw), 300))
	}
	var out orResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("%w: resposta inválida: %v", ErrTransient, err)
	}
	if out.Error != nil {
		return Response{}, fmt.Errorf("%w: %s", ErrTransient, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("%w: resposta sem choices", ErrTransient)
	}
	return Response{
		Content: out.Choices[0].Message.Content,
		Usage: Usage{
			Model:        out.Model,
			InputTokens:  out.Usage.PromptTokens,
			OutputTokens: out.Usage.CompletionTokens,
			CostUSD:      out.Usage.Cost,
		},
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
