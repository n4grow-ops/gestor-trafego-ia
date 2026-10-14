package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

// A conexão com a Meta vence (~60 dias). 7 dias antes, o cliente recebe o link de reconexão e a equipe é
// avisada, uma vez; quando vence, um aviso a mais.
func TestTokenExpiringWarnsOnce(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.connectMeta()
	h.say("oi") // abre a conversa
	h.att.Responses = nil
	_, err := h.pool.Exec(ctx, `UPDATE meta_connections SET token_expires_at = $1`, h.now.Add(5*24*time.Hour))
	h.must(err)
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobTokenCheck)))
	h.runJobs()
	got := h.sentText()
	if !strings.Contains(got, "vence em") || !strings.Contains(got, metaBase+"/") {
		t.Fatalf("aviso sem prazo ou link: %q", got)
	}
	n := h.wa.count()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobTokenCheck)))
	h.runJobs()
	if h.wa.count() != n {
		t.Fatal("avisou duas vezes antes de vencer")
	}
	h.now = h.now.Add(6 * 24 * time.Hour) // venceu
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobTokenCheck)))
	h.runJobs()
	if h.wa.count() == n || !strings.Contains(h.wa.last().Text, "venceu") {
		t.Fatalf("sem aviso de vencido: %q", h.wa.last().Text)
	}
}

// Conexão antiga (sem validade gravada): a checagem diária lê na Meta e grava.
func TestTokenCheckFillsOldConnections(t *testing.T) {
	h, _, pub := newPublishHarness(t, "owner")
	pub.expiry = h.now.Add(40 * 24 * time.Hour)
	h.connectMeta()
	h.must(h.eng.HandleJob(ctx, jobOf(engine.JobTokenCheck)))
	var at *time.Time
	h.must(h.pool.QueryRow(ctx, `SELECT token_expires_at FROM meta_connections WHERE status = 'active'`).Scan(&at))
	if at == nil || !at.Equal(pub.expiry) {
		t.Fatalf("validade = %v", at)
	}
}
