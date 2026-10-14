package shield_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/shield"
)

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		status, reason int
		want           shield.State
	}{
		{0, 0, shield.StateOK}, // leitura incompleta não acusa problema
		{1, 0, shield.StateOK},
		{201, 0, shield.StateOK},
		{2, 1, shield.StateDisabled},
		{2, 3, shield.StatePayment},
		{2, 7, shield.StateClosed},
		{3, 0, shield.StatePayment},
		{8, 0, shield.StatePayment},
		{7, 0, shield.StateRestricted},
		{9, 0, shield.StateGrace},
		{100, 0, shield.StateClosed},
		{101, 0, shield.StateClosed},
		{202, 0, shield.StateClosed},
		{999, 0, shield.StateOK},
	} {
		if got := shield.Classify(meta.AccountHealth{Status: tc.status, DisableReason: tc.reason}); got != tc.want {
			t.Errorf("status %d motivo %d = %s, quer %s", tc.status, tc.reason, got, tc.want)
		}
	}
}

// Janela de silêncio: nada das 21h às 8h de Brasília (UTC-3).
func TestQuietWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 14, h+3, m, 0, 0, time.UTC) } // h:m em Brasília
	for _, tc := range []struct {
		h, m  int
		quiet bool
	}{{7, 59, true}, {8, 0, false}, {12, 0, false}, {20, 59, false}, {21, 0, true}, {23, 30, true}, {0, 0, true}, {3, 0, true}} {
		if got := shield.Quiet(at(tc.h, tc.m)); got != tc.quiet {
			t.Errorf("%02d:%02d BRT: Quiet = %v", tc.h, tc.m, got)
		}
	}
	// 22h de terça: abre às 8h de quarta; 3h de quarta: abre às 8h de quarta; 10h: já aberto.
	if got := shield.NextOpen(at(22, 0)); !got.Equal(time.Date(2026, 10, 15, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("NextOpen(22h) = %v", got)
	}
	if got := shield.NextOpen(at(3, 0)); !got.Equal(time.Date(2026, 10, 14, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("NextOpen(3h) = %v", got)
	}
	if got := shield.NextOpen(at(10, 0)); !got.Equal(at(10, 0)) {
		t.Errorf("NextOpen(10h) = %v", got)
	}
}

// Idempotência: uma mudança gera no máximo um aviso, mesmo com várias checagens seguidas.
func TestDecideNotifiesOncePerChange(t *testing.T) {
	day := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)  // 12h BRT
	night := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC) // 22h BRT do dia 13

	notified := shield.StateOK
	run := func(current shield.State, at time.Time) shield.Decision {
		d := shield.Decide(current, notified, at)
		switch d {
		case shield.DecideNotify:
			notified = current
		case shield.DecideReset:
			notified = shield.StateOK
		}
		return d
	}

	steps := []struct {
		name    string
		current shield.State
		at      time.Time
		want    shield.Decision
	}{
		{"conta saudável", shield.StateOK, day, shield.DecideNone},
		{"vira restrita de dia: avisa", shield.StateRestricted, day, shield.DecideNotify},
		{"segue restrita: não repete", shield.StateRestricted, day, shield.DecideNone},
		{"segue restrita de novo: não repete", shield.StateRestricted, day.Add(4 * time.Hour), shield.DecideNone},
		{"piora para desativada: novo aviso", shield.StateDisabled, day.Add(8 * time.Hour), shield.DecideNotify},
		{"segue desativada: não repete", shield.StateDisabled, day.Add(12 * time.Hour), shield.DecideNone},
		{"volta ao normal: zera em silêncio", shield.StateOK, day.Add(16 * time.Hour), shield.DecideReset},
		{"normal de novo: nada", shield.StateOK, day.Add(20 * time.Hour), shield.DecideNone},
		{"novo problema depois de normalizar avisa", shield.StatePayment, day.Add(24 * time.Hour), shield.DecideNotify},
	}
	for _, s := range steps {
		if got := run(s.current, s.at); got != s.want {
			t.Errorf("%s: %s, quer %s", s.name, got, s.want)
		}
	}

	// À noite: adia sem marcar como avisado; de manhã avisa uma vez só.
	notified = shield.StateOK
	if got := run(shield.StatePayment, night); got != shield.DecideDefer || notified != shield.StateOK {
		t.Fatalf("à noite = %s, notified = %s", got, notified)
	}
	if got := run(shield.StatePayment, night.Add(time.Hour)); got != shield.DecideDefer {
		t.Fatalf("ainda à noite = %s", got)
	}
	morning := time.Date(2026, 10, 14, 11, 5, 0, 0, time.UTC) // 8h05 BRT
	if got := run(shield.StatePayment, morning); got != shield.DecideNotify {
		t.Fatalf("de manhã = %s", got)
	}
	if got := run(shield.StatePayment, morning.Add(4*time.Hour)); got != shield.DecideNone {
		t.Fatalf("depois do aviso = %s", got)
	}
	// Se normalizar durante a noite sem nunca ter avisado: nada a zerar nem a avisar.
	notified = shield.StateOK
	if got := run(shield.StateOK, night); got != shield.DecideNone {
		t.Fatalf("normalizou antes de avisar = %s", got)
	}
}

