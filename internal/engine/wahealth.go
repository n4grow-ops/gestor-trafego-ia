package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	// JobWAHealth lê a situação do número do bot na Meta de tempos em tempos.
	JobWAHealth = "whatsapp.health"
	// WAHealthProvider é a linha de whatsapp_status com o resultado (o painel mostra o alerta).
	WAHealthProvider = "meta-envio"
	waHealthEvery    = 15 * time.Minute
)

// Mailer manda e-mail de aviso à equipe.
type Mailer interface {
	Send(ctx context.Context, to []string, subject, text string) error
}

// waHealthText traduz os erros conhecidos; os outros vão como a Meta escreveu.
var waHealthText = map[int]string{
	141006: "forma de pagamento com erro na conta do WhatsApp Business. Corrija em business.facebook.com > Configurações > Pagamentos do WhatsApp",
}

func (e *Engine) waHealthChecker() (whatsapp.HealthChecker, bool) {
	hc, ok := e.WA.(whatsapp.HealthChecker)
	return hc, ok
}

// EnsureWAHealth agenda a primeira leitura (se ainda não houver uma na fila).
func (e *Engine) EnsureWAHealth(ctx context.Context) error {
	if _, ok := e.waHealthChecker(); !ok {
		return nil
	}
	pending, err := e.Store.HasPendingKind(ctx, JobWAHealth)
	if err != nil || pending {
		return err
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobWAHealth, Payload: map[string]string{}})
}

// waHealth lê, grava e avisa por e-mail quando muda. Erro não para a rotina.
func (e *Engine) waHealth(ctx context.Context) error {
	if err := e.checkWAHealth(ctx); err != nil {
		e.Log.Error("falha ao ler a situação do WhatsApp na Meta", "erro", err)
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobWAHealth, Payload: map[string]string{}, Delay: waHealthEvery})
}

func (e *Engine) checkWAHealth(ctx context.Context) error {
	hc, ok := e.waHealthChecker()
	if !ok {
		return nil
	}
	h, ok, err := hc.Health(ctx)
	if err != nil || !ok {
		return err
	}
	ev := whatsapp.ConnectionEvent{Connected: !h.Blocked}
	if h.Blocked {
		ev.Code, ev.Reason = waHealthReason(h)
	}
	changed, err := e.Store.SetWhatsAppStatus(ctx, WAHealthProvider, ev)
	if err != nil || !changed {
		return err
	}
	if h.Blocked {
		e.Log.Warn("Meta bloqueou mensagens iniciadas pelo bot", "codigo", ev.Code, "motivo", ev.Reason)
	} else {
		e.Log.Info("Meta liberou mensagens iniciadas pelo bot")
	}
	return e.mailWAHealth(ctx, h.Blocked, ev.Reason)
}

func waHealthReason(h whatsapp.Health) (int, string) {
	if len(h.Problems) == 0 {
		return 0, "a Meta não deu o motivo"
	}
	var parts []string
	for _, p := range h.Problems {
		if t, ok := waHealthText[p.Code]; ok {
			parts = append(parts, t)
		} else {
			parts = append(parts, strings.TrimSpace(p.Description+" "+p.Solution))
		}
	}
	return h.Problems[0].Code, strings.Join(parts, "; ")
}

func (e *Engine) mailWAHealth(ctx context.Context, blocked bool, reason string) error {
	if e.Mailer == nil {
		return nil
	}
	to, err := e.Store.AdminAlertEmails(ctx)
	if err != nil || len(to) == 0 {
		return err
	}
	subject := "meu anúncio.io: WhatsApp voltou a mandar avisos"
	text := "A Meta liberou de novo as mensagens que o bot inicia (avisos da equipe, lembretes e modelos)."
	if blocked {
		subject = "meu anúncio.io: Meta bloqueou os avisos do WhatsApp"
		text = fmt.Sprintf("A Meta está bloqueando as mensagens que o bot inicia: avisos da equipe, lembretes e modelos não chegam.\n"+
			"Respostas a quem escreveu nas últimas 24 h continuam saindo.\n\nMotivo: %s.", reason)
	}
	if e.AdminURL != "" {
		text += "\n\nPainel: " + e.AdminURL
	}
	return e.Mailer.Send(ctx, to, subject, text)
}
