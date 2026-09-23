package db_test

import (
	"context"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

// Conversas que já existem antes da 0005 não podem juntar mensagens antigas no primeiro lote.
func TestMigration0005MarksExistingMessagesReplied(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	var convID, lastIn int64
	err := pool.QueryRow(ctx, `WITH o AS (INSERT INTO organizations (name, niche, max_daily_budget_brl, allowed_objectives)
			VALUES ('Org', 'restaurante', 100, '{OUTCOME_ENGAGEMENT}') RETURNING id),
		m AS (INSERT INTO members (organization_id, name, role) SELECT id, 'Ana', 'owner' FROM o RETURNING id),
		w AS (INSERT INTO whatsapp_identities (member_id, provider, phone_e164) SELECT id, 'waha', '5511988887777' FROM m RETURNING id)
		INSERT INTO conversations (identity_id) SELECT id FROM w RETURNING id`).Scan(&convID)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"in", "out", "in"} {
		if err := pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, direction, kind, body) VALUES ($1, $2, 'text', 'x') RETURNING id`,
			convID, dir).Scan(&lastIn); err != nil {
			t.Fatal(err)
		}
	}
	// Volta o banco para antes da 0005 e roda de novo.
	if _, err := pool.Exec(ctx, `ALTER TABLE conversations DROP COLUMN replied_through;
		ALTER TABLE llm_usage DROP CONSTRAINT llm_usage_agent_check;
		ALTER TABLE llm_usage ADD CONSTRAINT llm_usage_agent_check CHECK (agent IN ('attendant', 'manager', 'transcriber'));
		DELETE FROM schema_migrations WHERE version LIKE '%0005%'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var replied int64
	if err := pool.QueryRow(ctx, `SELECT replied_through FROM conversations WHERE id = $1`, convID).Scan(&replied); err != nil {
		t.Fatal(err)
	}
	if replied != lastIn {
		t.Fatalf("replied_through = %d; want %d (última entrada existente)", replied, lastIn)
	}
}
