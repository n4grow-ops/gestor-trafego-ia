package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Testador automático: enquanto o app da Meta está em desenvolvimento, só testadores conectam a conta.
// O cliente manda o link do perfil, a equipe adiciona no app, a Mia avisa e manda o link de conexão.
const (
	JobTesterAdded = "tester.added"
	JobTesterPoll  = "tester.poll"

	testerPollEvery   = 5 * time.Minute
	testerRemindAfter = 24 * time.Hour
	shareResolveLimit = 8 * time.Second
)

// TesterAddedJob é enfileirado pelo painel quando a equipe aperta "Adicionei".
type TesterAddedJob struct {
	RequestID int64 `json:"request_id"`
}

// TesterStore é a parte do banco que o fluxo do testador usa (trocada por um fake nos testes).
type TesterStore interface {
	UpsertTesterRequest(ctx context.Context, r store.TesterRequest) (store.TesterRequest, error)
	OpenTesterRequest(ctx context.Context, identityID int64) (store.TesterRequest, error)
	ListTesterRequests(ctx context.Context, status string) ([]store.TesterRequestView, error)
	MarkTesterAcceptedByIdentity(ctx context.Context, identityID int64) error
	TestersAwaitingAcceptance(ctx context.Context) ([]store.TesterRequestView, error)
	MarkTesterReminded(ctx context.Context, id int64) error
	SyncAppTesters(ctx context.Context, userIDs []string) ([]string, error)
}

func (e *Engine) testers() TesterStore {
	if e.Testers != nil {
		return e.Testers
	}
	return e.Store
}

func (e *Engine) parseProfile(text string) (meta.ProfileRef, bool) {
	if e.ParseProfile != nil {
		return e.ParseProfile(text)
	}
	return meta.ParseProfileLink(text)
}

func (e *Engine) resolveShare(ctx context.Context, url string) (string, error) {
	if e.ResolveShare != nil {
		return e.ResolveShare(ctx, url)
	}
	rctx, cancel := context.WithTimeout(ctx, shareResolveLimit)
	defer cancel()
	return meta.ResolveShareLink(rctx, &http.Client{Timeout: shareResolveLimit}, url)
}

// profileLinkTurn trata o link de perfil do Facebook antes da IA. ok=false: a mensagem segue o fluxo normal.
// Fica fora do atendimento humano, da etapa dos anúncios e de lote com código de aprovação.
func (e *Engine) profileLinkTurn(ctx context.Context, ident store.Identity, conv store.Conversation, text string, msgs []string) (bool, error) {
	if e.MetaConnectBaseURL == "" {
		return false, nil
	}
	switch conv.State {
	case store.StateNeedsHuman, store.StateAwaitingCreatives:
		return false, nil
	case store.StateAwaitingApproval:
		for _, m := range msgs {
			if _, ok := campaign.ParseApproval(m); ok {
				return false, nil
			}
		}
	}
	ref, ok := e.parseProfile(text)
	if !ok {
		return false, nil
	}
	if ref.Kind == "share" {
		resolved, err := e.resolveShare(ctx, ref.Value)
		if err != nil {
			e.Log.Warn("link de compartilhamento do perfil não resolvido", "conversation_id", conv.ID, "erro", err)
		}
		r, ok := e.parseProfile(resolved)
		if err != nil || !ok || r.Kind == "share" {
			return true, e.send(ctx, conv, msgTesterAddressBar)
		}
		ref = r
	}
	return true, e.openTesterRequest(ctx, ident, conv, ref)
}

