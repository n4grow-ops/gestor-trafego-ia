package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Anúncio reprovado pela Meta: a campanha pausa, o cliente recebe o motivo e volta para a etapa dos
// anúncios; com a imagem nova e o PRONTO, a publicação é refeita no mesmo rascunho aprovado.
func TestRejectedAdReopensAndRepublishes(t *testing.T) {
	h, pubs, pub := newPublishHarness(t, "owner")
	h.connectMeta()
	h.readyProject()
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if h.pendingJobs(engine.JobAdsReview) != 1 {
		t.Fatal("revisão não foi agendada depois de publicar")
	}
	pub.reviews = []meta.AdReview{{ID: "A1", EffectiveStatus: "DISAPPROVED", Reasons: []string{"Imagem com texto demais."}}}
	h.runKind(engine.JobAdsReview)
	if p := pubs.latest(); p.Status != "rejected" || !strings.Contains(p.Error, "texto demais") {
		t.Fatalf("publicação = %+v", p)
	}
	if !slices.Contains(pub.statuses, "C1:PAUSED") {
		t.Fatalf("campanha não foi pausada: %v", pub.statuses)
	}
	sent := h.wa.to(chatID)
	msg := sent[len(sent)-1]
	if !strings.Contains(msg, "reprovou") || !strings.Contains(msg, "Imagem com texto demais.") || !strings.Contains(msg, "PRONTO") {
		t.Fatalf("mensagem = %q", msg)
	}
	if c := h.conv(); c.State != store.StateAwaitingCreatives {
		t.Fatalf("estado = %s", c.State)
	}
	if h.creatives() != 0 {
		t.Fatal("anúncio reprovado continuou no rascunho")
	}
	pub.reviews = nil
	h.sendFile(square)
	h.say("pronto")
	h.say("pronto")
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 2 || pubs.latest().Status != "paused" {
		t.Fatalf("não republicou: calls=%d status=%s", pub.calls(), pubs.latest().Status)
	}
}

// Em análise: confere de novo mais tarde; aprovado: para de conferir.
func TestAdReviewPendingReschedules(t *testing.T) {
	h, _, pub := newPublishHarness(t, "owner")
	h.connectMeta()
	h.readyProject()
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	pub.reviews = []meta.AdReview{{ID: "A1", EffectiveStatus: "PENDING_REVIEW"}}
	h.runKind(engine.JobAdsReview)
	if h.pendingJobs(engine.JobAdsReview) != 1 {
		t.Fatal("em análise devia conferir de novo")
	}
	pub.reviews = []meta.AdReview{{ID: "A1", EffectiveStatus: "PAUSED"}}
	h.runKind(engine.JobAdsReview)
	if h.pendingJobs(engine.JobAdsReview) != 0 {
		t.Fatal("aprovado não precisa conferir de novo")
	}
}
