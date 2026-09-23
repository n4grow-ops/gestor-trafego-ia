package contract_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

const validProposalJSON = `{"objective":"OUTCOME_ENGAGEMENT","audience":{"age_min":25,"age_max":55,"genders":[],"interests":["pizza"],"radius_km":8},"daily_budget_brl":40,"duration_days":7,"copies":[{"headline":"Pizza quentinha","text":"Peça pelo WhatsApp","cta":"WHATSAPP_MESSAGE"}],"rationale":"Público local.","risks":[]}`

func briefing() contract.Briefing {
	return contract.Briefing{
		OrganizationID: 1, Offer: "Pizza grande", Audience: "famílias", Location: "Campinas", DailyBudgetBRL: 50,
		Constraints: contract.Constraints{MaxDailyBudgetBRL: 100, AllowedObjectives: []string{"OUTCOME_ENGAGEMENT"}, Niche: "restaurante"},
	}
}

func validProposal(t *testing.T) contract.CampaignProposal {
	t.Helper()
	var p contract.CampaignProposal
	if err := json.Unmarshal([]byte(validProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMergeKeepsCollectedDataWhenNewValueIsBlank(t *testing.T) {
	f := contract.Fields{Offer: contract.Str("Pizza"), DailyBudgetBRL: contract.Num(50)}
	got := f.Merge(contract.Fields{Offer: contract.Str("  "), Location: contract.Str("Campinas"), DailyBudgetBRL: contract.Num(0)})
	if *got.Offer != "Pizza" {
		t.Errorf("offer = %q", *got.Offer)
	}
	if *got.Location != "Campinas" {
		t.Errorf("location = %v", got.Location)
	}
	if *got.DailyBudgetBRL != 50 {
		t.Errorf("budget = %v", *got.DailyBudgetBRL)
	}
}

func TestMergeAppendsMedia(t *testing.T) {
	f := contract.Fields{Media: []string{"a"}}
	got := f.Merge(contract.Fields{Media: []string{"b"}})
	if !slices.Equal(got.Media, []string{"a", "b"}) || !slices.Equal(f.Media, []string{"a"}) {
		t.Errorf("media = %v, original = %v", got.Media, f.Media)
	}
}

func TestHasData(t *testing.T) {
	if (contract.Fields{Offer: contract.Str(" ")}).HasData() {
		t.Error("texto em branco não é dado")
	}
	if !(contract.Fields{DailyBudgetBRL: contract.Num(10)}).HasData() {
		t.Error("orçamento é dado")
	}
}

func TestMissingListsRequiredFieldsInOrder(t *testing.T) {
	got := contract.Fields{Location: contract.Str("X")}.Missing()
	want := []string{"offer", "audience", "daily_budget_brl"}
	if !slices.Equal(got, want) {
		t.Errorf("missing = %v, want %v", got, want)
	}
}

func TestNewBriefing(t *testing.T) {
	c := contract.Constraints{MaxDailyBudgetBRL: 100, AllowedObjectives: []string{"OUTCOME_ENGAGEMENT"}}
	if _, err := contract.NewBriefing(1, contract.Fields{Offer: contract.Str("x")}, c); err == nil {
		t.Fatal("briefing incompleto deveria falhar")
	}
	f := contract.Fields{Offer: contract.Str("Pizza"), Audience: contract.Str("famílias"), Location: contract.Str("Campinas"),
		DailyBudgetBRL: contract.Num(50), ObjectiveHint: contract.Str("mensagens")}
	b, err := contract.NewBriefing(7, f, c)
	if err != nil {
		t.Fatal(err)
	}
	if b.OrganizationID != 7 || b.Offer != "Pizza" || b.DailyBudgetBRL != 50 || b.ObjectiveHint != "mensagens" || b.Constraints.MaxDailyBudgetBRL != 100 {
		t.Errorf("briefing = %+v", b)
	}
}

func TestAttendantTurnValidate(t *testing.T) {
	cases := []struct {
		name string
		turn contract.AttendantTurn
		ok   bool
	}{
		{"válido", contract.AttendantTurn{Intent: contract.IntentQuestion, Reply: "Oi!"}, true},
		{"intent desconhecida", contract.AttendantTurn{Intent: "hack", Reply: "x"}, false},
		{"reply vazio", contract.AttendantTurn{Intent: contract.IntentOther, Reply: "  "}, false},
		{"reply longo", contract.AttendantTurn{Intent: contract.IntentOther, Reply: strings.Repeat("a", 1001)}, false},
	}
	for _, c := range cases {
		if err := c.turn.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func TestProposalValidateAcceptsValid(t *testing.T) {
	if err := validProposal(t).Validate(briefing()); err != nil {
		t.Fatal(err)
	}
}

func TestProposalValidateRejects(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(p *contract.CampaignProposal, b *contract.Briefing)
		contains string
	}{
		{"objetivo", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Objective = "OUTCOME_SALES" }, "objective"},
		{"orçamento zero", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.DailyBudgetBRL = 0 }, "maior que zero"},
		{"acima do cliente", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.DailyBudgetBRL = 60 }, "orçamento do cliente"},
		{"acima do limite", func(p *contract.CampaignProposal, b *contract.Briefing) {
			b.DailyBudgetBRL = 500
			p.DailyBudgetBRL = 150
		}, "limite da conta"},
		{"duração", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.DurationDays = 0 }, "duration_days"},
		{"idade", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Audience.AgeMin = 16 }, "idade"},
		{"idade invertida", func(p *contract.CampaignProposal, _ *contract.Briefing) {
			p.Audience.AgeMin, p.Audience.AgeMax = 50, 30
		}, "idade"},
		{"gênero", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Audience.Genders = []string{"x"} }, "gender"},
		{"raio", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Audience.RadiusKM = 0 }, "radius_km"},
		{"sem copies", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Copies = nil }, "copies"},
		{"headline longo", func(p *contract.CampaignProposal, _ *contract.Briefing) {
			p.Copies[0].Headline = strings.Repeat("é", 41)
		}, "headline"},
		{"texto vazio", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Copies[0].Text = "" }, "text"},
		{"cta", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Copies[0].CTA = "BUY_NOW" }, "cta"},
		{"rationale", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Rationale = " " }, "rationale"},
	}
	for _, c := range cases {
		p, b := validProposal(t), briefing()
		c.mutate(&p, &b)
		err := p.Validate(b)
		if err == nil || !strings.Contains(err.Error(), c.contains) {
			t.Errorf("%s: err = %v, want contendo %q", c.name, err, c.contains)
		}
	}
}
