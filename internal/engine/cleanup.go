package engine

import (
	"context"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const (
	JobCleanup = "maintenance.cleanup"
	// keepEvents: eventos do webhook e jobs encerrados ficam 30 dias para investigar problemas.
	keepEvents  = 30 * 24 * time.Hour
	cleanupHour = 4 // horário de Brasília, fora do movimento
)

// EnsureCleanup garante uma limpeza diária agendada (chamado quando o worker sobe).
func (e *Engine) EnsureCleanup(ctx context.Context) error {
	pending, err := e.Store.HasPendingKind(ctx, JobCleanup)
	if err != nil || pending {
		return err
	}
	return e.enqueueCleanup(ctx)
}

func (e *Engine) enqueueCleanup(ctx context.Context) error {
	now := e.now()
	t := now.In(brt)
	y, m, d := t.Date()
	at := time.Date(y, m, d, cleanupHour, 0, 0, 0, brt)
	if !at.After(t) {
		at = at.AddDate(0, 0, 1)
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobCleanup, Payload: map[string]string{}, Delay: at.Sub(now)})
}

// cleanup apaga eventos e jobs encerrados há mais de 30 dias e agenda a próxima.
func (e *Engine) cleanup(ctx context.Context) error {
	inbox, jobs, err := e.Store.PurgeOld(ctx, keepEvents)
	if err != nil {
		return err
	}
	if n, err := e.Store.PurgeMiaTurns(ctx, e.now().Add(-keepTurns)); err != nil {
		e.Log.Error("limpeza dos turnos da Mia", "erro", err)
	} else if n > 0 {
		e.Log.Info("turnos da Mia apagados", "quantos", n)
	}
	renewed, err := e.renewMediaLinks(ctx)
	if err != nil {
		e.Log.Error("renovação dos links de mídia", "erro", err)
	}
	e.Log.Info("limpeza diária", "eventos", inbox, "jobs", jobs, "links_renovados", renewed)
	return e.enqueueCleanup(ctx)
}
