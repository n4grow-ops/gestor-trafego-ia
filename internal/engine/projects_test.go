package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const newPhone = "5511922223333"

func (h *harness) sayFrom(phone, text string) {
	h.t.Helper()
	h.seq++
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: fmt.Sprintf("m%d", h.seq), ChatID: phone + "@c.us", Phone: phone, Kind: whatsapp.KindText, Text: text}))
}

func (h *harness) ident() store.Identity {
	h.t.Helper()
	i, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	return i
}

func (h *harness) activeProject() store.Project {
	h.t.Helper()
	c := h.conv()
	if c.ActiveProjectID == nil {
		h.t.Fatal("sem projeto ativo")
	}
	p, err := h.st.GetProject(ctx, h.ident().OrgID, *c.ActiveProjectID)
	h.must(err)
	return p
}

func TestAutoSignupStartsOnboarding(t *testing.T) {
	h := newHarness(t, "owner")
	h.eng.AutoSignup, h.eng.SignupMaxBudget, h.eng.SignupDailyLimit = true, 50, 60
	h.att.Responses = []string{
		`{"intent":"other","fields":{},"profile":{},"reply":"Bem-vindo! Qual o nome do seu negócio e a cidade?"}`,
		`{"intent":"provide_info","fields":{},"profile":{"business_name":"Açaí da Bia","city":"Salvador"},"reply":"Prazer, Bia!"}`,
	}
	h.sayFrom(newPhone, "oi")
	ident, err := h.st.FindIdentity(ctx, "openwa", newPhone)
	h.must(err)
	if ident.SignupSource != "auto" || ident.MaxDailyBudgetBRL != 50 || ident.Role != "owner" {
		t.Fatalf("cliente automático = %+v", ident)
	}
	if !strings.Contains(h.att.Requests[0].Messages[1].Content, "Estado da conversa: onboarding") {
		t.Error("primeiro contato deveria estar em onboarding")
	}
	if got := h.wa.last(); got.ChatID != newPhone+"@c.us" || !strings.Contains(got.Text, "Bem-vindo") {
		t.Errorf("resposta = %+v", got)
	}
	h.sayFrom(newPhone, "Açaí da Bia, em Salvador")
	ident, _ = h.st.FindIdentity(ctx, "openwa", newPhone)
	if ident.Profile.BusinessName == nil || *ident.Profile.City != "Salvador" {
		t.Errorf("memória = %+v", ident.Profile)
	}
	c, _ := h.st.GetOrCreateConversation(ctx, ident.ID, "")
	if c.State != store.StateIdle {
		t.Errorf("estado após onboarding = %s", c.State)
	}
}

func TestUnknownPhoneWithoutAutoSignup(t *testing.T) {
	h := newHarness(t, "owner")
	h.sayFrom(newPhone, "oi")
	if !strings.Contains(h.wa.last().Text, "não está liberado") || h.att.Calls() != 0 {
		t.Errorf("resposta = %q", h.wa.last().Text)
	}
}

func TestBlockedClientIsSilent(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.SetOrgStatus(ctx, h.ident().OrgID, "blocked"))
	h.say("oi")
	if h.wa.count() != 0 || h.att.Calls() != 0 {
		t.Errorf("bloqueado recebeu resposta: %v", h.wa.sent)
	}
}

func TestDailyLimitWarnsOnceThenSilence(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.SetOrgLimits(ctx, h.ident().OrgID, 100, 2))
	q := `{"intent":"question","fields":{},"reply":"Claro!"}`
	h.att.Responses = []string{q, q}
	h.say("1")
	h.say("2")
	h.say("3")
	if !strings.Contains(h.wa.last().Text, "limite de mensagens") {
		t.Fatalf("3ª mensagem = %q", h.wa.last().Text)
	}
	n := h.wa.count()
	h.say("4")
	if h.wa.count() != n || h.att.Calls() != 2 {
		t.Errorf("depois do aviso deveria ficar em silêncio (envios %d, IA %d)", h.wa.count(), h.att.Calls())
	}
}

