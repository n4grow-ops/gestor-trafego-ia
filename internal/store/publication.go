package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// ErrAlreadyPublished: o rascunho já tem publicação (em andamento ou concluída).
var ErrAlreadyPublished = errors.New("store: rascunho já publicado")

// Publication é a campanha criada na conta de anúncios do cliente.
type Publication struct {
	ID           int64
	OrgID        int64
	DraftID      int64
	ProjectID    *int64
	ConnectionID int64
	Status       string // publishing | paused | active | failed
	CampaignID   string
	AdSetID      string
	AdIDs        []string
	Warnings     []string
	Error        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ActivatedAt  *time.Time
	ProjectName  string // só em ListPublications
}

// PublicationResult são os IDs devolvidos pela Meta.
type PublicationResult struct {
	CampaignID string
	AdSetID    string
	AdIDs      []string
	Warnings   []string
}

// PublishData é o que a publicação lê do rascunho aprovado.
type PublishData struct {
	DraftID     int64
	OrgID       int64
	ProjectID   *int64
	OrgName     string
	ProjectName string
	Proposal    json.RawMessage // contract.CampaignProposal
	Fields      json.RawMessage // contract.Fields (offer, location...)
	ClientCopy  string
}

// Limites dos textos gravados.
const (
	maxClientCopy       = 2000
	maxPublicationError = 2000
)

const publicationCols = `id, organization_id, draft_id, project_id, meta_connection_id, status, campaign_id, adset_id,
	ad_ids, error, warnings, created_at, updated_at, activated_at`

func scanPublication(row pgx.Row) (Publication, error) {
	var p Publication
	err := row.Scan(&p.ID, &p.OrgID, &p.DraftID, &p.ProjectID, &p.ConnectionID, &p.Status, &p.CampaignID, &p.AdSetID,
		&p.AdIDs, &p.Error, &p.Warnings, &p.CreatedAt, &p.UpdatedAt, &p.ActivatedAt)
	return p, err
}

// nonNil evita NULL nas colunas text[] NOT NULL.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// MetaTokenSealed devolve o token cifrado da conexão ativa (ErrNotFound sem conexão).
func (s *Store) MetaTokenSealed(ctx context.Context, orgID int64) (connID int64, sealed []byte, err error) {
	err = s.pool.QueryRow(ctx, `SELECT id, token_ciphertext FROM meta_connections
		WHERE organization_id = $1 AND status = 'active' AND token_ciphertext IS NOT NULL`, orgID).Scan(&connID, &sealed)
	if err != nil {
		return 0, nil, notFound(err)
	}
	return connID, sealed, nil
}

// StartPublication abre a publicação do rascunho; se já existe uma que não falhou, devolve ela com ErrAlreadyPublished.
// Uma publicação que falhou volta para publishing (mesma linha, IDs e erro limpos, created_at renovado para ela
// voltar a ser a mais recente). O rascunho precisa ser da organização.
func (s *Store) StartPublication(ctx context.Context, orgID, draftID int64, projectID *int64, connID int64) (Publication, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Publication{}, err
	}
	defer tx.Rollback(ctx)
	p, err := scanPublication(tx.QueryRow(ctx, `INSERT INTO meta_publications (organization_id, draft_id, project_id, meta_connection_id)
		SELECT d.organization_id, d.id, $3, $4 FROM campaign_drafts d WHERE d.id = $2 AND d.organization_id = $1
		ON CONFLICT (draft_id) DO NOTHING RETURNING `+publicationCols, orgID, draftID, projectID, connID))
	if err == nil {
		return p, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Publication{}, err
	}
	// Sem linha nova: ou o rascunho não é da organização, ou já existe publicação (trava a linha).
	p, err = scanPublication(tx.QueryRow(ctx, `SELECT `+publicationCols+` FROM meta_publications
		WHERE draft_id = $1 AND organization_id = $2 FOR UPDATE`, draftID, orgID))
	if err != nil {
		return Publication{}, notFound(err)
	}
	if p.Status != "failed" {
		return p, ErrAlreadyPublished
	}
	p, err = scanPublication(tx.QueryRow(ctx, `UPDATE meta_publications SET status = 'publishing', project_id = $2, meta_connection_id = $3,
			campaign_id = '', adset_id = '', ad_ids = '{}', warnings = '{}', error = '', activated_at = NULL,
				created_at = now(), updated_at = now()
		WHERE id = $1 RETURNING `+publicationCols, p.ID, projectID, connID))
	if err != nil {
		return Publication{}, err
	}
	return p, tx.Commit(ctx)
}

