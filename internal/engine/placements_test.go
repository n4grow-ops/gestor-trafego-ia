package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Cliente pediu "só no feed": a proposta mostra, a etapa dos anúncios não cobra o vertical dos Stories e a
// campanha sobe só no feed.
func TestFeedOnlyPlacement(t *testing.T) {
	h, _, pub := newPublishHarness(t, "owner")
	h.connectMeta()
	h.att.Responses = []string{turnOffer, `{"intent":"provide_info","fields":{"audience":"famílias","location":"Campinas","daily_budget_brl":50,"placements":["Feed"]},"reply":"Certo, só no feed."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia, só no feed")
	h.runJobs()
	summary := h.wa.last().Text
	if !strings.Contains(summary, "Onde aparece: só no Feed") {
		t.Fatalf("proposta não mostra o posicionamento pedido:\n%s", summary)
	}
	m := codeRe.FindStringSubmatch(summary)
	if m == nil {
		t.Fatalf("sem código: %q", summary)
	}
	h.say("SIM " + m[1])
	ask := h.wa.last().Text
	if strings.Contains(ask, "9:16") || !strings.Contains(ask, "4:5") {
		t.Errorf("só feed não pode pedir vertical de Stories: %q", ask)
	}
	h.sendFile(square)
	if st := h.wa.last().Text; strings.Contains(st, "Stories") {
		t.Errorf("status cobra Stories em campanha só de feed: %q", st)
	}
	h.say("pronto")
	if h.conv().State != store.StateIdle {
		t.Fatalf("PRONTO com o feed coberto devia fechar; estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 1 || !slices.Equal(pub.inputs[0].Placements, []string{"feed"}) {
		t.Fatalf("publicação: calls=%d placements=%v", pub.calls(), pub.inputs)
	}
}

// Sem pedido do cliente fica automático, como sempre foi.
func TestPlacementDefaultsToAutomatic(t *testing.T) {
	h, _, pub := newPublishHarness(t, "owner")
	h.connectMeta()
	h.readyProject()
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 1 || len(pub.inputs[0].Placements) != 0 {
		t.Fatalf("placements = %v", pub.inputs[0].Placements)
	}
}

// O texto do sistema da Atendente ensina a gravar o posicionamento pedido.
func TestAttendantKnowsPlacements(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"other","reply":"oi"}`}
	h.say("oi")
	sys := h.att.Requests[0].Messages[0].Content
	if !strings.Contains(sys, "placements") || !strings.Contains(sys, `"feed"`) {
		t.Fatal("prompt da Atendente sem placements")
	}
}
