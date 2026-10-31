package engine

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestMatchPlanAndMethod(t *testing.T) {
	plans := []store.Plan{{Code: "free", Name: "Básico"}, {Code: "starter", Name: "Intermediário"}, {Code: "pro", Name: "Avançado"}}
	for text, want := range map[string]string{
		"2": "starter", "plano 3": "pro", "quero o 1": "free", "opção 2.": "starter", "o intermediario": "starter",
		"Avançado por favor": "pro", "pro": "pro", "free": "free",
		// "pro" no meio da frase é "para o"; número fora da lista e frase com número não escolhem.
		"anúncio pro meu negócio": "", "4": "", "quero 2 campanhas": "", "oi": "",
	} {
		p, ok := matchPlan(text, plans)
		if (want == "") == ok || (ok && p.Code != want) {
			t.Errorf("matchPlan(%q) = %q %v, esperava %q", text, p.Code, ok, want)
		}
	}
	for text, want := range map[string]string{"pix": "pix", "Pix por favor": "pix", "cartão": "card", "no crédito": "card",
		"cartão de crédito": "card", "pix ou cartão?": "", "tanto faz": ""} {
		if got := matchMethod(text); got != want {
			t.Errorf("matchMethod(%q) = %q, esperava %q", text, got, want)
		}
	}
	if localPhone("5511988887777") != "11988887777" || localPhone("11988887777") != "11988887777" {
		t.Error("telefone local")
	}
	if planPrice(14700) != "R$ 147" {
		t.Errorf("preço = %q", planPrice(14700))
	}
}
