package contract_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

const validProposalJSON = `{"objective":"OUTCOME_ENGAGEMENT","audience":{"age_min":25,"age_max":55,"genders":[],"interests":["pizza"],"radius_km":20},"daily_budget_brl":40,"duration_days":7,"copies":[{"headline":"Pizza quentinha","text":"Peça pelo WhatsApp","cta":"WHATSAPP_MESSAGE"}],"rationale":"Público local.","risks":[]}`

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
		{"raio", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Audience.RadiusKM = 90 }, "radius_km"},
		{"sem copies", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Copies = nil }, "copies"},
		{"headline longo", func(p *contract.CampaignProposal, _ *contract.Briefing) {
			p.Copies[0].Headline = strings.Repeat("é", 41)
		}, "headline"},
		{"texto vazio", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Copies[0].Text = "" }, "text"},
		{"cta", func(p *contract.CampaignProposal, _ *contract.Briefing) { p.Copies[0].CTA = "BUY_NOW" }, "cta"},
		{"objetivo do cliente prevalece", func(p *contract.CampaignProposal, b *contract.Briefing) {
			b.Constraints.AllowedObjectives = []string{"OUTCOME_ENGAGEMENT", "OUTCOME_TRAFFIC"}
			b.ObjectiveHint = "OUTCOME_TRAFFIC"
		}, "objetivo que o cliente definiu"},
		{"mais copies que o teto", func(p *contract.CampaignProposal, _ *contract.Briefing) {
			for len(p.Copies) <= contract.MaxCopies {
				p.Copies = append(p.Copies, p.Copies[0])
			}
		}, "copies"},
		{"copies diferente do pedido", func(p *contract.CampaignProposal, b *contract.Briefing) { b.CopyVariations = 2 }, "exatamente 2"},
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

func TestConnectMetaIntentIsValid(t *testing.T) {
	if !contract.IntentConnectMeta.Valid() || contract.IntentConnectMeta != "connect_meta" {
		t.Fatal("connect_meta precisa ser intent válida")
	}
}

func TestNormalizePlacements(t *testing.T) {
	cases := map[string][]string{
		"feed":                      {"feed"},
		"Stories,feed":              {"feed", "stories"},
		"story,reel,Feed,feed,xpto": {"feed", "stories", "reels"},
		"":                          nil,
		"instagram,explorar":        nil,
	}
	for in, want := range cases {
		var raw []string
		if in != "" {
			raw = strings.Split(in, ",")
		}
		if got := contract.NormalizePlacements(raw); !slices.Equal(got, want) {
			t.Errorf("%q = %v, quer %v", in, got, want)
		}
	}
}

// Placements novos substituem os antigos (o cliente mudou de ideia); vazio não apaga.
func TestMergePlacements(t *testing.T) {
	f := contract.Fields{Placements: []string{"feed", "stories"}}
	if got := f.Merge(contract.Fields{}).Placements; !slices.Equal(got, []string{"feed", "stories"}) {
		t.Fatalf("vazio apagou: %v", got)
	}
	if got := f.Merge(contract.Fields{Placements: []string{"feed"}}).Placements; !slices.Equal(got, []string{"feed"}) {
		t.Fatalf("não substituiu: %v", got)
	}
	if !(contract.Fields{Placements: []string{"feed"}}).HasData() {
		t.Error("placements conta como dado informado")
	}
	b, err := contract.NewBriefing(1, contract.Fields{Offer: contract.Str("a"), Audience: contract.Str("b"), Location: contract.Str("c"),
		DailyBudgetBRL: contract.Num(10), Placements: []string{"stories"}}, contract.Constraints{})
	if err != nil || !slices.Equal(b.Placements, []string{"stories"}) {
		t.Fatalf("briefing sem placements: %v %v", b.Placements, err)
	}
}

// Raio: 0 é só a cidade; a Meta não aceita raio de 1 a 16 km em volta de cidade.
func TestProposalRadius(t *testing.T) {
	var p contract.CampaignProposal
	if err := json.Unmarshal([]byte(validProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	b := contract.Briefing{DailyBudgetBRL: 50, Constraints: contract.Constraints{MaxDailyBudgetBRL: 100, AllowedObjectives: []string{"OUTCOME_ENGAGEMENT"}}}
	for r, ok := range map[int]bool{0: true, 17: true, 80: true, 5: false, 16: false, 81: false} {
		p.Audience.RadiusKM = r
		if err := p.Validate(b); (err == nil) != ok {
			t.Errorf("raio %d: err = %v", r, err)
		}
	}
	p.Audience.RadiusKM = 10
	p.Normalize()
	if p.Audience.RadiusKM != 0 {
		t.Errorf("raio pequeno devia virar só a cidade: %d", p.Audience.RadiusKM)
	}
}

func TestProposalClientChoicesAreAccepted(t *testing.T) {
	p, b := validProposal(t), briefing()
	// rationale é opcional (recomendação), dica de objetivo em texto livre não força nada
	p.Rationale = ""
	b.ObjectiveHint = "mensagens"
	if err := p.Validate(b); err != nil {
		t.Fatal(err)
	}
	// o cliente pediu 3 variações: 3 copies valem; sem pedido, 1 ou mais (até o teto) valem
	p.Copies = append(p.Copies, p.Copies[0], p.Copies[0])
	if err := p.Validate(b); err != nil {
		t.Fatal(err)
	}
	b.CopyVariations = 3
	if err := p.Validate(b); err != nil {
		t.Fatal(err)
	}
	// objetivo igual ao definido pelo cliente passa
	b.ObjectiveHint = p.Objective
	if err := p.Validate(b); err != nil {
		t.Fatal(err)
	}
}

func TestCopyAngleIsOptionalInJSON(t *testing.T) {
	b, err := json.Marshal(contract.Copy{Headline: "H", Text: "T", CTA: "LEARN_MORE"})
	if err != nil || strings.Contains(string(b), "angle") {
		t.Errorf("angle vazio não deve aparecer: %s %v", b, err)
	}
	var c contract.Copy
	if err := json.Unmarshal([]byte(`{"headline":"H","text":"T","cta":"LEARN_MORE","angle":"preco"}`), &c); err != nil || c.Angle != "preco" {
		t.Errorf("angle = %q err = %v", c.Angle, err)
	}
	if err := json.Unmarshal([]byte(`{"headline":"H","text":"T","cta":"LEARN_MORE"}`), &c); err != nil {
		t.Errorf("JSON sem angle deve valer: %v", err)
	}
}

func TestWantsLeadForm(t *testing.T) {
	for hint, want := range map[string]bool{
		"formulário": true, "campanha de formulario": true, "leads por Formulário da Meta": true, "cadastro": true,
		"mensagens": false, "visitas": false, "": false,
	} {
		if got := contract.WantsLeadForm(hint); got != want {
			t.Errorf("WantsLeadForm(%q) = %v", hint, got)
		}
	}
}
