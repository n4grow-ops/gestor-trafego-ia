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

	// Envios ao cliente aguentam o WhatsApp fora por uns 14 min (espera de 30 s × tentativa).
	sendAttempts = 8
)

// errMediaFailed marca falha ao baixar ou transcrever mídia: o cliente é avisado e o evento não é reprocessado.
var errMediaFailed = errors.New("engine: mídia indisponível")

func sendJob(convID int64, chatID, text string) store.JobSpec {
	return store.JobSpec{Kind: JobSend, Payload: SendJob{ConversationID: convID, ChatID: chatID, Text: text}, MaxAttempts: sendAttempts}
}

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

	// Cadastro automático de número novo, com limites de custo.
	AutoSignup       bool
	SignupMaxBudget  float64
	SignupDailyLimit int
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
	newClient := false
	if errors.Is(err, store.ErrNotFound) {
		if !e.AutoSignup {
			e.Log.Info("telefone não cadastrado", "provider", e.WA.Name())
			return e.WA.SendText(ctx, in.ChatID, msgUnknown)
		}
		if ident, err = e.Store.AutoSignup(ctx, e.WA.Name(), in.Phone, e.SignupMaxBudget, e.SignupDailyLimit); err != nil {
			return err
		}
		newClient = true
		e.audit(ctx, ident.OrgID, "system", "auto_signup", map[string]any{"identity_id": ident.ID})
	}
	if err != nil {
		return err
	}
	if ident.Status == "blocked" {
		return nil
	}
	conv, err := e.Store.GetOrCreateConversation(ctx, ident.ID, in.ChatID)
	if err != nil {
		return err
	}
	// Uma mensagem por vez por conversa: evita que mensagens seguidas sobrescrevam campos.
	unlock, err := e.Store.LockConversation(ctx, conv.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if conv, err = e.Store.GetConversation(ctx, conv.ID); err != nil {
		return err
	}
	if newClient {
		conv.State = store.StateOnboarding
		if err := e.Store.UpdateConversation(ctx, conv.ID, conv.State, conv.Fields); err != nil {
			return err
		}
	}
	// Limite diário antes de qualquer custo (transcrição ou IA): avisa uma vez e depois silencia.
	received, err := e.Store.CountInboundSince(ctx, ident.OrgID, startOfDay(e.now()))
	if err != nil {
		return err
	}
	if received >= ident.DailyMessageLimit {
		if err := e.Store.AddMessage(ctx, conv.ID, "in", string(in.Kind), in.Text); err != nil {
			return err
		}
		if received == ident.DailyMessageLimit {
			e.audit(ctx, ident.OrgID, "system", "daily_limit_reached", map[string]any{"limit": ident.DailyMessageLimit})
			return e.send(ctx, conv, msgDailyLimit)
		}
		return nil
	}
	text, err := e.inboundText(ctx, ident, &conv, in)
	if errors.Is(err, errMediaFailed) {
		e.Log.Error("falha na mídia", "conversation_id", conv.ID, "erro", err)
		e.audit(ctx, ident.OrgID, "system", "media_failed", map[string]any{"conversation_id": conv.ID, "error": err.Error()})
		if err := e.Store.AddMessage(ctx, conv.ID, "in", string(in.Kind), "[áudio não processado]"); err != nil {
			return err
		}
		if conv.State == store.StateNeedsHuman {
			return nil
		}
		return e.send(ctx, conv, msgAudioFailed)
	}
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
			return "", fmt.Errorf("%w: %v", errMediaFailed, err)
		}
		resp, err := e.Transcriber.Transcribe(ctx, e.TranscribeModel, data, audioFormat(mime))
		e.recordUsage(ctx, ident.OrgID, "transcriber", resp.Usage)
		if err != nil {
			return "", fmt.Errorf("%w: %v", errMediaFailed, err)
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
	projects, names, err := e.projectNames(ctx, ident.OrgID)
	if err != nil {
		return err
	}
	activeName := ""
	for _, p := range projects {
		if conv.ActiveProjectID != nil && p.ID == *conv.ActiveProjectID {
			activeName = p.Name
		}
	}
	current := withDefaults(ident.Profile, conv.Fields)
	turn, usage, err := e.Attendant.Reply(ctx, attendant.Input{
		OrgName: ident.OrgName, Niche: ident.Niche, State: conv.State,
		Fields: current, Missing: current.Missing(), History: history, Message: text,
		Profile: ident.Profile, ProjectName: activeName, Projects: names,
	})
	e.recordUsage(ctx, ident.OrgID, "attendant", usage)
	if err != nil {
		e.Log.Error("atendente falhou", "conversation_id", conv.ID, "erro", err)
		e.audit(ctx, ident.OrgID, "attendant", "llm_failed", map[string]any{"conversation_id": conv.ID, "error": err.Error()})
		return e.send(ctx, conv, msgRetryLater)
	}

	// Memória do negócio: o Go mescla e grava; texto vazio não apaga.
	if turn.Profile.HasData() {
		ident.Profile = ident.Profile.Merge(turn.Profile)
		if err := e.Store.UpdateOrgProfile(ctx, ident.OrgID, ident.Profile); err != nil {
			return err
		}
	}
	if conv.State == store.StateOnboarding {
		if !ident.Profile.Complete() {
			return e.send(ctx, conv, turn.Reply)
		}
		conv.State = store.StateIdle
		if err := e.Store.UpdateConversation(ctx, conv.ID, conv.State, conv.Fields); err != nil {
			return err
		}
		if turn.Intent != contract.IntentCreateCampaign && turn.Intent != contract.IntentNewProject && !turn.Fields.HasData() {
			return e.send(ctx, conv, turn.Reply)
		}
	}

	switch turn.Intent {
	case contract.IntentListProjects:
		return e.send(ctx, conv, listText(projects))
	case contract.IntentProjectStatus:
		return e.projectStatus(ctx, ident, conv, turn.Project, projects)
	case contract.IntentNewProject:
		return e.startProject(ctx, ident, conv, turn, projects)
	case contract.IntentSwitchProject:
		return e.switchProject(ctx, ident, conv, turn, projects)
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

	merged := current.Merge(turn.Fields)
	campaignTalk := turn.Intent == contract.IntentCreateCampaign || turn.Intent == contract.IntentProvideInfo || turn.Fields.HasData()
	if campaignTalk {
		if err := e.ensureProject(ctx, ident, &conv, turn.Project, projects); err != nil {
			return err
		}
	}
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
	if err := e.Store.SetStateAndEnqueue(ctx, conv.ID, store.StateAwaitingProposal, f, store.JobSpec{Kind: JobPropose, Payload: job}); err != nil {
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
// Os avisos vão pelo outbox na mesma transação do estado, para sobreviver ao WhatsApp fora do ar.
func (e *Engine) escalate(ctx context.Context, ident store.Identity, conv store.Conversation, reason, clientMsg string) error {
	jobs := []store.JobSpec{sendJob(conv.ID, conv.ChatID, clientMsg)}
	if e.OperatorPhone != "" {
		note := fmt.Sprintf(msgOperator, ident.MemberName, ident.OrgName, ident.Phone, reason)
		jobs = append(jobs, sendJob(0, e.WA.ChatIDForPhone(e.OperatorPhone), note))
	}
	if err := e.Store.SetStateAndEnqueue(ctx, conv.ID, store.StateNeedsHuman, conv.Fields, jobs...); err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "escalated_human", map[string]any{"conversation_id": conv.ID, "reason": reason})
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
