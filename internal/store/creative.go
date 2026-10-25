package store

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// StateAwaitingCreatives: proposta aprovada, esperando as imagens ou vídeos do anúncio.
const StateAwaitingCreatives = "awaiting_creatives"

// ApprovedDraft é o rascunho aprovado mais recente da conversa.
type ApprovedDraft struct {
	ID        int64
	ProjectID *int64
	Nudged    bool // já avisou do posicionamento faltando
	// Placements: onde o cliente pediu que o anúncio apareça (da proposta aprovada); vazio = automático.
	Placements []string
}

func (s *Store) LatestApprovedDraft(ctx context.Context, convID int64) (ApprovedDraft, error) {
	var d ApprovedDraft
	err := s.pool.QueryRow(ctx, `SELECT id, project_id, creatives_nudged, coalesce(proposal->'placements', '[]'::jsonb) FROM campaign_drafts
		WHERE conversation_id = $1 AND status = 'approved' ORDER BY id DESC LIMIT 1`, convID).Scan(&d.ID, &d.ProjectID, &d.Nudged, &d.Placements)
	if errors.Is(err, pgx.ErrNoRows) {
		return ApprovedDraft{}, ErrNotFound
	}
	return d, err
}

func (s *Store) MarkCreativesNudged(ctx context.Context, draftID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE campaign_drafts SET creatives_nudged = true WHERE id = $1`, draftID)
	return err
}

// Creative é uma imagem ou vídeo do anúncio, guardado no Supabase Storage.
type Creative struct {
	ID         int64
	OrgID      int64
	DraftID    int64
	ProjectID  *int64
	Kind       string // image ou video
	Path       string
	Mime       string
	Size       int64
	Width      int
	Height     int
	DurationMS int
	Format     string // square, portrait, vertical, landscape, other
	Placement  string // feed ou stories
	// Origin: upload (WhatsApp), instagram_own (post da conta conectada) ou instagram_public. Vazio = upload.
	Origin    string
	IGMediaID string // id do post na Graph API (só instagram_own)
	FBPostID  string // id do post da Página, <página>_<post> (só facebook_own)
	// RightsPending: post público sem o cliente confirmar que pode usar; não vai para a Meta.
	RightsPending bool
	SourceURL     string // link do post
	SourceCaption string // legenda do post (vira referência no SÓ REFERÊNCIA)
	// SourcePosition: posição do item no post (0 = primeira mídia); nil = upload. Só gravado, não lido.
	SourcePosition *int
}

// Origens dos anúncios.
const (
	OriginUpload          = "upload"
	OriginInstagramOwn    = "instagram_own"
	OriginInstagramPublic = "instagram_public"
	OriginFacebookOwn     = "facebook_own"
)

const creativeCols = `id, organization_id, draft_id, project_id, kind, storage_path, mime, size_bytes,
	coalesce(width, 0), coalesce(height, 0), coalesce(duration_ms, 0), format, placement,
	origin, ig_media_id, fb_post_id, NOT rights_confirmed, source_url, source_caption`

func scanCreative(r pgx.Row) (Creative, error) {
	var c Creative
	err := r.Scan(&c.ID, &c.OrgID, &c.DraftID, &c.ProjectID, &c.Kind, &c.Path, &c.Mime, &c.Size,
		&c.Width, &c.Height, &c.DurationMS, &c.Format, &c.Placement,
		&c.Origin, &c.IGMediaID, &c.FBPostID, &c.RightsPending, &c.SourceURL, &c.SourceCaption)
	return c, err
}

func (s *Store) AddCreative(ctx context.Context, c Creative) (int64, error) {
	if c.Format == "" {
		c.Format = "other"
	}
	if c.Placement == "" {
		c.Placement = "feed"
	}
	if c.Origin == "" {
		c.Origin = OriginUpload
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO creatives (organization_id, draft_id, project_id, kind, storage_path, mime, size_bytes,
			width, height, duration_ms, format, placement, origin, ig_media_id, rights_confirmed, source_url, source_caption,
			source_position, fb_post_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, 0), nullif($9, 0), nullif($10, 0), $11, $12, $13, $14, $15, $16, $17, $18, $19) RETURNING id`,
		c.OrgID, c.DraftID, c.ProjectID, c.Kind, c.Path, c.Mime, c.Size, c.Width, c.Height, c.DurationMS, c.Format, c.Placement,
		c.Origin, c.IGMediaID, !c.RightsPending, c.SourceURL, c.SourceCaption, c.SourcePosition, c.FBPostID).Scan(&id)
	return id, err
}

