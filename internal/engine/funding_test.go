package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// publishWith sobe o projeto pronto com a conta pagando do jeito dado e devolve a mensagem da publicação.
func publishWith(t *testing.T, f *meta.Funding) (*harness, *fakePublisher, string) {
	t.Helper()
	h, _, pub := newPublishHarness(t, "owner")
	pub.funding = f
	h.connectMeta()
	h.readyProject() // proposta de R$ 40,00 por dia
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 1 {
		t.Fatalf("Publish chamado %d vezes", pub.calls())
	}
	return h, pub, strings.Join(h.wa.to(chatID), "\n")
}

func TestPublishShowsPrepaidBalance(t *testing.T) {
	_, _, msg := publishWith(t, &meta.Funding{Status: 1, Prepaid: true, HasSource: true, SourceType: 20, AvailableCents: 20000})
	if !strings.Contains(msg, "Saldo disponível na conta: R$ 200,00") || !strings.Contains(msg, "5 dias") {
		t.Fatalf("mensagem = %q", msg)
	}
	if strings.Contains(msg, "billing_hub") {
		t.Error("saldo suficiente não precisa do link de pagamento")
	}
}

func TestPublishWarnsWithoutFunds(t *testing.T) {
	_, _, msg := publishWith(t, &meta.Funding{Status: 1, Prepaid: true, AvailableCents: -1})
	if !strings.Contains(msg, "não tem saldo nem forma de pagamento") || !strings.Contains(msg, engine.BillingURL("act_1")) {
		t.Fatalf("mensagem = %q", msg)
	}
}

func TestPublishWarnsLowBalance(t *testing.T) {
	_, _, msg := publishWith(t, &meta.Funding{Status: 1, Prepaid: true, HasSource: true, SourceType: 20, AvailableCents: 1500})
	if !strings.Contains(msg, "R$ 15,00") || !strings.Contains(msg, "menos que um dia") || !strings.Contains(msg, engine.BillingURL("act_1")) {
		t.Fatalf("mensagem = %q", msg)
	}
}

func TestPublishShowsCardAndCap(t *testing.T) {
	_, _, msg := publishWith(t, &meta.Funding{Status: 1, HasSource: true, SourceType: 1, Source: "Visa *1234", AvailableCents: -1,
		SpendCapCents: 10000, SpentCents: 8000})
	if !strings.Contains(msg, "Visa *1234") || !strings.Contains(msg, "limite de gastos") || !strings.Contains(msg, "R$ 20,00") {
		t.Fatalf("mensagem = %q", msg)
	}
}

// Meta sem resposta (sem permissão de ler pagamento): a publicação segue e a mensagem não inventa saldo.
func TestPublishFundingUnavailable(t *testing.T) {
	_, _, msg := publishWith(t, nil)
	if !strings.Contains(msg, "*pausada*") || strings.Contains(msg, "Saldo") {
		t.Fatalf("mensagem = %q", msg)
	}
}

// ATIVAR sem saldo: liga a campanha e avisa que ela só roda depois do saldo, com o link.
func TestActivateWarnsWithoutFunds(t *testing.T) {
	h, pub, _ := publishWith(t, &meta.Funding{Status: 1, Prepaid: true, AvailableCents: -1})
	h.say("ATIVAR")
	msg := h.wa.last().Text
	if len(pub.statuses) != 1 || !strings.Contains(msg, "No ar") || !strings.Contains(msg, "não tem saldo nem forma de pagamento") {
		t.Fatalf("statuses = %v mensagem = %q", pub.statuses, msg)
	}
}
