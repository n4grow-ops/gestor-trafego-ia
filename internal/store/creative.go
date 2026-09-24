package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// StateAwaitingCreatives: proposta aprovada, esperando as imagens ou vídeos do anúncio.
const StateAwaitingCreatives = "awaiting_creatives"

// ApprovedDraft é o rascunho aprovado mais recente da conversa.
type ApprovedDraft struct {
	ID        int64
	ProjectID *int64
}

func (s *Store) LatestApprovedDraft(ctx context.Context, convID int64) (ApprovedDraft, error) {
	var d ApprovedDraft
	err := s.pool.QueryRow(ctx, `SELECT id, project_id FROM campaign_drafts
		WHERE conversation_id = $1 AND status = 'approved' ORDER BY id DESC LIMIT 1`, convID).Scan(&d.ID, &d.ProjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ApprovedDraft{}, ErrNotFound
	}
	return d, err
}

// Creative é uma imagem ou vídeo do anúncio, guardado no Supabase Storage.
type Creative struct {
	OrgID     int64
	DraftID   int64
	ProjectID *int64
	Kind      string // image ou video
	Path      string
	Mime      string
	Size      int64
}

func (s *Store) AddCreative(ctx context.Context, c Creative) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO creatives (organization_id, draft_id, project_id, kind, storage_path, mime, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		c.OrgID, c.DraftID, c.ProjectID, c.Kind, c.Path, c.Mime, c.Size).Scan(&id)
	return id, err
}

func (s *Store) CountCreatives(ctx context.Context, draftID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM creatives WHERE draft_id = $1`, draftID).Scan(&n)
	return n, err
}
