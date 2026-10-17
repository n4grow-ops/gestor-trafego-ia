package engine

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

func TestGroundFields(t *testing.T) {
	cases := []struct {
		msg, loc string
		budget   bool
		keepLoc  bool
		keepBud  bool
	}{
		{"famílias de Campinas, 30 reais por dia", "Campinas", true, true, true},
		{"tom descontraído, chamar no WhatsApp", "cidade onde fica a pizzaria", true, false, false},
		{"cinquenta por dia em são paulo", "São Paulo, raio de 20 km", true, true, true},
		{"quero em Sorocaba e região", "Sorocaba", false, true, false},
		{"pode ser em ribeirão", "Ribeirão Preto", false, true, false},
	}
	for _, c := range cases {
		f := contract.Fields{Location: contract.Str(c.loc)}
		if c.budget {
			f.DailyBudgetBRL = contract.Num(50)
		}
		got, _ := groundFields(f, c.msg)
		if (got.Location != nil) != c.keepLoc {
			t.Errorf("%q: local = %v", c.msg, got.Location)
		}
		if c.budget && (got.DailyBudgetBRL != nil) != c.keepBud {
			t.Errorf("%q: orçamento = %v", c.msg, got.DailyBudgetBRL)
		}
	}
	// "aqui na minha cidade" com a cidade na ficha vale; número da ficha não vira orçamento.
	got, _ := groundFields(contract.Fields{Location: contract.Str("Campinas"), DailyBudgetBRL: contract.Num(9)}, "anuncia aqui na minha cidade", "Campinas", "raio de 5 km")
	if got.Location == nil || got.DailyBudgetBRL != nil {
		t.Fatalf("ficha: %+v", got)
	}
}
