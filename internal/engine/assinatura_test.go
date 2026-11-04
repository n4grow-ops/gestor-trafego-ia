package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/asaas"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fakeBilling é o Asaas de mentira: guarda as chamadas e devolve ids e links previsíveis.
type fakeBilling struct {
	mu        sync.Mutex
	customers []asaas.Customer
	subs      []asaas.Subscription
	deleted   []string
	docs      []string // CPF/CNPJ gravados em cliente que já existia
	found     string   // cliente já existente no Asaas
	failSub   error
}

func (f *fakeBilling) FindCustomer(_ context.Context, _ string) (string, error) { return f.found, nil }
func (f *fakeBilling) CreateCustomer(_ context.Context, c asaas.Customer) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.customers = append(f.customers, c)
	return fmt.Sprintf("cus_%d", len(f.customers)), nil
}
func (f *fakeBilling) SetCustomerDocument(_ context.Context, id, doc string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs = append(f.docs, id+"="+doc)
	return nil
}
func (f *fakeBilling) CreateSubscription(_ context.Context, s asaas.Subscription) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSub != nil {
		return "", f.failSub
	}
	f.subs = append(f.subs, s)
	return fmt.Sprintf("sub_%d", len(f.subs)), nil
}
func (f *fakeBilling) FirstPaymentURL(_ context.Context, subID string) (string, error) {
	return "https://sandbox.asaas.com/i/" + subID, nil
}
func (f *fakeBilling) DeleteSubscription(_ context.Context, subID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, subID)
	return nil
}

func newBillingHarness(t *testing.T) (*harness, *fakeBilling) {
	h := newHarness(t, "owner")
	fb := &fakeBilling{}
	h.eng.Billing = fb
	return h, fb
}

func (h *harness) subStatus() string {
	h.t.Helper()
	var s string
	h.must(h.pool.QueryRow(ctx, `SELECT status FROM subscriptions LIMIT 1`).Scan(&s))
	return s
}

func TestBillingSignupCreatesPendingAndSendsPlans(t *testing.T) {
	h, wa := newFlowHarness(t)
	h.eng.Billing = &fakeBilling{}
	h.say("oi")
	h.sayFlow(answer(wa.lastFlow(t).Token, "11.222.333/0001-81"))
	if s := h.registrationStatus(); s != "complete" {
		t.Fatalf("cadastro = %s", s)
	}
	if s := h.subStatus(); s != "pending" {
		t.Fatalf("assinatura = %s, esperava pending", s)
	}
	var payload string
	h.must(h.pool.QueryRow(ctx, `SELECT payload::text FROM outbox_jobs WHERE kind = 'whatsapp.send' ORDER BY id LIMIT 1`).Scan(&payload))
	var job engine.SendJob
	h.must(json.Unmarshal([]byte(payload), &job))
	if !strings.Contains(job.Text, "Cadastro concluído") || len(job.Next) == 0 {
		t.Fatalf("boas-vindas = %+v", job)
	}
	list := job.Next[len(job.Next)-1]
	for _, want := range []string{"1. *Básico*: R$ 147/mês, 4 campanhas por mês", "2. *Intermediário*: R$ 397/mês, 35 campanhas por mês",
		"3. *Avançado*: R$ 997/mês, campanhas ilimitadas", "projetos", "Responda com o número"} {
		if !strings.Contains(list, want) {
			t.Errorf("lista sem %q:\n%s", want, list)
		}
	}
}

