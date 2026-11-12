package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Cliente novo no WhatsApp: avisa "cliente chamou" uma vez e "mensagem" a cada mensagem.
func TestInboundEmitsClientEvents(t *testing.T) {
	h := newHarness(t, "owner")
	h.eng.AutoSignup, h.eng.SignupMaxBudget, h.eng.SignupDailyLimit = true, 50, 60
	_, err := h.st.CreateNotifyDestination(ctx, store.NotifyDestinationInput{Name: "celular", Kind: "pushcut",
		URL: "https://api.pushcut.io/x/notifications/V", Events: []string{"cliente.novo", "cliente.mensagem"}, Active: true})
	h.must(err)
	h.att.Responses = []string{
		`{"intent":"other","fields":{},"profile":{},"reply":"Oi! Qual o seu negócio?"}`,
		`{"intent":"other","fields":{},"profile":{},"reply":"Legal!"}`,
	}
	h.sayFrom(newPhone, "oi, quero anunciar")
	h.sayFrom(newPhone, "tenho uma pizzaria")
	count := func(ev string) (int, string) {
		var n int
		var last string
		h.must(h.pool.QueryRow(ctx, `SELECT count(*), coalesce(max(payload::text), '') FROM outbox_jobs WHERE kind = 'notify.deliver' AND payload->>'event' = $1`, ev).Scan(&n, &last))
		return n, last
	}
	if n, p := count("cliente.novo"); n != 1 || !strings.Contains(p, newPhone) || !strings.Contains(p, "quero anunciar") {
		t.Fatalf("cliente.novo = %d %s", n, p)
	}
	if n, _ := count("cliente.mensagem"); n != 2 {
		t.Fatalf("cliente.mensagem = %d", n)
	}
}
