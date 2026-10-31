package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/asaas"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// JobBillingNotify avisa o dono pelo WhatsApp depois de um evento do Asaas (pagou, venceu).
const JobBillingNotify = "billing.notify"

// pastDueGrace: depois disto em atraso, a Mia não cria campanha nova (as que rodam seguem).
const pastDueGrace = 72 * time.Hour

// BillingProvider é o Asaas (asaas.Client); nos testes, um fake. nil no Engine = cobrança desligada.
type BillingProvider interface {
	FindCustomer(ctx context.Context, externalRef string) (string, error)
	CreateCustomer(ctx context.Context, c asaas.Customer) (string, error)
	CreateSubscription(ctx context.Context, s asaas.Subscription) (string, error)
	FirstPaymentURL(ctx context.Context, subID string) (string, error)
	DeleteSubscription(ctx context.Context, subID string) error
}

// BillingNotifyJob é o job do aviso ao dono (o webhook enfileira na transação do evento).
func BillingNotifyJob(n store.BillingNotice) store.JobSpec {
	return store.JobSpec{Kind: JobBillingNotify, Payload: n, MaxAttempts: sendAttempts}
}

// planGate segura quem não tem plano liberado: a mensagem fica gravada sem ir para a IA e o cliente
// escolhe o plano, a forma (Pix ou cartão) e recebe o link. handled=false = liberado, segue para a Mia.
func (e *Engine) planGate(ctx context.Context, ident store.Identity, conv store.Conversation, in whatsapp.Inbound, text string) (bool, error) {
	bs, err := e.Store.BillingState(ctx, ident.OrgID)
	if err != nil {
		return false, err
	}
	if bs.Released(e.now()) {
		return false, nil
	}
	body := text
	if body == "" {
		body = "[" + string(in.Kind) + "]"
	}
	if err := e.Store.AddMessage(ctx, conv.ID, "in", store.KindSkipped, body); err != nil {
		return true, err
	}
	plans, err := e.billingPlans(ctx)
	if err != nil {
		return true, err
	}
	if p, ok := matchPlan(text, plans); ok {
		if err := e.Store.ChooseBillingPlan(ctx, ident.OrgID, p.Code); err != nil {
			return true, err
		}
		e.audit(ctx, ident.OrgID, member(ident), "billing_plan_chosen", map[string]any{"plan": p.Code})
		return true, e.send(ctx, conv, fmt.Sprintf(msgPlanChosen, p.Name, planPrice(p.PriceCents)))
	}
	if bs.PendingPlan != "" {
		if method := matchMethod(text); method != "" {
			for _, p := range plans {
				if p.Code == bs.PendingPlan {
					return true, e.checkout(ctx, ident, conv, bs, p, method)
				}
			}
		}
	}
	if bs.CheckoutURL != "" {
		return true, e.send(ctx, conv, fmt.Sprintf(msgPlanStillOpen, bs.CheckoutURL, planNames(plans)))
	}
	// Assinatura criada sem link (o Asaas ainda não tinha gerado a cobrança): tenta de novo.
	if bs.Status == "pending" && bs.Provider == "asaas" && bs.ProviderRef != "" && bs.PendingMethod != "" {
		link, err := e.Billing.FirstPaymentURL(ctx, bs.ProviderRef)
		if err == nil {
			if err := e.Store.SetCheckoutURL(ctx, ident.OrgID, link); err != nil {
				return true, err
			}
			return true, e.send(ctx, conv, fmt.Sprintf(msgPlanStillOpen, link, planNames(plans)))
		}
		e.Log.Warn("link da cobrança ainda indisponível no Asaas", "org_id", ident.OrgID, "erro", err)
	}
	return true, e.send(ctx, conv, planList(msgPlanNone, plans))
}

// checkout cria (ou reaproveita) o cliente no Asaas, troca a assinatura pendente antiga e manda o link.
func (e *Engine) checkout(ctx context.Context, ident store.Identity, conv store.Conversation, bs store.BillingState, p store.Plan, method string) error {
	link, err := e.createCheckout(ctx, ident.OrgID, bs, p, method)
	if err != nil {
		e.Log.Error("falha no Asaas ao gerar a cobrança", "org_id", ident.OrgID, "plano", p.Code, "erro", err)
		e.audit(ctx, ident.OrgID, "system", "billing_checkout_failed", map[string]any{"plan": p.Code, "method": method, "error": err.Error()})
		reason := fmt.Sprintf(msgTeamBilling, p.Name, methodLabel(method), err.Error())
		if err := e.enqueueAll(ctx, e.teamJobs(ctx, TeamNotice{Kind: TeamAlert, Ident: ident, Reason: reason, Text: "💳 " + ident.OrgName + ": " + reason})...); err != nil {
			e.Log.Error("falha ao avisar a equipe", "org_id", ident.OrgID, "erro", err)
		}
		return e.send(ctx, conv, msgPlanLinkSoon)
	}
	e.audit(ctx, ident.OrgID, member(ident), "billing_checkout", map[string]any{"plan": p.Code, "method": method})
	return e.send(ctx, conv, fmt.Sprintf(msgPlanCheckout, p.Name, methodLabel(method), link))
}

