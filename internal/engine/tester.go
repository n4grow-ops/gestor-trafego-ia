package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	// JobTesterResolve segue o link de compartilhamento fora do turno da mensagem.
	JobTesterResolve = "tester.resolve"

	testerPollEvery   = 5 * time.Minute
	testerRemindAfter = 24 * time.Hour
	shareResolveLimit = 8 * time.Second
	// testerNotifyEvery: no máximo um aviso de pedido novo à equipe nesse intervalo.
	testerNotifyEvery = 30 * time.Minute
	// testerLinkContext: link de conexão Meta mandado nesse prazo dá contexto ao link de perfil.
	testerLinkContext = 14 * 24 * time.Hour
	// testerMaxWords: até quantas palavras fora do link a mensagem conta como "só o link".
	testerMaxWords = 3
)

// TesterAddedJob é enfileirado pelo painel quando a equipe aperta "Adicionei".
type TesterAddedJob struct {
	RequestID int64 `json:"request_id"`
}

// TesterResolveJob segue um link de compartilhamento do perfil fora do turno da mensagem.
type TesterResolveJob struct {
	IdentityID     int64  `json:"identity_id"`
	ConversationID int64  `json:"conversation_id"`
	URL            string `json:"url"`
}

// TesterStore é a parte do banco que o fluxo do testador usa (trocada por um fake nos testes).
type TesterStore interface {
	UpsertTesterRequest(ctx context.Context, r store.TesterRequest) (store.TesterRequest, error)
	OpenTesterRequest(ctx context.Context, identityID int64) (store.TesterRequest, error)
	TesterRequestByID(ctx context.Context, id int64) (store.TesterRequestView, error)
	MarkTesterAcceptedByOrg(ctx context.Context, orgID int64) error
	TestersAwaitingAcceptance(ctx context.Context) ([]store.TesterRequestView, error)
	OpenTestersWithoutMeta(ctx context.Context) ([]store.TesterRequestView, error)
	MarkTesterReminded(ctx context.Context, id int64) error
	MarkTesterNotified(ctx context.Context, id int64, within time.Duration) (bool, error)
	MarkTesterAcceptNotified(ctx context.Context, id int64) error
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
// Só abre pedido quando faz sentido: quem fala é o responsável, a organização ainda não tem conta Meta
// conectada e há contexto de testador (testerContext). Link de página no meio da conversa vai para a IA.
// Fica fora do atendimento humano, da etapa dos anúncios e de lote com código de aprovação.
func (e *Engine) profileLinkTurn(ctx context.Context, ident store.Identity, conv store.Conversation, text string, msgs []string) (bool, error) {
	if e.MetaConnectBaseURL == "" || ident.Role != "owner" {
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
	if _, active, err := e.Store.ActiveMeta(ctx, ident.OrgID); err != nil || active {
		return false, err
	}
	if ok, err := e.testerContext(ctx, ident, text); err != nil || !ok {
		return false, err
	}
	if ref.Kind == "share" {
		// Seguir o redirect leva segundos: vai para um job próprio e o turno volta logo.
		return true, e.Store.Enqueue(ctx, store.JobSpec{Kind: JobTesterResolve,
			Payload: TesterResolveJob{IdentityID: ident.ID, ConversationID: conv.ID, URL: ref.Value}})
	}
	return true, e.openTesterRequest(ctx, ident, conv, ref)
}

// testerWords são sinais de que o link é o perfil para liberar como testador.
var testerWords = []string{"perfil", "testador", "libera", "facebook pessoal", "face pessoal"}

// testerContext: a mensagem é sobre o testador. Vale se fala em perfil/testador/liberar, se é
// praticamente só o link (até 3 palavras fora dele), se já há pedido aberto ou se o link de conexão
// Meta saiu nos últimos 14 dias.
func (e *Engine) testerContext(ctx context.Context, ident store.Identity, text string) (bool, error) {
	low := strings.ToLower(text)
	for _, w := range testerWords {
		if strings.Contains(low, w) {
			return true, nil
		}
	}
	if wordsOutsideLinks(low) <= testerMaxWords {
		return true, nil
	}
	// Pedido aberto: a conversa já é sobre o testador (ex.: "na verdade é este: <link>").
	if _, err := e.testers().OpenTesterRequest(ctx, ident.ID); err == nil {
		return true, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	return e.Store.MetaLinkSentWithin(ctx, ident.ID, testerLinkContext)
}

// wordsOutsideLinks conta as palavras do texto que não são link nem pontuação solta.
func wordsOutsideLinks(text string) int {
	n := 0
	for _, f := range strings.Fields(text) {
		if strings.Contains(f, "://") || strings.HasPrefix(f, "www.") || strings.Contains(f, "facebook.com") ||
			strings.Contains(f, "fb.com") || strings.Contains(f, "fb.me") {
			continue
		}
		if strings.Trim(f, ".,;:!?()[]{}\"'«»-–—…") == "" {
			continue
		}
		n++
	}
	return n
}

// testerResolve segue o link de compartilhamento e, se chegar num perfil, abre o pedido.
func (e *Engine) testerResolve(ctx context.Context, raw []byte) error {
	var j TesterResolveJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("engine: payload de link de compartilhamento inválido: %w", err)
	}
	resolved, rerr := e.resolveShare(ctx, j.URL) // fora da trava: pode levar segundos
	unlock, err := e.Store.LockConversation(ctx, j.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	conv, err := e.Store.GetConversation(ctx, j.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, j.IdentityID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ident.Status == "blocked" || conv.IdentityID != ident.ID || conv.State == store.StateNeedsHuman {
		return nil
	}
	if rerr != nil {
		e.Log.Warn("link de compartilhamento do perfil não resolvido", "conversation_id", conv.ID, "erro", rerr)
	}
	ref, ok := e.parseProfile(resolved)
	if rerr != nil || !ok || ref.Kind == "share" {
		return e.Store.Enqueue(ctx, e.clientJob(ident.OrgID, conv, msgTesterAddressBar))
	}
	if _, active, err := e.Store.ActiveMeta(ctx, ident.OrgID); err != nil || active {
		return err // conectou enquanto o link era seguido: não precisa mais de testador
	}
	return e.openTesterRequest(ctx, ident, conv, ref)
}

// openTesterRequest grava o pedido, responde o cliente e avisa a equipe pelo outbox (no máximo um
// aviso de pedido a cada 30 min; a fila do painel sempre mostra o perfil atual).
func (e *Engine) openTesterRequest(ctx context.Context, ident store.Identity, conv store.Conversation, ref meta.ProfileRef) error {
	ts := e.testers()
	prev, err := ts.OpenTesterRequest(ctx, ident.ID)
	switch {
	case err == nil && prev.RefKind == ref.Kind && prev.RefValue == ref.Value:
		return e.Store.Enqueue(ctx, e.clientJob(ident.OrgID, conv, msgTesterAlready)) // mesmo perfil: a equipe já sabe
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return err
	}
	req, err := ts.UpsertTesterRequest(ctx, store.TesterRequest{OrgID: ident.OrgID, IdentityID: ident.ID, ConversationID: conv.ID,
		ProfileURL: ref.URL, RefKind: ref.Kind, RefValue: ref.Value, Status: store.TesterRequested})
	if err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, member(ident), "tester_requested", map[string]any{"request_id": req.ID, "ref_kind": ref.Kind})
	jobs := []store.JobSpec{e.clientJob(ident.OrgID, conv, msgTesterReceived)}
	notify, err := ts.MarkTesterNotified(ctx, req.ID, testerNotifyEvery)
	if err != nil {
		return err
	}
	if notify {
		text := fmt.Sprintf(msgTeamTester, ident.MemberName, ident.OrgName, ident.Phone, ref.Value, ref.URL)
		if e.MetaAppID != "" {
			text += fmt.Sprintf(msgTeamTesterWhere, e.MetaAppID)
		}
		jobs = append(jobs, e.teamJobs(ctx, TeamNotice{Kind: TeamTester, Ident: ident,
			Reason: testerReason("testador", ref.Value, "ver fila Testadores no painel"), Text: text})...)
	} else {
		e.Log.Info("aviso de testador à equipe segurado: já avisado há pouco", "request_id", req.ID)
	}
	// Mesmo estado e campos: a transação só garante que resposta e aviso saem juntos.
	return e.Store.SetStateAndEnqueue(ctx, conv.ID, conv.State, conv.Fields, jobs...)
}

// testerReason é o motivo curto do modelo aviso_equipe (usado quando o chat da equipe está fora da
// janela de 24 h): leva o essencial do testador, já que o texto completo não sai.
func testerReason(what, ref, action string) string {
	return fmt.Sprintf("%s: ID/usuário %s — %s", what, ref, action)
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
	r, err := e.testers().TesterRequestByID(ctx, j.RequestID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && r.Status != store.TesterAdded) {
		e.Log.Info("pedido de testador fora de added; nada a avisar", "request_id", j.RequestID)
		return nil
	}
	if err != nil {
		return err
	}
	return e.testerMessage(ctx, r, msgTesterAdded, "testador adicionado")
}

// testerConv lê a conversa e a identidade do pedido (com a conversa travada pelo chamador).
// ok=false: nada a fazer (sem conversa, bloqueado, ou humano atendendo, e aí quem fala é a equipe).
func (e *Engine) testerConv(ctx context.Context, r store.TesterRequestView) (store.Conversation, store.Identity, bool, error) {
	conv, err := e.Store.GetConversation(ctx, r.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		return conv, store.Identity{}, false, nil
	}
	if err != nil {
		return conv, store.Identity{}, false, err
	}
	ident, err := e.Store.GetIdentity(ctx, r.IdentityID)
	if errors.Is(err, store.ErrNotFound) {
		return conv, ident, false, nil
	}
	if err != nil {
		return conv, ident, false, err
	}
	return conv, ident, ident.Status != "blocked" && conv.State != store.StateNeedsHuman, nil
}

// clientOutsideWindow: na Cloud API, o cliente está calado há mais de 23 h (texto livre seria recusado).
func (e *Engine) clientOutsideWindow(ctx context.Context, convID int64) (bool, error) {
	if _, ok := e.templateSender(); !ok {
		return false, nil
	}
	return e.outsideWindow(ctx, convID)
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
	conv, ident, ok, err := e.testerConv(ctx, r)
	if err != nil || !ok {
		return err
	}
	out, err := e.clientOutsideWindow(ctx, conv.ID)
	if err != nil {
		return err
	}
	if out {
		e.audit(ctx, ident.OrgID, "system", "tester_outside_window", map[string]any{"request_id": r.ID})
		return e.enqueueAll(ctx, e.teamJobs(ctx, TeamNotice{Kind: TeamTester, Ident: ident,
			Reason: testerReason(what+", cliente fora da janela de 24 h", r.RefValue, "mande o link pelo painel"),
			Text:   fmt.Sprintf(msgTeamTesterOutside, what, ident.MemberName, ident.OrgName, ident.Phone, r.RefValue)})...)
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
	// A Meta devolve IDs do app, que não casam com o perfil: só dá pra saber quem aceitou se houver um
	// só pedido aberto (requested ou added) de organização sem conexão.
	if fresh > 0 {
		cands, err := ts.OpenTestersWithoutMeta(ctx)
		switch {
		case err != nil:
			errs = append(errs, err)
		case len(cands) == 1:
			if err := e.acceptNotice(ctx, cands[0]); err != nil {
				errs = append(errs, err)
			}
		default:
			e.Log.Info("testador novo na Meta sem dono certo; nada a avisar", "candidatos", len(cands))
		}
	}
	awaiting, err := ts.TestersAwaitingAcceptance(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
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

// acceptNotice manda o "Vi que você aceitou" uma vez só por pedido. Marca antes de enviar (a marca é
// a trava): no pior caso o aviso se perde, mas nunca se repete.
func (e *Engine) acceptNotice(ctx context.Context, r store.TesterRequestView) error {
	if err := e.testers().MarkTesterAcceptNotified(ctx, r.ID); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	return e.testerMessage(ctx, r, msgTesterAccepted, "testador aceitou o convite")
}

// remindTester lembra o cliente uma vez e avisa a equipe. Com o cliente fora da janela de 24 h não há
// como lembrar: sai um aviso só, para a equipe chamar. Marca antes de enviar: no pior caso o lembrete
// se perde, mas nunca se repete. ErrNotFound = já lembrado (a marca é a trava).
func (e *Engine) remindTester(ctx context.Context, r store.TesterRequestView) error {
	if err := e.testers().MarkTesterReminded(ctx, r.ID); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if e.MetaConnectBaseURL == "" {
		return nil
	}
	unlock, err := e.Store.LockConversation(ctx, r.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	conv, ident, ok, err := e.testerConv(ctx, r)
	if err != nil || !ok {
		return err
	}
	out, err := e.clientOutsideWindow(ctx, conv.ID)
	if err != nil {
		return err
	}
	if out {
		e.audit(ctx, ident.OrgID, "system", "tester_outside_window", map[string]any{"request_id": r.ID})
		return e.enqueueAll(ctx, e.teamJobs(ctx, TeamNotice{Kind: TeamTester, Ident: ident,
			Reason: testerReason("testador sem aceite em 24 h, cliente fora da janela", r.RefValue, "chame pelo painel"),
			Text:   fmt.Sprintf(msgTeamTesterLateOutside, ident.MemberName, ident.OrgName, ident.Phone, r.RefValue)})...)
	}
	link, err := e.metaLinkFor(ctx, ident)
	if err != nil {
		return err
	}
	jobs := append(e.teamJobs(ctx, TeamNotice{Kind: TeamTester, Ident: ident,
		Reason: testerReason("testador sem aceite em 24 h", r.RefValue, "lembrei o cliente"),
		Text:   fmt.Sprintf(msgTeamTesterLate, ident.MemberName, ident.OrgName, ident.Phone, r.RefValue)}),
		e.clientJob(ident.OrgID, conv, join(msgTesterReminder, link)))
	e.audit(ctx, ident.OrgID, "system", "tester_link_sent", map[string]any{"request_id": r.ID, "motivo": "lembrete de testador"})
	return e.enqueueAll(ctx, jobs...)
}

// testerAccepted fecha os pedidos de testador da organização quando a conexão Meta conclui: com a
// conta conectada, ninguém dela precisa mais ser testador.
func (e *Engine) testerAccepted(ctx context.Context, convID int64) {
	conv, err := e.Store.GetConversation(ctx, convID)
	var ident store.Identity
	if err == nil {
		ident, err = e.Store.GetIdentity(ctx, conv.IdentityID)
	}
	if err == nil {
		err = e.testers().MarkTesterAcceptedByOrg(ctx, ident.OrgID)
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
