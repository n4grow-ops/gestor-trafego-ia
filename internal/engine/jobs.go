package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/copylab"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// HandleJob executa um job do outbox. Erro devolvido = o outbox tenta de novo.
func (e *Engine) HandleJob(ctx context.Context, job store.Job) error {
	switch job.Kind {
	case store.NotifyJobKind:
		// Notificação por webhook (aba Notificações): sem entregador configurado, descarta.
		if e.Notify == nil {
			return nil
		}
		return e.Notify.Deliver(ctx, job)
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
		if s.MetaConnected {
			e.testerAccepted(ctx, s.ConversationID)
			s.MetaConnected = false // os balões seguintes não repetem
		}
		if s.Human {
			return e.sendHuman(ctx, s)
		}
		if err := e.deliver(ctx, s); err != nil {
			return err
		}
		if s.ConversationID == 0 {
			return nil // aviso ao operador: fora de conversa
		}
		if s.Author != "" {
			return e.Store.AddStaffMessage(ctx, s.ConversationID, redactLinks(s.Text), s.Author)
		}
		return e.Store.AddMessage(ctx, s.ConversationID, "out", "text", redactLinks(s.Text))
	case JobReply:
		var r ReplyJob
		if err := json.Unmarshal(job.Payload, &r); err != nil {
			return fmt.Errorf("engine: payload de resposta inválido: %w", err)
		}
		return e.reply(ctx, r)
	case JobTeamGroup:
		return e.sendTeamGroup(ctx, job.Payload)
	case JobDailyReport:
		return e.dailyReport(ctx)
	case JobCleanup:
		return e.cleanup(ctx)
	case JobProjectPropose:
		return e.projectPropose(ctx, job.Payload)
	case JobTeamPause:
		return e.teamPause(ctx, job.Payload)
	case JobDailyResults:
		return e.dailyResults(ctx)
	case JobFundingUpdate:
		return e.fundingUpdate(ctx, job.Payload)
	case JobAdsReview:
		return e.adsReview(ctx, job.Payload)
	case JobTokenCheck:
		return e.tokenCheck(ctx)
	case JobGuardrailCheck:
		return e.guardrailCheck(ctx)
	case JobAccountDiagnostic:
		return e.accountDiagnostic(ctx, job.Payload)
	case JobShieldHealth:
		return e.shieldHealth(ctx)
	case JobMediaArchive:
		m, err := decodeMediaArchive(job.Payload)
		if err != nil {
			return err
		}
		return e.archiveMedia(ctx, m)
	case JobSignupRemind:
		var r RemindJob
		if err := json.Unmarshal(job.Payload, &r); err != nil {
			return fmt.Errorf("engine: payload de lembrete inválido: %w", err)
		}
		return e.remind(ctx, r)
	case JobTesterAdded:
		j, err := decodeTesterAdded(job.Payload)
		if err != nil {
			return err
		}
		return e.testerAdded(ctx, j)
	case JobTesterPoll:
		return e.testerPoll(ctx)
	case JobWAHealth:
		return e.waHealth(ctx)
	case JobBillingNotify:
		return e.billingNotify(ctx, job.Payload)
	case JobTesterResolve:
		return e.testerResolve(ctx, job.Payload)
	case JobMetaLinkFollowup:
		return e.metaLinkFollowup(ctx, job.Payload)
	case JobCreativesPreview:
		var p PreviewJob
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("engine: payload de prévia inválido: %w", err)
		}
		return e.sendPreview(ctx, p)
	case JobMetaPublish:
		var p PublishJob
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("engine: payload de publicação inválido: %w", err)
		}
		return e.publish(ctx, p)
	case JobInstagramFetch:
		var j InstagramJob
		if err := json.Unmarshal(job.Payload, &j); err != nil {
			return fmt.Errorf("engine: payload do post do instagram inválido: %w", err)
		}
		return e.fetchInstagram(ctx, j)
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
	case JobTeamGroup:
		// O privado da equipe já recebeu o aviso: o grupo é reforço, então só registra.
		e.Log.Error("aviso ao grupo da equipe não entregue", "job_id", job.ID)
		return nil
	case JobCreativesPreview:
		// Prévia é cortesia: a etapa já fechou, então só registra.
		e.Log.Error("prévia do anúncio não entregue", "job_id", job.ID)
		return nil
	case JobMetaPublish:
		var p PublishJob
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		return e.publishGaveUp(ctx, p)
	case JobInstagramFetch:
		var j InstagramJob
		if err := json.Unmarshal(job.Payload, &j); err != nil {
			return fmt.Errorf("engine: %w", err)
		}
		return e.instagramGaveUp(ctx, j)
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
	// O tipo de campanha é pedido do cliente: todos os tipos valem, qualquer que seja a lista da organização.
	goal := contract.GoalFromHint(b.ObjectiveHint)
	if b.LeadForm != nil {
		goal = contract.GoalLeadForm
	}
	for _, o := range contract.AllObjectives {
		if !slices.Contains(b.Constraints.AllowedObjectives, o) {
			b.Constraints.AllowedObjectives = append(slices.Clone(b.Constraints.AllowedObjectives), o)
		}
	}
	leadForm, followers := goal == contract.GoalLeadForm, goal == contract.GoalFollowers
	b.Adjustment = p.Adjustment
	if n := e.copyVariations(ctx, conv.ID, p.Adjustment); n > 0 || b.CopyVariations == 0 {
		b.CopyVariations = n // pedido escrito na conversa vale acima da resposta "quantos anúncios"
	}
	if n := len(b.ClientCopies); n > 0 {
		b.CopyVariations = n // o cliente já fechou os textos: uma variação para cada
	}
	b.Business = campaignBusiness(ident.Profile, b.Offer)

	cfg := e.miaConfig(ctx)
	seg := cfg.SegmentFor(ident.Segment, ident.Niche)
	if !mainOffer(ident.Profile, b.Offer) {
		// Outro produto: o ramo da empresa não vale para esta campanha (consultoria anunciando imóvel).
		b.Constraints.Niche, seg = "", nil
	}
	prop, usage, err := e.Manager.ProposeWith(ctx, b, cfg.ManagerContext(seg))
	e.recordUsage(ctx, ident.OrgID, "manager", usage)
	if errors.Is(err, manager.ErrInvalidProposal) {
		e.audit(ctx, ident.OrgID, "manager", "proposal_invalid", map[string]any{"conversation_id": conv.ID, "error": err.Error()})
		return e.escalate(ctx, ident, conv, "proposta inválida após as tentativas", msgProposalFailed)
	}
	if err != nil {
		return err
	}

	// Texto que o cliente fechou com a Mia vale como veio (o Gestor só completa o que faltar).
	prop.Copies = contract.ApplyClientCopies(b.ClientCopies, prop.Copies)

	// Laboratório de copy: troca só o que tem versão segura mecânica; o resto vira aviso no resumo.
	copySegment := b.Constraints.Niche
	if seg != nil {
		copySegment += " " + seg.Name
	}
	lab := copylab.Apply(prop.Copies, copySegment)
	prop.Copies = lab.Copies

	// Onde o anúncio aparece, por quantos dias e com quais interesses é escolha do cliente, não do Gestor.
	prop.Placements = b.Placements
	switch {
	case b.DurationDays > 0:
		prop.DurationDays = b.DurationDays
	case b.NoEnd:
		prop.DurationDays = 0 // contínua: a Meta não recebe data de fim
	}
	switch {
	case len(b.Interests) > 0:
		prop.Audience.Interests = b.Interests
	case b.NoInterests:
		prop.Audience.Interests = nil
	}
	// Raio que o cliente escreveu no local vale acima do que o Gestor pôs (caso real: "Maravilha + 15km"
	// virou só a cidade). Com vários lugares, cada um leva o seu raio. Abaixo do mínimo da Meta fica o
	// mínimo, e o resumo explica.
	places := meta.ParsePlaces(b.Location)
	explicit := false
	for _, pl := range places {
		explicit = explicit || (!pl.Pin && pl.RadiusKM > 0)
	}
	switch {
	case len(places) == 1 && !places[0].Pin && places[0].RadiusKM > 0:
		prop.Audience.RadiusKM = min(max(places[0].RadiusKM, contract.MinRadiusKM), 80)
	case len(places) > 1 && explicit:
		prop.Audience.RadiusKM = 0 // cada lugar com o raio que o cliente deu; sem raio = só a cidade
	}
	for _, pl := range places {
		if !pl.Pin && pl.RadiusKM > 0 && pl.RadiusKM < contract.MinRadiusKM {
			prop.Rationale = strings.TrimSpace(fmt.Sprintf("A Meta só aceita raio a partir de %d km em volta de cidade, então ficou %d km (você pediu %d). Pra área menor, me manda um pino (localização) com o raio que quiser. %s",
				contract.MinRadiusKM, contract.MinRadiusKM, pl.RadiusKM, prop.Rationale))
			break
		}
	}
	if len(places) > 0 {
		prop.Location = meta.PlacesText(places, prop.Audience.RadiusKM)
	}
	if goal != "" {
		prop.Goal, prop.Objective = goal, contract.GoalObjective(goal)
	}
	if followers {
		// Anúncio que leva ao perfil do Instagram: só aparece no Instagram.
		prop.Followers = true
	}
	if leadForm {
		prop.Objective, prop.LeadForm = "OUTCOME_LEADS", true
		// O formulário é do cliente (perguntas, textos, tela final): o Gestor não mexe.
		form := contract.LeadForm{}.Normalize()
		if b.LeadForm != nil {
			form = *b.LeadForm
		}
		prop.Form = &form
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
		Send: sendJob(conv.ID, conv.ChatID, campaign.SummaryWith(prop, code, campaign.SummaryOptions{Segment: copySegment, Adjusted: lab.Adjusted})),
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

var copyCountRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}\d])(\d+|um|uma|dois|duas|tr[eê]s|quatro|cinco)\s+(?:varia[cç][õo]es|vers[õo]es|op[cç][õo]es|textos|copys?|headlines?|t[ií]tulos|modelos)(?:$|[^\p{L}])`)

var copyCountWords = map[string]int{"um": 1, "uma": 1, "dois": 2, "duas": 2, "tres": 3, "três": 3, "quatro": 4, "cinco": 5}

// parseCopyVariations lê quantas variações de texto o cliente pediu ("3 variações", "quero duas opções").
// 0 = não pediu. Acima do teto vira MaxCopies.
func parseCopyVariations(text string) int {
	m := copyCountRe.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		n = copyCountWords[strings.ToLower(m[1])]
	}
	if n < 1 {
		return 0
	}
	return min(n, contract.MaxCopies)
}

// copyVariations acha o pedido de variações no ajuste ou nas últimas mensagens do cliente (a mais nova vale).
func (e *Engine) copyVariations(ctx context.Context, convID int64, adjustment string) int {
	if n := parseCopyVariations(adjustment); n > 0 {
		return n
	}
	msgs, err := e.Store.RecentMessages(ctx, convID, 12)
	if err != nil {
		return 0
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Direction == "in" {
			if n := parseCopyVariations(msgs[i].Body); n > 0 {
				return n
			}
		}
	}
	return 0
}
