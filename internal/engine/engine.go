// Package engine orquestra a conversa: WhatsApp, Atendente, Gestor e banco.
// Toda ação (rascunho, aprovação, envio) é decidida aqui, nunca pelas IAs.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/humanize"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	JobPropose = "manager.propose"
	JobSend    = "whatsapp.send"
	JobReply   = "conversation.reply"

	codeTTL     = 30 * time.Minute
	historySize = 12
	maxBatch    = 10 // mensagens seguidas respondidas de uma vez

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
	ConversationID int64    `json:"conversation_id"`
	ChatID         string   `json:"chat_id"`
	Text           string   `json:"text"`
	OrgID          int64    `json:"org_id,omitempty"`
	Human          bool     `json:"human,omitempty"` // com presença e tempo de digitação
	Voice          bool     `json:"voice,omitempty"` // tenta mandar como mensagem de voz
	Next           []string `json:"next,omitempty"`  // balões seguintes, enviados em jobs encadeados
	Typed          bool     `json:"typed,omitempty"` // "digitando…" já apareceu: agora é entregar
	// Template: modelo aprovado usado no lugar do texto fora da janela de 24 h (Cloud API).
	Template *whatsapp.Template `json:"template,omitempty"`
	// Author: nome de quem da equipe escreveu pelo painel; a mensagem é gravada como "staff".
	Author string `json:"author,omitempty"`
	// After: o que fazer depois que o último balão sair (ex.: publicar depois da confirmação da conexão Meta).
	After string `json:"after,omitempty"`
	// MetaConnected: confirmação da conexão Meta; ao sair fecha o pedido de testador da conversa.
	MetaConnected bool `json:"meta_connected,omitempty"`
	// VoiceOnly: só voz (relatório falado, o texto já saiu); se a voz falhar, não repete em texto.
	VoiceOnly bool `json:"voice_only,omitempty"`
}

// StaffSendJob é a mensagem que alguém da equipe escreveu no painel durante o atendimento humano.
// Sai como texto simples (sem "digitando…" nem voz) e é gravada com o nome de quem escreveu.
func StaffSendJob(convID int64, chatID, text, author string) store.JobSpec {
	return store.JobSpec{Kind: JobSend, Payload: SendJob{ConversationID: convID, ChatID: chatID, Text: text, Author: author}, MaxAttempts: sendAttempts}
}

// ReplyJob responde as mensagens seguidas de uma conversa depois do debounce.
type ReplyJob struct {
	ConversationID int64 `json:"conversation_id"`
	MessageID      int64 `json:"message_id"` // mensagem que agendou; só a mais nova responde
}

