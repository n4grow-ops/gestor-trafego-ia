package campaign_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
)

func proposal() contract.CampaignProposal {
	return contract.CampaignProposal{
		Objective:      "OUTCOME_ENGAGEMENT",
		Audience:       contract.Audience{AgeMin: 25, AgeMax: 55, Interests: []string{"pizza", "delivery"}, RadiusKM: 8},
		DailyBudgetBRL: 40, DurationDays: 7,
		Copies:    []contract.Copy{{Headline: "Pizza quentinha", Text: "Peça pelo WhatsApp", CTA: "WHATSAPP_MESSAGE"}},
		Rationale: "Público local.",
	}
}

func TestProposalHashIsStableAndSensitive(t *testing.T) {
	a, err := campaign.ProposalHash(proposal())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := campaign.ProposalHash(proposal())
	p := proposal()
	p.DailyBudgetBRL = 41
	c, _ := campaign.ProposalHash(p)
	if a != b || a == c || len(a) != 64 {
		t.Errorf("a=%s b=%s c=%s", a, b, c)
	}
}

func TestNewApprovalCodeHasSixDigits(t *testing.T) {
	re := regexp.MustCompile(`^\d{6}$`)
	for i := 0; i < 200; i++ {
		code, err := campaign.NewApprovalCode()
		if err != nil || !re.MatchString(code) {
			t.Fatalf("code = %q err = %v", code, err)
		}
	}
}

func TestHashCode(t *testing.T) {
	if campaign.HashCode("123456") == campaign.HashCode("123457") || campaign.HashCode("123456") != campaign.HashCode("123456") {
		t.Error("hash de código inconsistente")
	}
}

func TestParseApproval(t *testing.T) {
	accept := map[string]string{
		"SIM 123456": "123456", "sim 123456": "123456", "Sim: 123456": "123456",
		"SIM123456": "123456", "  sim 123456  ": "123456", "SIM 123456.": "123456", "sim - 654321!": "654321",
	}
	for in, want := range accept {
		if got, ok := campaign.ParseApproval(in); !ok || got != want {
			t.Errorf("ParseApproval(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"SIM", "SIM 12345", "sim 1234567", "não 123456", "SIM 123456 e mais", "123456"} {
		if _, ok := campaign.ParseApproval(in); ok {
			t.Errorf("ParseApproval(%q) deveria recusar", in)
		}
	}
}

func TestBRL(t *testing.T) {
	if got := campaign.BRL(50); got != "R$ 50,00" {
		t.Errorf("BRL = %q", got)
	}
	if got := campaign.BRL(12.5); got != "R$ 12,50" {
		t.Errorf("BRL = %q", got)
	}
}

func TestSummaryHasCodeAndValues(t *testing.T) {
	s := campaign.Summary(proposal(), "482913")
	for _, want := range []string{"*SIM 482913*", "R$ 40,00", "7 dias", "R$ 280,00", "25 a 55 anos", "todos os gêneros",
		"8 km", "pizza, delivery", "Pizza quentinha", "Público local.", "30 minutos", "conversas no WhatsApp"} {
		if !strings.Contains(s, want) {
			t.Errorf("resumo sem %q:\n%s", want, s)
		}
	}
}

func TestSummaryGenders(t *testing.T) {
	p := proposal()
	p.Audience.Genders = []string{"female"}
	if !strings.Contains(campaign.Summary(p, "000000"), "mulheres") {
		t.Error("esperava 'mulheres'")
	}
}
