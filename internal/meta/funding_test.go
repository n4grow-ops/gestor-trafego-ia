package meta_test

import (
	"context"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

func TestFundingPrepaid(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9", `{"account_status":1,"is_prepay_account":true,"funding_source":"FS1",
		"funding_source_details":{"id":"FS1","display_string":"Saldo disponível (R$1.234,56 BRL)","type":20},
		"spend_cap":"0","amount_spent":"5000"}`)
	got, err := c.Funding(context.Background(), tok, "9")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Prepaid || !got.HasSource || got.AvailableCents != 123456 || got.SpendCapCents != 0 || got.Status != 1 {
		t.Fatalf("%+v", got)
	}
	if fields := f.one("GET /act_9").Form.Get("fields"); fields == "" {
		t.Error("sem fields")
	}
}

func TestFundingCardAndCap(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9", `{"account_status":1,"is_prepay_account":false,"funding_source":"FS2",
		"funding_source_details":{"id":"FS2","display_string":"Visa *1234","type":1},"spend_cap":"20000","amount_spent":"15000"}`)
	got, err := c.Funding(context.Background(), tok, "act_9")
	if err != nil {
		t.Fatal(err)
	}
	if got.Prepaid || !got.HasSource || got.Source != "Visa *1234" || got.AvailableCents != -1 || got.SpendCapCents != 20000 || got.SpentCents != 15000 {
		t.Fatalf("%+v", got)
	}
}

func TestFundingNoSource(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9", `{"account_status":1,"is_prepay_account":true,"spend_cap":"0","amount_spent":"0"}`)
	got, err := c.Funding(context.Background(), tok, "9")
	if err != nil || got.HasSource || got.AvailableCents != -1 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestParseBRL(t *testing.T) {
	for in, want := range map[string]int64{
		"Saldo disponível (R$1.234,56 BRL)": 123456,
		"Available balance (R$50.00 BRL)":   5000,
		"Saldo disponível (R$ 0,00)":        0,
		"Visa *1234":                        -1,
		"R$ 10":                             1000,
	} {
		if got := meta.ParseBRL(in); got != want {
			t.Errorf("%q = %d, quer %d", in, got, want)
		}
	}
}