func TestAlertForHasStepsAndNoPromise(t *testing.T) {
	for _, tc := range []struct {
		state  shield.State
		reason int
		sc     shield.Scenario
	}{
		{shield.StateRestricted, 0, shield.ScenarioRestricted},
		{shield.StateDisabled, 1, shield.ScenarioRestricted},
		{shield.StateDisabled, 5, shield.ScenarioIdentity},
		{shield.StatePayment, 0, shield.ScenarioPayment},
		{shield.StateGrace, 0, shield.ScenarioPayment},
		{shield.StateClosed, 7, shield.ScenarioRestricted},
	} {
		al := shield.AlertFor(tc.state, tc.reason, "Loja")
		if al.Scenario != tc.sc {
			t.Errorf("%s/%d: cenário %s, quer %s", tc.state, tc.reason, al.Scenario, tc.sc)
		}
		if !strings.Contains(al.Message, "O que fazer agora") || !strings.Contains(al.Message, "1. ") || !strings.Contains(al.Message, "Loja") {
			t.Errorf("%s: mensagem = %q", tc.state, al.Message)
		}
	}
}

func TestGuidesAreHonest(t *testing.T) {
	gs := shield.AllGuides()
	if len(gs) != 4 {
		t.Fatalf("%d guias, quer 4", len(gs))
	}
	seen := map[shield.Scenario]bool{}
	for _, g := range gs {
		seen[g.Scenario] = true
		if g.Title == "" || g.What == "" || len(g.Steps) < 3 || g.Note == "" {
			t.Errorf("guia incompleto: %+v", g)
		}
		all := strings.ToLower(g.What + " " + strings.Join(g.Steps, " ") + " " + g.Note)
		for _, bad := range []string{"garantimos", "vamos reverter", "reverteremos", "100% de chance", "resolvemos em", "liberamos sua conta"} {
			if strings.Contains(all, bad) {
				t.Errorf("%s promete demais (%q)", g.Scenario, bad)
			}
		}
		// Os que dependem da Meta dizem com todas as letras que o resultado não é garantido.
		if g.Scenario != shield.ScenarioPayment && !strings.Contains(strings.ToLower(g.Note), "meta") {
			t.Errorf("%s: nota não diz que a decisão é da Meta: %q", g.Scenario, g.Note)
		}
	}
	for _, s := range shield.Scenarios {
		if !seen[s] {
			t.Errorf("sem guia para %s", s)
		}
	}
}

func TestExplainRejection(t *testing.T) {
	for _, tc := range []struct {
		reason string
		want   string
	}{
		{"Your ad was rejected because it contains too much text in the image.", "texto demais"},
		{"Imagem com texto demais.", "texto demais"},
		{"Ad doesn't comply with Meta's policy on personal attributes", "pessoal dela"},
		{"Atributos pessoais: o anúncio afirma ou sugere atributos pessoais", "pessoal"},
		{"Before and after images are not allowed", "antes e depois"},
		{"Promessa de resultado garantido", "promessa exagerada"},
		{"Destination not working: the landing page does not load", "página de destino"},
		{"This ad is in the Housing special ad category", "imóvel"},
	} {
		ex := shield.ExplainRejection([]string{tc.reason})
		if !ex.Known || !strings.Contains(strings.ToLower(ex.Text()), strings.ToLower(tc.want)) || len(ex.Tips) != len(ex.Causes) {
			t.Errorf("%q → %q", tc.reason, ex.Text())
		}
	}
	// Motivo vazio ou desconhecido: explicação genérica honesta, sem inventar causa.
	for _, r := range [][]string{nil, {""}, {"código interno 4821"}} {
		ex := shield.ExplainRejection(r)
		if ex.Known || !strings.Contains(ex.Text(), "não disse exatamente") {
			t.Errorf("%v → %q", r, ex.Text())
		}
	}
	// Farmácia não é arma: palavra inteira.
	if ex := shield.ExplainRejection([]string{"Farmácia de manipulação"}); ex.Known {
		t.Errorf("farmácia virou causa: %q", ex.Text())
	}
}

func TestExplainWithCopyUsesFixerOnlyWhenSet(t *testing.T) {
	fix := func(h, tx, seg string) (string, string, bool) {
		return strings.ReplaceAll(h, "garantido", "possível"), strings.ReplaceAll(tx, "garantido", "possível"), true
	}
	ex := shield.ExplainWithCopy(context.Background(), []string{"resultado garantido"}, fix, "Resultado garantido", "Emagreça garantido", "saude")
	txt := ex.Text()
	if !strings.Contains(txt, "Sugestão de texto") || !strings.Contains(txt, "Resultado possível") {
		t.Fatalf("sem sugestão: %q", txt)
	}
	none := shield.ExplainWithCopy(context.Background(), []string{"resultado garantido"}, nil, "x", "y", "")
	if strings.Contains(none.Text(), "Sugestão de texto") {
		t.Fatal("sugeriu texto sem o ajuste ligado")
	}
	unchanged := func(h, tx, seg string) (string, string, bool) { return h, tx, false }
	if ex := shield.ExplainWithCopy(context.Background(), nil, unchanged, "a", "b", ""); strings.Contains(ex.Text(), "Sugestão de texto") {
		t.Fatal("sugeriu texto sem mudança")
	}
}

func TestReportHelpers(t *testing.T) {
	r := shield.Report{Items: []shield.Item{
		{Key: "a", Level: shield.OK},
		{Key: "b", Level: shield.Attention},
		{Key: "c", Level: shield.Blocks},
	}}
	if r.Worst() != shield.Blocks || len(r.Blockers()) != 1 || len(r.Attention()) != 1 || len(r.Problems()) != 2 || r.Problems()[0].Key != "c" {
		t.Fatalf("%+v", r)
	}
	if w := r.Without("c").Worst(); w != shield.Attention {
		t.Fatalf("Without(c).Worst = %s", w)
	}
}
