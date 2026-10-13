package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Caso real (conversa 51): a ficha é de consultoria de marketing e o cliente pediu um anúncio de imóvel.
// A proposta saiu falando da consultoria, com o público e a idade da ficha. O cliente pode anunciar
// qualquer coisa: quando a oferta da campanha é outra, a ficha não define público nem textos.
func TestOtherProductBriefingIgnoresProfile(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.UpdateOrgProfile(ctx, h.ident().OrgID, fullProfile()))
	h.att.Responses = []string{`{"intent":"new_project","project":"Imóvel","fields":{"offer":"imóvel à venda","audience":"homens e mulheres de 30 a 60 anos","location":"Maravilha, raio de 10 km","daily_budget_brl":50},"reply":"Vou pedir a proposta."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar um imóvel na minha cidade, raio de 10km, homens e mulheres de 30 a 60 anos, 50 por dia")
	if c := h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("estado = %s campos = %+v", c.State, c.Fields)
	}
	h.runJobs()
	if len(h.mgr.Requests) == 0 {
		t.Fatal("especialista não foi chamado")
	}
	req := h.mgr.Requests[0]
	var all strings.Builder
	for _, m := range req.Messages {
		all.WriteString(m.Content + "\n")
	}
	got := all.String()
	for _, banned := range []string{"consultoria de marketing", "empresários", "27 a 50", "método próprio"} {
		if strings.Contains(strings.ToLower(got), banned) {
			t.Errorf("briefing de imóvel levou %q da ficha da consultoria", banned)
		}
	}
	if !strings.Contains(got, "imóvel à venda") || !strings.Contains(got, "30 a 60 anos") || !strings.Contains(got, "raio de 10 km") {
		t.Errorf("briefing perdeu os dados da campanha: %s", got)
	}
}

// Campanha de outro produto sem público informado: a Mia pergunta o público em vez de usar o da ficha.
func TestOtherProductAsksAudience(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.UpdateOrgProfile(ctx, h.ident().OrgID, fullProfile()))
	h.att.Responses = []string{`{"intent":"new_project","project":"Imóvel","fields":{"offer":"imóvel à venda","daily_budget_brl":50},"reply":"Certo."}`}
	h.say("quero anunciar um imóvel, 50 por dia")
	c := h.conv()
	if c.State != store.StateCollecting {
		t.Fatalf("sem público, não podia pedir proposta; estado = %s campos = %+v", c.State, c.Fields)
	}
	if c.Fields.Audience != nil {
		t.Errorf("público da ficha (consultoria) entrou na campanha do imóvel: %q", *c.Fields.Audience)
	}
	if h.pendingJobs(engine.JobPropose) != 0 {
		t.Error("proposta pedida sem público")
	}
}

// A campanha do produto principal continua usando a ficha inteira.
func TestMainProductBriefingKeepsProfile(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.UpdateOrgProfile(ctx, h.ident().OrgID, fullProfile()))
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"daily_budget_brl":50},"reply":"Vou pedir a proposta."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar, 50 por dia")
	h.runJobs()
	if len(h.mgr.Requests) == 0 {
		t.Fatal("especialista não foi chamado")
	}
	got := h.mgr.Requests[0].Messages[len(h.mgr.Requests[0].Messages)-1].Content
	if !strings.Contains(got, "método próprio") || !strings.Contains(got, "27 a 50") {
		t.Errorf("campanha do produto principal perdeu a ficha: %s", got)
	}
}
