package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

func TestNextReportAt(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	for now, want := range map[string]string{
		"2026-09-24T07:30": "2026-09-24T08:00",
		"2026-09-24T08:00": "2026-09-25T08:00",
		"2026-09-24T15:00": "2026-09-25T08:00",
	} {
		n, _ := time.ParseInLocation("2006-01-02T15:04", now, brt)
		w, _ := time.ParseInLocation("2006-01-02T15:04", want, brt)
		if got := engine.NextReportAt(n); !got.Equal(w) {
			t.Errorf("NextReportAt(%s) = %s; want %s", now, got.In(brt), want)
		}
	}
}

func TestDailyReport(t *testing.T) {
	h := newHarness(t, "owner")
	h.eng.ReportPhone = "5511900000000"
	h.must(h.eng.EnsureDailyReport(ctx))
	h.must(h.eng.EnsureDailyReport(ctx))
	if n := h.pendingJobs("report.daily"); n != 1 {
		t.Fatalf("relatórios agendados = %d", n)
	}
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE kind = 'report.daily'`)
	h.must(err)
	h.runJobs()
	sent := h.wa.to(operatorChat)
	if len(sent) != 1 || !strings.Contains(sent[0], "Cadastros concluídos") || !strings.Contains(sent[0], "Mensagens recebidas: 1") {
		t.Fatalf("relatório = %q", sent)
	}
	if n := h.pendingJobs("report.daily"); n != 1 {
		t.Fatalf("próximo relatório agendado = %d", n)
	}
}

func TestDailyReportOffWithoutPhone(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.eng.EnsureDailyReport(ctx))
	if h.pendingJobs("report.daily") != 0 {
		t.Fatal("sem número não agenda relatório")
	}
}