type Engine struct {
	Store *store.Store
	WA    whatsapp.Provider
	// IdentityProvider é o namespace de whatsapp_identities. Fica "waha" mesmo com a Wame,
	// para os clientes atuais continuarem cadastrados. Vazio = WA.Name().
	IdentityProvider string
	Attendant        *attendant.Agent
	Manager          *manager.Agent
	Transcriber      llm.Provider
	TranscribeModel  string
	// OperatorPhone: aviso à equipe quando TeamPhones está vazio (compatibilidade).
	OperatorPhone string
	Now           func() time.Time
	Log           *slog.Logger

	// Cadastro automático de número novo, com limites de custo.
	AutoSignup       bool
	SignupMaxBudget  float64
	SignupDailyLimit int

	// Atendente humana: debounce, visto, presença, balões e voz. Sem Voice, só texto.
	Humanize bool
	Debounce time.Duration
	Voice    llm.Speaker
	TTSModel string
	TTSVoice string
	Rand     func() float64 // variação dos tempos; trocada nos testes

	// Cadastro: com URL, quem não concluiu o cadastro recebe o link em vez de falar com a IA.
	SignupBaseURL string
	// SignupFlowID: formulário do WhatsApp publicado; com ele (e provedor que manda formulário)
	// o cadastro é feito sem sair do WhatsApp. TermsVersion é a versão dos termos aceitos ali.
	SignupFlowID string
	// SignupFlowDraft manda o formulário em rascunho (enquanto a Meta não libera a publicação).
	SignupFlowDraft bool
	TermsVersion    string

	// Anúncios (imagens e vídeos) pedidos depois da aprovação. Sem Storage, a aprovação encerra a conversa.
	Storage Storage

	// Relatório diário às 8 h para este número. Vazio = sem relatório.
	ReportPhone string

	// Mia configurada no painel (textos, campos, persona, funil, segmentos e voz), lida do banco
	// com cache curto. nil = padrão do código.
	Mia *mia.Cache

	// Conexão da conta Meta: base do link (…/m). Vazio = desligada.
	MetaConnectBaseURL string
	// LeadPrivacyURL é a política de privacidade que a Meta exige no formulário de cadastro dos anúncios.
	LeadPrivacyURL string

	// Publicação na Meta: cria a campanha aprovada, pausada, na conta do cliente. Sem Publisher
	// ou sem MetaTokenKey (cofre da META_TOKEN_KEY, que abre o token do cliente), o cliente
	// fica sabendo que a publicação automática está desligada e a equipe é avisada.
	Publisher    meta.Publisher
	MetaTokenKey *secretbox.Box
	// Publications: nil = Store. Os testes trocam por um fake.
	Publications PublicationStore
	// CopyFix ajusta o texto de um anúncio reprovado (internal/copylab.Fix entra aqui). nil = a Mia explica o
	// motivo da reprovação sem sugerir texto novo.
	CopyFix func(headline, text, segment string) (string, string, bool)
	// Canal da equipe: privado de cada número (painel, senão TeamPhones, senão OperatorPhone) e,
	// opcional, o grupo pela instância não oficial. AdminURL vai no aviso de atendimento humano; vazio = sem link.
	TeamPhones []string
	// TeamDirectory lê os números marcados no painel (Equipe → "Recebe avisos"); quando devolve
	// algum, vale no lugar de TeamPhones. nil = só TeamPhones/OperatorPhone. Cache de 30 s.
	TeamDirectory func(ctx context.Context) ([]string, error)
	teamDir       teamDirCache
	TeamGroup     TeamGroupSender
	TeamGroupJID  string
	AdminURL      string
	// Testador automático do app da Meta. MetaAppID monta o link de Funções no aviso à equipe.
	MetaAppID string
	// AppTesters lê os testadores do app (Graph API); nil = leitura a cada 5 min desligada.
	AppTesters func(ctx context.Context) ([]string, error)
	// Testers: pedidos de testador; nil = Store.
	Testers TesterStore
	// ParseProfile e ResolveShare: nil = meta.ParseProfileLink e meta.ResolveShareLink (trocados nos testes).
	ParseProfile func(text string) (meta.ProfileRef, bool)
	ResolveShare func(ctx context.Context, url string) (string, error)
	// Instagram baixa o post do link que o cliente manda na etapa dos anúncios. nil = link segue como texto.
	Instagram InstagramFetcher
	// InstagramBudget: prazo total dos downloads de um post. 0 = InstagramBudget (3 min).
	InstagramBudget time.Duration
	igPermWarned    atomic.Bool // aviso de falta de instagram_basic sai uma vez só

	// Proteções do cliente (limite por conversa): gasto e janela mínimos para julgar sem agir por ruído.
	// Zero = padrão (R$ 30 e 24 h).
	GuardrailMinSpendCents int64
	GuardrailMinWindow     time.Duration
}

