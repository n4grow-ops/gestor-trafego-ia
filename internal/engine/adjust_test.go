package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Caso real (conversa 51): "Altere a idade para 40 a 56" com a proposta na mesa. A IA respondeu "Ajustei"
// sem dados de campanha (idade não é campo próprio) e nenhuma proposta nova saiu. Pedido de ajuste com a
// proposta aberta sempre gera proposta nova, com o pedido indo para o especialista.
func TestAdjustmentWithoutFieldsMakesNewProposal(t *testing.T) {
	h := newHarness(t, "owner")
	first := h.reachApproval()
	h.att.Responses = append(h.att.Responses, `{"intent":"other","reply":"Ajustei a faixa etária para 40 a 56 anos."}`)
	h.mgr.Responses = append(h.mgr.Responses, validProposal)
	h.say("Altere a idade para 40 a 56")
	if c := h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("ajuste não pediu proposta nova; estado = %s", c.State)
	}
	var adj string
	h.must(h.pool.QueryRow(ctx, `SELECT payload->>'adjustment' FROM outbox_jobs WHERE kind = $1 ORDER BY id DESC LIMIT 1`, engine.JobPropose).Scan(&adj))
	if !strings.Contains(adj, "40 a 56") {
		t.Fatalf("especialista não recebeu o pedido: %q", adj)
	}
	h.runJobs()
	m := codeRe.FindStringSubmatch(h.wa.last().Text)
	if m == nil || m[1] == first {
		t.Fatalf("sem proposta nova: %q", h.wa.last().Text)
	}
}

// "Ok" com a proposta na mesa não aprova (só SIM com o código): a Mia lembra como aprovar, sem gastar IA e
// sem dizer que algo foi ajustado.
func TestOkDuringApprovalExplainsHowToApprove(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	calls := h.att.Calls()
	for _, s := range []string{"Ok", "beleza", "pode ser", "sim", "aprovado!"} {
		h.say(s)
		got := h.wa.last().Text
		if !strings.Contains(got, "*SIM*") || strings.Contains(strings.ToLower(got), "ajustad") {
			t.Fatalf("%q: resposta = %q", s, got)
		}
	}
	if h.att.Calls() != calls {
		t.Fatal("lembrete de aprovação não deve chamar a IA")
	}
	if h.conv().State != store.StateAwaitingApproval {
		t.Fatal("estado mudou")
	}
}
