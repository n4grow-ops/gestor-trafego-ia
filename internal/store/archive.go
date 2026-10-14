package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// PendingProject é o pedido de projeto novo parado no limite do plano, esperando o cliente escolher qual
// projeto arquivar. Options são os projetos mostrados, na ordem da lista (o número que o cliente responde).
type PendingProject struct {
	Name    string          `json:"name"`
	Fields  contract.Fields `json:"fields"`
	Options []int64         `json:"options"`
}

// SetPendingProject guarda (ou, com nil, limpa) o pedido de projeto parado no limite.
func (s *Store) SetPendingProject(ctx context.Context, convID int64, p *PendingProject) error {
	var raw any
	if p != nil {
		b, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("store: %w", err)
		}
		raw = string(b)
	}
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET pending_project = $2 WHERE id = $1`, convID, raw)
	return err
}

// GetPendingProject devolve o pedido parado no limite, se houver.
func (s *Store) GetPendingProject(ctx context.Context, convID int64) (PendingProject, bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT pending_project FROM conversations WHERE id = $1`, convID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && raw == nil) {
		return PendingProject{}, false, nil
	}
	if err != nil {
		return PendingProject{}, false, err
	}
	var p PendingProject
	if err := json.Unmarshal(raw, &p); err != nil {
		return PendingProject{}, false, fmt.Errorf("store: projeto pendente inválido: %w", err)
	}
	return p, true, nil
}

// ArchiveProject arquiva o projeto da organização. Proposta aberta dele é cancelada e a conversa que estava
// nele volta ao início. Nada muda nas campanhas já criadas na Meta. ErrNotFound se não existe ou já foi
// arquivado.
func (s *Store) ArchiveProject(ctx context.Context, orgID, projectID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE projects SET stage = 'archived', updated_at = now()
		WHERE id = $1 AND organization_id = $2 AND stage <> 'archived'`, projectID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE campaign_drafts SET status = 'cancelled' WHERE project_id = $1 AND status = 'proposed'`, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET active_project_id = NULL, state = 'idle', fields = '{}', updated_at = now()
		WHERE active_project_id = $1`, projectID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