func (e *Engine) identityProvider() string {
	if e.IdentityProvider != "" {
		return e.IdentityProvider
	}
	return e.WA.Name()
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// HandleInbound processa uma mensagem recebida do cliente.
func (e *Engine) HandleInbound(ctx context.Context, in whatsapp.Inbound) error {
	ident, err := e.Store.FindIdentity(ctx, e.identityProvider(), in.Phone)
	newClient := false
	if errors.Is(err, store.ErrNotFound) {
		if !e.AutoSignup {
			e.Log.Info("telefone não cadastrado", "provider", e.identityProvider())
			return e.WA.SendText(ctx, in.ChatID, msgUnknown)
		}
		if ident, err = e.Store.AutoSignup(ctx, e.identityProvider(), in.Phone, e.SignupMaxBudget, e.SignupDailyLimit); err != nil {
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
		if err := e.Store.AddMessage(ctx, conv.ID, "in", store.KindSkipped, in.Text); err != nil {
			return err
		}
		if received == ident.DailyMessageLimit {
			e.audit(ctx, ident.OrgID, "system", "daily_limit_reached", map[string]any{"limit": ident.DailyMessageLimit})
			return e.send(ctx, conv, msgDailyLimit)
		}
		return nil
	}
	if in.Kind == whatsapp.KindFlow {
		return e.signupFlowAnswer(ctx, ident, conv, in)
	}
	// Portão do cadastro. Em atendimento humano, a pessoa da equipe conduz: nada de link.
	if e.SignupBaseURL != "" && ident.RegistrationStatus != "complete" && conv.State != store.StateNeedsHuman {
		return e.signupGate(ctx, ident, conv, in)
	}
	text, err := e.inboundText(ctx, ident, &conv, in)
	if errors.Is(err, errMediaFailed) {
		e.Log.Error("falha na mídia", "conversation_id", conv.ID, "erro", err)
		e.audit(ctx, ident.OrgID, "system", "media_failed", map[string]any{"conversation_id": conv.ID, "error": err.Error()})
		if err := e.Store.AddMessage(ctx, conv.ID, "in", store.KindSkipped, "[áudio não processado]"); err != nil {
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
	if e.Humanize {
		id, err := e.addInbound(ctx, conv.ID, in, text)
		if err != nil {
			return err
		}
		if conv.State == store.StateNeedsHuman {
			return e.Store.MarkReplied(ctx, conv.ID, id) // humano atende; o bot não junta isso depois
		}
		// Debounce: quem responde é o job da mensagem mais nova, depois de um tempo sem mensagens.
		return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobReply, Payload: ReplyJob{ConversationID: conv.ID, MessageID: id}, Delay: e.debounce()})
	}
	// Sem modo humano responde na hora; a marca de respondido acompanha para o modo poder ser ligado depois.
	id, err := e.addInbound(ctx, conv.ID, in, text)
	if err != nil {
		return err
	}
	if err := e.Store.MarkReplied(ctx, conv.ID, id); err != nil {
		return err
	}
	return e.dispatch(ctx, ident, conv, text, []string{text}, nil)
}

// addInbound grava a mensagem do cliente; foto e vídeo levam a referência da mídia para o painel mostrar.
func (e *Engine) addInbound(ctx context.Context, convID int64, in whatsapp.Inbound, text string) (int64, error) {
	if in.Kind == whatsapp.KindImage || in.Kind == whatsapp.KindVideo {
		id, err := e.Store.AddInboundMedia(ctx, convID, string(in.Kind), text, in.ExternalID)
		if err == nil && e.Storage != nil {
			// cópia guardada à parte: falha aqui não segura a resposta ao cliente
			if qerr := e.Store.Enqueue(ctx, store.JobSpec{Kind: JobMediaArchive, Payload: MediaArchiveJob{
				ConversationID: convID, MessageID: id, ExternalID: in.ExternalID, Kind: string(in.Kind)}}); qerr != nil {
				e.Log.Warn("mídia não enfileirada para guardar", "message_id", id, "erro", qerr)
			}
		}
		return id, err
	}
	return e.Store.AddInbound(ctx, convID, string(in.Kind), text)
}

// dispatch decide a resposta pelo estado da conversa. msgs são as mensagens do lote, em ordem
// (a aprovação vale a mais nova que tiver código); batch são os ids do lote, fora do histórico
// (nil = só a última mensagem gravada).
func (e *Engine) dispatch(ctx context.Context, ident store.Identity, conv store.Conversation, text string, msgs []string, batch []int64) error {
	ctx, tl := withTurnLog(ctx)
	defer e.saveTurn(ctx, tl, conv.ID, batch)
	// ATIVAR, PAUSAR e "tenta de novo" valem com campanha na Meta, em qualquer etapa.
	if conv.State != store.StateNeedsHuman && len(msgs) > 0 {
		if handled, err := e.campaignCommand(ctx, ident, conv, msgs[len(msgs)-1]); handled || err != nil {
			return err
		}
		if handled, err := e.guardrailCommand(ctx, ident, conv, msgs[len(msgs)-1]); handled || err != nil {
			return err
		}
		if handled, err := e.diagnosticCommand(ctx, ident, conv, msgs[len(msgs)-1]); handled || err != nil {
			return err
		}
	}
	if conv.State != store.StateNeedsHuman && len(msgs) > 0 {
		if handled, err := e.reportPrefCommand(ctx, ident, conv, msgs[len(msgs)-1]); handled || err != nil {
			return err
		}
	}
	if e.MetaConnectBaseURL != "" && isMetaConnectCommand(conv.State, text) {
		noteAction(ctx, "comando de conexão (sem IA)")
		return e.sendMetaLink(ctx, ident, conv, "")
	}
	if isPaidCommand(conv.State, text) {
		noteAction(ctx, "aviso de pagamento (sem IA)")
		return e.paidCheck(ctx, ident, conv)
	}
	if isBillingCommand(conv.State, text) {
		noteAction(ctx, "pedido de pagamento (sem IA)")
		return e.sendBillingLink(ctx, ident, conv, "")
	}
	if conv.State != store.StateNeedsHuman {
		if done, err := e.archiveChoiceTurn(ctx, ident, conv, text); done || err != nil {
			return err
		}
	}
	if done, err := e.profileLinkTurn(ctx, ident, conv, text, msgs); done || err != nil {
		return err
	}
	switch conv.State {
	case store.StateNeedsHuman:
		return nil
	case store.StateAwaitingProposal:
		noteAction(ctx, "proposta em preparo: pediu para aguardar")
		return e.send(ctx, conv, msgBusy)
	case store.StateAwaitingCreatives:
		if e.Storage != nil {
			return e.creativesTurn(ctx, conv, msgs)
		}
	case store.StateAwaitingApproval:
		for i := len(msgs) - 1; i >= 0; i-- {
			if code, ok := campaign.ParseApproval(msgs[i]); ok {
				return e.approve(ctx, ident, conv, code)
			}
		}
		// "ok", "sim" sem código: não aprova; lembra como aprovar sem gastar IA.
		if len(msgs) > 0 && approvalAck.MatchString(strings.TrimSpace(msgs[len(msgs)-1])) {
			noteAction(ctx, "concordou sem código: lembrou como aprovar")
			return e.send(ctx, conv, msgApprovalHowTo)
		}
	}
	return e.converse(ctx, ident, conv, text, batch)
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
	case whatsapp.KindImage, whatsapp.KindVideo:
		if conv.State == store.StateAwaitingCreatives && e.Storage != nil {
			return e.saveCreative(ctx, ident, *conv, in), nil
		}
		if in.Kind == whatsapp.KindVideo {
			return strings.TrimSpace("[vídeo enviado] " + in.Text), nil
		}
		conv.Fields = conv.Fields.Merge(contract.Fields{Media: []string{in.ExternalID}})
		if err := e.Store.UpdateConversation(ctx, conv.ID, conv.State, conv.Fields); err != nil {
			return "", err
		}
		return strings.TrimSpace("[imagem enviada] " + in.Text), nil
	default:
		return msgUnsupported, nil
	}
}

func (e *Engine) converse(ctx context.Context, ident store.Identity, conv store.Conversation, text string, batch []int64) error {
	history, err := e.history(ctx, conv.ID, batch)
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
	cfg := e.miaConfig(ctx)
	metaState, err := e.metaStatus(ctx, ident.OrgID)
	if err != nil {
		return err
	}
	turn, usage, err := e.Attendant.Reply(ctx, attendant.Input{
		OrgName: ident.OrgName, Niche: ident.Niche, State: conv.State,
		Fields: current, Missing: cfg.CampaignMissing(current), History: history, Message: text,
		Profile: ident.Profile, ProjectName: activeName, Projects: names,
		Voice:  e.voiceOn(cfg) && voiceWanted(ctx),
		Meta:   metaState,
		Config: &cfg, Segment: cfg.SegmentFor(ident.Segment, ident.Niche),
	})
	e.recordUsage(ctx, ident.OrgID, "attendant", usage)
	if err != nil {
		e.Log.Error("atendente falhou", "conversation_id", conv.ID, "erro", err)
		e.audit(ctx, ident.OrgID, "attendant", "llm_failed", map[string]any{"conversation_id": conv.ID, "error": err.Error()})
		return e.send(ctx, conv, msgRetryLater)
	}

	noteTurn(ctx, turn)
	// Código de aprovação só no resumo oficial; antes do SIM, a IA não diz que aprovou.
	if clean := scrubReply(turn.Reply, conv.State == store.StateAwaitingApproval || conv.State == store.StateAwaitingProposal); clean != turn.Reply {
		noteAction(ctx, "tirou código ou aprovação falsa da fala da IA")
		turn.Reply = clean
	}

	// Memória do negócio: o Go mescla e grava; texto vazio não apaga.
	if turn.Profile.HasData() {
		ident.Profile = ident.Profile.Merge(turn.Profile)
		if err := e.Store.UpdateOrgProfile(ctx, ident.OrgID, ident.Profile); err != nil {
			return err
		}
	}
	if conv.State == store.StateOnboarding {
		// A ficha do negócio é opcional: qualquer mensagem tira o cliente do onboarding e ela nunca bloqueia.
		conv.State = store.StateIdle
		if err := e.Store.UpdateConversation(ctx, conv.ID, conv.State, conv.Fields); err != nil {
			return err
		}
		wantsCampaign := turn.Intent == contract.IntentCreateCampaign || turn.Intent == contract.IntentNewProject || turn.Fields.HasData()
		if !wantsCampaign {
			return e.speakNoPromise(ctx, ident, conv, turn.Reply, current, cfg)
		}
	}

	switch turn.Intent {
	case contract.IntentListProjects:
		return e.send(ctx, conv, listText(e.miaConfig(ctx), projects))
	case contract.IntentProjectStatus:
		return e.projectStatus(ctx, ident, conv, turn.Project, projects)
	case contract.IntentConnectMeta:
		if e.MetaConnectBaseURL == "" {
			return e.speak(ctx, ident, conv, turn.Reply)
		}
		return e.sendMetaLink(ctx, ident, conv, turn.Reply)
	case contract.IntentBilling:
		return e.sendBillingLink(ctx, ident, conv, turn.Reply)
	case contract.IntentNewProject:
		return e.startProject(ctx, ident, conv, turn, projects)
	case contract.IntentSwitchProject:
		return e.switchProject(ctx, ident, conv, turn, projects)
	case contract.IntentHuman:
		return e.escalateWith(ctx, ident, conv, e.humanNotice(ident, text), "cliente pediu atendimento humano", msgHuman)
	case contract.IntentReject:
		rejectedDraft := conv.State == store.StateAwaitingApproval
		if rejectedDraft {
			if err := e.closeOpenDraft(ctx, ident, conv, "rejected"); err != nil {
				return err
			}
		}
		if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateIdle, contract.Fields{}); err != nil {
			return err
		}
		if rejectedDraft {
			return e.send(ctx, conv, msgRejected)
		}
		return e.speak(ctx, ident, conv, turn.Reply)
	}

	merged := campaignFields(ident.Profile, conv.Fields, turn.Fields)
	campaignTalk := turn.Intent == contract.IntentCreateCampaign || turn.Intent == contract.IntentProvideInfo || turn.Fields.HasData()
	if campaignTalk {
		if err := e.ensureProject(ctx, ident, &conv, turn.Project, projects); err != nil {
			if max, ok := projectLimit(err); ok {
				return e.askArchive(ctx, ident, conv, max, turn.Project, merged)
			}
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
		// Pedido de ajuste com a proposta na mesa vira proposta nova mesmo que a IA não tenha separado
		// dados (ex.: idade, que não é campo próprio): o pedido vai inteiro para o especialista.
		if !campaignTalk && looksLikeAdjustment(text) {
			noteAction(ctx, "pedido de ajuste reconhecido pelo sistema")
			campaignTalk = true
		}
		if !campaignTalk {
			return e.speak(ctx, ident, conv, turn.Reply)
		}
		// Ajuste pedido: descarta o rascunho atual e pede nova proposta ao Gestor.
		if err := e.closeOpenDraft(ctx, ident, conv, "cancelled"); err != nil {
			return err
		}
		return e.requestProposal(ctx, ident, conv, merged, text, turn.Reply, false)
	}
	if !campaignTalk && conv.State != store.StateCollecting {
		return e.speakNoPromise(ctx, ident, conv, turn.Reply, merged, cfg)
	}
	if len(cfg.CampaignMissing(merged)) == 0 {
		return e.requestProposal(ctx, ident, conv, merged, "", turn.Reply, false)
	}
	if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateCollecting, merged); err != nil {
		return err
	}
	return e.speakNoPromise(ctx, ident, conv, turn.Reply, merged, cfg)
}

