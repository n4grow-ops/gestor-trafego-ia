package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Caso real (Método v3): "anúncio de seguidores" em "Maravilha + 15km" saiu como conversa no WhatsApp,
// só a cidade (sem raio). O raio escrito no local vale (abaixo de 17 km fica 17, com aviso) e o pedido de
// seguidores vira tráfego para o perfil do Instagram.
func TestFollowersProposalKeepsRadiusAndObjective(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"perfil da Método v3","audience":"donos de negócio","location":"Maravilha + 15km","daily_budget_brl":40,"objective_hint":"seguidores"},"reply":"Fechado."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anúncio de seguidores para donos de negócio em Maravilha + 15km, 40 por dia")
	if c := h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("estado = %s campos = %+v", c.State, c.Fields)
	}
	h.runJobs()
	var prop contract.CampaignProposal
	h.must(h.pool.QueryRow(ctx, `SELECT proposal FROM campaign_drafts ORDER BY id DESC LIMIT 1`).Scan(&prop))
	if prop.Audience.RadiusKM != contract.MinRadiusKM || !prop.Followers || prop.Objective != "OUTCOME_TRAFFIC" {
		t.Fatalf("proposta = %+v", prop)
	}
	sum := h.wa.last().Text
	for _, want := range []string{"raio de 17 km", "seguidores no Instagram", "só no Instagram", "a partir de 17 km"} {
		if !strings.Contains(sum, want) {
			t.Errorf("resumo sem %q:\n%s", want, sum)
		}
	}
}
