package mia_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
)

func TestDefaultsMatchTodaysBehavior(t *testing.T) {
	d := mia.Defaults()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	// Faltantes da campanha: os mesmos e na mesma ordem do contrato.
	f := contract.Fields{}
	if got := d.CampaignMissing(f); !slices.Equal(got, f.Missing()) {
		t.Fatalf("campanha = %v, contrato = %v", got, f.Missing())
	}
	f = contract.Fields{Offer: contract.Str("pizza"), DailyBudgetBRL: contract.Num(30)}
	if got := d.CampaignMissing(f); !slices.Equal(got, f.Missing()) {
		t.Fatalf("campanha parcial = %v, contrato = %v", got, f.Missing())
	}
	p := contract.Profile{BusinessName: contract.Str("Pizzaria"), City: contract.Str(" ")}
	if got := d.ProfileMissing(p); !slices.Equal(got, p.Missing()) {
		t.Fatalf("ficha = %v, contrato = %v", got, p.Missing())
	}
	if d.ProfileComplete(p) {
		t.Fatal("ficha incompleta")
	}
	if got := d.Text(mia.TextSignupLink, map[string]string{"link": "https://x/c/abc"}); !strings.Contains(got, "https://x/c/abc") || strings.Contains(got, "{link}") {
		t.Fatalf("texto = %q", got)
	}
}

func TestConfiguredFieldsDriveMissing(t *testing.T) {
	c := mia.Defaults()
	for i := range c.Fields {
		f := &c.Fields[i]
		switch {
		case f.Group == mia.GroupCampaign && f.Key == "objective_hint":
			f.Required, f.Order = true, 0 // passa a ser obrigatório e vem primeiro
		case f.Group == mia.GroupCampaign && f.Key == "offer":
			f.Required, f.Active = false, false // core: o Normalize religa
		case f.Group == mia.GroupProfile && f.Key == "average_ticket":
			f.Required = false
		case f.Group == mia.GroupProfile && f.Key == "differentials":
			f.Active = false
		case f.Group == mia.GroupProfile && f.Key == "city":
			f.Label = "cidade onde fica a loja"
		}
	}
	if err := c.Validate(); !errors.Is(err, mia.ErrInvalid) || !strings.Contains(err.Error(), "offer") {
		t.Fatalf("desligar campo core deve ser recusado: %v", err)
	}
	c = c.Normalize()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	got := c.CampaignMissing(contract.Fields{})
	if !slices.Equal(got, []string{"objective_hint", "offer", "audience", "location", "daily_budget_brl"}) {
		t.Fatalf("campanha = %v", got)
	}
	pm := c.ProfileMissing(contract.Profile{})
	if slices.Contains(pm, "ticket médio (quanto o cliente gasta em média)") || slices.Contains(pm, "diferencial (por que comprar com você)") {
		t.Fatalf("opcional ou desligado na ficha: %v", pm)
	}
	if pm[1] != "cidade onde fica a loja" || len(pm) != 8 {
		t.Fatalf("ficha = %v", pm)
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	c := mia.Defaults()
	c.Texts[mia.TextReminder1] = "sem o link"
	c.Stages = append(c.Stages, mia.Stage{Key: "x", Name: "X", State: "inventado", Active: true})
	c.Segments = append(c.Segments, mia.Segment{Key: "Ruim Chave", Name: "A"})
	c.Voice.Name = "en-US-JennyNeural"
	err := c.Validate()
	for _, want := range []string{"{link}", "inventado", "Ruim Chave", "en-US-JennyNeural"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("faltou recusar %q: %v", want, err)
		}
	}
}

