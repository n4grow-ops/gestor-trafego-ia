package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

func TestReportPrefDefaultsAndOfferOnce(t *testing.T) {
	st := store.New(testdb.New(t))
	org, err := st.CreateOrganization(ctx, "Pizzaria Boa", "restaurante", 100, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.GetReportPref(ctx, org)
	if err != nil || p.Audio || p.Offered {
		t.Fatalf("padrão = %+v %v", p, err)
	}
	if first, err := st.MarkReportOffered(ctx, org); err != nil || !first {
		t.Fatalf("primeiro convite = %v %v", first, err)
	}
	if first, err := st.MarkReportOffered(ctx, org); err != nil || first {
		t.Fatalf("convite repetido = %v %v", first, err)
	}
	if err := st.SetReportAudio(ctx, org, true); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.GetReportPref(ctx, org); !p.Audio || !p.Offered {
		t.Fatalf("depois do aceite = %+v", p)
	}
	if err := st.SetReportAudio(ctx, org, false); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.GetReportPref(ctx, org); p.Audio {
		t.Fatal("só texto não desligou")
	}
}

// Responder "áudio" antes de qualquer convite também conta como convidado.
func TestReportPrefSetWithoutRow(t *testing.T) {
	st := store.New(testdb.New(t))
	org, err := st.CreateOrganization(ctx, "Loja", "varejo", 100, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetReportAudio(ctx, org, true); err != nil {
		t.Fatal(err)
	}
	if first, _ := st.MarkReportOffered(ctx, org); first {
		t.Fatal("já estava convidado")
	}
}

func TestDayTotalsWithoutResults(t *testing.T) {
	st := store.New(testdb.New(t))
	org, err := st.CreateOrganization(ctx, "Loja", "varejo", 100, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := st.DayTotals(ctx, org, time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)); err != nil || ok {
		t.Fatalf("sem resultado: ok=%v err=%v", ok, err)
	}
}
