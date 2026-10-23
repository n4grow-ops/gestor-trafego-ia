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
		Audience:       contract.Audience{AgeMin: 25, AgeMax: 55, Interests: []string{"pizza", "delivery"}, RadiusKM: 20},
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
		"raio de 20 km", "pizza, delivery", "Pizza quentinha", "Público local.", "30 minutos", "conversas no WhatsApp"} {
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

func TestSummaryPlacements(t *testing.T) {
	p := proposal()
	if s := campaign.Summary(p, "000000"); !strings.Contains(s, "Onde aparece: automático") {
		t.Errorf("sem posicionamento escolhido devia dizer automático:\n%s", s)
	}
	p.Placements = []string{"feed"}
	if s := campaign.Summary(p, "000000"); !strings.Contains(s, "Onde aparece: só no Feed (Facebook e Instagram)") {
		t.Errorf("só feed:\n%s", s)
	}
	p.Placements = []string{"feed", "stories"}
	if s := campaign.Summary(p, "000000"); !strings.Contains(s, "Onde aparece: só no Feed e nos Stories (Facebook e Instagram)") {
		t.Errorf("feed e stories:\n%s", s)
	}
}

// Raio 0 = só a cidade: a proposta diz isso em vez de "raio de 0 km".
func TestSummaryCityOnly(t *testing.T) {
	p := proposal()
	p.Audience.RadiusKM = 0
	s := campaign.Summary(p, "000000")
	if !strings.Contains(s, "somente a cidade (sem raio)") || strings.Contains(s, "raio de 0 km") {
		t.Fatalf("resumo:\n%s", s)
	}
}

func TestSummaryCopyNotesOnlyWhenNeeded(t *testing.T) {
	if s := campaign.Summary(proposal(), "000000"); strings.Contains(s, "Atenção no texto") {
		t.Errorf("texto limpo não deveria ter aviso:\n%s", s)
	}
	p := proposal()
	p.Copies = []contract.Copy{{Headline: "Veja o antes e depois", Text: "Peça pelo WhatsApp", CTA: "WHATSAPP_MESSAGE"}}
	s := campaign.Summary(p, "000000")
	for _, want := range []string{"*Atenção no texto*", "Anúncio 1", "antes e depois", "Sugestão:", "A decisão é sua", "*SIM 000000*"} {
		if !strings.Contains(s, want) {
			t.Errorf("resumo sem %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "Atenção no texto") > strings.Index(s, "Por quê") {
		t.Error("aviso deve vir antes do 'Por quê'")
	}
}

func TestSummaryCopyNotesSegmentAndAdjusted(t *testing.T) {
	p := proposal()
	p.Copies = []contract.Copy{{Headline: "Advogado trabalhista", Text: "Primeira consulta grátis", CTA: "LEARN_MORE"}}
	if s := campaign.Summary(p, "000000"); strings.Contains(s, "Atenção no texto") {
		t.Errorf("sem segmento conhecido, não aplica regra da OAB:\n%s", s)
	}
	o := campaign.SummaryOptions{Segment: "Advocacia", Adjusted: []int{0}}
	s := campaign.SummaryWith(p, "000000", o)
	if !strings.Contains(s, "OAB") || !strings.Contains(s, "troquei trechos") {
		t.Errorf("resumo sem aviso do segmento/ajuste:\n%s", s)
	}
}

func TestSummaryCopyNotesAreShort(t *testing.T) {
	p := proposal()
	p.Copies = []contract.Copy{
		{Headline: "Antes e depois", Text: "Resultado garantido, renda extra, emagrecimento. Aproveite!!!", CTA: "LEARN_MORE"},
		{Headline: "Perca 10 kg", Text: "Você está acima do peso? Dinheiro fácil.", CTA: "LEARN_MORE"},
	}
	s := campaign.Summary(p, "000000")
	if n := strings.Count(s, "\n- "); n > 4 {
		t.Errorf("seção longa demais (%d itens):\n%s", n, s)
	}
	if !strings.Contains(s, "Mais ") {
		t.Errorf("deveria resumir o excedente:\n%s", s)
	}
}

func TestSummaryShowsAngleOnlyWithManyCopies(t *testing.T) {
	p := proposal()
	p.Copies = []contract.Copy{
		{Headline: "A", Text: "a", CTA: "LEARN_MORE", Angle: "preco"},
		{Headline: "B", Text: "b", CTA: "LEARN_MORE", Angle: "urgencia"},
	}
	s := campaign.Summary(p, "000000")
	if !strings.Contains(s, "Anúncio 1 (foco em preço)") || !strings.Contains(s, "Anúncio 2 (foco em urgência)") {
		t.Errorf("sem rótulo de ângulo:\n%s", s)
	}
	p.Copies = p.Copies[:1]
	if s := campaign.Summary(p, "000000"); strings.Contains(s, "foco em") {
		t.Errorf("1 variação não deve mostrar ângulo:\n%s", s)
	}
}

func TestSummaryFollowers(t *testing.T) {
	p := proposal()
	p.Objective, p.Followers = "OUTCOME_TRAFFIC", true
	s := campaign.Summary(p, "000000")
	if !strings.Contains(s, "Objetivo: seguidores no Instagram") || !strings.Contains(s, "Onde aparece: só no Instagram") {
		t.Errorf("resumo de seguidores:\n%s", s)
	}
}