func (e *Engine) createCheckout(ctx context.Context, orgID int64, bs store.BillingState, p store.Plan, method string) (string, error) {
	ref := fmt.Sprintf("org-%d", orgID)
	customer := bs.CustomerID
	if customer == "" {
		id, err := e.Billing.FindCustomer(ctx, ref)
		if err != nil {
			return "", err
		}
		if id == "" {
			c, err := e.Store.BillingCustomer(ctx, orgID)
			if err != nil {
				return "", err
			}
			if id, err = e.Billing.CreateCustomer(ctx, asaas.Customer{Name: c.Name, CpfCnpj: c.CpfCnpj, Email: c.Email,
				MobilePhone: localPhone(c.Phone), ExternalReference: ref}); err != nil {
				return "", err
			}
		}
		if err := e.Store.SetAsaasCustomer(ctx, orgID, id); err != nil {
			return "", err
		}
		customer = id
	}
	// Troca de plano ou de forma antes de pagar: a assinatura pendente antiga sai do Asaas.
	if bs.Status == "pending" && bs.Provider == "asaas" && bs.ProviderRef != "" {
		if err := e.Billing.DeleteSubscription(ctx, bs.ProviderRef); err != nil {
			return "", err
		}
	}
	billingType := asaas.BillingPix
	if method == "card" {
		billingType = asaas.BillingCard
	}
	subID, err := e.Billing.CreateSubscription(ctx, asaas.Subscription{Customer: customer, BillingType: billingType,
		Value: float64(p.PriceCents) / 100, NextDueDate: e.now().In(brt).Format("2006-01-02"), Cycle: "MONTHLY",
		Description: "meuanuncio.io - " + p.Name, ExternalReference: ref})
	if err != nil {
		return "", err
	}
	link, linkErr := e.Billing.FirstPaymentURL(ctx, subID)
	// A assinatura já existe no Asaas: grava mesmo sem link, para não criar outra na próxima mensagem.
	if err := e.Store.SetBillingCheckout(ctx, orgID, method, subID, link); err != nil {
		return "", err
	}
	return link, linkErr
}

// pastDueBlocked: com cobrança ligada, atraso há mais de 3 dias barra campanha nova. Devolve o link.
func (e *Engine) pastDueBlocked(ctx context.Context, orgID int64) (bool, string, error) {
	if e.Billing == nil {
		return false, "", nil
	}
	bs, err := e.Store.BillingState(ctx, orgID)
	if err != nil {
		return false, "", err
	}
	if bs.Unlimited || bs.Status != "past_due" || bs.PastDueSince == nil {
		return false, "", nil
	}
	return e.now().Sub(*bs.PastDueSince) > pastDueGrace, bs.CheckoutURL, nil
}

// billingNotify avisa o dono do pagamento confirmado ou da mensalidade vencida.
func (e *Engine) billingNotify(ctx context.Context, payload []byte) error {
	var n store.BillingNotice
	if err := json.Unmarshal(payload, &n); err != nil {
		return fmt.Errorf("engine: payload do aviso de cobrança inválido: %w", err)
	}
	conv, err := e.Store.OwnerConversation(ctx, n.OrgID)
	if errors.Is(err, store.ErrNotFound) {
		e.Log.Warn("aviso de cobrança sem conversa do dono", "org_id", n.OrgID, "tipo", n.Kind)
		return nil
	}
	if err != nil {
		return err
	}
	var text string
	switch n.Kind {
	case store.BillingNoticePaid:
		text = fmt.Sprintf(msgPlanPaid, n.PlanName)
	case store.BillingNoticeOverdue:
		text = msgPlanOverdueNL
		if n.InvoiceURL != "" {
			text = fmt.Sprintf(msgPlanOverdue, n.InvoiceURL)
		}
	default:
		return nil
	}
	return e.send(ctx, conv, text)
}

// billingPlans são os planos visíveis e disponíveis, na ordem do painel.
func (e *Engine) billingPlans(ctx context.Context) ([]store.Plan, error) {
	all, err := e.Store.Plans(ctx)
	if err != nil {
		return nil, err
	}
	return onlyAvailable(all), nil
}

