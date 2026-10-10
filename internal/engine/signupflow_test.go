package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// flowWA é o fake com formulário do WhatsApp.
type flowWA struct {
	*fakeWA
	mu    sync.Mutex
	flows []whatsapp.Flow
	fail  bool
}

func (f *flowWA) SendFlow(_ context.Context, _ string, fl whatsapp.Flow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("formulário indisponível")
	}
	f.flows = append(f.flows, fl)
	return nil
}

func (f *flowWA) lastFlow(t *testing.T) whatsapp.Flow {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.flows) == 0 {
		t.Fatal("nenhum formulário enviado")
	}
	return f.flows[len(f.flows)-1]
}

func newFlowHarness(t *testing.T) (*harness, *flowWA) {
	h := newSignupHarness(t)
	wa := &flowWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.eng.SignupFlowID = "FLOW1"
	h.eng.TermsVersion = "v1"
	// O link vence pelo relógio do banco: o relógio do harness precisa ser o de agora.
	h.now = time.Now()
	return h, wa
}

func answer(token, doc string) string {
	return `{"flow_token":"` + token + `","full_name":"Ana Souza","email":"ana@exemplo.com","documento":"` + doc + `",
"business_name":"Pizzaria Boa","segment":"restaurante","city":"Campinas","uf":"SP","objectives":["mensagens"],
"monthly_budget":"ate_500","ads_experience":"nunca","meta_ad_account":"sim","approver":"self","accept_terms":true}`
}

func (h *harness) sayFlow(text string) {
	h.t.Helper()
	h.seq++
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: "f" + string(rune('0'+h.seq)), ChatID: chatID, Phone: phone, Kind: whatsapp.KindFlow, Text: text}))
}

func (h *harness) registrationStatus() string {
	h.t.Helper()
	var s string
	h.must(h.pool.QueryRow(ctx, `SELECT registration_status FROM organizations LIMIT 1`).Scan(&s))
	return s
}

func TestSignupGateSendsFlowInsteadOfLink(t *testing.T) {
	h, wa := newFlowHarness(t)
	h.say("oi")
	fl := wa.lastFlow(t)
	if fl.ID != "FLOW1" || fl.Token == "" || fl.Screen != "DADOS" || fl.CTA == "" {
		t.Fatalf("formulário = %+v", fl)
	}
	if strings.Contains(h.wa.last().Text, signupBase) {
		t.Fatalf("link enviado junto: %q", h.wa.last().Text)
	}
	if n := h.pendingJobs("signup.remind"); n != 1 {
		t.Fatalf("lembrete agendado = %d", n)
	}
}

func TestSignupGateFallsBackToLink(t *testing.T) {
	h, wa := newFlowHarness(t)
	wa.fail = true
	h.say("oi")
	if !strings.Contains(h.wa.last().Text, signupBase+"/") {
		t.Fatalf("sem formulário, o link vai: %q", h.wa.last().Text)
	}
}

func TestFlowAnswerCompletesRegistration(t *testing.T) {
	h, wa := newFlowHarness(t)
	h.say("oi")
	h.sayFlow(answer(wa.lastFlow(t).Token, "11.222.333/0001-81"))
	if s := h.registrationStatus(); s != "complete" {
		t.Fatalf("cadastro = %s", s)
	}
	if h.pendingJobs("whatsapp.send") == 0 {
		t.Fatal("boas-vindas não enfileiradas")
	}
	// Resposta repetida (reenvio da Meta ou toque duplo) não refaz nada.
	before := h.wa.count()
	h.sayFlow(answer(wa.lastFlow(t).Token, "11.222.333/0001-81"))
	if h.registrationStatus() != "complete" || h.wa.count() > before+1 {
		t.Fatalf("resposta repetida: enviados %d", h.wa.count()-before)
	}
}

func TestFlowAnswerInvalidAsksAgain(t *testing.T) {
	h, wa := newFlowHarness(t)
	h.say("oi")
	first := wa.lastFlow(t)
	h.sayFlow(answer(first.Token, "123"))
	if h.registrationStatus() == "complete" {
		t.Fatal("CPF inválido não pode concluir")
	}
	if body := wa.lastFlow(t).Body; !strings.Contains(body, "CPF") {
		t.Fatalf("explicação = %q", body)
	}
	if len(wa.flows) != 2 {
		t.Fatalf("formulário reenviado = %d", len(wa.flows))
	}
	// O mesmo link continua valendo para a correção.
	h.sayFlow(answer(wa.lastFlow(t).Token, ""))
	if h.registrationStatus() != "complete" {
		t.Fatal("correção não concluiu")
	}
}

func TestFlowAnswerUnknownTokenResends(t *testing.T) {
	h, wa := newFlowHarness(t)
	h.say("oi")
	h.sayFlow(answer("token-que-nao-existe-123", ""))
	if h.registrationStatus() == "complete" || len(wa.flows) != 2 {
		t.Fatalf("status=%s formulários=%d", h.registrationStatus(), len(wa.flows))
	}
}

func TestFlowAnswerBrokenJSON(t *testing.T) {
	h, wa := newFlowHarness(t)
	h.say("oi")
	h.sayFlow("{nope")
	if h.registrationStatus() == "complete" || len(wa.flows) != 2 {
		t.Fatalf("status=%s formulários=%d", h.registrationStatus(), len(wa.flows))
	}
}