func TestPlanGateChoosePlanMethodAndLink(t *testing.T) {
	h, fb := newBillingHarness(t)
	h.must(h.st.SetRegistrationStatus(ctx, mustOrg(h), "complete"))
	h.att.Responses = []string{turnOffer}

	h.say("oi, quero anunciar")
	if got := h.wa.last().Text; !strings.Contains(got, "Você ainda não tem um plano ativo") || !strings.Contains(got, "1. *Básico*") {
		t.Fatalf("lista = %q", got)
	}
	if h.att.Calls() != 0 {
		t.Fatal("sem plano não chama a IA")
	}

	h.say("2")
	if got := h.wa.last().Text; !strings.Contains(got, "*Intermediário*") || !strings.Contains(got, "*Pix* ou *cartão*") {
		t.Fatalf("escolha = %q", got)
	}

	h.say("pix")
	if got := h.wa.last().Text; !strings.Contains(got, "*CPF*") || len(fb.subs) != 0 || len(fb.customers) != 0 {
		t.Fatalf("sem documento: %q, assinaturas %d", got, len(fb.subs))
	}
	h.say("529.982.247-24")
	if got := h.wa.last().Text; !strings.Contains(got, "não parece um CPF") || len(fb.subs) != 0 {
		t.Fatalf("documento errado: %q", got)
	}
	h.say("meu cpf é 529.982.247-25")
	if len(fb.customers) != 1 || fb.customers[0].CpfCnpj != "52998224725" || fb.customers[0].ExternalReference != fmt.Sprintf("org-%d", mustOrg(h)) {
		t.Fatalf("clientes = %+v", fb.customers)
	}
	if fb.customers[0].MobilePhone != strings.TrimPrefix(phone, "55") {
		t.Errorf("telefone = %q", fb.customers[0].MobilePhone)
	}
	if len(fb.subs) != 1 || fb.subs[0].BillingType != asaas.BillingPix || fb.subs[0].Value != 397 || fb.subs[0].Cycle != "MONTHLY" ||
		fb.subs[0].NextDueDate != "2026-09-22" || fb.subs[0].Description != "meuanuncio.io - Intermediário" {
		t.Fatalf("assinaturas = %+v", fb.subs)
	}
	if got := h.wa.last().Text; !strings.Contains(got, "https://sandbox.asaas.com/i/sub_1") || !strings.Contains(got, "por Pix") {
		t.Fatalf("link = %q", got)
	}
	if s := h.subStatus(); s != "pending" {
		t.Fatalf("antes de pagar = %s", s)
	}

	// Outra mensagem com o link aberto: reenvia o link.
	h.say("e agora?")
	if got := h.wa.last().Text; !strings.Contains(got, "continua aqui") || !strings.Contains(got, "sub_1") {
		t.Fatalf("reenvio = %q", got)
	}

	// Troca a forma: a assinatura pendente antiga sai do Asaas e o cliente é reaproveitado.
	h.say("prefiro cartão")
	if len(fb.deleted) != 1 || fb.deleted[0] != "sub_1" || len(fb.subs) != 2 || fb.subs[1].BillingType != asaas.BillingCard || len(fb.customers) != 1 {
		t.Fatalf("troca: apagadas %v, assinaturas %+v, clientes %d", fb.deleted, fb.subs, len(fb.customers))
	}
	if h.att.Calls() != 0 {
		t.Fatal("ainda sem pagamento: nada de IA")
	}

	// Pagamento confirmado (webhook): a Mia atende normal.
	_, err := h.pool.Exec(ctx, `UPDATE subscriptions SET status = 'active'`)
	h.must(err)
	h.say("quero anunciar pizza")
	if h.att.Calls() != 1 || h.wa.last().Text != "Legal! Para quem e em qual cidade?" {
		t.Fatalf("depois de pagar: chamadas %d, resposta %q", h.att.Calls(), h.wa.last().Text)
	}
}

func mustOrg(h *harness) int64 {
	h.t.Helper()
	ident, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	return ident.OrgID
}

func TestPlanGateAsaasErrorWarnsTeam(t *testing.T) {
	h, fb := newBillingHarness(t)
	fb.failSub = errors.New("asaas: POST /subscriptions: status 400: invalid_cpfCnpj")
	_, err := h.pool.Exec(ctx, `UPDATE members SET cpf = '52998224725'`)
	h.must(err)
	h.say("3")
	h.say("pix")
	if got := h.wa.last().Text; !strings.Contains(got, "sai em instantes") || strings.Contains(got, "cpfCnpj") {
		t.Fatalf("cliente = %q", got)
	}
	var n int
	h.must(h.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = 'whatsapp.send' AND payload->>'text' LIKE '%Asaas%'`).Scan(&n))
	if n != 1 {
		t.Fatalf("aviso à equipe = %d", n)
	}
}

func TestPlanGateSkipsNeedsHumanAndLegacy(t *testing.T) {
	// Assinatura active antiga (provider nulo): liberada.
	h, _ := newBillingHarness(t)
	_, err := h.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'free', 'active')`, mustOrg(h))
	h.must(err)
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	if h.att.Calls() != 1 {
		t.Fatal("legado ativo deveria falar com a Mia")
	}

	// Atendimento humano: nada de portão.
	h2, _ := newBillingHarness(t)
	c := h2.conv()
	h2.must(h2.st.UpdateConversation(ctx, c.ID, store.StateNeedsHuman, c.Fields))
	h2.say("oi")
	if h2.wa.count() != 0 {
		t.Fatalf("em atendimento humano o bot não responde: %q", h2.wa.last().Text)
	}

	// Organização liberada (unlimited) sem assinatura: liberada.
	h3, _ := newBillingHarness(t)
	_, err = h3.pool.Exec(ctx, `UPDATE organizations SET unlimited = true`)
	h3.must(err)
	h3.att.Responses = []string{turnOffer}
	h3.say("quero anunciar pizza")
	if h3.att.Calls() != 1 {
		t.Fatal("org ilimitada deveria falar com a Mia")
	}
}