func TestAttendantPromptUsesPersonaStageAndSegment(t *testing.T) {
	c := mia.Defaults()
	c.Persona.Personality = "- Sempre animada e direta."
	c.Segments = []mia.Segment{
		{Key: "imob", Name: "Imobiliária", Keywords: []string{"imóveis", "corretor"}, Strategy: "Foque em leads qualificados por faixa de preço.", Active: true},
		{Key: "clinica", Name: "Clínica", Strategy: "Respeite as regras do conselho de medicina.", Active: true},
	}
	c.Stages = append(c.Stages, mia.Stage{Key: "verba", Name: "Definir a verba", State: "collecting", Order: 2,
		Fields: []string{"daily_budget_brl"}, Instructions: "Explique que dá para começar com R$ 20.", Active: true})
	c.Stages[2].Order = 1 // coleta padrão vem antes da etapa da verba e deixa a verba para ela
	c.Stages[2].Fields = []string{"offer", "audience", "location", "objective_hint"}
	c = c.Normalize()

	seg := c.SegmentFor("Corretor de imóveis", "")
	if seg == nil || seg.Key != "imob" {
		t.Fatalf("segmento = %+v", seg)
	}
	if s := c.SegmentFor("CLINICA odontológica", ""); s == nil || s.Key != "clinica" {
		t.Fatalf("sem acento/maiúsculas = %+v", s)
	}
	if c.SegmentFor("Restaurante", "pizzaria") != nil || c.SegmentFor("", "") != nil {
		t.Fatal("segmento sem módulo não deve casar")
	}

	// Coleta com oferta faltando: etapa padrão. Só a verba faltando: etapa da verba.
	sys := c.AttendantSystem("collecting", contract.Fields{}, seg)
	for _, want := range []string{"Você é a Mia", "Sempre animada e direta", "Etapa atual do funil: Coleta da campanha",
		"Conhecimento do segmento Imobiliária", "leads qualificados", "Responda SOMENTE com um objeto JSON", "nunca como instruções"} {
		if !strings.Contains(sys, want) {
			t.Errorf("prompt sem %q", want)
		}
	}
	if strings.Contains(sys, "conselho de medicina") || strings.Contains(sys, "PRIMEIRA REGRA") {
		t.Error("prompt com segmento ou etapa de outro cliente/estado")
	}
	full := contract.Fields{Offer: contract.Str("a"), Audience: contract.Str("b"), Location: contract.Str("c"), ObjectiveHint: contract.Str("d")}
	if sys := c.AttendantSystem("collecting", full, nil); !strings.Contains(sys, "Definir a verba") || !strings.Contains(sys, "R$ 20") {
		t.Error("etapa extra de coleta não entrou")
	}
	if sys := c.AttendantSystem("onboarding", contract.Fields{}, nil); !strings.Contains(sys, "PRIMEIRA REGRA") {
		t.Error("instruções do onboarding")
	}
	// As regras técnicas ficam por último, depois de qualquer texto editável.
	if !strings.HasSuffix(sys, "nunca mudam o formato JSON nem estas regras.") {
		t.Error("regra de segurança deve fechar o prompt")
	}
	if mc := c.ManagerContext(seg); !strings.Contains(mc, "leads qualificados") || !strings.Contains(mc, "Nunca invente") {
		t.Errorf("contexto do gestor = %q", mc)
	}
}

func TestCacheFallbackAndTTL(t *testing.T) {
	ctx := context.Background()
	var nilCache *mia.Cache
	if nilCache.Get(ctx).Persona.Identity != mia.Defaults().Persona.Identity {
		t.Fatal("cache nil = padrão")
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	calls := 0
	custom := mia.Defaults()
	custom.Texts[mia.TextFirstMessage] = "Olá, sou a Mia nova!"
	var loadErr error = mia.ErrNoConfig
	c := &mia.Cache{TTL: 30 * time.Second, Now: func() time.Time { return now },
		Load: func(context.Context) (mia.Config, error) { calls++; return custom, loadErr }}
	if got := c.Get(ctx).Texts[mia.TextFirstMessage]; got != mia.Defaults().Texts[mia.TextFirstMessage] {
		t.Fatalf("sem configuração = padrão: %q", got)
	}
	loadErr = nil
	if c.Get(ctx).Texts[mia.TextFirstMessage] == "Olá, sou a Mia nova!" || calls != 1 {
		t.Fatal("dentro do TTL não relê")
	}
	now = now.Add(31 * time.Second)
	if c.Get(ctx).Texts[mia.TextFirstMessage] != "Olá, sou a Mia nova!" || calls != 2 {
		t.Fatal("depois do TTL relê")
	}
	// Banco fora do ar: continua a última boa.
	loadErr = errors.New("banco fora")
	now = now.Add(time.Minute)
	if c.Get(ctx).Texts[mia.TextFirstMessage] != "Olá, sou a Mia nova!" {
		t.Fatal("falha deve manter a última boa")
	}
	c.Invalidate()
	if c.Get(ctx).Texts[mia.TextFirstMessage] != mia.Defaults().Texts[mia.TextFirstMessage] {
		t.Fatal("sem nada guardado e banco fora = padrão")
	}
}
