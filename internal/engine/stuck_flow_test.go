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
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"location":"Maravilha","daily_budget_brl":50},"reply":"Perfeito! O especialista vai montar a proposta."}`}
	h.say("quero anunciar em Maravilha, 50 por dia")
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
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"seguidores no Instagram","location":"Maravilha","daily_budget_brl":10},"reply":"Recebi os dados."}`}
	h.say("quero anunciar para ganhar seguidores em Maravilha, 10 por dia")
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

// "O especialista vai montar" sem a proposta pedida também é promessa falsa (a regra tinha um caractere de
// controle no lugar do \b e essa parte nunca casava).
func TestNoFalsePromiseEspecialistaVai(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"pizza"},"reply":"Ótimo! O especialista vai montar tudo pra você."}`}
	h.say("quero anunciar pizza")
	if got := h.wa.last().Text; strings.Contains(got, "especialista vai") {
		t.Fatalf("prometeu proposta sem pedir: %q", got)
	}
}

// A ficha do negócio é opcional: com a ficha vazia, qualquer mensagem tira o cliente do onboarding e a
// campanha segue só com oferta, local e orçamento.
func TestEmptyProfileDoesNotBlockOnboarding(t *testing.T) {
	h := newHarness(t, "owner")
	c := h.conv()
	h.must(h.st.UpdateConversation(ctx, c.ID, store.StateOnboarding, c.Fields))
	h.att.Responses = []string{`{"intent":"question","reply":"Oi! Me conta o que quer anunciar."}`}
	h.say("oi")
	if c = h.conv(); c.State != store.StateIdle {
		t.Fatalf("ficha vazia não pode manter o onboarding; estado = %s", c.State)
	}
	// append: o fake devolve as respostas por índice de chamada; reatribuir a lista faria a 2ª chamada cair no padrão.
	h.att.Responses = append(h.att.Responses, `{"intent":"create_campaign","fields":{"offer":"pizza","audience":"moradores","location":"Campinas","daily_budget_brl":20},"reply":"Anotado."}`)
	h.say("pizza em Campinas, 20 por dia")
	if c = h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("campanha com os dados mínimos deve seguir sem ficha; estado = %s campos = %+v", c.State, c.Fields)
	}
}

// Pedido do conselho do Meu Anúncio: a proposta não sai direto com a cidade da ficha. A Mia conversa e
// pergunta a região desta campanha; "sim" à sugestão dela confirma a cidade da ficha.
func TestRegionIsAskedNotTakenFromProfile(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.UpdateOrgProfile(ctx, h.ident().OrgID, fullProfile()))
	h.att.Responses = []string{
		`{"intent":"create_campaign","fields":{"daily_budget_brl":30,"location":"Maravilha"},"reply":"Boa! Maravilha mesmo ou outra região?"}`,
		`{"intent":"provide_info","fields":{"location":"Maravilha"},"reply":"Fechado."}`,
	}
	h.say("quero uma campanha de formulário, 30 reais por dia")
	if c := h.conv(); c.State != store.StateCollecting || c.Fields.Location != nil {
		t.Fatalf("região veio da ficha sem o cliente dizer; estado = %s campos = %+v", c.State, c.Fields)
	}
	if h.pendingJobs(engine.JobPropose) != 0 {
		t.Fatal("proposta pedida sem a região")
	}
	h.say("sim, pode ser")
	if c := h.conv(); c.State != store.StateAwaitingProposal || c.Fields.Location == nil || *c.Fields.Location != "Maravilha" {
		t.Fatalf("confirmação da cidade sugerida não valeu; estado = %s campos = %+v", c.State, c.Fields)
	}
}
