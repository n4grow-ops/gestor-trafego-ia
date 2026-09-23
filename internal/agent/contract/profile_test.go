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
	if !(contract.Profile{BusinessName: contract.Str("X"), City: contract.Str("Y")}).Complete() {
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
