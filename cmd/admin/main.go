// Comando admin: cadastro de organizações e membros e retorno de atendimento humano.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/admin"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load(os.Getenv)
	if err := cfg.Require("DATABASE_URL"); err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	return admin.Run(ctx, store.New(pool), os.Args[1:], os.Stdout)
}
