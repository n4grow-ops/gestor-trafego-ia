package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetPublication devolve a publicação pelo id.
func (s *Store) GetPublication(ctx context.Context, id int64) (Publication, error) {
	p, err := scanPublication(s.pool.QueryRow(ctx, `SELECT `+publicationCols+` FROM meta_publications WHERE id = $1`, id))
	return p, notFound(err)
}

// RejectPublication marca a publicação como reprovada pela Meta, com o motivo.
func (s *Store) RejectPublication(ctx context.Context, id int64, reason string) error {
	_, err := s.pool.Exec(ctx, `UPDATE meta_publications SET status = 'rejected', error = left($2, 2000), updated_at = now()
		WHERE id = $1 AND status IN ('paused', 'active')`, id, reason)
	return err
}

// ReopenForCreatives devolve o rascunho aprovado para a etapa dos anúncios depois da reprovação: apaga os
// anúncios recusados, limpa os textos finais e o aviso de formato, volta o projeto para "aguardando os
// anúncios" e, se a conversa está livre (idle ou coletando), põe ela nessa etapa. Devolve a conversa e se ela
// foi movida.
func (s *Store) ReopenForCreatives(ctx context.Context, draftID int64) (int64, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	var convID int64
	var projectID *int64
	err = tx.QueryRow(ctx, `UPDATE campaign_drafts SET final_copies = NULL, creatives_nudged = false
		WHERE id = $1 AND status = 'approved' RETURNING conversation_id, project_id`, draftID).Scan(&convID, &projectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, ErrNotFound
	}
	if err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM creatives WHERE draft_id = $1`, draftID); err != nil {
		return 0, false, err
	}
	if projectID != nil {
		if _, err := tx.Exec(ctx, `UPDATE projects SET stage = 'awaiting_creatives', updated_at = now() WHERE id = $1`, *projectID); err != nil {
			return 0, false, err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE conversations SET state = 'awaiting_creatives', active_project_id = coalesce($2, active_project_id), updated_at = now()
		WHERE id = $1 AND state IN ('idle', 'collecting', 'awaiting_creatives')`, convID, projectID)
	if err != nil {
		return 0, false, err
	}
	return convID, tag.RowsAffected() > 0, tx.Commit(ctx)
}
