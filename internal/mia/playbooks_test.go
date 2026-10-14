package mia

import (
	"strings"
	"testing"
)

// withPlaybooks troca os playbooks embutidos por fixtures durante o teste.
func withPlaybooks(t *testing.T, list []Segment) {
	t.Helper()
	playbooksOnce.Do(func() {})
	old := playbooksList
	playbooksList = list
	t.Cleanup(func() { playbooksList = old })
}

func TestEmbeddedPlaybooksAreValid(t *testing.T) {
	list, err := loadPlaybooks()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range list {
		if seen[s.Key] {
			t.Errorf("key repetida %q", s.Key)
		}
		seen[s.Key] = true
		if !slug.MatchString(s.Key) || strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Strategy) == "" ||
			len(s.Keywords) == 0 || len(s.Keywords) > 20 || len([]rune(s.Strategy)) > 6000 || !s.Active || !s.Builtin {
			t.Errorf("playbook %q fora do contrato: %+v", s.Key, s)
		}
	}
	// O que sai do Normalize precisa passar no Validate com todos os playbooks ligados.
	if err := Defaults().Normalize().Validate(); err != nil {
		t.Fatalf("Defaults com playbooks não valida: %v", err)
	}
}

func TestCheckPlaybookRequiresEveryField(t *testing.T) {
	ok := playbookFile{Key: "pizzaria", Name: "Pizzaria", Keywords: []string{"pizza"}, Strategy: "Foque em delivery."}
	if p := checkPlaybook("pizzaria.json", ok); p != "" {
		t.Fatalf("válido recusado: %s", p)
	}
	cases := map[string]func(p *playbookFile){
		"key vazia":        func(p *playbookFile) { p.Key = "" },
		"key fora do slug": func(p *playbookFile) { p.Key = "Pizza Ria" },
		"name vazio":       func(p *playbookFile) { p.Name = "  " },
		"sem keywords":     func(p *playbookFile) { p.Keywords = nil },
		"keyword vazia":    func(p *playbookFile) { p.Keywords = []string{"pizza", " "} },
		"21 keywords":      func(p *playbookFile) { p.Keywords = make([]string, 21) },
		"sem strategy":     func(p *playbookFile) { p.Strategy = "" },
		"strategy 6001":    func(p *playbookFile) { p.Strategy = strings.Repeat("a", 6001) },
	}
	for name, mut := range cases {
		p := ok
		mut(&p)
		if checkPlaybook("pizzaria.json", p) == "" {
			t.Errorf("%s: deveria ser recusado", name)
		}
	}
	if checkPlaybook("outro.json", ok) == "" {
		t.Error("arquivo com nome diferente da key deveria ser recusado")
	}
	p := ok
	p.Strategy = strings.Repeat("ã", 6000)
	if checkPlaybook("pizzaria.json", p) != "" {
		t.Error("6000 caracteres (runes) deve valer")
	}
}

func TestMergePlaybooksKeepsAdminEdits(t *testing.T) {
	withPlaybooks(t, []Segment{
		{Key: "pizzaria", Name: "Pizzaria", Keywords: []string{"pizza"}, Strategy: "padrão", Active: true, Builtin: true},
		{Key: "clinica", Name: "Clínica", Keywords: []string{"consultório"}, Strategy: "padrão clínica", Active: true, Builtin: true},
	})
	saved := Config{Segments: []Segment{
		{Key: "pizzaria", Name: "Pizzaria da casa", Keywords: []string{"rodízio"}, Strategy: "editado pelo admin", Active: false},
		{Key: "meu-nicho", Name: "Meu nicho", Strategy: "só meu", Active: true},
	}}
	got := saved.Normalize().Segments
	by := map[string]Segment{}
	for _, s := range got {
		by[s.Key] = s
	}
	if len(got) != 3 {
		t.Fatalf("esperava 3 segmentos (2 salvos + clinica nova), veio %d", len(got))
	}
	if p := by["pizzaria"]; p.Strategy != "editado pelo admin" || p.Active || p.Name != "Pizzaria da casa" || !p.Builtin {
		t.Errorf("edição do admin perdida ou sem marca de origem: %+v", p)
	}
	if c := by["clinica"]; c.Strategy != "padrão clínica" || !c.Active || !c.Builtin {
		t.Errorf("playbook novo não entrou: %+v", c)
	}
	if m := by["meu-nicho"]; m.Builtin || m.Strategy != "só meu" {
		t.Errorf("segmento do admin alterado: %+v", m)
	}
	if got[0].Key != "pizzaria" || got[1].Key != "meu-nicho" {
		t.Errorf("ordem do admin deve ser mantida: %v", got)
	}
	// Idempotente: normalizar de novo não duplica nem muda nada.
	again := (Config{Segments: got}).Normalize().Segments
	if len(again) != 3 || again[0].Strategy != "editado pelo admin" {
		t.Errorf("Normalize não é idempotente: %+v", again)
	}
	// Builtin vem da chave, não do JSON: segmento próprio com flag forjada é desmarcado.
	forged := (Config{Segments: []Segment{{Key: "x", Name: "X", Active: true, Builtin: true}}}).Normalize().Segments[0]
	if forged.Builtin {
		t.Error("Builtin deve ser derivado da chave, não do JSON")
	}
}

