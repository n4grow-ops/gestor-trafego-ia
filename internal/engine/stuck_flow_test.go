package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fullProfile é a ficha do negócio completa, como fica depois do onboarding.
func fullProfile() contract.Profile {
	s := contract.Str
	return contract.Profile{BusinessName: s("Método v3"), City: s("Maravilha"), Offer: s("consultoria de marketing"),
		Audience: s("empresários"), AudienceAge: s("27 a 50"), AudienceGender: s("todos"), ServiceArea: s("Brasil"),
		Differentials: s("método próprio"), AverageTicket: s("1.200"), SalesChannel: s("WhatsApp")}
}

// Caso real (conversa 51): a ficha tem o que a empresa faz, a Mia não pergunta de novo (a regra manda não
// repetir o que está na memória) e o campo offer da campanha ficava vazio para sempre. A oferta da ficha
// tem que valer como padrão da campanha.
func TestCampaignUsesOfferFromProfile(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.UpdateOrgProfile(ctx, h.ident().OrgID, fullProfile()))
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"daily_budget_brl":50},"reply":"Perfeito! O especialista vai montar a proposta."}`}
	h.say("quero anunciar, 50 por dia")
	if c := h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("com a ficha completa e o orçamento, a proposta tinha que ser pedida; estado = %s campos = %+v", c.State, c.Fields)
	}
	if h.pendingJobs(engine.JobPropose) != 1 {
		t.Error("job da proposta não foi para a fila")
	}
}

// Caso real (conversa 41): a IA não gravou um item da ficha e o cliente ficou preso no onboarding, que só
// conversa. Quem já quer anunciar segue para a campanha; o que faltar na ficha a Mia pergunta depois.
func TestOnboardingDoesNotBlockCampaign(t *testing.T) {
	h := newHarness(t, "owner")
	p := fullProfile()
	p.Offer = nil
	h.must(h.st.UpdateOrgProfile(ctx, h.ident().OrgID, p))
	c := h.conv()
	h.must(h.st.UpdateConversation(ctx, c.ID, store.StateOnboarding, c.Fields))
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"seguidores no Instagram","daily_budget_brl":10},"reply":"Recebi os dados."}`}
	h.say("quero anunciar para ganhar seguidores, 10 por dia")
	c = h.conv()
	if c.State != store.StateAwaitingProposal {
		t.Fatalf("pedido de anúncio com tudo preenchido ficou preso; estado = %s campos = %+v", c.State, c.Fields)
	}
}

// A Mia não pode dizer que a proposta está a caminho quando o sistema não pediu a proposta: o cliente fica
// esperando algo que nunca chega. Nesse caso a resposta vira a pergunta do que falta.
func TestNoFalseProposalPromise(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"pizza"},"reply":"Perfeito! O especialista vai montar a proposta e ela chega em instantes."}`}
	h.say("quero anunciar pizza")
	got := h.wa.last().Text
	if strings.Contains(got, "chega em instantes") || strings.Contains(strings.ToLower(got), "especialista vai montar") {
		t.Fatalf("prometeu proposta sem pedir: %q", got)
	}
	if !strings.Contains(got, "orçamento por dia") {
		t.Errorf("deveria perguntar o que falta (orçamento); resposta = %q", got)
	}
	if c := h.conv(); c.State != store.StateCollecting {
		t.Errorf("estado = %s", c.State)
	}
}
