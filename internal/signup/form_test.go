package signup_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
)

func TestValidCPF(t *testing.T) {
	for _, ok := range []string{"529.982.247-25", "52998224725", "111.444.777-35"} {
		if !signup.ValidCPF(ok) {
			t.Errorf("ValidCPF(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "123", "111.111.111-11", "529.982.247-24", "5299822472a"} {
		if signup.ValidCPF(bad) {
			t.Errorf("ValidCPF(%q) = true", bad)
		}
	}
}

func TestValidCNPJ(t *testing.T) {
	for _, ok := range []string{"11.222.333/0001-81", "11222333000181", "33.000.167/0001-01"} {
		if !signup.ValidCNPJ(ok) {
			t.Errorf("ValidCNPJ(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "11.222.333/0001-80", "00000000000000", "1122233300018"} {
		if signup.ValidCNPJ(bad) {
			t.Errorf("ValidCNPJ(%q) = true", bad)
		}
	}
}

func validForm() signup.Form {
	return signup.Form{
		FullName: "  Ana  Souza ", BusinessName: "Pizzaria Boa", Segment: "restaurante",
		City: "Campinas", UF: "sp", Objectives: []string{"mensagens"}, MonthlyBudget: "500_1500",
		AdsExperience: "nunca", MetaAdAccount: "nao_sei", Approver: "self", AcceptTerms: true,
	}
}

func TestNormalizeValidForm(t *testing.T) {
	f := validForm()
	f.CPF = "529.982.247-25"
	f.CNPJ = "11.222.333/0001-81"
	f.Instagram = " @pizzariaboa "
	got, err := f.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != "Ana Souza" || got.UF != "SP" || got.CPF != "52998224725" || got.CNPJ != "11222333000181" || got.Instagram != "@pizzariaboa" {
		t.Fatalf("normalizado = %+v", got)
	}
}

func TestNormalizeReportsFields(t *testing.T) {
	f := validForm()
	f.FullName = ""
	f.UF = "XX"
	f.Objectives = []string{"mensagens", "hackear"}
	f.CPF = "123"
	f.Segment = "outro"
	f.Approver = "other"
	f.AcceptTerms = false
	_, err := f.Normalize()
	var ve *signup.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("erro = %v", err)
	}
	for _, field := range []string{"full_name", "uf", "objectives", "cpf", "segment_other", "approver_name", "accept_terms"} {
		if ve.Fields[field] == "" {
			t.Errorf("sem erro em %s: %v", field, ve.Fields)
		}
	}
}

func TestObjectivesAreMultiple(t *testing.T) {
	f := validForm()
	f.Objectives = []string{"vendas", "mensagens", "vendas", " leads "}
	got, err := f.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	// sem repetição, na ordem da lista oficial
	if strings.Join(got.Objectives, ",") != "mensagens,vendas,leads" {
		t.Fatalf("objetivos = %v", got.Objectives)
	}
	f.Objectives = nil
	var ve *signup.ValidationError
	if _, err := f.Normalize(); !errors.As(err, &ve) || ve.Fields["objectives"] == "" {
		t.Fatalf("sem objetivo deveria falhar: %v", err)
	}
}

func TestNormalizeCutsLongText(t *testing.T) {
	f := validForm()
	f.BusinessName = strings.Repeat("a", 500)
	got, err := f.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(got.BusinessName)) != 120 {
		t.Fatalf("tamanho = %d", len([]rune(got.BusinessName)))
	}
}

func TestSegmentOtherIsStored(t *testing.T) {
	f := validForm()
	f.Segment, f.SegmentOther = "outro", "Pet shop"
	got, err := f.Normalize()
	if err != nil || got.SegmentLabel() != "Pet shop" {
		t.Fatalf("segmento = %q err = %v", got.SegmentLabel(), err)
	}
	if got := validForm(); got.SegmentLabel() != "Restaurante e alimentação" {
		t.Fatalf("rótulo = %q", got.SegmentLabel())
	}
}
