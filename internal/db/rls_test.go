package db_test

import (
	"context"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

// No Supabase, tabela sem RLS em schema exposto vira leitura pública pela Data API.
// Toda tabela do app precisa nascer com RLS ligado.
func TestEveryTableHasRowLevelSecurity(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	rows, err := pool.Query(ctx, `SELECT c.relname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind = 'r' AND NOT c.relrowsecurity`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		t.Errorf("tabela %s sem RLS", name)
	}
}
