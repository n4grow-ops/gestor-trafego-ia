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
		if s.Human {
			return e.sendHuman(ctx, s)
		}
		if err := e.WA.SendText(ctx, s.ChatID, s.Text); err != nil {
			return err
		}
		if s.ConversationID == 0 {
			return nil // aviso ao operador: fora de conversa
		}
		return e.Store.AddMessage(ctx, s.ConversationID, "out", "text", redactLinks(s.Text))
	case JobReply:
		var r ReplyJob
		if err := json.Unmarshal(job.Payload, &r); err != nil {
			return fmt.Errorf("engine: payload de resposta inválido: %w", err)
		}
		return e.reply(ctx, r)
	case JobSignupRemind:
		var r RemindJob
		if err := json.Unmarshal(job.Payload, &r); err != nil {
			return fmt.Errorf("engine: payload de lembrete inválido: %w", err)
		}
		return e.remind(ctx, r)
	default:
		return fmt.Errorf("engine: job desconhecido %q", job.Kind)
	}
}

// OnJobFailed é chamado quando um job esgota as tentativas: o cliente não pode ficar esperando.
func (e *Engine) OnJobFailed(ctx context.Context, job store.Job) error {
	switch job.Kind {
	case JobPropose:
		var p ProposeJob
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		return e.escalateIf(ctx, p.ConversationID, store.StateAwaitingProposal, "falha ao gerar proposta: tentativas esgotadas")
	case JobSend:
		var s SendJob
		if err := json.Unmarshal(job.Payload, &s); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		if s.ConversationID == 0 {
			e.Log.Error("aviso ao operador não entregue", "job_id", job.ID)
			return nil
		}
		if s.Human {
			// Balão de conversa não entregue: só registra; a proposta (envio comum) segue valendo.
			e.Log.Error("balão não entregue", "job_id", job.ID, "conversation_id", s.ConversationID)
			return nil
		}
		// Resumo com código não entregue: a proposta perde a validade e um humano assume.
		return e.escalateIf(ctx, s.ConversationID, store.StateAwaitingApproval, "não foi possível entregar a proposta pelo WhatsApp")
	case JobReply:
		// Resposta esgotou as tentativas: o cliente não fica sem retorno.
		var r ReplyJob
		if err := json.Unmarshal(job.Payload, &r); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		unlock, err := e.Store.LockConversation(ctx, r.ConversationID)
		if err != nil {
			return err
		}
		defer unlock()
		conv, err := e.Store.GetConversation(ctx, r.ConversationID)
		if err != nil {
			return err
		}
		if err := e.Store.MarkReplied(ctx, conv.ID, r.MessageID); err != nil {
			return err
		}
		ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
		if err != nil {
			return err
		}
		if ident.Status == "blocked" || conv.State == store.StateNeedsHuman {
			return nil
		}
		return e.Store.Enqueue(ctx, sendJob(conv.ID, conv.ChatID, msgRetryLater))
	}
	return nil
}

// escalateIf escala a conversa só se ela ainda estiver no estado esperado.
func (e *Engine) escalateIf(ctx context.Context, convID int64, state, reason string) error {
	unlock, err := e.Store.LockConversation(ctx, convID)
	if err != nil {
		return err
	}
	defer unlock()
	conv, err := e.Store.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.State != state {
		return nil
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	if state == store.StateAwaitingApproval {
		if err := e.closeOpenDraft(ctx, ident, conv, "expired"); err != nil {
			return err
		}
	}
	return e.escalate(ctx, ident, conv, reason, msgProposalFailed)
}

func (e *Engine) propose(ctx context.Context, p ProposeJob) error {
	unlock, err := e.Store.LockConversation(ctx, p.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
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
	b.Business = ident.Profile

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
		Send: sendJob(conv.ID, conv.ChatID, campaign.Summary(prop, code)),
	})
	if errors.Is(err, store.ErrStateChanged) {
		return nil // a conversa seguiu enquanto o Gestor trabalhava
	}
	if err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "manager", "proposal_created", map[string]any{"draft_id": draftID, "proposal_hash": hash})
	return nil
}
