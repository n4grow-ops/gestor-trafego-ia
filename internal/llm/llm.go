// Package llm define o contrato com os modelos de linguagem e o adaptador do OpenRouter.
package llm

import (
	"context"
	"errors"
	"strings"
)

type Message struct {
	Role    string // system, user ou assistant
	Content string
}

type Request struct {
	Model     string
	Messages  []Message
	JSON      bool // pede resposta em objeto JSON
	MaxTokens int
}

type Usage struct {
	Model        string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// Add soma o uso de várias chamadas; o modelo fica o da última.
func (u Usage) Add(o Usage) Usage {
	model := u.Model
	if o.Model != "" {
		model = o.Model
	}
	return Usage{
		Model:        model,
		InputTokens:  u.InputTokens + o.InputTokens,
		OutputTokens: u.OutputTokens + o.OutputTokens,
		CostUSD:      u.CostUSD + o.CostUSD,
	}
}

type Response struct {
	Content string
	Usage   Usage
}

type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
	Transcribe(ctx context.Context, model string, audio []byte, format string) (Response, error)
}

// ErrTransient marca falhas que valem nova tentativa (limite, erro do provedor, rede).
var ErrTransient = errors.New("llm: falha temporária")

// StripFences remove a cerca de código markdown que alguns modelos põem em volta do JSON.
func StripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}
