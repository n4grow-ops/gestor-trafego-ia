package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/shield"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// Escudo anti-bloqueio: pré-checagem da conta antes de publicar, resumo da Mia e o job de saúde da conta.

const (
	// JobShieldHealth confere a situação da conta de anúncios dos clientes conectados e avisa quando piora.
	JobShieldHealth = "shield.health"
	shieldEvery     = 4 * time.Hour
	// shieldCheckTimeout: prazo da checagem completa dentro de uma conversa (as leituras rodam juntas).
	shieldCheckTimeout = 25 * time.Second
	// shieldOpenSlack: depois da janela de silêncio, o job roda poucos minutos depois das 8h.
	shieldOpenSlack = 5 * time.Minute

	TplAccountAlert = "alerta_conta_anuncios"
)

func init() {
	TemplateDefinitions = append(TemplateDefinitions, TemplateDefinition{
		Name:    TplAccountAlert,
		Body:    "Aviso do meu anúncio: {{1}} Primeiro passo: {{2}} Responda esta mensagem se precisar de ajuda.",
		Example: []string{"a Meta desativou a sua conta de anúncios.", "Abra o Gerenciador de Anúncios e entre em Qualidade da Conta para ver o motivo."},
	})
}

// shieldGraph: as leituras do escudo, quando o Publisher as tem (o cliente da Meta tem; os fakes dos testes não).
func (e *Engine) shieldGraph() (shield.Graph, bool) {
	g, ok := e.Publisher.(shield.Graph)
	return g, ok && e.MetaTokenKey != nil
}

// EnsureShieldHealth garante o job periódico de saúde da conta (chamado quando o worker sobe).
func (e *Engine) EnsureShieldHealth(ctx context.Context) error {
	if _, ok := e.shieldGraph(); !ok {
		return nil
	}
	pending, err := e.Store.HasPendingKind(ctx, JobShieldHealth)
	if err != nil || pending {
		return err
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobShieldHealth, Payload: map[string]string{}, Delay: time.Minute})
}

// shieldCheck roda a checagem completa da conta conectada da organização e guarda o resultado para o painel.
// ok=false: sem conexão, sem leitura da Meta ou token que não abre; quem chama segue sem o escudo.
func (e *Engine) shieldCheck(ctx context.Context, orgID int64, dailyBRL float64) (shield.Report, bool) {
	g, ok := e.shieldGraph()
	if !ok {
		return shield.Report{}, false
	}
	conn, ok, err := e.Store.ActiveMeta(ctx, orgID)
	if err != nil || !ok {
		return shield.Report{}, false
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, orgID)
	if err != nil {
		return shield.Report{}, false
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		return shield.Report{}, false
	}
	now := e.now()
	in := shield.Input{Token: string(token), AdAccountID: conn.AdAccountID, PageID: conn.PageID, InstagramID: conn.InstagramID,
		NeedsInstagram: true, Currency: conn.Currency, DailyBudgetBRL: dailyBRL, Now: now}
	if list, err := e.Store.RecentRejected(ctx, orgID, now.Add(-shield.RecentRejection)); err != nil {
		e.Log.Warn("reprovações recentes não lidas", "org_id", orgID, "erro", err)
	} else {
		for _, r := range list {
			in.Rejected = append(in.Rejected, shield.Rejected{At: r.At, Reason: r.Reason})
		}
	}
	cctx, cancel := context.WithTimeout(ctx, shieldCheckTimeout)
	defer cancel()
	rep := shield.Check(cctx, g, in)
	e.saveShieldReport(ctx, conn.ID, orgID, rep, now)
	return rep, true
}

// saveShieldReport guarda o relatório para o painel, sem mexer no estado avisado (falha só vai pro log).
func (e *Engine) saveShieldReport(ctx context.Context, connID, orgID int64, rep shield.Report, at time.Time) {
	prev, found, err := e.Store.ShieldStateFor(ctx, connID)
	if err != nil {
		e.Log.Warn("estado do escudo não lido", "org_id", orgID, "erro", err)
		return
	}
	state, status, reason := string(shield.StateOK), 0, 0
	if found {
		state, status, reason = prev.State, prev.AccountStatus, prev.DisableReason
	}
	if err := e.Store.SaveShieldState(ctx, connID, orgID, state, status, reason, rep, at); err != nil {
		e.Log.Warn("relatório do escudo não guardado", "org_id", orgID, "erro", err)
	}
}

