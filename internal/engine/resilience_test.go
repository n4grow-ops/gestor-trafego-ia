package engine_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// retryAll antecipa os jobs reagendados e roda de novo, simulando o tempo passar.
func (h *harness) retryAll(rounds int) {
	h.t.Helper()
	for i := 0; i < rounds; i++ {
		h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() - interval '1 second' WHERE status = 'pending'`)
		h.runJobs()
	}
}

func TestEscalationSurvivesWhatsAppOutage(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"human","fields":{},"reply":"ok"}`}
	h.wa.setFail(true)
	h.say("quero falar com uma pessoa")
	if h.conv().State != store.StateNeedsHuman {
		t.Fatalf("estado = %s", h.conv().State)
	}
	if n := h.pendingJobs(engine.JobSend); n != 2 {
		t.Fatalf("avisos na fila = %d, want 2 (cliente e operador)", n)
	}
	h.runJobs() // falha com o WhatsApp fora
	h.wa.setFail(false)
	h.retryAll(1)
	if client := h.wa.to(chatID); len(client) != 1 || !strings.Contains(client[0], "pessoa da nossa equipe") {
		t.Errorf("cliente = %v", client)
	}
	if ops := h.wa.to(operatorChat); len(ops) != 1 {
		t.Errorf("operador = %v", ops)
	}
}

func TestSummarySendsRetryLongEnough(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer, turnRest}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	h.wa.setFail(true)
	h.retryAll(5) // mais que as 3 tentativas padrão
	if h.conv().State != store.StateAwaitingApproval {
		t.Fatalf("resumo desistiu cedo demais: estado = %s", h.conv().State)
	}
	h.wa.setFail(false)
	h.retryAll(1)
	if !codeRe.MatchString(h.wa.last().Text) {
		t.Errorf("resumo não chegou: %q", h.wa.last().Text)
	}
}

func TestUndeliveredSummaryEscalates(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer, turnRest}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	h.wa.setFail(true)
	h.retryAll(12)
	if h.conv().State != store.StateNeedsHuman {
		t.Fatalf("estado = %s", h.conv().State)
	}
	if s := h.draftStatuses(); s["expired"] != 1 {
		t.Errorf("rascunhos = %v", s)
	}
}

func TestAudioFailureAsksClientToWrite(t *testing.T) {
	h := newHarness(t, "owner")
	h.wa.mediaErr = errors.New("mídia indisponível")
	err := h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: "a1", ChatID: chatID, Phone: phone, Kind: whatsapp.KindAudio})
	if err != nil {
		t.Fatalf("falha de áudio não deve reprocessar o evento: %v", err)
	}
	if !strings.Contains(h.wa.last().Text, "áudio") || h.att.Calls() != 0 {
		t.Errorf("resposta = %q calls = %d", h.wa.last().Text, h.att.Calls())
	}
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action = 'media_failed'`); n != 1 {
		t.Errorf("auditoria = %d", n)
	}
}

func TestConcurrentMessagesKeepAllFields(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{
		`{"intent":"create_campaign","fields":{"offer":"Pizza grande","audience":"famílias"},"reply":"a"}`,
		`{"intent":"provide_info","fields":{"location":"Campinas","daily_budget_brl":50},"reply":"b"}`,
	}
	var wg sync.WaitGroup
	for i, text := range []string{"quero anunciar pizza para famílias", "em Campinas, 50 por dia"} {
		wg.Add(1)
		go func(i int, text string) {
			defer wg.Done()
			h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: "c" + string(rune('0'+i)), ChatID: chatID, Phone: phone, Kind: whatsapp.KindText, Text: text})
		}(i, text)
	}
	wg.Wait()
	c := h.conv()
	if c.State != store.StateAwaitingProposal || len(c.Fields.Missing()) != 0 {
		t.Fatalf("campos perdidos na concorrência: estado = %s faltam = %v", c.State, c.Fields.Missing())
	}
	if n := h.pendingJobs(engine.JobPropose); n != 1 {
		t.Errorf("jobs de proposta = %d", n)
	}
}
