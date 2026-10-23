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
	for _, want := range []string{"Maravilha + 17 km em volta", "seguidores no Instagram", "só no Instagram", "a partir de 17 km"} {
		if !strings.Contains(sum, want) {
			t.Errorf("resumo sem %q:\n%s", want, sum)
		}
	}
}

// Vários lugares, cada um com o seu raio, e tipo de campanha fora do que a organização tinha na lista.
func TestManyPlacesAndAnyGoal(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"loja de roupas","audience":"mães de bebê","location":"Maravilha, SC + 20 km; Chapecó, SC; pino -26.761234,-53.173456 + 3 km","daily_budget_brl":40,"objective_hint":"site","link_url":"https://loja.com.br"},"reply":"Fechado."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("campanha pro site em Maravilha 20 km, Chapecó e no pino, 40 por dia")
	if c := h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("estado = %s campos = %+v", c.State, c.Fields)
	}
	h.runJobs()
	var prop contract.CampaignProposal
	h.must(h.pool.QueryRow(ctx, `SELECT proposal FROM campaign_drafts ORDER BY id DESC LIMIT 1`).Scan(&prop))
	if prop.Goal != contract.GoalSite || prop.Objective != "OUTCOME_TRAFFIC" || prop.Audience.RadiusKM != 0 {
		t.Fatalf("proposta = %+v", prop)
	}
	sum := h.wa.last().Text
	for _, want := range []string{"visitas ao seu site", "Maravilha, SC + 20 km em volta; Chapecó, SC (só a cidade); pino em localização enviada (-26.76123, -53.17346), raio de 3 km", "Interesses da Meta: pizza"} {
		if !strings.Contains(sum, want) {
			t.Errorf("resumo sem %q:\n%s", want, sum)
		}
	}
}

// Campanha para o site sem link: a Mia pede o link antes da proposta.
func TestSiteGoalAsksForLink(t *testing.T) {
	f := contract.Fields{Offer: contract.Str("x"), Audience: contract.Str("y"), Location: contract.Str("Chapecó"), DailyBudgetBRL: contract.Num(20), ObjectiveHint: contract.Str("site")}
	if m := f.Missing(); len(m) != 1 || m[0] != "link_url" {
		t.Fatalf("faltando = %v", m)
	}
}
