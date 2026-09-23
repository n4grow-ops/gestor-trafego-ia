package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// HandleJob executa um job do outbox. Erro devolvido = o outbox tenta de novo.
func (e *Engine) HandleJob(ctx context.Context, job store.Job) error {
	switch job.Kind {
	case JobPropose:
		var p ProposeJob
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("engine: payload de proposta inválido: %w", err)
		}
		return e.propose(ctx, p)
	case JobSend:
		var s SendJob
		if err := json.Unmarshal(job.Payload, &s); err != nil {
			return fmt.Errorf("engine: payload de envio inválido: %w", err)
		}
		if err := e.WA.SendText(ctx, s.ChatID, s.Text); err != nil {
			return err
		}
		return e.Store.AddMessage(ctx, s.ConversationID, "out", "text", s.Text)
	default:
		return fmt.Errorf("engine: job desconhecido %q", job.Kind)
	}
}

// OnJobFailed é chamado quando um job esgota as tentativas: o cliente não pode ficar esperando.
func (e *Engine) OnJobFailed(ctx context.Context, job store.Job) error {
	if job.Kind != JobPropose {
		return nil
	}
	var p ProposeJob
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	conv, err := e.Store.GetConversation(ctx, p.ConversationID)
	if err != nil {
		return err
	}
	if conv.State != store.StateAwaitingProposal {
		return nil
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	return e.escalate(ctx, ident, conv, "falha ao gerar proposta: tentativas esgotadas", msgProposalFailed)
}

func (e *Engine) propose(ctx context.Context, p ProposeJob) error {
	conv, err := e.Store.GetConversation(ctx, p.ConversationID)
	if err != nil {
		return err
	}
	if conv.State != store.StateAwaitingProposal {
		return nil // job obsoleto: a conversa já seguiu
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	b, err := contract.NewBriefing(ident.OrgID, conv.Fields, contract.Constraints{
		MaxDailyBudgetBRL: ident.MaxDailyBudgetBRL,
		AllowedObjectives: ident.AllowedObjectives,
		Niche:             ident.Niche,
	})
	if err != nil {
		return e.escalate(ctx, ident, conv, err.Error(), msgProposalFailed)
	}
	b.Adjustment = p.Adjustment

	prop, usage, err := e.Manager.Propose(ctx, b)
	e.recordUsage(ctx, ident.OrgID, "manager", usage)
	if errors.Is(err, manager.ErrInvalidProposal) {
		e.audit(ctx, ident.OrgID, "manager", "proposal_invalid", map[string]any{"conversation_id": conv.ID, "error": err.Error()})
		return e.escalate(ctx, ident, conv, "proposta inválida após as tentativas", msgProposalFailed)
	}
	if err != nil {
		return err
	}

	hash, err := campaign.ProposalHash(prop)
	if err != nil {
		return err
	}
	code, err := campaign.NewApprovalCode()
	if err != nil {
		return err
	}
	draftID, err := e.Store.SaveProposal(ctx, store.ProposalRecord{
		OrgID: ident.OrgID, ConversationID: conv.ID,
		Briefing: b, Proposal: prop, ProposalHash: hash,
		CodeHash: campaign.HashCode(code), ExpiresAt: e.now().Add(codeTTL),
		SendKind:    JobSend,
		SendPayload: SendJob{ConversationID: conv.ID, ChatID: conv.ChatID, Text: campaign.Summary(prop, code)},
	})
	if err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "manager", "proposal_created", map[string]any{"draft_id": draftID, "proposal_hash": hash})
	return nil
}
