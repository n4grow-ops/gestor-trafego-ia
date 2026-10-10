package db_test

import (
	"context"
	"path/filepath"
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
		"inbox_events", "outbox_jobs", "campaign_drafts", "approval_codes", "llm_usage", "audit_events",
		"team_members", "admin_audit_log"}
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
	// Uma linha por arquivo: conta os arquivos para o teste não mudar a cada migration nova.
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations: %v %v", files, err)
	}
	if n != len(files) {
		t.Errorf("schema_migrations = %d linhas, want %d", n, len(files))
	}
}
