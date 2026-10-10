// Package worker é o laço que processa o inbox (webhooks) e o outbox (jobs).
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type Worker struct {
	Store *store.Store
	WA    whatsapp.Provider
	// Parsers lê eventos gravados por outro provedor (troca WAHA ↔ Wame). Sem entrada, usa WA.
	Parsers map[string]whatsapp.Provider
	Engine  *engine.Engine
	Log     *slog.Logger
	Idle    time.Duration // espera quando não há trabalho (padrão 1 s)
	Timeout time.Duration // prazo de cada evento ou job (padrão 2 min)
}

// Prazos próprios por tipo de job: a publicação na Meta sobe vídeos e espera a Meta processar (até ~10 min);
// o post do Instagram baixa por até 3 min (vídeo de até 100 MB) e grava com contexto próprio.
// A trava do job no store (ClaimJob) é maior que esses prazos.
var jobTimeouts = map[string]time.Duration{
	engine.JobMetaPublish:    engine.PublishTimeout,
	engine.JobInstagramFetch: engine.InstagramTimeout,
}

// TimeoutFor é o prazo de um job do tipo: o próprio do tipo, senão Timeout (padrão 2 min).
func (w *Worker) TimeoutFor(kind string) time.Duration {
	if d, ok := jobTimeouts[kind]; ok {
		return d
	}
	if w.Timeout > 0 {
		return w.Timeout
	}
	return defaultTimeout
}

const defaultTimeout = 2 * time.Minute

// Run processa até o contexto ser cancelado.
func (w *Worker) Run(ctx context.Context) error {
	idle := w.Idle
	if idle <= 0 {
		idle = time.Second
	}
	for {
		did, err := w.Tick(ctx)
		if err != nil {
			w.Log.Error("falha no worker", "erro", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		if did && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(idle):
		}
	}
}

// Tick processa no máximo um evento do inbox e um job. did=true se houve trabalho.
func (w *Worker) Tick(ctx context.Context) (bool, error) {
	didInbox, err := w.inbox(ctx)
	if err != nil {
		return didInbox, err
	}
	didJob, err := w.job(ctx)
	return didInbox || didJob, err
}

func (w *Worker) inbox(ctx context.Context) (bool, error) {
	ev, ok, err := w.Store.ClaimInbox(ctx)
	if err != nil || !ok {
		return false, err
	}
	var kind whatsapp.Kind
	procErr := w.safely(ctx, w.TimeoutFor(""), func(ctx context.Context) error {
		parser := w.WA
		if p, ok := w.Parsers[ev.Provider]; ok && p != nil {
			parser = p
		}
		in, keep, err := parser.ParseWebhook(ev.Payload)
		if err != nil || !keep {
			return err
		}
		kind = in.Kind
		return w.Engine.HandleInbound(ctx, in)
	})
	if procErr != nil {
		w.Log.Error("evento falhou", "inbox_id", ev.ID, "tentativa", ev.Attempts, "erro", procErr)
	}
	if err := w.Store.FinishInbox(ctx, ev.ID, procErr); err != nil {
		return true, err
	}
	// Resposta do formulário de cadastro traz CPF e e-mail: já está no cadastro, não fica no evento.
	if procErr == nil && kind == whatsapp.KindFlow {
		return true, w.Store.RedactInbox(ctx, ev.ID)
	}
	return true, nil
}

func (w *Worker) job(ctx context.Context) (bool, error) {
	job, ok, err := w.Store.ClaimJob(ctx)
	if err != nil || !ok {
		return false, err
	}
	procErr := w.safely(ctx, w.TimeoutFor(job.Kind), func(ctx context.Context) error { return w.Engine.HandleJob(ctx, job) })
	if procErr != nil {
		w.Log.Error("job falhou", "job_id", job.ID, "tipo", job.Kind, "tentativa", job.Attempts, "erro", procErr)
	}
	failed, err := w.Store.FinishJob(ctx, job.ID, procErr)
	if err != nil {
		return true, err
	}
	if failed {
		if err := w.safely(ctx, w.TimeoutFor(""), func(ctx context.Context) error { return w.Engine.OnJobFailed(ctx, job) }); err != nil {
			w.Log.Error("falha ao tratar job esgotado", "job_id", job.ID, "erro", err)
		}
	}
	return true, nil
}

// safely aplica o prazo e transforma panic em erro, para um evento ruim não derrubar o worker.
func (w *Worker) safely(ctx context.Context, timeout time.Duration, fn func(context.Context) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(ctx)
}
