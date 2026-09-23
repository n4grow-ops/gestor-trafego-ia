package db_test

import (
	"context"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

func TestMigrateCreatesTablesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t) // já migrado uma vez
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("segunda execução: %v", err)
	}
	tables := []string{"organizations", "members", "whatsapp_identities", "conversations", "messages",
		"inbox_events", "outbox_jobs", "campaign_drafts", "approval_codes", "llm_usage", "audit_events"}
	for _, table := range tables {
		var ok bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("tabela %s não existe", table)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("schema_migrations = %d linhas, want 3", n)
	}
}