// promiseRe acha a resposta da IA que anuncia a proposta ("o especialista vai montar", "chega em instantes").
var promiseRe = regexp.MustCompile(`(?i)especialista\s+(vai|já|ja|está|esta)\b|proposta.{0,80}(instantes|a caminho|já já|ja ja|chega)|(instantes|a caminho).{0,80}proposta`)

// speakNoPromise fala a resposta da IA, menos quando ela promete a proposta sem o sistema ter pedido: o
// cliente ficaria esperando algo que nunca chega. Aí a resposta vira a pergunta do que falta.
func (e *Engine) speakNoPromise(ctx context.Context, ident store.Identity, conv store.Conversation, reply string, f contract.Fields, cfg mia.Config) error {
	if promiseRe.MatchString(reply) {
		noteAction(ctx, "trocou promessa de proposta que não foi pedida")
		if m := missingText(cfg, f); m != "" {
			reply = fmt.Sprintf(msgStillMissing, m)
		} else {
			reply = msgWhatToAdvertise
		}
		e.Log.Warn("resposta prometia proposta sem pedido; trocada", "conversation_id", conv.ID)
	}
	return e.speak(ctx, ident, conv, reply)
}

// requestProposal pede a proposta ao Gestor; fixed indica que reply é mensagem fixa (nunca vira voz).
// No modo humano o Gestor só começa depois dos balões da resposta, para o resumo chegar por último.
func (e *Engine) requestProposal(ctx context.Context, ident store.Identity, conv store.Conversation, f contract.Fields, adjustment, reply string, fixed bool) error {
	if adjustment != "" {
		noteAction(ctx, "ajuste: pediu proposta nova ao especialista")
	} else {
		noteAction(ctx, "pediu proposta ao especialista")
	}
	job := store.JobSpec{Kind: JobPropose, Payload: ProposeJob{ConversationID: conv.ID, Adjustment: adjustment}}
	if e.Humanize {
		job.Delay = humanize.ChainDuration(reply) + time.Second
		if !fixed && e.voiceFor(ctx, reply) {
			job.Delay = humanize.MaxTyping + ttsTimeout
		}
	}
	if err := e.Store.SetStateAndEnqueue(ctx, conv.ID, store.StateAwaitingProposal, f, job); err != nil {
		return err
	}
	if fixed {
		return e.send(ctx, conv, reply)
	}
	return e.speak(ctx, ident, conv, reply)
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

// history devolve as últimas mensagens sem as do lote atual (batch nil = sem a última gravada).
func (e *Engine) history(ctx context.Context, convID int64, batch []int64) ([]attendant.Turn, error) {
	msgs, err := e.Store.RecentMessages(ctx, convID, historySize+max(len(batch), 1))
	if err != nil {
		return nil, err
	}
	if batch == nil && len(msgs) > 0 {
		msgs = msgs[:len(msgs)-1] // a última é a mensagem atual
	}
	skip := make(map[int64]bool, len(batch))
	for _, id := range batch {
		skip[id] = true
	}
	turns := make([]attendant.Turn, 0, len(msgs))
	for _, m := range msgs {
		if !skip[m.ID] {
			turns = append(turns, attendant.Turn{FromClient: m.Direction == "in", Text: m.Body})
		}
	}
	if len(turns) > historySize {
		turns = turns[len(turns)-historySize:]
	}
	return turns, nil
}

// escalate passa a conversa para um humano: o bot para de responder e a equipe é avisada.
func (e *Engine) escalate(ctx context.Context, ident store.Identity, conv store.Conversation, reason, clientMsg string) error {
	return e.escalateWith(ctx, ident, conv, TeamNotice{Kind: TeamAlert, Ident: ident, Reason: reason}, reason, clientMsg)
}

// escalateWith escala com o aviso dado; reason fica na auditoria. Os avisos vão pelo outbox na
// mesma transação do estado, para sobreviver ao WhatsApp fora do ar.
func (e *Engine) escalateWith(ctx context.Context, ident store.Identity, conv store.Conversation, n TeamNotice, reason, clientMsg string) error {
	jobs := append([]store.JobSpec{sendJob(conv.ID, conv.ChatID, clientMsg)}, e.teamJobs(ctx, n)...)
	if err := e.Store.SetStateAndEnqueue(ctx, conv.ID, store.StateNeedsHuman, conv.Fields, jobs...); err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "escalated_human", map[string]any{"conversation_id": conv.ID, "reason": reason})
	return nil
}

