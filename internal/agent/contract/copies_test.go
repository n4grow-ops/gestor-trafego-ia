package contract_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

func TestCleanCopies(t *testing.T) {
	long := strings.Repeat("palavra ", 10) // 80 caracteres
	cs := contract.CleanCopies([]contract.Copy{
		{Headline: " Pizza no capricho ", Text: "Peça já", CTA: "whatsapp_message", Angle: "preco"},
		{Headline: "Pizza no capricho", Text: "Peça já"}, // repetida
		{},                              // vazia
		{Headline: long, CTA: "COMPRE"}, // título longo e CTA inválida
	})
	if len(cs) != 2 {
		t.Fatalf("copies = %+v", cs)
	}
	if cs[0].Headline != "Pizza no capricho" || cs[0].CTA != "WHATSAPP_MESSAGE" || cs[0].Angle != "" {
		t.Fatalf("primeira = %+v", cs[0])
	}
	if n := len([]rune(cs[1].Headline)); n > contract.MaxHeadline || strings.HasSuffix(cs[1].Headline, "palavr") || cs[1].CTA != "" {
		t.Fatalf("segunda = %+v", cs[1])
	}
}

func TestApplyClientCopies(t *testing.T) {
	gestor := []contract.Copy{{Headline: "G1", Text: "T1", CTA: "LEARN_MORE"}}
	if got := contract.ApplyClientCopies(nil, gestor); len(got) != 1 || got[0].Headline != "G1" {
		t.Fatalf("sem texto do cliente = %+v", got)
	}
	got := contract.ApplyClientCopies([]contract.Copy{{Headline: "Meu título"}, {Text: "Só o texto", CTA: "CONTACT_US"}}, gestor)
	if len(got) != 2 || got[0].Headline != "Meu título" || got[0].Text != "T1" || got[0].CTA != "LEARN_MORE" ||
		got[1].Headline != "G1" || got[1].Text != "Só o texto" || got[1].CTA != "CONTACT_US" {
		t.Fatalf("com texto do cliente = %+v", got)
	}
}

func TestFieldsMergeCopiesReplaceList(t *testing.T) {
	f := contract.Fields{Copies: []contract.Copy{{Headline: "A", Text: "a"}, {Headline: "B", Text: "b"}}}
	m := f.Merge(contract.Fields{Copies: []contract.Copy{{Headline: "C", Text: "c"}}})
	if len(m.Copies) != 1 || m.Copies[0].Headline != "C" {
		t.Fatalf("merge = %+v", m.Copies)
	}
	if m2 := m.Merge(contract.Fields{Offer: contract.Str("x")}); len(m2.Copies) != 1 {
		t.Fatal("turno sem copies apagou os textos")
	}
}