// FinishPublication grava os IDs e deixa a publicação pausada (ErrNotFound se não estava publicando).
func (s *Store) FinishPublication(ctx context.Context, id int64, r PublicationResult) error {
	if strings.TrimSpace(r.CampaignID) == "" {
		return errors.New("store: publicação sem campanha")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE meta_publications SET status = 'paused', campaign_id = $2, adset_id = $3, ad_ids = $4,
			warnings = $5, error = '', updated_at = now()
		WHERE id = $1 AND status = 'publishing'`, id, r.CampaignID, r.AdSetID, nonNil(r.AdIDs), nonNil(r.Warnings))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FailPublication marca a falha com a mensagem técnica (ErrNotFound se não estava publicando).
func (s *Store) FailPublication(ctx context.Context, id int64, msg string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE meta_publications SET status = 'failed', error = left($2, $3), updated_at = now()
		WHERE id = $1 AND status = 'publishing'`, id, RedactSecrets(msg), maxPublicationError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPublicationStatus muda para active ou paused (active grava activated_at).
// Só vale para publicação já criada na Meta (paused ou active); senão ErrNotFound.
func (s *Store) SetPublicationStatus(ctx context.Context, id int64, status string) error {
	if status != "active" && status != "paused" {
		return fmt.Errorf("store: status de publicação inválido: %q", status)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE meta_publications SET status = $2, updated_at = now(),
			activated_at = CASE WHEN $2 = 'active' AND status <> 'active' THEN now() ELSE activated_at END
		WHERE id = $1 AND status IN ('paused', 'active')`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LatestPublication devolve a publicação mais recente da organização (ErrNotFound se nenhuma).
func (s *Store) LatestPublication(ctx context.Context, orgID int64) (Publication, error) {
	p, err := scanPublication(s.pool.QueryRow(ctx, `SELECT `+publicationCols+` FROM meta_publications
		WHERE organization_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, orgID))
	if err != nil {
		return Publication{}, notFound(err)
	}
	return p, nil
}

// ListPublications devolve as publicações da organização no status, da mais antiga para a mais nova,
// com o nome do projeto (vazio sem projeto).
func (s *Store) ListPublications(ctx context.Context, orgID int64, status string) ([]Publication, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id, m.organization_id, m.draft_id, m.project_id, m.meta_connection_id, m.status,
			m.campaign_id, m.adset_id, m.ad_ids, m.error, m.warnings, m.created_at, m.updated_at, m.activated_at, coalesce(p.name, '')
		FROM meta_publications m LEFT JOIN projects p ON p.id = m.project_id
		WHERE m.organization_id = $1 AND m.status = $2 ORDER BY m.created_at, m.id LIMIT 50`, orgID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Publication
	for rows.Next() {
		var p Publication
		if err := rows.Scan(&p.ID, &p.OrgID, &p.DraftID, &p.ProjectID, &p.ConnectionID, &p.Status, &p.CampaignID, &p.AdSetID,
			&p.AdIDs, &p.Error, &p.Warnings, &p.CreatedAt, &p.UpdatedAt, &p.ActivatedAt, &p.ProjectName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AppendClientCopy acumula referências de headline/texto do cliente no rascunho (até 2000 runas).
// Passou do limite, o excesso é cortado; o que já estava fica.
func (s *Store) AppendClientCopy(ctx context.Context, draftID int64, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	// left() conta caracteres, não bytes.
	tag, err := s.pool.Exec(ctx, `UPDATE campaign_drafts SET client_copy =
			left(CASE WHEN client_copy = '' THEN $2 ELSE client_copy || E'\n' || $2 END, $3)
		WHERE id = $1`, draftID, text, maxClientCopy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PublishData lê o rascunho aprovado para publicar (ErrNotFound se não existe ou não foi aprovado).
// Fields são os campos do projeto com os do briefing aprovado por cima: o que foi aprovado vale.
func (s *Store) PublishData(ctx context.Context, draftID int64) (PublishData, error) {
	var d PublishData
	var proposal, briefing, projectFields []byte
	err := s.pool.QueryRow(ctx, `SELECT d.id, d.organization_id, d.project_id, o.name, coalesce(p.name, ''),
			d.proposal, d.briefing, coalesce(p.fields, '{}'), d.client_copy
		FROM campaign_drafts d
		JOIN organizations o ON o.id = d.organization_id
		LEFT JOIN projects p ON p.id = d.project_id
		WHERE d.id = $1 AND d.status = 'approved'`, draftID).
		Scan(&d.DraftID, &d.OrgID, &d.ProjectID, &d.OrgName, &d.ProjectName, &proposal, &briefing, &projectFields, &d.ClientCopy)
	if err != nil {
		return PublishData{}, notFound(err)
	}
	var f contract.Fields
	if err := json.Unmarshal(projectFields, &f); err != nil {
		return PublishData{}, fmt.Errorf("store: campos do projeto inválidos: %w", err)
	}
	var b contract.Briefing
	if err := json.Unmarshal(briefing, &b); err != nil {
		return PublishData{}, fmt.Errorf("store: briefing inválido: %w", err)
	}
	f = f.Merge(contract.Fields{Offer: contract.Str(b.Offer), Audience: contract.Str(b.Audience), Location: contract.Str(b.Location),
		ObjectiveHint: contract.Str(b.ObjectiveHint), DailyBudgetBRL: contract.Num(b.DailyBudgetBRL)})
	if len(b.Media) > 0 {
		f.Media = b.Media
	}
	fields, err := json.Marshal(f)
	if err != nil {
		return PublishData{}, fmt.Errorf("store: %w", err)
	}
	d.Proposal = json.RawMessage(proposal)
	d.Fields = json.RawMessage(fields)
	return d, nil
}
