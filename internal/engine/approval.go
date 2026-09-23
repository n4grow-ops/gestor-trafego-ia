package engine

import (
	"context"
	"errors"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// approve trata "SIM <código>". Não passa pela IA: a decisão é só do Go.
func (e *Engine) approve(ctx context.Context, ident store.Identity, conv store.Conversation, code string) error {
	if !ident.CanApprove() {
		e.audit(ctx, ident.OrgID, member(ident), "approval_refused", map[string]any{"conversation_id": conv.ID, "reason": "role"})
		return e.send(ctx, conv, msgNotAllowed)
	}
	d, err := e.Store.OpenDraft(ctx, conv.ID)
	if errors.Is(err, store.ErrNotFound) {
		if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateIdle, contract.Fields{}); err != nil {
			return err
		}
		return e.send(ctx, conv, msgNoDraft)
	}
	if err != nil {
		return err
	}

	err = e.Store.ApproveDraft(ctx, d.ID, ident.MemberID, campaign.HashCode(code), e.now())
	switch {
	case err == nil:
		e.audit(ctx, ident.OrgID, member(ident), "approval_granted", map[string]any{"draft_id": d.ID, "proposal_hash": d.ProposalHash})
		if conv.ActiveProjectID != nil {
			if err := e.Store.SetProjectStage(ctx, *conv.ActiveProjectID, store.StageApproved); err != nil {
				return err
			}
		}
		if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateIdle, contract.Fields{}); err != nil {
			return err
		}
		return e.send(ctx, conv, msgApproved)
	case errors.Is(err, store.ErrCodeInvalid):
		e.audit(ctx, ident.OrgID, member(ident), "approval_refused", map[string]any{"draft_id": d.ID, "reason": "code"})
		return e.send(ctx, conv, msgCodeInvalid)
	case errors.Is(err, store.ErrCodeExpired), errors.Is(err, store.ErrCodeLocked):
		reason, msg := "expired", msgCodeExpired
		if errors.Is(err, store.ErrCodeLocked) {
			reason, msg = "locked", msgCodeLocked
		}
		e.audit(ctx, ident.OrgID, member(ident), "approval_refused", map[string]any{"draft_id": d.ID, "reason": reason})
		if err := e.Store.SetDraftStatus(ctx, d.ID, "expired"); err != nil {
			return err
		}
		if err := e.Store.SetStateAndEnqueue(ctx, conv.ID, store.StateAwaitingProposal, conv.Fields, store.JobSpec{Kind: JobPropose, Payload: ProposeJob{ConversationID: conv.ID}}); err != nil {
			return err
		}
		return e.send(ctx, conv, msg)
	default:
		return err
	}
}
