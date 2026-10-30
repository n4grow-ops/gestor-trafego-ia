package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// healthWA é o fakeWA com a leitura da situação do número na Meta.
type healthWA struct {
	*fakeWA
	h whatsapp.Health
}

func (w *healthWA) Health(context.Context) (whatsapp.Health, bool, error) { return w.h, true, nil }

type fakeMailer struct {
	to       [][]string
	subjects []string
	texts    []string
}

func (m *fakeMailer) Send(_ context.Context, to []string, subject, text string) error {
	m.to, m.subjects, m.texts = append(m.to, to), append(m.subjects, subject), append(m.texts, text)
	return nil
}

func TestWAHealthBlockedGoesToPanelAndEmail(t *testing.T) {
	h := newHarness(t, "owner")
	wa := &healthWA{fakeWA: h.wa, h: whatsapp.Health{Blocked: true, Problems: []whatsapp.HealthProblem{
		{Entity: "WABA", Code: 141006, Description: "There is an error with the payment method."}}}}
	mailer := &fakeMailer{}
	h.eng.WA, h.eng.Mailer = wa, mailer
	_, err := h.st.AddTeamMember(ctx, "chefe@meuanuncio.io", "Chefe", "admin", "teste")
	h.must(err)
	_, err = h.st.AddTeamMember(ctx, "bia@meuanuncio.io", "Bia", "staff", "teste")
	h.must(err)

	h.must(h.eng.EnsureWAHealth(ctx))
	h.runJobs()
	st, err := h.st.WhatsAppStatus(ctx, engine.WAHealthProvider)
	h.must(err)
	if st.Connected || st.Code != 141006 || !strings.Contains(st.Reason, "forma de pagamento") {
		t.Fatalf("status = %+v", st)
	}
	if len(mailer.to) != 1 || len(mailer.to[0]) != 1 || mailer.to[0][0] != "chefe@meuanuncio.io" ||
		!strings.Contains(mailer.subjects[0], "bloqueou") || !strings.Contains(mailer.texts[0], "forma de pagamento") {
		t.Fatalf("e-mail = %v %v %v", mailer.to, mailer.subjects, mailer.texts)
	}
	// A próxima leitura fica agendada.
	if pending, err := h.st.HasPendingKind(ctx, engine.JobWAHealth); err != nil || !pending {
		t.Fatalf("sem próxima leitura: %v %v", pending, err)
	}

	// Mesmo bloqueio de novo: sem e-mail repetido. Liberou: e-mail de volta.
	_, err = h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() - interval '1 second' WHERE status = 'pending'`)
	h.must(err)
	h.runJobs()
	if len(mailer.to) != 1 {
		t.Fatalf("e-mail repetido: %v", mailer.subjects)
	}
	wa.h = whatsapp.Health{}
	_, err = h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() - interval '1 second' WHERE status = 'pending'`)
	h.must(err)
	h.runJobs()
	if len(mailer.subjects) != 2 || !strings.Contains(mailer.subjects[1], "voltou") {
		t.Fatalf("e-mail de volta = %v", mailer.subjects)
	}
}