func TestProjectsCreateSwitchListAndStatus(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{
		`{"intent":"create_campaign","project":"Pizzaria","fields":{"offer":"Pizza grande"},"reply":"Legal!"}`,
		`{"intent":"new_project","project":"Delivery","fields":{"offer":"Açaí"},"reply":"Vamos ao delivery."}`,
		`{"intent":"switch_project","project":"pizza","fields":{},"reply":"ok"}`,
		`{"intent":"list_projects","fields":{},"reply":"ok"}`,
		`{"intent":"project_status","project":"delivery","fields":{},"reply":"ok"}`,
	}
	h.say("quero anunciar minha pizzaria")
	if p := h.activeProject(); p.Name != "Pizzaria" || *p.Fields.Offer != "Pizza grande" {
		t.Fatalf("projeto 1 = %+v", p)
	}
	h.say("agora quero anunciar o delivery de açaí")
	if p := h.activeProject(); p.Name != "Delivery" || *p.Fields.Offer != "Açaí" {
		t.Fatalf("projeto 2 = %+v", p)
	}
	if !strings.Contains(h.wa.last().Text, "Delivery") {
		t.Errorf("resposta ao criar = %q", h.wa.last().Text)
	}
	h.say("volta pra pizzaria")
	p := h.activeProject()
	c := h.conv()
	if p.Name != "Pizzaria" || c.Fields.Offer == nil || *c.Fields.Offer != "Pizza grande" {
		t.Fatalf("retomada = %+v campos = %+v", p, c.Fields)
	}
	if last := h.wa.last().Text; !strings.Contains(last, "Retomei") || !strings.Contains(last, "Pizzaria") {
		t.Errorf("resposta ao retomar = %q", last)
	}
	h.say("quais projetos eu tenho?")
	if last := h.wa.last().Text; !strings.Contains(last, "Pizzaria") || !strings.Contains(last, "Delivery") {
		t.Errorf("lista = %q", last)
	}
	h.say("como está o delivery?")
	if last := h.wa.last().Text; !strings.Contains(last, "Delivery") || !strings.Contains(last, "Açaí") {
		t.Errorf("resumo = %q", last)
	}
}

func TestSwitchToUnknownOrAmbiguousProject(t *testing.T) {
	h := newHarness(t, "owner")
	org := h.ident().OrgID
	for _, n := range []string{"Pizzaria Centro", "Pizzaria Bairro"} {
		_, _, err := h.st.CreateOrReopenProject(ctx, org, n, contract.Fields{})
		h.must(err)
	}
	h.att.Responses = []string{
		`{"intent":"switch_project","project":"hamburgueria","fields":{},"reply":"ok"}`,
		`{"intent":"switch_project","project":"pizzaria","fields":{},"reply":"ok"}`,
	}
	h.say("volta pra hamburgueria")
	if !strings.Contains(h.wa.last().Text, "Não encontrei") {
		t.Errorf("inexistente = %q", h.wa.last().Text)
	}
	h.say("volta pra pizzaria")
	if last := h.wa.last().Text; !strings.Contains(last, "Qual deles") || !strings.Contains(last, "Pizzaria Centro") {
		t.Errorf("ambíguo = %q", last)
	}
	if h.conv().ActiveProjectID != nil {
		t.Error("nenhum projeto deveria ser ativado")
	}
}

func TestNewProjectDuringApprovalCancelsDraft(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	first := h.activeProject()
	h.att.Responses = append(h.att.Responses, `{"intent":"new_project","project":"Delivery","fields":{},"reply":"ok"}`)
	h.say("quero anunciar outra coisa, o delivery")
	if s := h.draftStatuses(); s["cancelled"] != 1 {
		t.Errorf("rascunhos = %v", s)
	}
	old, err := h.st.GetProject(ctx, h.ident().OrgID, first.ID)
	h.must(err)
	if old.Stage != store.StageCollecting || old.Fields.Offer == nil {
		t.Errorf("projeto anterior = %+v", old)
	}
	if h.activeProject().Name != "Delivery" {
		t.Error("Delivery deveria estar ativo")
	}
}

func TestBusinessMemoryFillsFieldsAndBriefing(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.st.UpdateOrgProfile(ctx, h.ident().OrgID, contract.Profile{
		BusinessName: contract.Str("Pizzaria Boa"), City: contract.Str("Salvador"), Audience: contract.Str("famílias do bairro"),
	}))
	h.att.Responses = []string{turnOffer, `{"intent":"provide_info","fields":{"daily_budget_brl":50},"reply":"Anotado."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	h.say("50 por dia")
	if c := h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("com a memória, os campos deveriam estar completos: %+v", c)
	}
	if !strings.Contains(h.att.Requests[0].Messages[1].Content, "Pizzaria Boa") {
		t.Error("memória não chegou ao Atendente")
	}
	h.runJobs()
	brief := h.mgr.Requests[0].Messages[1].Content
	if !strings.Contains(brief, `"location": "Salvador"`) || !strings.Contains(brief, `"business_name": "Pizzaria Boa"`) {
		t.Errorf("briefing:\n%s", brief)
	}
	if !codeRe.MatchString(h.wa.last().Text) {
		t.Errorf("proposta não chegou: %q", h.wa.last().Text)
	}
	_ = engine.JobPropose
}
