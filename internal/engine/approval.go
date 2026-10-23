package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
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

	// Aprovada: com Storage, a própria IA pede os anúncios (imagens ou vídeos) que vão subir na Meta.
	adv := &store.ApprovalAdvance{ConversationID: conv.ID, ConversationState: store.StateIdle,
		ProjectID: conv.ActiveProjectID, ProjectStage: store.StageApproved}
	reply := msgApproved
	if e.Storage != nil {
		adv.ConversationState, adv.ProjectStage, reply = store.StateAwaitingCreatives, store.StageAwaitingCreatives, askCreatives(d.Proposal.Placements)
	}
	err = e.Store.ApproveDraftAdvance(ctx, d.ID, ident.MemberID, campaign.HashCode(code), e.now(), adv)
	switch {
	case err == nil:
		e.audit(ctx, ident.OrgID, member(ident), "approval_granted", map[string]any{"draft_id": d.ID, "proposal_hash": d.ProposalHash})
		if adv.ConversationState == store.StateAwaitingCreatives {
			if note := e.importEarlyMedia(ctx, ident, conv, d.ID); note != "" {
				reply = join(reply, note)
			}
		}
		// Escudo: já na aprovação a Mia diz se a conta está pronta para publicar ("tudo certo" ou o que resolver).
		return e.send(ctx, conv, join(reply, e.shieldApprovalNote(ctx, ident.OrgID, d.Proposal.DailyBudgetBRL)))
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

// earlyLinkPrefix marca em Fields.Media o link do Instagram mandado antes da aprovação (o resto são ids de
// imagens do WhatsApp).
const earlyLinkPrefix = "ig:"

// importEarlyMedia leva para a etapa dos anúncios o que o cliente mandou antes da aprovação: imagens do
// WhatsApp (baixadas agora pelo id da mensagem) e links do Instagram (buscados em job). Devolve a frase
// para o cliente, vazia quando não havia nada.
func (e *Engine) importEarlyMedia(ctx context.Context, ident store.Identity, conv store.Conversation, draftID int64) string {
	saved, links := 0, 0
	for _, m := range conv.Fields.Media {
		if link, ok := strings.CutPrefix(m, earlyLinkPrefix); ok {
			if e.Instagram == nil {
				continue
			}
			if err := e.Store.Enqueue(ctx, store.JobSpec{Kind: JobInstagramFetch, MaxAttempts: 2,
				Payload: InstagramJob{ConversationID: conv.ID, DraftID: draftID, Link: link}}); err != nil {
				e.Log.Error("link do Instagram de antes não agendado", "conversation_id", conv.ID, "erro", err)
				continue
			}
			links++
			continue
		}
		in := whatsapp.Inbound{ExternalID: m, ChatID: conv.ChatID, Kind: whatsapp.KindImage}
		if r := e.saveCreative(ctx, ident, conv, in); strings.HasPrefix(r, creativeSaved) || strings.HasPrefix(r, creativeWarned) {
			saved++
		}
	}
	var parts []string
	if saved > 0 {
		parts = append(parts, fmt.Sprintf(msgEarlyImages, saved))
	}
	if links > 0 {
		parts = append(parts, msgEarlyLinks)
	}
	return strings.Join(parts, " ")
}