// shieldApprovalNote é o resumo curto da Mia ao aprovar a proposta: "tudo certo" ou os pontos a resolver.
// Vazio quando a conta ainda não está conectada ou a Meta não respondeu.
func (e *Engine) shieldApprovalNote(ctx context.Context, orgID int64, dailyBRL float64) string {
	rep, ok := e.shieldCheck(ctx, orgID, dailyBRL)
	if !ok {
		return ""
	}
	e.audit(ctx, orgID, "system", "shield_checked", map[string]any{"when": "approval", "level": rep.Worst()})
	return rep.Summary()
}

// shieldPublishNotes: os pontos de atenção para a mensagem de publicação. Pagamento e limite ficam de fora
// (o texto de saldo da publicação já cobre), moeda também (a publicação recusa sozinha) e a verificação do
// Business (não impede publicar; fica só no painel).
func shieldPublishNotes(rep shield.Report) string {
	only := rep.Without(shield.KeyPayment, shield.KeyCap, shield.KeyCurrency, shield.KeyBusiness)
	if len(only.Problems()) == 0 {
		return ""
	}
	return only.Summary()
}

// shieldBlocked: a Meta recusaria a publicação por isso. Avisa o cliente com o passo a passo e a equipe;
// não publica nem tenta de novo sozinho (o cliente resolve e manda "tentar de novo").
func (e *Engine) shieldBlocked(ctx context.Context, ident store.Identity, conv store.Conversation, rep shield.Report, draftID int64) error {
	bl := rep.Blockers()
	var lines []string
	for _, it := range bl {
		lines = append(lines, fmt.Sprintf("*%s*: %s", it.Title, it.Message))
	}
	var b strings.Builder
	b.WriteString("⚠️ Antes de publicar eu conferi a sua conta na Meta e tem " + pointsWord(len(bl)) + " que a Meta recusaria:")
	for _, it := range bl {
		fmt.Fprintf(&b, "\n\n• *%s*: %s", it.Title, it.Message)
		for i, s := range it.Steps {
			fmt.Fprintf(&b, "\n   %d. %s", i+1, s)
		}
	}
	b.WriteString("\n\nQuando resolver, me responde *tentar de novo* que eu publico.")
	e.audit(ctx, ident.OrgID, "system", "shield_publish_blocked", map[string]any{"draft_id": draftID, "items": keysOf(bl)})
	e.alertTeam(ctx, ident, "publicação parada pelo escudo: "+strings.Join(lines, " | "))
	return e.send(ctx, conv, b.String())
}

func pointsWord(n int) string {
	switch n {
	case 1:
		return "um ponto"
	case 2:
		return "dois pontos"
	}
	return fmt.Sprintf("%d pontos", n)
}

func keysOf(items []shield.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Key
	}
	return out
}

// ---------- job de saúde da conta ----------

// shieldHealth confere cada conexão ativa e avisa o cliente uma vez por mudança para pior, nunca das 21h às
// 8h de Brasília. O estado fica na tabela shield_account_state: o mesmo estado ruim não avisa de novo.
func (e *Engine) shieldHealth(ctx context.Context) error {
	next := shieldEvery
	g, ok := e.shieldGraph()
	if ok {
		targets, err := e.Store.ShieldTargets(ctx)
		if err != nil {
			return err
		}
		deferred := false
		for _, t := range targets {
			d, err := e.shieldHealthOne(ctx, g, t)
			if err != nil {
				e.Log.Warn("saúde da conta não conferida", "org_id", t.OrgID, "erro", err)
			}
			deferred = deferred || d
		}
		if deferred {
			// Aviso adiado pela janela de silêncio: a próxima rodada é logo depois das 8h, não só daqui a 4 h.
			next = shield.NextOpen(e.now()).Add(shieldOpenSlack).Sub(e.now())
		}
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobShieldHealth, Payload: map[string]string{}, Delay: next})
}