// send manda uma mensagem fixa. No modo humano ela vai em balões com "digitando…", nunca em voz.
func (e *Engine) send(ctx context.Context, conv store.Conversation, text string) error {
	if e.Humanize {
		return e.Store.Enqueue(ctx, humanJob(0, conv, text, false))
	}
	if err := e.WA.SendText(ctx, conv.ChatID, text); err != nil {
		return err
	}
	return e.Store.AddMessage(ctx, conv.ID, "out", "text", redactLinks(text))
}

// speak manda a resposta da Atendente; vira voz quando o cliente falou por áudio.
func (e *Engine) speak(ctx context.Context, ident store.Identity, conv store.Conversation, text string) error {
	if !e.Humanize {
		return e.send(ctx, conv, text)
	}
	return e.Store.Enqueue(ctx, humanJob(ident.OrgID, conv, text, e.voiceFor(ctx, text)))
}

// voiceFor diz se a resposta da Atendente vai em voz.
func (e *Engine) voiceFor(ctx context.Context, text string) bool {
	return e.voiceOn(e.miaConfig(ctx)) && voiceWanted(ctx) && humanize.ShouldVoice(text)
}

// miaConfig é a configuração da Mia em vigor (cache do banco; padrão do código sem configuração).
func (e *Engine) miaConfig(ctx context.Context) mia.Config { return e.Mia.Get(ctx) }