// openTesterRequest grava o pedido, responde o cliente e avisa a equipe pelo outbox.
func (e *Engine) openTesterRequest(ctx context.Context, ident store.Identity, conv store.Conversation, ref meta.ProfileRef) error {
	ts := e.testers()
	prev, err := ts.OpenTesterRequest(ctx, ident.ID)
	switch {
	case err == nil && prev.RefKind == ref.Kind && prev.RefValue == ref.Value:
		return e.send(ctx, conv, msgTesterAlready) // mesmo perfil: a equipe já sabe
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	}
	req, err := ts.UpsertTesterRequest(ctx, store.TesterRequest{OrgID: ident.OrgID, IdentityID: ident.ID, ConversationID: conv.ID,
		ProfileURL: ref.URL, RefKind: ref.Kind, RefValue: ref.Value, Status: store.TesterRequested})
	if err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, member(ident), "tester_requested", map[string]any{"request_id": req.ID, "ref_kind": ref.Kind})
	text := fmt.Sprintf(msgTeamTester, ident.MemberName, ident.OrgName, ident.Phone, ref.Value, ref.URL)
	if e.MetaAppID != "" {
		text += fmt.Sprintf(msgTeamTesterWhere, e.MetaAppID)
	}
	jobs := append([]store.JobSpec{e.clientJob(ident.OrgID, conv, msgTesterReceived)},
		e.teamJobs(ctx, TeamNotice{Kind: TeamTester, Ident: ident, Reason: "testador", Text: text})...)
	// Mesmo estado e campos: a transação só garante que resposta e aviso saem juntos.
	return e.Store.SetStateAndEnqueue(ctx, conv.ID, conv.State, conv.Fields, jobs...)
}

// clientJob é a mensagem fixa ao cliente pela fila: balões no modo humano, envio simples fora dele.
func (e *Engine) clientJob(orgID int64, conv store.Conversation, text string) store.JobSpec {
	if e.Humanize {
		return humanJob(orgID, conv, text, false)
	}
	return sendJob(conv.ID, conv.ChatID, text)
}

// testerAdded avisa o cliente que a equipe o adicionou como testador e manda o link de conexão.
func (e *Engine) testerAdded(ctx context.Context, j TesterAddedJob) error {
	list, err := e.testers().ListTesterRequests(ctx, store.TesterAdded)
	if err != nil {
		return err
	}
	for _, r := range list {
		if r.ID == j.RequestID {
			return e.testerMessage(ctx, r, msgTesterAdded, "testador adicionado")
		}
	}
	e.Log.Info("pedido de testador fora de added; nada a avisar", "request_id", j.RequestID)
	return nil
}

// testerMessage manda ao cliente o texto com o link de conexão Meta. Fora da janela de 24 h da
// Cloud API não há modelo aprovado para isso: a equipe é avisada para retomar o contato.
func (e *Engine) testerMessage(ctx context.Context, r store.TesterRequestView, text, what string) error {
	if e.MetaConnectBaseURL == "" {
		return nil
	}
	unlock, err := e.Store.LockConversation(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	conv, err := e.Store.GetConversation(ctx, r.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, r.IdentityID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ident.Status == "blocked" || conv.State == store.StateNeedsHuman {
		return nil // humano atendendo: quem fala é a equipe
	}
	if _, ok := e.templateSender(); ok {
		out, err := e.outsideWindow(ctx, conv.ID)
		if err != nil {
			return err
		}
		if out {
			reason := what + ", mas o cliente está fora da janela de 24 h; mande o link de conexão pelo painel"
			e.audit(ctx, ident.OrgID, "system", "tester_outside_window", map[string]any{"request_id": r.ID})
			return e.enqueueAll(ctx, e.teamJobs(ctx, TeamNotice{Kind: TeamTester, Ident: ident, Reason: reason})...)
		}
	}
	link, err := e.metaLinkFor(ctx, ident)
	if err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "tester_link_sent", map[string]any{"request_id": r.ID, "motivo": what})
	return e.Store.Enqueue(ctx, e.clientJob(ident.OrgID, conv, join(text, link)))
}

// metaLinkFor cria o link de conexão; quem não é responsável recebe o aviso de que só ele conecta.
func (e *Engine) metaLinkFor(ctx context.Context, ident store.Identity) (string, error) {
	if ident.Role != "owner" {
		return msgMetaOwnerOnly, nil
	}
	return e.newMetaLink(ctx, ident)
}

func (e *Engine) enqueueAll(ctx context.Context, jobs ...store.JobSpec) error {
	for _, j := range jobs {
		if err := e.Store.Enqueue(ctx, j); err != nil {
			return err
		}
	}
	return nil
}

// EnsureTesterPoll garante uma leitura dos testadores agendada (chamado quando o worker sobe).
func (e *Engine) EnsureTesterPoll(ctx context.Context) error {
	if e.AppTesters == nil {
		return nil
	}
	pending, err := e.Store.HasPendingKind(ctx, JobTesterPoll)
	if err != nil || pending {
		return err
	}
	return e.enqueueTesterPoll(ctx)
}

func (e *Engine) enqueueTesterPoll(ctx context.Context) error {
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobTesterPoll, Payload: map[string]string{}, Delay: testerPollEvery})
}