// shieldHealthOne lê a conta e aplica a decisão. deferred=true: há aviso devido, adiado pelo silêncio.
func (e *Engine) shieldHealthOne(ctx context.Context, g shield.Graph, t store.ShieldTarget) (deferred bool, err error) {
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, t.OrgID)
	if err != nil {
		return false, nil // sem conexão: o aviso de conexão cuida disso
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		return false, nil
	}
	h, err := g.AccountHealth(ctx, string(token), t.AdAccountID)
	if meta.IsAuthError(err) {
		return false, nil // token vencido: o aviso de conexão vencendo cuida disso
	}
	if err != nil {
		return false, err
	}
	state := shield.Classify(h)
	now := e.now()
	prev, found, err := e.Store.ShieldStateFor(ctx, t.ConnectionID)
	if err != nil {
		return false, err
	}
	notified := shield.StateOK
	if found {
		notified = shield.State(prev.NotifiedState)
	}
	if err := e.Store.SaveShieldState(ctx, t.ConnectionID, t.OrgID, string(state), h.Status, h.DisableReason, nil, now); err != nil {
		return false, err
	}
	switch shield.Decide(state, notified, now) {
	case shield.DecideReset:
		e.audit(ctx, t.OrgID, "system", "shield_account_ok", map[string]any{"account": t.AdAccountID})
		return false, e.Store.MarkShieldNotified(ctx, t.ConnectionID, string(shield.StateOK), now)
	case shield.DecideDefer:
		return true, nil
	case shield.DecideNotify:
		if err := e.notifyShield(ctx, t, state, h); err != nil {
			return false, err
		}
		return false, e.Store.MarkShieldNotified(ctx, t.ConnectionID, string(state), now)
	}
	return false, nil
}

// notifyShield avisa o cliente (com o passo a passo) e a equipe. O envio vai pelo outbox; fora da janela de
// 24 h do WhatsApp vai o modelo aprovado, com o primeiro passo.
func (e *Engine) notifyShield(ctx context.Context, t store.ShieldTarget, state shield.State, h meta.AccountHealth) error {
	conv, err := e.Store.OwnerConversation(ctx, t.OrgID)
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	name := h.Name
	if name == "" {
		name = t.AdAccountName
	}
	al := shield.AlertFor(state, h.DisableReason, name)
	g := shield.GuideFor(al.Scenario)
	step := ""
	if len(g.Steps) > 0 {
		step = g.Steps[0]
	}
	tpl := &whatsapp.Template{Name: TplAccountAlert, Params: []string{templateParam(shield.StateMessage(state, 0)), templateParam(step)}}
	if conv.ChatID != "" && ident.Role == "owner" {
		if err := e.Store.Enqueue(ctx, systemSendJob(conv.ID, conv.ChatID, al.Message, tpl)); err != nil {
			return err
		}
	}
	e.audit(ctx, t.OrgID, "system", "shield_account_alert", map[string]any{"account": t.AdAccountID, "state": state,
		"account_status": h.Status, "disable_reason": h.DisableReason, "scenario": al.Scenario})
	e.alertTeam(ctx, ident, fmt.Sprintf("a conta de anúncios %s mudou de situação: %s", t.AdAccountID, shield.StateMessage(state, h.DisableReason)))
	return nil
}

// ---------- reprovação de anúncio ----------

// explainRejection traduz o motivo da reprovação para o cliente e, com o ajuste de copy ligado, sugere o texto.
func (e *Engine) explainRejection(ctx context.Context, ident store.Identity, draftID int64, reasons []string) string {
	var headline, text string
	if e.CopyFix != nil {
		if data, err := e.pubs().PublishData(ctx, draftID); err == nil && len(data.FinalCopies) > 0 {
			var copies []meta.PublishCopy
			if json.Unmarshal(data.FinalCopies, &copies) == nil && len(copies) > 0 {
				headline, text = copies[0].Headline, copies[0].Text
			}
		}
	}
	return shield.ExplainWithCopy(ctx, reasons, e.CopyFix, headline, text, ident.Segment).Text()
}