func TestPlaybookForRestoresOriginal(t *testing.T) {
	withPlaybooks(t, []Segment{{Key: "pizzaria", Name: "Pizzaria", Strategy: "padrão", Active: true, Builtin: true}})
	if s, ok := PlaybookFor("pizzaria"); !ok || s.Strategy != "padrão" {
		t.Fatalf("PlaybookFor = %+v %v", s, ok)
	}
	if _, ok := PlaybookFor("nao-existe"); ok {
		t.Fatal("chave desconhecida não é playbook")
	}
	// Mutar a cópia não contamina o padrão.
	a := Playbooks()
	a[0].Strategy = "mutado"
	if Playbooks()[0].Strategy != "padrão" {
		t.Fatal("Playbooks deve devolver cópias")
	}
}

func TestSegmentForPrefersMostSpecific(t *testing.T) {
	c := Config{Segments: []Segment{
		{Key: "saude", Name: "Saúde", Keywords: []string{"clínica"}, Active: true},
		{Key: "odonto", Name: "Odontologia", Keywords: []string{"clínica odontológica", "dentista"}, Active: true},
		{Key: "off", Name: "Estética avançada", Keywords: []string{"clínica odontológica premium"}, Active: false},
	}}
	if s := c.SegmentFor("", "Clínica odontológica no centro"); s == nil || s.Key != "odonto" {
		t.Fatalf("deveria vencer o termo mais longo: %+v", s)
	}
	if s := c.SegmentFor("", "clínica de fisioterapia"); s == nil || s.Key != "saude" {
		t.Fatalf("só um casa: %+v", s)
	}
	// Empate de tamanho: o primeiro da lista.
	tie := Config{Segments: []Segment{{Key: "a", Name: "Loja", Active: true}, {Key: "b", Name: "Moda", Keywords: []string{"loja"}, Active: true}}}
	if s := tie.SegmentFor("", "loja"); s == nil || s.Key != "a" {
		t.Fatalf("empate: %+v", s)
	}
}

func TestSegmentForFormSegmentIsWeakFallback(t *testing.T) {
	c := Config{Segments: []Segment{
		{Key: "restaurante", Name: "Restaurantes", Keywords: []string{"alimentação"}, Active: true},
		{Key: "pizzaria", Name: "Pizzaria", Keywords: []string{"pizza"}, Active: true},
		{Key: "loja-roupa", Name: "Moda", Keywords: []string{"varejo"}, Active: true},
	}}
	// Nicho livre casa com algo: o forte vence o segmento do formulário.
	if s := c.SegmentFor("restaurante", "pizzaria artesanal"); s == nil || s.Key != "pizzaria" {
		t.Fatalf("nicho livre deve vencer: %+v", s)
	}
	// Nicho livre não casa: cai no segmento do cadastro (código ou rótulo gravado).
	if s := c.SegmentFor("restaurante", "xyz"); s == nil || s.Key != "restaurante" {
		t.Fatalf("fallback pelo código: %+v", s)
	}
	if s := c.SegmentFor("Loja e varejo", ""); s == nil || s.Key != "loja-roupa" {
		t.Fatalf("fallback pelo rótulo/keyword exata: %+v", s)
	}
	// Sem módulo equivalente (ou desligado): nada.
	if s := c.SegmentFor("automotivo", "xyz"); s != nil {
		t.Fatalf("não deveria casar: %+v", s)
	}
	c.Segments[0].Active = false
	if s := c.SegmentFor("restaurante", "xyz"); s != nil {
		t.Fatalf("módulo inativo não vale: %+v", s)
	}
	// Segmento livre digitado em "outro" nunca vira fallback.
	if s := c.SegmentFor("Outro", ""); s != nil {
		t.Fatalf("outro não tem fallback: %+v", s)
	}
}

func TestValidateStrategyLimit(t *testing.T) {
	d := Defaults()
	c := Config{Texts: d.Texts, Fields: d.Fields, Persona: d.Persona, Stages: d.Stages}
	c.Segments = []Segment{{Key: "a", Name: "A", Strategy: strings.Repeat("é", 6000), Active: true}}
	if err := c.Validate(); err != nil {
		t.Fatalf("6000 deve valer: %v", err)
	}
	c.Segments[0].Strategy += "x"
	if err := c.Validate(); err == nil {
		t.Fatal("6001 deve ser recusado")
	}
}
