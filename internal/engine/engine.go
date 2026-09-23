// Package engine orquestra a conversa: WhatsApp, Atendente, Gestor e banco.
// Toda ação (rascunho, aprovação, envio) é decidida aqui, nunca pelas IAs.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	JobPropose = "manager.propose"
	JobSend    = "whatsapp.send"

	codeTTL     = 30 * time.Minute
	historySize = 12
)

type ProposeJob struct {
	ConversationID int64  `json:"conversation_id"`
	Adjustment     string `json:"adjustment,omitempty"`
}

type SendJob struct {
	ConversationID int64  `json:"conversation_id"`
	ChatID         string `json:"chat_id"`
	Text           string `json:"text"`
}

type Engine struct {
	Store           *store.Store
	WA              whatsapp.Provider
	Attendant       *attendant.Agent
	Manager         *manager.Agent
	Transcriber     llm.Provider
	TranscribeModel string
	OperatorPhone   string
	Now             func() time.Time
	Log             *slog.Logger
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// HandleInbound processa uma mensagem recebida do cliente.
func (e *Engine) HandleInbound(ctx context.Context, in whatsapp.Inbound) error {
	ident, err := e.Store.FindIdentity(ctx, e.WA.Name(), in.Phone)
	if errors.Is(err, store.ErrNotFound) {
		e.Log.Info("telefone não cadastrado", "provider", e.WA.Name())
		return e.WA.SendText(ctx, in.ChatID, msgUnknown)
	}
	if err != nil {
		return err
	}
	conv, err := e.Store.GetOrCreateConversation(ctx, ident.ID, in.ChatID)
	if err != nil {
		return err
	}
	text, err := e.inboundText(ctx, ident, &conv, in)
	if err != nil {
		return err
	}
	if err := e.Store.AddMessage(ctx, conv.ID, "in", string(in.Kind), text); err != nil {
		return err
	}

	switch conv.State {
	case store.StateNeedsHuman:
		return nil
	case store.StateAwaitingProposal:
		return e.send(ctx, conv, msgBusy)
	case store.StateAwaitingApproval:
		if code, ok := campaign.ParseApproval(text); ok {
			return e.approve(ctx, ident, conv, code)
		}
	}
	return e.converse(ctx, ident, conv, text)
}

func (e *Engine) inboundText(ctx context.Context, ident store.Identity, conv *store.Conversation, in whatsapp.Inbound) (string, error) {
	switch in.Kind {
	case whatsapp.KindText:
		return in.Text, nil
	case whatsapp.KindAudio:
		data, mime, err := e.WA.DownloadMedia(ctx, in)
		if err != nil {
			return "", err
		}
		resp, err := e.Transcriber.Transcribe(ctx, e.TranscribeModel, data, audioFormat(mime))
		e.recordUsage(ctx, ident.OrgID, "transcriber", resp.Usage)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.Content), nil
	case whatsapp.KindImage:
		conv.Fields = conv.Fields.Merge(contract.Fields{Media: []string{in.ExternalID}})
		if err := e.Store.UpdateConversation(ctx, conv.ID, conv.State, conv.Fields); err != nil {
			return "", err
		}
		return strings.TrimSpace("[imagem enviada] " + in.Text), nil
	default:
		return msgUnsupported, nil
	}
}

func (e *Engine) converse(ctx context.Context, ident store.Identity, conv store.Conversation, text string) error {
	history, err := e.history(ctx, conv.ID)
	if err != nil {
		return err
	}
	turn, usage, err := e.Attendant.Reply(ctx, attendant.Input{
		OrgName: ident.OrgName, Niche: ident.Niche, State: conv.State,
		Fields: conv.Fields, Missing: conv.Fields.Missing(), History: history, Message: text,
	})
	e.recordUsage(ctx, ident.OrgID, "attendant", usage)
	if err != nil {
		e.Log.Error("atendente falhou", "conversation_id", conv.ID, "erro", err)
		e.audit(ctx, ident.OrgID, "attendant", "llm_failed", map[string]any{"conversation_id": conv.ID, "error": err.Error()})
		return e.send(ctx, conv, msgRetryLater)
	}

	switch turn.Intent {
	case contract.IntentHuman:
		return e.escalate(ctx, ident, conv, "cliente pediu atendimento humano", msgHuman)
	case contract.IntentReject:
		reply := turn.Reply
		if conv.State == store.StateAwaitingApproval {
			if err := e.closeOpenDraft(ctx, ident, conv, "rejected"); err != nil {
				return err
			}
			reply = msgRejected
		}
		if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateIdle, contract.Fields{}); err != nil {
			return err
		}
		return e.send(ctx, conv, reply)
	}

	merged := conv.Fields.Merge(turn.Fields)
	if merged.DailyBudgetBRL != nil && *merged.DailyBudgetBRL > ident.MaxDailyBudgetBRL {
		merged.DailyBudgetBRL = conv.Fields.DailyBudgetBRL
		state := conv.State
		if state == store.StateIdle {
			state = store.StateCollecting
		}
		if err := e.Store.UpdateConversation(ctx, conv.ID, state, merged); err != nil {
			return err
		}
		return e.send(ctx, conv, fmt.Sprintf(msgOverBudget, campaign.BRL(ident.MaxDailyBudgetBRL)))
	}

	campaignTalk := turn.Intent == contract.IntentCreateCampaign || turn.Intent == contract.IntentProvideInfo || turn.Fields.HasData()

	if conv.State == store.StateAwaitingApproval {
		if !campaignTalk {
			return e.send(ctx, conv, turn.Reply)
		}
		// Ajuste pedido: descarta o rascunho atual e pede nova proposta ao Gestor.
		if err := e.closeOpenDraft(ctx, ident, conv, "cancelled"); err != nil {
			return err
		}
		return e.requestProposal(ctx, conv, merged, text, turn.Reply)
	}
	if !campaignTalk && conv.State != store.StateCollecting {
		return e.send(ctx, conv, turn.Reply)
	}
	if len(merged.Missing()) == 0 {
		return e.requestProposal(ctx, conv, merged, "", turn.Reply)
	}
	if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateCollecting, merged); err != nil {
		return err
	}
	return e.send(ctx, conv, turn.Reply)
}

