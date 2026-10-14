package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// O painel mostra o que a Mia entendeu e o que o sistema fez em cada resposta.
func TestMiaTurnsAreRecorded(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	h.att.Responses = append(h.att.Responses, `{"intent":"other","reply":"Ajustei a faixa etária."}`)
	h.mgr.Responses = append(h.mgr.Responses, validProposal)
	h.say("Altere a idade para 40 a 56")
	h.runJobs()
	h.say("ok")
	rows, err := h.pool.Query(ctx, `SELECT intent, action, coalesce(fields::text, '') FROM mia_turns ORDER BY id`)
	h.must(err)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var intent, action, fields string
		h.must(rows.Scan(&intent, &action, &fields))
		got = append(got, intent+" | "+action+" | "+fields)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"provide_info | pediu proposta ao especialista", "other | pedido de ajuste reconhecido pelo sistema · ajuste: pediu proposta nova",
		"sistema | concordou sem código: lembrou como aprovar"} {
		if !strings.Contains(all, want) {
			t.Fatalf("turnos sem %q:\n%s", want, all)
		}
	}
	var msgOK int
	h.must(h.pool.QueryRow(ctx, `SELECT count(*) FROM mia_turns t JOIN messages m ON m.id = t.message_id AND m.direction = 'in'`).Scan(&msgOK))
	if msgOK != len(got) {
		t.Fatalf("turnos sem a mensagem do cliente: %d de %d", msgOK, len(got))
	}
}

// Equipe pede proposta nova pelo painel: a conversa vai para o projeto, a proposta aberta é cancelada e o
// cliente recebe o resumo novo com código.
func TestTeamRequestedProposal(t *testing.T) {
	h := newHarness(t, "owner")
	first := h.reachApproval()
	org := h.ident().OrgID
	p, _, err := h.st.CreateOrReopenProject(ctx, org, "Chalé", contract.Fields{Offer: contract.Str("chalé"),
		Audience: contract.Str("homens de 40 a 56 anos"), Location: contract.Str("Maravilha"), DailyBudgetBRL: contract.Num(50)})
	h.must(err)
	h.mgr.Responses = append(h.mgr.Responses, validProposal)
	h.must(h.st.Enqueue(ctx, store.JobSpec{Kind: engine.JobProjectPropose, Payload: engine.ProjectProposeJob{ProjectID: p.ID, By: "dono@x"}}))
	h.runJobs()
	c := h.conv()
	if c.ActiveProjectID == nil || *c.ActiveProjectID != p.ID || c.State != store.StateAwaitingApproval {
		t.Fatalf("conversa = %+v", c)
	}
	sent := strings.Join(h.wa.to(chatID), "\n")
	if !strings.Contains(sent, "A equipe do meu anúncio pediu uma proposta nova para *Chalé*") {
		t.Fatalf("sem aviso da equipe: %q", h.wa.last().Text)
	}
	m := codeRe.FindStringSubmatch(h.wa.last().Text)
	if m == nil || m[1] == first {
		t.Fatalf("sem resumo novo: %q", h.wa.last().Text)
	}
	if n := h.count(`SELECT count(*) FROM campaign_drafts WHERE status = 'cancelled'`); n != 1 {
		t.Fatalf("proposta antiga não foi cancelada (%d)", n)
	}
}
