package store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
)

var (
	ErrCodeInvalid = errors.New("store: código de aprovação inválido")
	ErrCodeExpired = errors.New("store: código de aprovação vencido")
	ErrCodeLocked  = errors.New("store: código bloqueado por tentativas erradas")
)

const MaxCodeAttempts = 5

type Draft struct {
	ID             int64
	OrgID          int64
	ConversationID int64
	Briefing       contract.Briefing
	Proposal       contract.CampaignProposal
	ProposalHash   string
	Status         string
}

// ProposalRecord é tudo que nasce junto com uma proposta.
type ProposalRecord struct {
	OrgID          int64
	ConversationID int64
	Briefing       contract.Briefing
	Proposal       contract.CampaignProposal
	ProposalHash   string
	CodeHash       string
	ExpiresAt      time.Time
	SendKind       string // job que leva o resumo ao cliente
	SendPayload    any
}

// SaveProposal grava rascunho, código, estado awaiting_approval e o job de envio numa transação.
func (s *Store) SaveProposal(ctx context.Context, r ProposalRecord) (int64, error) {
	briefing, err := json.Marshal(r.Briefing)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	proposal, err := json.Marshal(r.Proposal)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO campaign_drafts (organization_id, conversation_id, briefing, proposal, proposal_hash)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		r.OrgID, r.ConversationID, string(briefing), string(proposal), r.ProposalHash).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO approval_codes (draft_id, code_hash, proposal_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		id, r.CodeHash, r.ProposalHash, r.ExpiresAt); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `UPDATE conversations SET state = $2, updated_at = now() WHERE id = $1`, r.ConversationID, StateAwaitingApproval)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	if err := enqueue(ctx, tx, r.SendKind, r.SendPayload); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// OpenDraft devolve o rascunho proposed mais recente da conversa.
func (s *Store) OpenDraft(ctx context.Context, convID int64) (Draft, error) {
	var d Draft
	var briefing, proposal []byte
	err := s.pool.QueryRow(ctx, `SELECT id, organization_id, conversation_id, briefing, proposal, proposal_hash, status
		FROM campaign_drafts WHERE conversation_id = $1 AND status = 'proposed' ORDER BY id DESC LIMIT 1`, convID).
		Scan(&d.ID, &d.OrgID, &d.ConversationID, &briefing, &proposal, &d.ProposalHash, &d.Status)
	if err != nil {
		return Draft{}, notFound(err)
	}
	if err := json.Unmarshal(briefing, &d.Briefing); err != nil {
		return Draft{}, fmt.Errorf("store: briefing inválido: %w", err)
	}
	if err := json.Unmarshal(proposal, &d.Proposal); err != nil {
		return Draft{}, fmt.Errorf("store: proposta inválida: %w", err)
	}
	return d, nil
}

// SetDraftStatus fecha um rascunho ainda proposed (rejected, expired, cancelled).
func (s *Store) SetDraftStatus(ctx context.Context, id int64, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE campaign_drafts SET status = $2 WHERE id = $1 AND status = 'proposed'`, id, status)
	return err
}

// ApproveDraft confere o código mais recente não usado do rascunho e aprova.
func (s *Store) ApproveDraft(ctx context.Context, draftID, memberID int64, codeHash string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var codeID int64
	var stored, codeProposalHash, draftHash, draftStatus string
	var expires time.Time
	var attempts int
	err = tx.QueryRow(ctx, `SELECT c.id, c.code_hash, c.proposal_hash, c.expires_at, c.attempts, d.proposal_hash, d.status
		FROM approval_codes c JOIN campaign_drafts d ON d.id = c.draft_id
		WHERE c.draft_id = $1 AND c.used_at IS NULL
		ORDER BY c.id DESC LIMIT 1 FOR UPDATE OF c, d`, draftID).
		Scan(&codeID, &stored, &codeProposalHash, &expires, &attempts, &draftHash, &draftStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCodeInvalid
	}
	if err != nil {
		return err
	}
	if draftStatus != "proposed" {
		return ErrCodeInvalid
	}
	if attempts >= MaxCodeAttempts {
		return ErrCodeLocked
	}
	if !now.Before(expires) {
		return ErrCodeExpired
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(codeHash)) != 1 || codeProposalHash != draftHash {
		if _, err := tx.Exec(ctx, `UPDATE approval_codes SET attempts = attempts + 1 WHERE id = $1`, codeID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		if attempts+1 >= MaxCodeAttempts {
			return ErrCodeLocked
		}
		return ErrCodeInvalid
	}
	if _, err := tx.Exec(ctx, `UPDATE approval_codes SET used_at = $2 WHERE id = $1`, codeID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE campaign_drafts SET status = 'approved', approved_by = $2, approved_at = $3 WHERE id = $1`,
		draftID, memberID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordUsage(ctx context.Context, orgID int64, agent string, u llm.Usage) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO llm_usage (organization_id, agent, model, input_tokens, output_tokens, cost_usd)
		VALUES ($1, $2, $3, $4, $5, $6)`, orgID, agent, u.Model, u.InputTokens, u.OutputTokens, u.CostUSD)
	return err
}

func (s *Store) Audit(ctx context.Context, orgID int64, actor, action string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_events (organization_id, actor, action, data) VALUES ($1, $2, $3, $4)`,
		orgID, actor, action, string(raw))
	return err
}
