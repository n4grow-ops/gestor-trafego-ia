package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Cliente pediu campanha de formulário: mesmo numa organização que só tinha mensagens liberadas, a proposta
// vira contatos por formulário e a publicação leva o formulário instantâneo.
func TestLeadFormCampaign(t *testing.T) {
	h, _, pub := newPublishHarness(t, "owner")
	h.eng.LeadPrivacyURL = "https://meuanuncio.io/privacidade"
	h.connectMeta()
	h.att.Responses = []string{turnOffer, `{"intent":"provide_info","fields":{"audience":"famílias","location":"Campinas","daily_budget_brl":50,"objective_hint":"formulário"},"reply":"Beleza, formulário."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia, campanha de formulário")
	h.runJobs()
	summary := h.wa.last().Text
	if !strings.Contains(summary, "contatos por formulário") {
		t.Fatalf("proposta não mostra o formulário:\n%s", summary)
	}
	m := codeRe.FindStringSubmatch(summary)
	if m == nil {
		t.Fatalf("sem código: %q", summary)
	}
	h.say("SIM " + m[1])
	h.sendFile(square)
	h.say("pronto")
	if h.conv().State != store.StateIdle {
		t.Fatalf("estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 1 {
		t.Fatalf("publicações = %d", pub.calls())
	}
	in := pub.inputs[0]
	if in.Objective != "OUTCOME_LEADS" || !in.LeadForm || in.FormPrivacyURL != "https://meuanuncio.io/privacidade" {
		t.Fatalf("entrada = %+v", in)
	}
}
