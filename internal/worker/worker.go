// Package worker é o laço que processa o inbox (webhooks) e o outbox (jobs).
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// Handler processa eventos e jobs. *engine.Engine implementa; os testes trocam.
type Handler interface {
	HandleInbound(ctx context.Context, in whatsapp.Inbound) error
	HandleJob(ctx context.Context, job store.Job) error
	OnJobFailed(ctx context.Context, job store.Job) error
}

type Worker struct {
	Store *store.Store
	WA    whatsapp.Provider
	// Parsers lê eventos gravados por outro provedor (troca WAHA ↔ Wame). Sem entrada, usa WA.
	Parsers map[string]whatsapp.Provider
	Engine  Handler
	Log     *slog.Logger
	Idle    time.Duration // espera quando não há trabalho (padrão 1 s)
	Timeout time.Duration // prazo de cada evento ou job (padrão 2 min)
	// SlowConcurrency: jobs lentos rodando ao mesmo tempo fora do laço (padrão 2).
	SlowConcurrency int

	once  sync.Once
	slots chan struct{} // vagas dos jobs lentos
	wg    sync.WaitGroup
}

// SlowKinds são os jobs que levam minutos (baixar o post do Instagram, subir vídeo e criar a campanha na
// Meta). Rodam em goroutines, no máximo SlowConcurrency por vez, enquanto o laço segue com o inbox e os
// outros jobs: um cliente esperando um post não trava a conversa dos outros.
var SlowKinds = []string{engine.JobInstagramFetch, engine.JobMetaPublish}

// finishTimeout: gravar o resultado do job com contexto próprio (o do worker pode estar parando).
const finishTimeout = 30 * time.Second

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

// Run processa até o contexto ser cancelado. Ao sair, espera os jobs lentos em andamento terminarem e
// gravarem o resultado (o cancelamento chega a eles pelo contexto).
func (w *Worker) Run(ctx context.Context) error {
	defer w.Wait()
	idle := w.Idle
	if idle <= 0 {
		idle = 300 * time.Millisecond
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

// Wait espera os jobs lentos em andamento.
func (w *Worker) Wait() { w.wg.Wait() }

// Tick processa no máximo um evento do inbox e um job rápido, e põe um job lento para rodar em goroutine
// se houver vaga. did=true se houve trabalho.
func (w *Worker) Tick(ctx context.Context) (bool, error) {
	didInbox, err := w.inbox(ctx)
	if err != nil {
		return didInbox, err
	}
	didJob, err := w.job(ctx)
	if err != nil {
		return didInbox || didJob, err
	}
	didSlow, err := w.slowJob(ctx)
	return didInbox || didJob || didSlow, err
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

// job roda no laço um job que não é lento.
func (w *Worker) job(ctx context.Context) (bool, error) {
	job, ok, err := w.Store.ClaimJobExcept(ctx, SlowKinds)
	if err != nil || !ok {
		return false, err
	}
	return true, w.run(ctx, job)
}

// slowJob pega um job lento só se houver vaga (a vaga é reservada antes de travar o job: nenhum job fica
// travado esperando) e roda em goroutine. A trava do job no store cobre o prazo do tipo.
func (w *Worker) slowJob(ctx context.Context) (bool, error) {
	w.once.Do(func() {
		n := w.SlowConcurrency
		if n <= 0 {
			n = 2
		}
		w.slots = make(chan struct{}, n)
	})
	select {
	case w.slots <- struct{}{}:
	default:
		return false, nil // todas as vagas ocupadas
	}
	job, ok, err := w.Store.ClaimJobOf(ctx, SlowKinds)
	if err != nil || !ok {
		<-w.slots
		return false, err
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() { <-w.slots }()
		if err := w.run(ctx, job); err != nil {
			w.Log.Error("falha ao fechar job", "job_id", job.ID, "tipo", job.Kind, "erro", err)
		}
	}()
	return true, nil
}

// run processa o job e grava o resultado com contexto próprio: worker parando não deixa o job travado
// (ele volta para nova tentativa).
func (w *Worker) run(ctx context.Context, job store.Job) error {
	procErr := w.safely(ctx, w.TimeoutFor(job.Kind), func(ctx context.Context) error { return w.Engine.HandleJob(ctx, job) })
	if procErr != nil {
		w.Log.Error("job falhou", "job_id", job.ID, "tipo", job.Kind, "tentativa", job.Attempts, "erro", procErr)
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	failed, err := w.Store.FinishJob(fctx, job.ID, procErr)
	if err != nil {
		return err
	}
	if failed {
		if err := w.safely(fctx, w.TimeoutFor(""), func(ctx context.Context) error { return w.Engine.OnJobFailed(ctx, job) }); err != nil {
			w.Log.Error("falha ao tratar job esgotado", "job_id", job.ID, "erro", err)
		}
	}
	return nil
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
