package engine_test

import (
	"strings"
	"testing"
)

// Trocar ou incluir conta de anúncios: o cliente já é testador e já conectou. Vai o link de novo, com a
// dica de editar o acesso no Facebook, e nunca o pedido do link do perfil.
func TestSwitchAdAccountSendsRelink(t *testing.T) {
	for _, msg := range []string{"quero trocar a conta de anúncio", "quero adicionar mais uma conta de anúncios",
		"usar outra conta de anúncio", "reconectar meta"} {
		t.Run(msg, func(t *testing.T) {
			h := newMetaHarness(t, "owner")
			h.connectMeta()
			h.say(msg)
			if h.att.Calls() != 0 {
				t.Fatal("pedido direto não deve chamar a IA")
			}
			got := h.sentText()
			if !strings.Contains(got, metaBase+"/") || !strings.Contains(got, "Editar acesso") {
				t.Fatalf("sem link de troca: %q", got)
			}
			if strings.Contains(got, "perfil do Facebook") {
				t.Fatalf("pediu o perfil de novo: %q", got)
			}
		})
	}
}

// Pela IA (frase longa) o caminho é o mesmo: intent connect_meta com conta já conectada.
func TestSwitchAdAccountViaAttendant(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.connectMeta()
	h.att.Responses = []string{`{"intent":"connect_meta","fields":{},"reply":"Claro, te mando o link pra trocar."}`}
	h.say("oi, abri uma conta nova no gerenciador e queria passar a anunciar por ela em vez da antiga")
	got := h.sentText()
	if !strings.Contains(got, "Editar acesso") || strings.Contains(got, "perfil do Facebook") {
		t.Fatalf("resposta = %q", got)
	}
	if sys := h.att.Requests[len(h.att.Requests)-1].Messages[0].Content; !strings.Contains(sys, "trocar, incluir ou reconectar") {
		t.Error("a IA precisa saber que trocar/incluir conta é connect_meta")
	}
}
