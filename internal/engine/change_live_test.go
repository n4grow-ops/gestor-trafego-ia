package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// Caso real (Método v3): com a campanha já criada, o cliente pediu "Ajuste somente para feed e stories"; a
// Mia respondeu "vou ajustar" e nada aconteceu. Agora a IA sabe que há campanha na Meta, a equipe recebe o
// pedido e o cliente fica sabendo quem faz, sem promessa falsa.
func TestChangeOnLiveCampaignGoesToTeam(t *testing.T) {
	h, _, _ := publishWith(t, &meta.Funding{Status: 1, HasSource: true, AvailableCents: -1})
	before := len(h.wa.to(operatorChat))
	// a próxima chamada da Atendente (as anteriores do fluxo de publicação já foram consumidas)
	h.att.Responses = append(make([]string, h.att.Calls()), `{"intent":"change_live","reply":"Entendi, vou ajustar."}`)
	h.say("Ajuste somente para feed e stories")
	if !strings.Contains(h.lastPrompt(), "Campanha na Meta: pausada") {
		t.Fatalf("IA não soube da campanha publicada: %s", h.lastPrompt())
	}
	got := h.wa.to(chatID)
	last := got[len(got)-1]
	if !strings.Contains(last, "Anotei o ajuste") || !strings.Contains(last, "feed e stories") || strings.Contains(last, "vou ajustar") {
		t.Fatalf("cliente recebeu %q", last)
	}
	h.runJobs() // o aviso à equipe vai pela fila
	ops := h.wa.to(operatorChat)
	if len(ops) != before+1 || !strings.Contains(ops[len(ops)-1], "Ajuste somente para feed e stories") {
		t.Fatalf("equipe = %v", ops)
	}
}
