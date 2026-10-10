package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func withMia(h *harness, cfg mia.Config) {
	h.eng.Mia = &mia.Cache{Load: func(context.Context) (mia.Config, error) { return cfg, nil }}
}

// Campo extra obrigatório no painel segura a proposta até a Mia coletar.
func TestMiaRequiredFieldHoldsProposal(t *testing.T) {
	h := newHarness(t, "owner")
	cfg := mia.Defaults()
	for i := range cfg.Fields {
		if cfg.Fields[i].Group == mia.GroupCampaign && cfg.Fields[i].Key == "objective_hint" {
			cfg.Fields[i].Required = true
			cfg.Fields[i].Hint = "pergunte se ele quer mensagens no WhatsApp"
		}
	}
	cfg.Segments = []mia.Segment{{Key: "pizza", Name: "Pizzaria", Keywords: []string{"restaurante"}, Strategy: "Anuncie no horário do jantar.", Active: true}}
	withMia(h, cfg)

	h.att.Responses = append(h.att.Responses, turnOffer, turnRest)
	h.say("quero anunciar pizza")
	h.say("famílias de Campinas, 50 por dia")
	h.runJobs()
	if n := h.pendingJobs(engine.JobPropose); n != 0 {
		t.Fatalf("proposta pedida com campo obrigatório faltando: %d", n)
	}
	if h.conv().State != store.StateCollecting {
		t.Fatalf("estado = %q", h.conv().State)
	}
	h.att.Responses = append(h.att.Responses, `{"intent":"provide_info","fields":{},"reply":"E o que você espera dos anúncios?"}`)
	h.say("e agora?")
	reqs := h.att.Requests
	last := reqs[len(reqs)-1]
	if !strings.Contains(last.Messages[1].Content, "Dados faltando: objective_hint") {
		t.Errorf("faltante configurado não chegou à Atendente:\n%s", last.Messages[1].Content)
	}
	sys := last.Messages[0].Content
	for _, want := range []string{"pergunte se ele quer mensagens no WhatsApp", "Etapa atual do funil: Coleta da campanha", "Anuncie no horário do jantar"} {
		if !strings.Contains(sys, want) {
			t.Errorf("prompt sem %q", want)
		}
	}
}

// O texto do link de cadastro vem do painel, com o link no lugar do marcador.
func TestMiaSignupTextFromPanel(t *testing.T) {
	h := newSignupHarness(t)
	cfg := mia.Defaults()
	cfg.Texts[mia.TextSignupLink] = "Olá! Cadastro aqui: {link} (2 min)"
	withMia(h, cfg)
	h.say("oi")
	h.runJobs()
	got := h.wa.last().Text
	if !strings.HasPrefix(got, "Olá! Cadastro aqui: "+signupBase+"/") || strings.Contains(got, "{link}") {
		t.Fatalf("mensagem = %q", got)
	}
}

// Sem configuração no banco, vale o padrão (o comportamento de antes do painel).
func TestMiaWithoutConfigUsesDefaults(t *testing.T) {
	h := newSignupHarness(t)
	h.eng.Mia = &mia.Cache{Load: func(context.Context) (mia.Config, error) { return mia.Config{}, mia.ErrNoConfig }}
	h.say("oi")
	h.runJobs()
	if !strings.Contains(h.wa.last().Text, "Pra gente começar, faz seu cadastro rapidinho") {
		t.Fatalf("mensagem = %q", h.wa.last().Text)
	}
}
