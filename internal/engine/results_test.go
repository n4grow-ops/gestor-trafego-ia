package engine_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Resultado diário às 8h: gasto, conversas e custo por conversa de ontem, gravados e enviados ao cliente.
func TestDailyResults(t *testing.T) {
	h, pub, _ := publishWith(t, &meta.Funding{Status: 1, Prepaid: true, HasSource: true, SourceType: 20, AvailableCents: 8000})
	h.say("ATIVAR")
	pub.insights = meta.Insights{SpendCents: 3500, Impressions: 2100, Reach: 1300, Conversations: 5}
	n := h.wa.count()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobDailyResults)))
	h.runJobs()
	if h.wa.count() == n {
		t.Fatal("sem mensagem de resultado")
	}
	sent := h.wa.to(chatID)
	got := sent[len(sent)-1]
	for _, want := range []string{"R$ 35,00", "5 conversas", "R$ 7,00 por conversa", "R$ 80,00"} {
		if !strings.Contains(got, want) {
			t.Fatalf("resultado sem %q: %q", want, got)
		}
	}
	if h.pendingJobs(engine.JobDailyResults) != 1 {
		t.Fatal("próximo resultado não foi agendado")
	}
}

// Dia sem gasto não gera mensagem.
func TestDailyResultsSkipsZeroSpend(t *testing.T) {
	h, _, _ := publishWith(t, &meta.Funding{Status: 1, HasSource: true, AvailableCents: -1})
	h.say("ATIVAR")
	n := h.wa.count()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobDailyResults)))
	h.runJobs()
	if h.wa.count() != n {
		t.Fatalf("mandou resultado sem gasto: %q", h.wa.last().Text)
	}
}

// Equipe pausa pelo painel: campanha pausada na Meta, publicação pausada e o cliente avisado.
func TestTeamPause(t *testing.T) {
	h, pub, _ := publishWith(t, &meta.Funding{Status: 1, HasSource: true, AvailableCents: -1})
	h.say("ATIVAR")
	pubID := int64(1)
	h.must(h.st.Enqueue(ctx, store.JobSpec{Kind: engine.JobTeamPause, Payload: engine.TeamPauseJob{PublicationID: pubID, By: "dono@meuanuncio.io"}}))
	h.runJobs()
	if !slices.Contains(pub.statuses, "C1:PAUSED") {
		t.Fatalf("statuses = %v", pub.statuses)
	}
	sent := h.wa.to(chatID)
	if got := sent[len(sent)-1]; !strings.Contains(got, "equipe do meu anúncio pausou") || !strings.Contains(got, "ATIVAR") {
		t.Fatalf("aviso = %q", got)
	}
}
