package contract_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

func TestProfileMergeKeepsSavedDataAndTruncates(t *testing.T) {
	p := contract.Profile{BusinessName: contract.Str("Pizzaria Boa"), City: contract.Str("Salvador")}
	got := p.Merge(contract.Profile{BusinessName: contract.Str("  "), Audience: contract.Str(strings.Repeat("é", 200))})
	if *got.BusinessName != "Pizzaria Boa" || *got.City != "Salvador" {
		t.Errorf("perfil = %+v", got)
	}
	if n := utf8.RuneCountInString(*got.Audience); n != 120 {
		t.Errorf("audience com %d runas, want 120", n)
	}
}

func TestProfileComplete(t *testing.T) {
	if (contract.Profile{BusinessName: contract.Str("X")}).Complete() {
		t.Error("sem cidade não está completo")
	}
	if (contract.Profile{BusinessName: contract.Str("X"), City: contract.Str("Y")}).Complete() {
		t.Error("nome e cidade não bastam: falta conhecer o negócio")
	}
	full := contract.Profile{BusinessName: contract.Str("X"), City: contract.Str("Y"), Offer: contract.Str("pizza"),
		Audience: contract.Str("famílias"), AudienceAge: contract.Str("25 a 50"), AudienceGender: contract.Str("todos"),
		ServiceArea: contract.Str("5 km"), Differentials: contract.Str("massa fina"), AverageTicket: contract.Str("R$ 80"),
		SalesChannel: contract.Str("WhatsApp e iFood")}
	if !full.Complete() {
		t.Error("nome e cidade bastam")
	}
}

func TestProjectIntentsAreValid(t *testing.T) {
	for _, i := range []contract.Intent{contract.IntentNewProject, contract.IntentSwitchProject, contract.IntentListProjects, contract.IntentProjectStatus} {
		if !i.Valid() {
			t.Errorf("%s deveria ser válida", i)
		}
	}
}

func TestAttendantTurnLimitsProjectName(t *testing.T) {
	turn := contract.AttendantTurn{Intent: contract.IntentNewProject, Reply: "ok", Project: strings.Repeat("a", 61)}
	if err := turn.Validate(); err == nil {
		t.Error("nome de projeto com mais de 60 caracteres deveria falhar")
	}
}

func TestProfileMissingInOrder(t *testing.T) {
	p := contract.Profile{BusinessName: contract.Str("Pizzaria"), City: contract.Str("Campinas"), Audience: contract.Str("famílias")}
	got := p.Missing()
	want := []string{"o que a empresa faz (principais produtos ou serviços)", "faixa de idade do cliente",
		"gênero predominante do cliente (mulheres, homens ou todos)", "região que atende (bairros, cidade, raio de entrega ou online)",
		"diferencial (por que comprar com você)", "ticket médio (quanto o cliente gasta em média)", "onde vende (WhatsApp, loja física, site, iFood...)"}
	if len(got) != len(want) {
		t.Fatalf("faltando = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("faltando = %q", got)
		}
	}
	merged := p.Merge(contract.Profile{Differentials: contract.Str(" massa de fermentação natural "), ServiceArea: contract.Str("delivery em 5 km")})
	if *merged.Differentials != "massa de fermentação natural" || *merged.ServiceArea != "delivery em 5 km" {
		t.Fatalf("merge = %+v", merged)
	}
}