// voiceOn: há TTS configurado e a voz está ligada no painel.
func (e *Engine) voiceOn(cfg mia.Config) bool { return e.Voice != nil && cfg.Voice.Enabled }

// ttsVoice é a voz escolhida no painel, ou TTS_VOICE.
func (e *Engine) ttsVoice(cfg mia.Config) string {
	if cfg.Voice.Name != "" {
		return cfg.Voice.Name
	}
	return e.TTSVoice
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

var (
	replySentenceRe = regexp.MustCompile(`[^.!?\n]+[.!?]*[ \t]*|\n`)
	// replyCodeRe: código de aprovação escrito pela IA (inventado ou de uma proposta antiga).
	replyCodeRe = regexp.MustCompile(`(?i)\bSIM\b\W{0,3}\d{6}|c[oó]digo\D{0,12}\d{6}`)
	// replyApprovedRe: a IA dizendo que a proposta foi aprovada (só o SIM com o código aprova).
	replyApprovedRe = regexp.MustCompile(`(?i)\baprovad[ao]s?\b`)
)

// scrubReply tira da fala da IA as frases com código de aprovação e, antes da aprovação (claims), as que
// dizem que algo foi aprovado. O código válido vai só no resumo oficial da proposta.
func scrubReply(reply string, claims bool) string {
	var b strings.Builder
	removed := false
	for _, s := range replySentenceRe.FindAllString(reply, -1) {
		if replyCodeRe.MatchString(s) || (claims && replyApprovedRe.MatchString(s)) {
			removed = true
			continue
		}
		b.WriteString(s)
	}
	out := strings.TrimSpace(b.String())
	if out == "" && removed {
		return "Certo!"
	}
	return out
}

var (
	// approvalAck: concordância sem o código (não aprova).
	approvalAck = regexp.MustCompile(`(?i)^(ok(ay|ey)?|okk*|beleza|blz|show|perfeito|fechado|combinado|pode ser|pode|sim|s|isso|top|certo|aprovad[oa]|aprovo|t[aá] bom|ta bom|massa|bora|vamos)[\s!.👍✅]*$`)
	// adjustTermRe e adjustVerbRe: pedido de mudança na proposta (o quê + verbo de mudança).
	adjustTermRe = regexp.MustCompile(`(?i)\b(idade|faixa|anos|raio|km|or[cç]amento|reais|r\$|p[uú]blico|homens?|mulheres?|masculino|feminino|feed|stories|reels|cidade|regi[aã]o|bairro|texto|t[ií]tulo|headline|objetivo|dias|dura[cç][aã]o|interesses?|valor|investimento)\b`)
	adjustVerbRe = regexp.MustCompile(`(?i)\b(alter\w*|mud\w*|troc\w*|ajust\w*|coloc\w*|bot\w*|aument\w*|diminu\w*|reduz\w*|tir\w*|remov\w*|quero|prefiro|deix\w*|fa[cç]a|faz|s[oó]|somente|apenas|sem|em vez|corrig\w*|inclu\w*|adicion\w*)\b`)
	adjustAgeRe  = regexp.MustCompile(`(?i)\b\d{2}\s*(a|até|ate|-)\s*\d{2}\s*anos\b`)
)

// looksLikeAdjustment diz se a mensagem pede mudança na proposta ("altere a idade para 40 a 56").
func looksLikeAdjustment(text string) bool {
	return adjustAgeRe.MatchString(text) || (adjustTermRe.MatchString(text) && adjustVerbRe.MatchString(text))
}
