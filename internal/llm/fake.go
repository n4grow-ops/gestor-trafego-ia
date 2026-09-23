package llm

import (
	"context"
	"fmt"
	"sync"
)

// Fake devolve respostas pré-gravadas em ordem e guarda os pedidos. Usado em testes;
// nenhum teste gasta tokens.
type Fake struct {
	mu         sync.Mutex
	Responses  []string // conteúdo devolvido por Complete, na ordem das chamadas
	Errors     []error  // se Errors[i] != nil, a chamada i falha com ele
	Transcript string   // devolvido por Transcribe
	Requests   []Request
	calls      int
}

func (f *Fake) Complete(_ context.Context, req Request) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Requests = append(f.Requests, req)
	i := f.calls
	f.calls++
	if i < len(f.Errors) && f.Errors[i] != nil {
		return Response{}, f.Errors[i]
	}
	if i >= len(f.Responses) {
		return Response{}, fmt.Errorf("llm.Fake: sem resposta para a chamada %d", i)
	}
	return Response{Content: f.Responses[i], Usage: Usage{Model: req.Model, InputTokens: 10, OutputTokens: 5, CostUSD: 0.0001}}, nil
}

func (f *Fake) Transcribe(_ context.Context, model string, _ []byte, _ string) (Response, error) {
	return Response{Content: f.Transcript, Usage: Usage{Model: model, InputTokens: 10, OutputTokens: 5}}, nil
}

// Calls conta as chamadas a Complete.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