// planList monta a lista numerada: nome, preço por mês, campanhas por mês e projetos.
func planList(intro string, plans []store.Plan) string {
	var b strings.Builder
	b.WriteString(intro)
	b.WriteString("\n")
	for i, p := range plans {
		camp := fmt.Sprintf("%d campanhas por mês", p.MaxCampaignsMonth)
		if p.MaxCampaignsMonth == 0 {
			camp = "campanhas ilimitadas"
		}
		proj := fmt.Sprintf("até %d projetos", p.MaxProjects)
		if p.MaxProjects == 1 {
			proj = "1 projeto"
		}
		fmt.Fprintf(&b, "\n%d. *%s*: %s/mês, %s, %s", i+1, p.Name, planPrice(p.PriceCents), camp, proj)
	}
	b.WriteString("\n\n")
	b.WriteString(msgPlanReply)
	return b.String()
}

// WelcomeWithPlansFor são as boas-vindas com a lista de planos no balão seguinte (cobrança ligada). Sem
// o texto padrão: ele fala em plano Free e já faz a primeira pergunta, que o portão do plano barraria.
// A pergunta do negócio vem no aviso de pagamento confirmado.
func WelcomeWithPlansFor(_ mia.Config, plans []store.Plan) func(convID int64, chatID, fullName, email string) store.JobSpec {
	list := planList(msgPlanPick, onlyAvailable(plans))
	return func(convID int64, chatID, fullName, email string) store.JobSpec {
		name := fullName
		if f := strings.Fields(fullName); len(f) > 0 {
			name = f[0]
		}
		j := wholeJob(store.Conversation{ID: convID, ChatID: chatID}, fmt.Sprintf(msgWelcomePlans, name, email))
		if s, ok := j.Payload.(SendJob); ok {
			s.Next = append(s.Next, list)
			j.Payload = s
		}
		return j
	}
}

func onlyAvailable(plans []store.Plan) []store.Plan {
	out := plans[:0:0]
	for _, p := range plans {
		if p.Available {
			out = append(out, p)
		}
	}
	return out
}

func planNames(plans []store.Plan) string {
	names := make([]string, len(plans))
	for i, p := range plans {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}

// planPrice: R$ 147 (sem centavos quando redondo).
func planPrice(cents int) string {
	if cents%100 == 0 {
		return fmt.Sprintf("R$ %d", cents/100)
	}
	return brlCents(int64(cents))
}

func methodLabel(m string) string {
	if m == "card" {
		return "cartão"
	}
	return "Pix"
}

var (
	planNumberRe = regexp.MustCompile(`^(?:(?:quero|escolho|vou de|prefiro)\s+)?(?:(?:o|a)\s+)?(?:(?:plano|opcao|numero|n)\s*)?(\d{1,2})\s*[.!)]?$`)
	pixRe        = regexp.MustCompile(`\bpix\b`)
	cardRe       = regexp.MustCompile(`\b(cartao|credito|cartao de credito)\b`)
)

// matchPlan acha o plano pela posição na lista (1, 2, 3), pelo nome ou pelo código.
func matchPlan(text string, plans []store.Plan) (store.Plan, bool) {
	t := normalizeName(text)
	if t == "" {
		return store.Plan{}, false
	}
	if m := planNumberRe.FindStringSubmatch(t); m != nil {
		var n int
		fmt.Sscan(m[1], &n)
		if n >= 1 && n <= len(plans) {
			return plans[n-1], true
		}
		return store.Plan{}, false
	}
	// Código só sozinho: "pro" solto numa frase é "para o" (pro meu negócio), não o plano.
	for _, p := range plans {
		if t == strings.ToLower(p.Code) {
			return p, true
		}
	}
	for _, p := range plans {
		if w := normalizeName(p.Name); w != "" && regexp.MustCompile(`\b`+regexp.QuoteMeta(w)+`\b`).MatchString(t) {
			return p, true
		}
	}
	return store.Plan{}, false
}

// matchMethod: "pix" ou "card" (cartão, crédito); "" quando a mensagem não escolhe.
func matchMethod(text string) string {
	t := normalizeName(text)
	pix, card := pixRe.MatchString(t), cardRe.MatchString(t)
	switch {
	case pix && !card:
		return "pix"
	case card && !pix:
		return "card"
	}
	return ""
}

// localPhone tira o 55 do E.164 (o Asaas quer DDD + número).
func localPhone(e164 string) string {
	p := strings.TrimPrefix(e164, "+")
	if strings.HasPrefix(p, "55") && len(p) >= 12 {
		return p[2:]
	}
	return p
}
