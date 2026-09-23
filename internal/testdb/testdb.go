// Package testdb cria um schema isolado e migrado por teste.
package testdb

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
)

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL não definido (ver README)")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("test_%d_%d", time.Now().UnixNano(), rand.IntN(1_000_000))

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("testdb: %v", err)
	}
	admin.Close(ctx)

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		c, err := pgx.Connect(context.Background(), url)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("testdb: migrate: %v", err)
	}
	return pool
}
