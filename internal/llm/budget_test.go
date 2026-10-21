package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type memSpend struct {
	mu    sync.Mutex
	spent map[string]float64
	err   error
}

func (m *memSpend) LLMSpent(_ context.Context, k string) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent[k], m.err
}

func (m *memSpend) AddLLMSpend(_ context.Context, k string, usd float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spent[k] += usd
	return m.err
}

// costly devolve sempre o mesmo custo por chamada.
type costly struct {
	Fake
	cost float64
}

func (c *costly) Complete(ctx context.Context, req Request) (Response, error) {
	r, err := c.Fake.Complete(ctx, req)
	r.Usage.CostUSD = c.cost
	return r, err
}

func TestBudgetStopsAtLimit(t *testing.T) {
	st := &memSpend{spent: map[string]float64{}}
	inner := &costly{Fake: Fake{Responses: []string{"a", "b", "c", "d"}}, cost: 0.4}
	r := NewBudget(inner, st, "sk-teste", 1, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ { // 0 -> 0.4 -> 0.8 -> 1.2: a terceira ainda passa (gasto 0.8 < 1)
		if _, err := r.Complete(ctx, Request{}); err != nil {
			t.Fatalf("chamada %d: %v", i+1, err)
		}
	}
	if _, err := r.Complete(ctx, Request{}); !errors.Is(err, ErrBudget) {
		t.Fatalf("quarta chamada = %v, quer ErrBudget", err)
	}
	if _, err := r.Speak(ctx, "m", "v", "oi"); !errors.Is(err, ErrBudget) {
		t.Fatalf("voz depois do teto = %v, quer ErrBudget", err)
	}
	if len(inner.Requests) != 3 {
		t.Fatalf("chamou o provedor %d vezes, quer 3", len(inner.Requests))
	}
}

func TestBudgetNewKeyStartsAtZero(t *testing.T) {
	st := &memSpend{spent: map[string]float64{KeyID("sk-velha"): 5}}
	r := NewBudget(&Fake{Responses: []string{"ok"}}, st, "sk-nova", 1, nil)
	if _, err := r.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("chave nova bloqueada: %v", err)
	}
}

func TestBudgetOffWithoutLimit(t *testing.T) {
	f := &Fake{}
	if r := NewBudget(f, &memSpend{}, "sk", 0, nil); r != Router(f) {
		t.Fatal("teto 0 deveria devolver o router original")
	}
}

func TestBudgetStoreErrorDoesNotBlock(t *testing.T) {
	st := &memSpend{spent: map[string]float64{}, err: errors.New("banco fora")}
	r := NewBudget(&Fake{Responses: []string{"ok"}}, st, "sk", 1, nil)
	if _, err := r.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("erro do banco bloqueou a IA: %v", err)
	}
}

func TestKeyIDHidesKey(t *testing.T) {
	id := KeyID("sk-or-v1-segredo")
	if len(id) != 16 || id == KeyID("sk-or-v1-outro") {
		t.Fatalf("KeyID = %q", id)
	}
}