func (e *Engine) requestProposal(ctx context.Context, conv store.Conversation, f contract.Fields, adjustment, reply string) error {
	job := ProposeJob{ConversationID: conv.ID, Adjustment: adjustment}
	if err := e.Store.SetStateAndEnqueue(ctx, conv.ID, store.StateAwaitingProposal, f, JobPropose, job); err != nil {
		return err
	}
	return e.send(ctx, conv, reply)
}

func (e *Engine) closeOpenDraft(ctx context.Context, ident store.Identity, conv store.Conversation, status string) error {
	d, err := e.Store.OpenDraft(ctx, conv.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := e.Store.SetDraftStatus(ctx, d.ID, status); err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, member(ident), "draft_"+status, map[string]any{"draft_id": d.ID})
	return nil
}

func (e *Engine) history(ctx context.Context, convID int64) ([]attendant.Turn, error) {
	msgs, err := e.Store.RecentMessages(ctx, convID, historySize+1)
	if err != nil {
		return nil, err
	}
	if len(msgs) > 0 {
		msgs = msgs[:len(msgs)-1] // a última é a mensagem atual
	}
	turns := make([]attendant.Turn, 0, len(msgs))
	for _, m := range msgs {
		turns = append(turns, attendant.Turn{FromClient: m.Direction == "in", Text: m.Body})
	}
	return turns, nil
}

// escalate passa a conversa para um humano: o bot para de responder e o operador é avisado.
func (e *Engine) escalate(ctx context.Context, ident store.Identity, conv store.Conversation, reason, clientMsg string) error {
	if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateNeedsHuman, conv.Fields); err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "escalated_human", map[string]any{"conversation_id": conv.ID, "reason": reason})
	if err := e.send(ctx, conv, clientMsg); err != nil {
		return err
	}
	if e.OperatorPhone != "" {
		note := fmt.Sprintf(msgOperator, ident.MemberName, ident.OrgName, ident.Phone, reason)
		if err := e.WA.SendText(ctx, e.WA.ChatIDForPhone(e.OperatorPhone), note); err != nil {
			e.Log.Error("falha ao avisar operador", "erro", err)
		}
	}
	return nil
}

func (e *Engine) send(ctx context.Context, conv store.Conversation, text string) error {
	if err := e.WA.SendText(ctx, conv.ChatID, text); err != nil {
		return err
	}
	return e.Store.AddMessage(ctx, conv.ID, "out", "text", text)
}

func (e *Engine) recordUsage(ctx context.Context, orgID int64, agent string, u llm.Usage) {
	if u.Model == "" && u.InputTokens == 0 && u.OutputTokens == 0 {
		return
	}
	if err := e.Store.RecordUsage(ctx, orgID, agent, u); err != nil {
		e.Log.Error("falha ao registrar uso", "agente", agent, "erro", err)
	}
}

func (e *Engine) audit(ctx context.Context, orgID int64, actor, action string, data any) {
	if err := e.Store.Audit(ctx, orgID, actor, action, data); err != nil {
		e.Log.Error("falha ao auditar", "acao", action, "erro", err)
	}
}

func member(i store.Identity) string { return fmt.Sprintf("member:%d", i.MemberID) }

func audioFormat(mime string) string {
	switch {
	case strings.Contains(mime, "mpeg"):
		return "mp3"
	case strings.Contains(mime, "mp4"), strings.Contains(mime, "m4a"), strings.Contains(mime, "aac"):
		return "m4a"
	case strings.Contains(mime, "wav"):
		return "wav"
	default:
		return "ogg"
	}
}
