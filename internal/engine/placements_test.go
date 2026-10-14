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
	if strings.Contains(ask, "9:16") || strings.Contains(ask, "4:5") || !strings.Contains(ask, "que quiser usar") {
		t.Errorf("o pedido não pode exigir formato: %q", ask)
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

// Caso real (conversa 51): no ajuste da proposta a IA escreveu "Pode confirmar com *SIM 250532*", um código
// que não valia, e em outro ajuste "Proposta aprovada!" sem o cliente ter aprovado. O código só sai no
// resumo oficial; a fala da IA nunca leva código nem diz que aprovou.
func TestAttendantNeverWritesCodeOrApproval(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	before := h.wa.count()
	h.att.Responses = append(h.att.Responses, `{"intent":"provide_info","fields":{"audience":"mulheres de 18 a 30 anos"},"reply":"Entendido! Vou ajustar a idade para 18 a 30 anos. Proposta aprovada! Pode confirmar com *SIM 250532* para ativar a campanha."}`)
	h.mgr.Responses = append(h.mgr.Responses, validProposal)
	h.say("quero a idade de 18 a 30")
	h.wa.mu.Lock()
	lead := h.wa.sent[before:]
	h.wa.mu.Unlock()
	if len(lead) == 0 {
		t.Fatal("nenhuma resposta")
	}
	for _, s := range lead {
		if strings.Contains(s.Text, "250532") || strings.Contains(strings.ToLower(s.Text), "aprovada") {
			t.Fatalf("fala da IA com código ou aprovação falsa: %q", s.Text)
		}
	}
	if !strings.Contains(lead[0].Text, "ajustar a idade") {
		t.Errorf("o resto da fala devia ficar: %q", lead[0].Text)
	}
	h.runJobs()
	if m := codeRe.FindStringSubmatch(h.wa.last().Text); m == nil || m[1] == "250532" {
		t.Fatalf("resumo oficial sem código novo: %q", h.wa.last().Text)
	}
}
