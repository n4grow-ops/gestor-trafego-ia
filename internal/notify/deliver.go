package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Deliverer roda o job notify.deliver no worker: uma entrega para um destino, com registro.
type Deliverer struct {
	Store  *store.Store
	Sender *Sender
	Log    *slog.Logger
}

// errRetry faz a fila tentar de novo (até 3 vezes no total, com espera crescente).
type errRetry struct{ msg string }

func (e errRetry) Error() string { return "notificação: " + e.msg }

// Deliver entrega o job. Destino apagado, desligado ou que deixou de assinar o evento: nada a fazer.
// Erro de rede, 429 ou 5xx devolve erro (a fila tenta de novo); 4xx fica registrado e não repete.
func (d *Deliverer) Deliver(ctx context.Context, job store.Job) error {
	var p store.NotifyDeliverJob
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return fmt.Errorf("notificação: payload inválido: %w", err)
	}
	dest, err := d.Store.NotifyDestination(ctx, p.DestinationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !dest.Active || !slices.Contains(dest.Events, p.Event) {
		return nil
	}
	ev, ok := EventByKey(p.Event)
	if !ok {
		return nil
	}
	res := d.Send(ctx, dest, ev, p.Data, p)
	if err := d.Store.RecordNotifyDelivery(ctx, store.NotifyDelivery{DestinationID: dest.ID, Event: ev.Key,
		Attempt: job.Attempts, OK: res.OK(), StatusCode: res.StatusCode, Error: res.Error}); err != nil {
		d.log().Error("notificação: registro da entrega falhou", "destino", dest.ID, "erro", err)
	}
	if res.OK() {
		return nil
	}
	// Só o nome e o id do destino vão para o log, nunca a URL.
	d.log().Warn("notificação não entregue", "destino", dest.ID, "evento", ev.Key, "tentativa", job.Attempts, "status", res.StatusCode, "erro", res.Error)
	if res.Retry {
		return errRetry{res.Error}
	}
	return nil
}

// Send monta a mensagem do destino e entrega uma vez.
func (d *Deliverer) Send(ctx context.Context, dest store.NotifyDestination, ev Event, data map[string]string, p store.NotifyDeliverJob) Result {
	vars := Vars(ev, data, p.At)
	title, text := Message(ev, dest.TitleTpl, dest.TextTpl, vars)
	s := d.Sender
	if s == nil {
		s = &Sender{}
	}
	return s.Send(ctx, Request{Kind: dest.Kind, URL: dest.URL, Event: ev.Key, Title: title, Text: text, Data: data, At: p.At})
}

func (d *Deliverer) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}
