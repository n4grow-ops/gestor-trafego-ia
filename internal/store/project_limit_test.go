package store_test

import (
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestProjectLimitFromPlan(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	// Sem assinatura (cliente trazido pela equipe): sem limite.
	for _, n := range []string{"A", "B", "C", "D"} {
		if _, _, err := st.CreateOrReopenProject(ctx, ident.OrgID, n, contract.Fields{}); err != nil {
			t.Fatalf("%s sem assinatura: %v", n, err)
		}
	}
	_, err := pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'free', 'active')`, ident.OrgID)
	must(t, err)
	_, _, err = st.CreateOrReopenProject(ctx, ident.OrgID, "E", contract.Fields{})
	var pl *store.ProjectLimitError
	if !errors.As(err, &pl) || pl.Max != 3 {
		t.Fatalf("Free com 4 projetos: %v", err)
	}
	// Reabrir um que já existe continua valendo.
	if _, _, err := st.CreateOrReopenProject(ctx, ident.OrgID, "a", contract.Fields{}); err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	// Arquivados não contam.
	_, err = pool.Exec(ctx, `UPDATE projects SET stage = 'archived' WHERE organization_id = $1 AND name IN ('B', 'C')`, ident.OrgID)
	must(t, err)
	if _, _, err := st.CreateOrReopenProject(ctx, ident.OrgID, "E", contract.Fields{}); err != nil {
		t.Fatalf("com 2 arquivados: %v", err)
	}
}
