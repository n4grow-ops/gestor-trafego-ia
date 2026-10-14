package store

import (
	"context"
	"time"
)

// MediaLink é um arquivo do Storage privado com link assinado (o worker renova antes de vencer).
type MediaLink struct {
	ID       int64     `json:"id"`
	Kind     string    `json:"kind"` // message, creative ou preview
	RefID    int64     `json:"ref_id"`
	Bucket   string    `json:"-"`
	Path     string    `json:"-"`
	Mime     string    `json:"mime"`
	URL      string    `json:"-"`
	URLUntil time.Time `json:"-"`
}

const mediaLinkCols = `id, kind, ref_id, bucket, path, mime, url, url_until`

// SaveMediaLink grava (ou atualiza, pelo caminho) o link do arquivo e devolve o id.
func (s *Store) SaveMediaLink(ctx context.Context, l MediaLink) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO media_links (kind, ref_id, bucket, path, mime, url, url_until)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bucket, path) DO UPDATE SET url = EXCLUDED.url, url_until = EXCLUDED.url_until
		RETURNING id`, l.Kind, l.RefID, l.Bucket, l.Path, l.Mime, l.URL, l.URLUntil).Scan(&id)
	return id, err
}

func scanMediaLink(r interface{ Scan(...any) error }) (MediaLink, error) {
	var l MediaLink
	err := r.Scan(&l.ID, &l.Kind, &l.RefID, &l.Bucket, &l.Path, &l.Mime, &l.URL, &l.URLUntil)
	return l, err
}

// GetMediaLink devolve o link pelo id; ErrNotFound se não existe.
func (s *Store) GetMediaLink(ctx context.Context, id int64) (MediaLink, error) {
	l, err := scanMediaLink(s.pool.QueryRow(ctx, `SELECT `+mediaLinkCols+` FROM media_links WHERE id = $1`, id))
	return l, notFound(err)
}

// MediaLinksFor devolve os links de um item (ex.: as duas imagens da prévia de um rascunho), em ordem.
func (s *Store) MediaLinksFor(ctx context.Context, kind string, refID int64) ([]MediaLink, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mediaLinkCols+` FROM media_links WHERE kind = $1 AND ref_id = $2 ORDER BY id`, kind, refID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaLink
	for rows.Next() {
		l, err := scanMediaLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// MediaLinksExpiring devolve até limit links que vencem antes de t (a renovação diária).
func (s *Store) MediaLinksExpiring(ctx context.Context, t time.Time, limit int) ([]MediaLink, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+mediaLinkCols+` FROM media_links WHERE url_until < $1 ORDER BY url_until LIMIT $2`, t, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaLink
	for rows.Next() {
		l, err := scanMediaLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RenewMediaLink troca a url assinada do link.
func (s *Store) RenewMediaLink(ctx context.Context, id int64, url string, until time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE media_links SET url = $2, url_until = $3 WHERE id = $1`, id, url, until)
	return err
}

// ProjectMedia é um arquivo do projeto que o painel mostra: anúncio (imagem ou vídeo) ou prévia.
type ProjectMedia struct {
	LinkID    int64  `json:"link_id"`
	Kind      string `json:"kind"` // image, video ou preview
	Format    string `json:"format,omitempty"`
	Placement string `json:"placement,omitempty"`
	Origin    string `json:"origin,omitempty"`
}

// ProjectMedia devolve os anúncios guardados do rascunho e as imagens da prévia, nessa ordem.
func (s *Store) ProjectMedia(ctx context.Context, draftID int64) ([]ProjectMedia, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, format, placement, origin FROM (
			SELECT l.id, c.kind, c.format, c.placement, coalesce(c.origin, '') AS origin
			FROM creatives c JOIN media_links l ON l.kind = 'creative' AND l.ref_id = c.id
			WHERE c.draft_id = $1
			UNION ALL
			SELECT l.id, 'preview', '', '', '' FROM media_links l WHERE l.kind = 'preview' AND l.ref_id = $1
		) t ORDER BY kind = 'preview', id`, draftID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectMedia{}
	for rows.Next() {
		var m ProjectMedia
		if err := rows.Scan(&m.LinkID, &m.Kind, &m.Format, &m.Placement, &m.Origin); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
