package store_test

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

func TestLLMSpendAccumulatesPerKey(t *testing.T) {
	st := store.New(testdb.New(t))
	if usd, err := st.LLMSpent(ctx, "chave-a"); err != nil || usd != 0 {
		t.Fatalf("sem gasto = %v %v", usd, err)
	}
	for _, v := range []float64{0.25, 0.5} {
		if err := st.AddLLMSpend(ctx, "chave-a", v); err != nil {
			t.Fatal(err)
		}
	}
	if usd, _ := st.LLMSpent(ctx, "chave-a"); usd != 0.75 {
		t.Fatalf("gasto da chave a = %v, quer 0.75", usd)
	}
	if usd, _ := st.LLMSpent(ctx, "chave-b"); usd != 0 {
		t.Fatalf("chave b herdou gasto: %v", usd)
	}
}
