package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// Estágios de projeto.
const (
	StageCollecting        = "collecting"
	StageAwaitingProposal  = "awaiting_proposal"
	StageAwaitingApproval  = "awaiting_approval"
	StageApproved          = "approved"
	StageAwaitingCreatives = "awaiting_creatives" // etapa dos anúncios (imagens e vídeos)
	StageReady             = "ready"              // pronto para publicar na Meta
	StageArchived          = "archived"
)

type Project struct {
	ID        int64
	OrgID     int64
	Name      string
	Stage     string
	Fields    contract.Fields
	UpdatedAt time.Time
}

const projectCols = `id, organization_id, name, stage, fields, updated_at`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	var raw []byte
	if err := row.Scan(&p.ID, &p.OrgID, &p.Name, &p.Stage, &raw, &p.UpdatedAt); err != nil {
		return Project{}, notFound(err)
	}
	if err := json.Unmarshal(raw, &p.Fields); err != nil {
		return Project{}, fmt.Errorf("store: fields do projeto inválidos: %w", err)
	}
	return p, nil
}

// CreateOrReopenProject cria o projeto ou, se o nome já existe (sem diferenciar maiúsculas), reabre.
// Projeto aprovado ou arquivado volta para collecting; os campos existentes são mantidos.
func (s *Store) CreateOrReopenProject(ctx context.Context, orgID int64, name string, initial contract.Fields) (Project, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, false, errors.New("store: nome de projeto vazio")
	}
	fields, err := json.Marshal(initial)
	if err != nil {
		return Project{}, false, fmt.Errorf("store: %w", err)
	}
	var p Project
	var raw []byte
	var created bool
	err = s.pool.QueryRow(ctx, `INSERT INTO projects (organization_id, name, fields) VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, lower(name)) DO UPDATE
		SET stage = CASE WHEN projects.stage IN ('approved', 'archived') THEN 'collecting' ELSE projects.stage END,
		    updated_at = now()
		RETURNING `+projectCols+`, (xmax = 0)`, orgID, name, string(fields)).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Stage, &raw, &p.UpdatedAt, &created)
	if err != nil {
		return Project{}, false, err
	}
	if err := json.Unmarshal(raw, &p.Fields); err != nil {
		return Project{}, false, fmt.Errorf("store: %w", err)
	}
	return p, created, nil
}

func (s *Store) GetProject(ctx context.Context, orgID, id int64) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE id = $1 AND organization_id = $2`, id, orgID))
}

// ListProjects devolve os projetos da organização, do mais recente para o mais antigo.
func (s *Store) ListProjects(ctx context.Context, orgID int64) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectCols+` FROM projects
		WHERE organization_id = $1 AND stage <> 'archived' ORDER BY updated_at DESC, id DESC`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Project, error) { return scanProject(r) })
}

// FindProjects busca por semelhança de nome dentro da organização: igual (sem acento e sem caixa)
// vence; senão, nomes que contêm o termo ou estão contidos nele.
func (s *Store) FindProjects(ctx context.Context, orgID int64, query string) ([]Project, error) {
	all, err := s.ListProjects(ctx, orgID)
	if err != nil {
		return nil, err
	}
	q := normalize(query)
	if q == "" {
		return nil, nil
	}
	var exact, partial []Project
	for _, p := range all {
		n := normalize(p.Name)
		switch {
		case n == q:
			exact = append(exact, p)
		case strings.Contains(n, q) || strings.Contains(q, n):
			partial = append(partial, p)
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	return partial, nil
}

var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "ê", "e", "è", "e", "ë", "e",
	"í", "i", "î", "i", "ì", "i", "ï", "i",
	"ó", "o", "ô", "o", "õ", "o", "ò", "o", "ö", "o",
	"ú", "u", "û", "u", "ù", "u", "ü", "u", "ç", "c",
)

func normalize(s string) string {
	s = accents.Replace(strings.ToLower(strings.TrimSpace(s)))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// ActivateProject aponta a conversa para o projeto e carrega o estado de trabalho.
func (s *Store) ActivateProject(ctx context.Context, convID, projectID int64, state string, f contract.Fields) error {
	fields, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE conversations SET active_project_id = $2, state = $3, fields = $4, updated_at = now()
		WHERE id = $1`, convID, projectID, state, string(fields))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetProjectStage(ctx context.Context, projectID int64, stage string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET stage = $2, updated_at = now() WHERE id = $1`, projectID, stage)
	return err
}

// ProjectDraft é o essencial da última proposta de um projeto, para o resumo.
type ProjectDraft struct {
	ID         int64
	Status     string
	Proposal   contract.CampaignProposal
	CreatedAt  time.Time
	ApprovedAt *time.Time
}

func (s *Store) LatestProjectDraft(ctx context.Context, projectID int64) (ProjectDraft, bool, error) {
	var d ProjectDraft
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id, status, proposal, created_at, approved_at FROM campaign_drafts
		WHERE project_id = $1 ORDER BY id DESC LIMIT 1`, projectID).Scan(&d.ID, &d.Status, &raw, &d.CreatedAt, &d.ApprovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectDraft{}, false, nil
	}
	if err != nil {
		return ProjectDraft{}, false, err
	}
	if err := json.Unmarshal(raw, &d.Proposal); err != nil {
		return ProjectDraft{}, false, fmt.Errorf("store: %w", err)
	}
	return d, true, nil
}

func (s *Store) UpdateOrgProfile(ctx context.Context, orgID int64, p contract.Profile) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_, err = s.pool.Exec(ctx, `UPDATE organizations SET profile = $2 WHERE id = $1`, orgID, string(raw))
	return err
}

func (s *Store) SetOrgStatus(ctx context.Context, orgID int64, status string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET status = $2 WHERE id = $1`, orgID, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) SetOrgLimits(ctx context.Context, orgID int64, maxBudget float64, dailyLimit int) error {
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET max_daily_budget_brl = $2, daily_message_limit = $3 WHERE id = $1`,
		orgID, maxBudget, dailyLimit)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// AutoSignup cria organização, dono e telefone numa transação, para um número que ainda não existe.
func (s *Store) AutoSignup(ctx context.Context, provider, phone string, maxBudget float64, dailyLimit int) (Identity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Identity{}, err
	}
	defer tx.Rollback(ctx)
	suffix, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return Identity{}, err
	}
	name := fmt.Sprintf("Cliente %s-%04d", lastDigits(phone, 4), suffix.Int64())
	var orgID, memberID int64
	if err := tx.QueryRow(ctx, `INSERT INTO organizations (name, max_daily_budget_brl, daily_message_limit, signup_source)
		VALUES ($1, $2, $3, 'auto') RETURNING id`, name, maxBudget, dailyLimit).Scan(&orgID); err != nil {
		return Identity{}, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO members (organization_id, name, role) VALUES ($1, 'Cliente', 'owner') RETURNING id`,
		orgID).Scan(&memberID); err != nil {
		return Identity{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO whatsapp_identities (provider, phone_e164, member_id) VALUES ($1, $2, $3)`,
		provider, phone, memberID); err != nil {
		return Identity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Identity{}, err
	}
	return s.FindIdentity(ctx, provider, phone)
}

func lastDigits(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// CountInboundSince conta as mensagens recebidas pela organização desde o instante dado.
func (s *Store) CountInboundSince(ctx context.Context, orgID int64, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		JOIN whatsapp_identities w ON w.id = c.identity_id
		JOIN members mb ON mb.id = w.member_id
		WHERE mb.organization_id = $1 AND m.direction = 'in' AND m.created_at >= $2`, orgID, since).Scan(&n)
	return n, err
}