// HasInstagramCreative diz se o item do post (posição pos) já está no rascunho: pelo link do post ou,
// vindo da Graph API, pelo id do post. A nova tentativa do job pula o que já guardou.
func (s *Store) HasInstagramCreative(ctx context.Context, draftID int64, sourceURL, igMediaID string, pos int) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM creatives
		WHERE draft_id = $1 AND source_position = $4
		  AND ((source_url <> '' AND source_url = $2) OR (ig_media_id <> '' AND ig_media_id = $3)))`,
		draftID, sourceURL, igMediaID, pos).Scan(&ok)
	return ok, err
}

func (s *Store) CountCreatives(ctx context.Context, draftID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM creatives WHERE draft_id = $1`, draftID).Scan(&n)
	return n, err
}

// ListCreatives devolve os anúncios do rascunho na ordem de envio (a numeração que o cliente vê).
func (s *Store) ListCreatives(ctx context.Context, draftID int64) ([]Creative, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+creativeCols+` FROM creatives WHERE draft_id = $1 ORDER BY id`, draftID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Creative, error) { return scanCreative(r) })
}

// ConfirmCreativeRights: o cliente disse que o post público é dele (ou que tem autorização). Devolve quantos mudaram.
func (s *Store) ConfirmCreativeRights(ctx context.Context, draftID int64) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE creatives SET rights_confirmed = true WHERE draft_id = $1 AND NOT rights_confirmed`, draftID)
	return tag.RowsAffected(), err
}

// DeletePendingCreatives apaga os posts públicos sem confirmação (SÓ REFERÊNCIA) e devolve os apagados,
// na ordem de envio, para apagar os arquivos no Storage e guardar link e legenda como referência.
func (s *Store) DeletePendingCreatives(ctx context.Context, draftID int64) ([]Creative, error) {
	rows, err := s.pool.Query(ctx, `DELETE FROM creatives WHERE draft_id = $1 AND NOT rights_confirmed RETURNING `+creativeCols, draftID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Creative, error) { return scanCreative(r) })
	slices.SortFunc(list, func(a, b Creative) int { return cmp.Compare(a.ID, b.ID) })
	return list, err
}

// DeleteCreative apaga o registro e devolve o caminho do arquivo, para apagar também no Storage.
func (s *Store) DeleteCreative(ctx context.Context, draftID, id int64) (string, error) {
	var path string
	err := s.pool.QueryRow(ctx, `DELETE FROM creatives WHERE id = $1 AND draft_id = $2 RETURNING storage_path`, id, draftID).Scan(&path)
	return path, notFound(err)
}

// PreviewData é o que a prévia do anúncio mostra: nome da conta e o primeiro texto da proposta.
type PreviewData struct {
	Account string
	Copy    contract.Copy
}

func (s *Store) PreviewData(ctx context.Context, draftID int64) (PreviewData, error) {
	var raw []byte
	var p PreviewData
	err := s.pool.QueryRow(ctx, `SELECT d.proposal,
			coalesce(nullif(trim(o.instagram), ''), nullif(trim(o.trade_name), ''), nullif(trim(o.profile->>'business_name'), ''), o.name)
		FROM campaign_drafts d JOIN organizations o ON o.id = d.organization_id WHERE d.id = $1`, draftID).Scan(&raw, &p.Account)
	if err != nil {
		return PreviewData{}, notFound(err)
	}
	var prop contract.CampaignProposal
	if err := json.Unmarshal(raw, &prop); err != nil {
		return PreviewData{}, fmt.Errorf("store: proposta inválida: %w", err)
	}
	if len(prop.Copies) > 0 {
		p.Copy = prop.Copies[0]
	}
	return p, nil
}

// PostChoice é um post recente oferecido ao cliente para virar anúncio.
type PostChoice struct {
	Source    string `json:"source"` // instagram | facebook
	ID        string `json:"id"`
	Permalink string `json:"permalink"`
}

// PostOffer é a última lista de posts mostrada no rascunho, na ordem (o número que o cliente responde).
type PostOffer struct {
	At    time.Time    `json:"at"`
	Items []PostChoice `json:"items"`
}

// SetPostOffer guarda a lista de posts mostrada ao cliente.
func (s *Store) SetPostOffer(ctx context.Context, draftID int64, o PostOffer) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_, err = s.pool.Exec(ctx, `UPDATE campaign_drafts SET post_choices = $2 WHERE id = $1`, draftID, raw)
	return err
}

// PostOffer devolve a última lista de posts mostrada no rascunho (sem itens = nenhuma).
func (s *Store) PostOffer(ctx context.Context, draftID int64) (PostOffer, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT post_choices FROM campaign_drafts WHERE id = $1`, draftID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return PostOffer{}, ErrNotFound
	}
	if err != nil {
		return PostOffer{}, err
	}
	var o PostOffer
	if json.Unmarshal(raw, &o) != nil {
		return PostOffer{}, nil // valor antigo ou estranho: nenhuma lista
	}
	return o, nil
}