func TestBillingOffKeepsOldBehavior(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	if h.att.Calls() != 1 {
		t.Fatal("sem cobrança, sem portão")
	}
	h2, wa := newFlowHarness(t)
	h2.say("oi")
	h2.sayFlow(answer(wa.lastFlow(t).Token, "11.222.333/0001-81"))
	if s := h2.subStatus(); s != "active" {
		t.Fatalf("sem cobrança o cadastro ativa o plano: %s", s)
	}
}

func TestPastDueBlocksNewCampaignAfterThreeDays(t *testing.T) {
	for _, tc := range []struct {
		name    string
		since   time.Duration
		blocked bool
	}{{"4 dias", 96 * time.Hour, true}, {"2 dias", 48 * time.Hour, false}} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newBillingHarness(t)
			_, err := h.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, provider, provider_ref, past_due_since, checkout_url)
				VALUES ($1, 'free', 'past_due', 'asaas', 'sub_1', $2, 'https://asaas/i/venc')`, mustOrg(h), h.now.Add(-tc.since))
			h.must(err)
			h.att.Responses = []string{turnOffer, turnRest}
			h.say("quero anunciar pizza")
			h.say("famílias em Campinas, 50 por dia")
			got := h.pendingJobs(engine.JobPropose)
			if tc.blocked {
				if got != 0 || !strings.Contains(h.wa.last().Text, "atraso") || !strings.Contains(h.wa.last().Text, "https://asaas/i/venc") {
					t.Fatalf("propostas = %d, resposta %q", got, h.wa.last().Text)
				}
				if c := h.conv(); c.State != store.StateCollecting {
					t.Fatalf("estado = %s", c.State)
				}
				return
			}
			if got != 1 {
				t.Fatalf("antes de 3 dias a proposta sai: %d", got)
			}
		})
	}
}

func TestBillingNotifyJobSendsToOwner(t *testing.T) {
	h, _ := newBillingHarness(t)
	h.conv()
	payload, _ := json.Marshal(store.BillingNotice{OrgID: mustOrg(h), Kind: store.BillingNoticePaid, PlanName: "Intermediário"})
	h.must(h.eng.HandleJob(ctx, store.Job{Kind: engine.JobBillingNotify, Payload: payload}))
	if got := h.wa.last().Text; !strings.Contains(got, "Pagamento confirmado") || !strings.Contains(got, "Intermediário") {
		t.Fatalf("aviso = %q", got)
	}
	payload, _ = json.Marshal(store.BillingNotice{OrgID: mustOrg(h), Kind: store.BillingNoticeOverdue, InvoiceURL: "https://asaas/i/x"})
	h.must(h.eng.HandleJob(ctx, store.Job{Kind: engine.JobBillingNotify, Payload: payload}))
	if got := h.wa.last().Text; !strings.Contains(got, "venceu") || !strings.Contains(got, "https://asaas/i/x") {
		t.Fatalf("vencido = %q", got)
	}
}

// Troca de forma com falha ao criar a nova: a assinatura antiga e o link continuam valendo.
func TestPlanGateMethodSwitchFailureKeepsOldCheckout(t *testing.T) {
	h, fb := newBillingHarness(t)
	_, err := h.pool.Exec(ctx, `UPDATE members SET cpf = '52998224725'`)
	h.must(err)
	h.say("1")
	h.say("cartão")
	if len(fb.subs) != 1 || len(fb.docs) != 0 {
		t.Fatalf("assinaturas %d, documentos %v", len(fb.subs), fb.docs)
	}
	fb.failSub = errors.New("asaas: POST /subscriptions: status 400")
	h.say("quero pix")
	if len(fb.deleted) != 0 {
		t.Fatalf("apagou a antiga antes de criar a nova: %v", fb.deleted)
	}
	h.say("e agora?")
	if got := h.wa.last().Text; !strings.Contains(got, "sub_1") {
		t.Fatalf("link antigo sumiu: %q", got)
	}
	// Cliente que já existia no Asaas ganha o documento antes de cada nova cobrança.
	fb.failSub = nil
	h.say("pix")
	if len(fb.docs) != 2 || fb.docs[1] != "cus_1=52998224725" || len(fb.deleted) != 1 || fb.deleted[0] != "sub_1" {
		t.Fatalf("documentos %v, apagadas %v", fb.docs, fb.deleted)
	}
}

func TestBillingNotifyDueSendsPixLink(t *testing.T) {
	h, _ := newBillingHarness(t)
	h.say("oi")
	payload, err := json.Marshal(store.BillingNotice{OrgID: mustOrg(h), Kind: store.BillingNoticeDue, PlanName: "Básico",
		InvoiceURL: "https://asaas/i/2", DueDate: "2026-11-30"})
	h.must(err)
	h.must(h.eng.HandleJob(ctx, store.Job{Kind: engine.JobBillingNotify, Payload: payload}))
	if got := h.wa.last().Text; !strings.Contains(got, "*Básico*") || !strings.Contains(got, "30/11") || !strings.Contains(got, "https://asaas/i/2") {
		t.Fatalf("aviso = %q", got)
	}
}

func TestPlanGateSecretPlanOnlyWithExactCode(t *testing.T) {
	h, fb := newBillingHarness(t)
	_, err := h.pool.Exec(ctx, `INSERT INTO plans (code, name, price_cents, currency, billing_interval, daily_message_limit,
		max_daily_budget_brl, max_projects, max_campaigns_month, available, sort_order, visible)
		VALUES ('segredo9', 'Teste', 500, 'BRL', 'month', 60, 250, 3, 4, true, 99, false)`)
	h.must(err)
	_, err = h.pool.Exec(ctx, `UPDATE members SET cpf = '52998224725'`)
	h.must(err)
	h.say("oi")
	if got := h.wa.last().Text; strings.Contains(got, "Teste") || strings.Contains(got, "4.") {
		t.Fatalf("plano secreto apareceu na lista: %q", got)
	}
	h.say("quero o segredo9 agora")
	if got := h.wa.last().Text; strings.Contains(got, "*Teste*") {
		t.Fatal("código numa frase não deveria valer")
	}
	h.say("segredo9")
	if got := h.wa.last().Text; !strings.Contains(got, "*Teste*") || !strings.Contains(got, "R$ 5") {
		t.Fatalf("escolha = %q", got)
	}
	h.say("pix")
	if len(fb.subs) != 1 || fb.subs[0].Value != 5 || fb.subs[0].BillingType != asaas.BillingPix {
		t.Fatalf("assinaturas = %+v", fb.subs)
	}
}

func TestTrialFromAdminReleasesUntilEnd(t *testing.T) {
	h, _ := newBillingHarness(t)
	h.say("oi")
	if got := h.wa.last().Text; !strings.Contains(got, "escolha um dos planos") {
		t.Fatalf("sem plano deveria pedir plano: %q", got)
	}
	org := mustOrg(h)
	end, err := h.st.GrantTrial(ctx, org, 7, h.now)
	h.must(err)
	if d := end.Sub(h.now); d != 7*24*time.Hour {
		t.Fatalf("fim do teste = %v", end)
	}
	if s := h.subStatus(); s != "trialing" {
		t.Fatalf("status = %q", s)
	}
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	if h.att.Calls() != 1 {
		t.Fatal("teste em andamento deveria falar com a Mia")
	}
	// Encerrado (0 dias): volta a pedir plano, avisando que o teste acabou.
	_, err = h.st.GrantTrial(ctx, org, 0, h.now)
	h.must(err)
	h.say("e agora")
	if got := h.wa.last().Text; !strings.Contains(got, "teste grátis terminou") {
		t.Fatalf("teste encerrado = %q", got)
	}
	if _, err := h.st.GrantTrial(ctx, org, 91, h.now); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("91 dias = %v", err)
	}
	// Quem já paga não vira teste.
	_, err = h.pool.Exec(ctx, `UPDATE subscriptions SET status = 'active'`)
	h.must(err)
	if _, err := h.st.GrantTrial(ctx, org, 7, h.now); !errors.Is(err, store.ErrPaidSubscription) {
		t.Fatalf("assinatura paga = %v", err)
	}
	if s := h.subStatus(); s != "active" {
		t.Fatalf("status mudou para %q", s)
	}
}
