package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

func TestLeadFormNormalizeDefaults(t *testing.T) {
	n := contract.LeadForm{}.Normalize()
	if n.Kind != "volume" || n.ButtonAction != "site" || len(n.Questions) != 3 || n.Questions[0].Kind != "full_name" {
		t.Fatalf("padrão = %+v", n)
	}
	if n.HasIntro() || n.HasThanks() {
		t.Fatal("padrão não tem abertura nem tela final")
	}
}

func TestLeadFormNormalizeQuestions(t *testing.T) {
	n := contract.LeadForm{Kind: "qualificado", ButtonAction: "ligar", ButtonURL: "javascript:x", Questions: []contract.LeadQuestion{
		{Kind: "nome"}, {Kind: "Phone"}, {Kind: "phone"}, // apelido e repetido
		{Kind: "custom", Label: "Qual o melhor horário?", Options: []string{"Manhã", "Tarde", "Tarde", " "}},
		{Kind: "custom", Label: "Conte seu caso"},
		{Kind: "custom", Label: "  "},                             // sem texto: sai
		{Kind: "renda", Label: "Qual sua renda?"},                 // tipo desconhecido com texto: vira pergunta própria
		{Kind: "custom", Label: "Só uma", Options: []string{"A"}}, // uma opção só: resposta livre
	}}.Normalize()
	if n.Kind != "qualified" || n.ButtonAction != "call" || n.ButtonURL != "" {
		t.Fatalf("normalizado = %+v", n)
	}
	want := []string{"full_name", "phone", "custom", "custom", "custom", "custom"}
	if len(n.Questions) != len(want) {
		t.Fatalf("perguntas = %+v", n.Questions)
	}
	for i, k := range want {
		if n.Questions[i].Kind != k {
			t.Fatalf("pergunta %d = %+v", i, n.Questions[i])
		}
	}
	if o := n.Questions[2].Options; len(o) != 2 || o[0] != "Manhã" || o[1] != "Tarde" {
		t.Fatalf("opções = %v", o)
	}
	if n.Questions[4].Label != "Qual sua renda?" || n.Questions[5].Options != nil {
		t.Fatalf("perguntas = %+v", n.Questions)
	}
}

func TestLeadFormMerge(t *testing.T) {
	base := contract.Fields{LeadForm: &contract.LeadForm{Headline: "Ganhe 10%", ThanksTitle: "Valeu!",
		Questions: []contract.LeadQuestion{{Kind: "full_name"}, {Kind: "email"}}}}
	// Só muda as perguntas: o resto fica.
	m := base.Merge(contract.Fields{LeadForm: &contract.LeadForm{Questions: []contract.LeadQuestion{{Kind: "full_name"}, {Kind: "whatsapp"}}}})
	if m.LeadForm.Headline != "Ganhe 10%" || m.LeadForm.ThanksTitle != "Valeu!" || m.LeadForm.Questions[1].Kind != "whatsapp" || len(m.LeadForm.Questions) != 2 {
		t.Fatalf("merge = %+v", m.LeadForm)
	}
	if base.LeadForm.Questions[1].Kind != "email" {
		t.Fatal("merge alterou o original")
	}
	// Tira a abertura e o botão.
	m = m.Merge(contract.Fields{LeadForm: &contract.LeadForm{Clear: []string{"intro", "button"}}})
	if m.LeadForm.Headline != "" || m.LeadForm.ButtonAction != "none" || m.LeadForm.ThanksTitle != "Valeu!" {
		t.Fatalf("clear = %+v", m.LeadForm)
	}
	// Turno sem formulário não mexe.
	if m2 := m.Merge(contract.Fields{Offer: contract.Str("x")}); m2.LeadForm == nil || m2.LeadForm.ThanksTitle != "Valeu!" {
		t.Fatal("turno sem lead_form apagou o formulário")
	}
	if !(contract.Fields{LeadForm: &contract.LeadForm{}}).HasData() {
		t.Fatal("lead_form conta como dado informado")
	}
}

func TestLeadFormFromAttendantJSON(t *testing.T) {
	var turn contract.AttendantTurn
	raw := `{"intent":"provide_info","fields":{"lead_form":{"kind":"qualified","questions":[{"kind":"full_name"},{"kind":"custom","label":"Tem carro?","options":["Sim","Não"]}],"thanks_title":"Recebido!","button_action":"whatsapp","button_phone":"11 99999-0000"}},"reply":"Feito."}`
	if err := json.Unmarshal([]byte(raw), &turn); err != nil {
		t.Fatal(err)
	}
	b, err := contract.NewBriefing(1, contract.Fields{Offer: contract.Str("a"), Audience: contract.Str("b"), Location: contract.Str("c"),
		DailyBudgetBRL: contract.Num(10)}.Merge(turn.Fields), contract.Constraints{})
	if err != nil {
		t.Fatal(err)
	}
	if b.LeadForm == nil || b.LeadForm.Kind != "qualified" || len(b.LeadForm.Questions) != 2 || b.LeadForm.ButtonAction != "whatsapp" {
		t.Fatalf("briefing = %+v", b.LeadForm)
	}
}
