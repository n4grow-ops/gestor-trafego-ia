package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

func TestReminderAtSkipsNight(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	cases := []struct{ now, want string }{
		{"2026-09-24T10:00", "2026-09-24T13:00"}, // 10h + 3h = 13h
		{"2026-09-24T19:30", "2026-09-25T09:00"}, // cairia 22h30: vai pras 9h
		{"2026-09-24T02:00", "2026-09-24T09:00"}, // cairia 5h: vai pras 9h
	}
	for _, c := range cases {
		now, _ := time.ParseInLocation("2006-01-02T15:04", c.now, brt)
		want, _ := time.ParseInLocation("2006-01-02T15:04", c.want, brt)
		if got := engine.ReminderAt(now, 3*time.Hour); !got.Equal(want) {
			t.Errorf("ReminderAt(%s) = %s; want %s", c.now, got.In(brt), c.want)
		}
	}
}

func (h *harness) runRemindJobs() {
	h.t.Helper()
	_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE status = 'pending' AND kind = 'signup.remind'`)
	h.must(err)
	h.runJobs()
}

func TestSignupReminders(t *testing.T) {
	h := newSignupHarness(t)
	h.say("oi")
	if n := h.pendingJobs("signup.remind"); n != 1 {
		t.Fatalf("lembrete agendado = %d", n)
	}
	// nova mensagem depois de 10 min gera outro link, mas não outro lembrete
	_, err := h.pool.Exec(ctx, `UPDATE signup_links SET created_at = now() - interval '11 minutes'`)
	h.must(err)
	h.say("oi de novo")
	if n := h.pendingJobs("signup.remind"); n != 1 {
		t.Fatalf("lembretes duplicados = %d", n)
	}
	sent := h.wa.count()
	h.runRemindJobs()
	if h.wa.count() != sent+1 || !strings.Contains(h.wa.last().Text, "Faltou pouquinho") || !strings.Contains(h.wa.last().Text, signupBase+"/") {
		t.Fatalf("1º lembrete = %q", h.wa.last().Text)
	}
	if n := h.pendingJobs("signup.remind"); n != 1 {
		t.Fatalf("2º lembrete agendado = %d", n)
	}
	h.runRemindJobs()
	if h.wa.count() != sent+2 || h.pendingJobs("signup.remind") != 0 {
		t.Fatalf("2º lembrete: enviados = %d pendentes = %d", h.wa.count()-sent, h.pendingJobs("signup.remind"))
	}
}

func TestReminderSkippedAfterSignup(t *testing.T) {
	h := newSignupHarness(t)
	h.say("oi")
	h.complete()
	sent := h.wa.count()
	h.runRemindJobs()
	if h.wa.count() != sent || h.pendingJobs("signup.remind") != 0 {
		t.Fatal("cadastro concluído não recebe lembrete")
	}
}
