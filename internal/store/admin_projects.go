package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// ProjectByID devolve o projeto pelo id, de qualquer organização (o painel acha a organização por ele).
func (s *Store) ProjectByID(ctx context.Context, id int64) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id = $1`, id))
}

// OrgMaxDailyBudget é o teto de orçamento por dia da organização.
func (s *Store) OrgMaxDailyBudget(ctx context.Context, orgID int64) (float64, error) {
	var v float64
	err := s.pool.QueryRow(ctx, `SELECT max_daily_budget_brl::float8 FROM organizations WHERE id = $1`, orgID).Scan(&v)
	return v, notFound(err)
}

// RenameProject troca o nome do projeto (único na organização, sem diferenciar maiúsculas).
func (s *Store) RenameProject(ctx context.Context, orgID, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 60 {
		return fmt.Errorf("%w: nome do projeto com 1 a 60 caracteres", ErrInvalidInput)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET name = $3, updated_at = now() WHERE id = $1 AND organization_id = $2`, id, orgID, name)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return fmt.Errorf("%w: já existe um projeto com esse nome", ErrInvalidInput)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetProjectFields grava os dados da campanha do projeto e, se uma conversa está nele, os dela também
// (a conversa guarda os dados do projeto ativo).
func (s *Store) SetProjectFields(ctx context.Context, orgID, id int64, f contract.Fields) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE projects SET fields = $3, updated_at = now() WHERE id = $1 AND organization_id = $2`, id, orgID, string(raw))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET fields = $2, updated_at = now() WHERE active_project_id = $1`, id, string(raw)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
