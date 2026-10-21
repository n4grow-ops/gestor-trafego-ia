package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
)

// ErrBudget: o gasto da chave chegou ao teto configurado (LLM_BUDGET_USD). Não vale nova tentativa.
var ErrBudget = errors.New("llm: teto de gasto da IA atingido")

// Router é o que o sistema usa do OpenRouter: texto, transcrição e voz.
type Router interface {
	Provider
	Speaker
}

// SpendStore guarda o gasto acumulado por chave, compartilhado entre api e worker.
type SpendStore interface {
	LLMSpent(ctx context.Context, keyID string) (float64, error)
	AddLLMSpend(ctx context.Context, keyID string, usd float64) error
}

// Budget corta as chamadas quando o gasto da chave passa de LimitUSD. Conta pelo custo que o OpenRouter
// devolve em cada resposta; a voz (TTS) não traz custo, então só é barrada depois do teto. Erro no banco
// não derruba a IA: registra e segue (o estouro máximo é uma chamada).
type Budget struct {
	Inner    Router
	Store    SpendStore
	KeyID    string
	LimitUSD float64
	Log      *slog.Logger
}

// NewBudget embrulha o router com o teto; limit <= 0 devolve o router sem teto.
func NewBudget(inner Router, st SpendStore, apiKey string, limit float64, log *slog.Logger) Router {
	if limit <= 0 || st == nil {
		return inner
	}
	if log == nil {
		log = slog.Default()
	}
	return &Budget{Inner: inner, Store: st, KeyID: KeyID(apiKey), LimitUSD: limit, Log: log}
}

// KeyID é a impressão curta da chave: identifica o contador sem guardar o segredo.
func KeyID(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:8])
}

func (b *Budget) check(ctx context.Context) error {
	spent, err := b.Store.LLMSpent(ctx, b.KeyID)
	if err != nil {
		b.Log.Warn("teto da IA: não consegui ler o gasto", "erro", err)
		return nil
	}
	if spent >= b.LimitUSD {
		return fmt.Errorf("%w: US$ %.4f de US$ %.2f", ErrBudget, spent, b.LimitUSD)
	}
	return nil
}

func (b *Budget) add(ctx context.Context, u Usage) {
	if u.CostUSD <= 0 {
		return
	}
	if err := b.Store.AddLLMSpend(ctx, b.KeyID, u.CostUSD); err != nil {
		b.Log.Warn("teto da IA: não consegui somar o gasto", "erro", err, "custo_usd", u.CostUSD)
	}
}

func (b *Budget) Complete(ctx context.Context, req Request) (Response, error) {
	if err := b.check(ctx); err != nil {
		return Response{}, err
	}
	res, err := b.Inner.Complete(ctx, req)
	b.add(ctx, res.Usage)
	return res, err
}

func (b *Budget) Transcribe(ctx context.Context, model string, audio []byte, format string) (Response, error) {
	if err := b.check(ctx); err != nil {
		return Response{}, err
	}
	res, err := b.Inner.Transcribe(ctx, model, audio, format)
	b.add(ctx, res.Usage)
	return res, err
}

func (b *Budget) Speak(ctx context.Context, model, voice, text string) (Speech, error) {
	if err := b.check(ctx); err != nil {
		return Speech{}, err
	}
	sp, err := b.Inner.Speak(ctx, model, voice, text)
	b.add(ctx, sp.Usage)
	return sp, err
}
