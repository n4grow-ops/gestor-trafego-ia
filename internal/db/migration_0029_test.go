package db_test

import (
	"context"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

// A 0029 roda de novo sem erro (colunas e trava já existem) e os criativos antigos ficam como upload confirmado.
func TestMigration0029AppliesTwice(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t) // já migrado uma vez
	var oldID int64
	err := pool.QueryRow(ctx, `WITH o AS (INSERT INTO organizations (name, niche, max_daily_budget_brl, allowed_objectives)
			VALUES ('Org', 'restaurante', 100, '{OUTCOME_ENGAGEMENT}') RETURNING id),
		m AS (INSERT INTO members (organization_id, name, role) SELECT id, 'Ana', 'owner' FROM o RETURNING id),
		w AS (INSERT INTO whatsapp_identities (member_id, provider, phone_e164) SELECT id, 'waha', '5511988887777' FROM m RETURNING id),
		c AS (INSERT INTO conversations (identity_id) SELECT id FROM w RETURNING id),
		d AS (INSERT INTO campaign_drafts (organization_id, conversation_id, briefing, proposal, proposal_hash, status)
			SELECT o.id, c.id, '{}', '{}', 'h', 'proposed' FROM o, c RETURNING id, organization_id)
		INSERT INTO creatives (organization_id, draft_id, kind, storage_path, mime, size_bytes)
		SELECT organization_id, id, 'image', 'a.jpg', 'image/jpeg', 1 FROM d RETURNING id`).Scan(&oldID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version LIKE '%0029%'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("segunda aplicação da 0029: %v", err)
	}
	var origin, mediaID string
	var rights bool
	if err := pool.QueryRow(ctx, `SELECT origin, ig_media_id, rights_confirmed FROM creatives WHERE id = $1`, oldID).
		Scan(&origin, &mediaID, &rights); err != nil {
		t.Fatal(err)
	}
	if origin != "upload" || mediaID != "" || !rights {
		t.Fatalf("criativo antigo: origin = %q ig_media_id = %q rights_confirmed = %v", origin, mediaID, rights)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conname = 'creatives_origin_check'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("trava da origem = %d (%v)", n, err)
	}
}
