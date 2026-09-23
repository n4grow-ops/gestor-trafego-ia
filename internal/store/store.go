// Package store concentra o acesso ao PostgreSQL.
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("store: registro não encontrado")

// Estados da conversa.
const (
	StateIdle             = "idle"
	StateCollecting       = "collecting"
	StateAwaitingProposal = "awaiting_proposal"
	StateAwaitingApproval = "awaiting_approval"
	StateNeedsHuman       = "needs_human"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// querier é o que pool e transação têm em comum.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
