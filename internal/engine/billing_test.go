package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

const billingURL = "https://business.facebook.com/billing_hub/payment_settings/?asset_id=1"

// Caso real (conversa 51): "Preciso depositar saldo" recebeu um "acessa o Gerenciador" genérico. Agora vai o
// link de pagamento da própria conta de anúncios, com o passo a passo de cartão e Pix, sem gastar IA.
func TestBillingCommandSendsLinkAndTutorial(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.connectMeta()
	for _, frase := range []string{"Preciso depositar saldo", "quero adicionar meu cartão", "faltou saldo", "como coloco crédito na conta de anúncios?"} {
		before := h.wa.count()
		h.say(frase)
		got := h.wa.last().Text
		if h.wa.count() == before || !strings.Contains(got, billingURL) || !strings.Contains(got, "Adicionar forma de pagamento") ||
			!strings.Contains(got, "Pix") || !strings.Contains(got, "Loja") {
			t.Fatalf("%q: resposta = %q", frase, got)
		}
	}
	if h.att.Calls() != 0 {
		t.Fatal("pedido de pagamento não deve chamar a IA")
	}
}

// Sem conta conectada não há onde pagar: primeiro vai o link de conexão.
func TestBillingWithoutConnectionSendsConnectLink(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.say("quero adicionar meu cartão")
	if got := h.sentText(); !strings.Contains(got, metaBase+"/") || strings.Contains(got, "billing_hub") {
		t.Fatalf("resposta = %q", got)
	}
}

// Frase longa vai pela IA, que reconhece o pedido (intent billing) e o sistema manda o link.
func TestBillingViaAttendant(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.connectMeta()
	h.att.Responses = []string{`{"intent":"billing","reply":"Claro, te mando o link pra pagar."}`}
	h.say("oi tudo bem, então a Meta me mandou um aviso que o meu pagamento não passou, o que eu faço agora?")
	if got := h.sentText(); !strings.Contains(got, billingURL) {
		t.Fatalf("resposta = %q", got)
	}
}

// Conexão com pagamento pendente já leva o link e o passo a passo junto do aviso.
func TestMetaConnectedPendingPaymentHasBillingLink(t *testing.T) {
	job := engine.MetaConnectedJob(1, "chat", engine.MetaSummary{AccountID: "act_987", AccountName: "Conta 01", Currency: "BRL",
		AccountStatus: 3, PageName: "P", PageWhatsApp: true})
	text := job.Payload.(engine.SendJob).Text
	for _, s := range job.Payload.(engine.SendJob).Next {
		text += "\n" + s
	}
	if !strings.Contains(text, "billing_hub/payment_settings/?asset_id=987") || !strings.Contains(text, "Pix") {
		t.Fatalf("aviso sem link de pagamento: %q", text)
	}
}

// Falar de pagamento do próprio negócio (o que o cliente aceita dos clientes dele) não é pedido de pagar a Meta.
func TestBillingIgnoresBusinessTalk(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.connectMeta()
	h.att.Responses = []string{`{"intent":"provide_info","profile":{"sales_channel":"WhatsApp"},"reply":"Anotado!"}`}
	h.say("aceito pagamento por cartão e pix")
	if strings.Contains(h.sentText(), "billing_hub") {
		t.Fatalf("mandou link de pagamento da Meta: %q", h.sentText())
	}
}
