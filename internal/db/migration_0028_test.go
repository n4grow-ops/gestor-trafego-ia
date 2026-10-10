package db_test

import (
	"context"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

// A 0028 roda de novo sem erro (produção pode recebê-la por outro caminho) e cria as colunas.
func TestMigration0028IsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	for range 2 {
		if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version LIKE '%0028%'`); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'tester_requests'
		AND column_name IN ('notified_at', 'accept_notified_at')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("colunas novas = %d", n)
	}
}
