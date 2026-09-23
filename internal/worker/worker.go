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
	Store   *store.Store
	WA      whatsapp.Provider
	Engine  *engine.Engine
	Log     *slog.Logger
	Idle    time.Duration // espera quando não há trabalho (padrão 1 s)
	Timeout time.Duration // prazo de cada evento ou job (padrão 2 min)
}

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
	procErr := w.safely(ctx, func(ctx context.Context) error {
		in, keep, err := w.WA.ParseWebhook(ev.Payload)
		if err != nil || !keep {
			return err
		}
		return w.Engine.HandleInbound(ctx, in)
	})
	if procErr != nil {
		w.Log.Error("evento falhou", "inbox_id", ev.ID, "tentativa", ev.Attempts, "erro", procErr)
	}
	return true, w.Store.FinishInbox(ctx, ev.ID, procErr)
}

func (w *Worker) job(ctx context.Context) (bool, error) {
	job, ok, err := w.Store.ClaimJob(ctx)
	if err != nil || !ok {
		return false, err
	}
	procErr := w.safely(ctx, func(ctx context.Context) error { return w.Engine.HandleJob(ctx, job) })
	if procErr != nil {
		w.Log.Error("job falhou", "job_id", job.ID, "tipo", job.Kind, "tentativa", job.Attempts, "erro", procErr)
	}
	failed, err := w.Store.FinishJob(ctx, job.ID, procErr)
	if err != nil {
		return true, err
	}
	if failed {
		if err := w.safely(ctx, func(ctx context.Context) error { return w.Engine.OnJobFailed(ctx, job) }); err != nil {
			w.Log.Error("falha ao tratar job esgotado", "job_id", job.ID, "erro", err)
		}
	}
	return true, nil
}

// safely aplica o prazo e transforma panic em erro, para um evento ruim não derrubar o worker.
func (w *Worker) safely(ctx context.Context, fn func(context.Context) error) (err error) {
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(ctx)
}