// testerPoll lê os testadores do app, manda o link a quem acabou de aceitar e lembra quem não aceitou
// em 24 h. Erro não derruba a rotina: registra e agenda a próxima leitura.
func (e *Engine) testerPoll(ctx context.Context) error {
	if e.AppTesters == nil {
		return nil
	}
	if err := e.pollTesters(ctx); err != nil {
		e.Log.Error("falha na leitura dos testadores", "erro", err)
	}
	return e.enqueueTesterPoll(ctx)
}

func (e *Engine) pollTesters(ctx context.Context) error {
	ts := e.testers()
	var errs []error
	fresh := 0
	if ids, err := e.AppTesters(ctx); err != nil {
		errs = append(errs, fmt.Errorf("testadores na meta: %w", err))
	} else if news, err := ts.SyncAppTesters(ctx, ids); err != nil {
		errs = append(errs, err)
	} else {
		fresh = len(news)
	}
	awaiting, err := ts.TestersAwaitingAcceptance(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	// A Meta devolve IDs do app, que não casam com o perfil: só dá pra saber quem aceitou se houver um só esperando.
	if fresh > 0 && len(awaiting) == 1 {
		if err := e.testerMessage(ctx, awaiting[0], msgTesterAccepted, "testador aceitou o convite"); err != nil {
			errs = append(errs, err)
		}
	}
	for _, r := range awaiting {
		if r.RemindedAt != nil || r.AddedAt == nil || e.now().Sub(*r.AddedAt) < testerRemindAfter {
			continue
		}
		if err := e.remindTester(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// remindTester lembra o cliente uma vez e avisa a equipe. Marca antes de enviar: no pior caso o
// lembrete se perde, mas nunca se repete.
func (e *Engine) remindTester(ctx context.Context, r store.TesterRequestView) error {
	if err := e.testers().MarkTesterReminded(ctx, r.ID); err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, r.IdentityID)
	if err != nil {
		return err
	}
	text := fmt.Sprintf(msgTeamTesterLate, ident.MemberName, ident.OrgName, ident.Phone, r.RefValue)
	if err := e.enqueueAll(ctx, e.teamJobs(ctx, TeamNotice{Kind: TeamTester, Ident: ident, Reason: "testador sem aceite em 24 h", Text: text})...); err != nil {
		return err
	}
	return e.testerMessage(ctx, r, msgTesterReminder, "lembrete de testador")
}

// testerAccepted fecha o pedido de testador quando a conexão Meta da conversa conclui.
func (e *Engine) testerAccepted(ctx context.Context, convID int64) {
	conv, err := e.Store.GetConversation(ctx, convID)
	if err == nil {
		err = e.testers().MarkTesterAcceptedByIdentity(ctx, conv.IdentityID)
	}
	if err != nil {
		e.Log.Error("falha ao fechar pedido de testador", "conversation_id", convID, "erro", err)
	}
}

func decodeTesterAdded(raw []byte) (TesterAddedJob, error) {
	var j TesterAddedJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, fmt.Errorf("engine: payload de testador inválido: %w", err)
	}
	return j, nil
}
