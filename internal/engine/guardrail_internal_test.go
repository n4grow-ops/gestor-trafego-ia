package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestParseGuardrailCommand(t *testing.T) {
	cases := []struct {
		in    string
		cmd   guardrailCmd
		cents int64
	}{
		{"LIMITE 15", gcSetCost, 1500},
		{"limite de R$ 15,50", gcSetCost, 1550},
		{"*Limite* R$15!", gcSetCost, 1500},
		{"custo máximo 12", gcSetCost, 1200},
		{"limite de custo por conversa 20", gcSetCost, 2000},
		{"limite 15 reais por conversa", gcSetCost, 1500},
		{"TETO 100", gcSetCap, 10000},
		{"teto diário de 1.500,00", gcSetCap, 150000},
		{"teto de gasto 80", gcSetCap, 8000},
		{"limite por dia 80", gcSetCap, 8000},
		{"limite 80 por dia", gcSetCap, 8000},
		{"teto 2.000", gcSetCap, 200000}, // ponto de milhar
		{"SEM LIMITE", gcClear, 0},
		{"tirar o limite", gcClear, 0},
		{"remover teto", gcClear, 0},
		{"só avisar", gcAutoOff, 0},
		{"Só me avise", gcAutoOff, 0},
		{"pausar sozinha", gcAutoOn, 0},
		{"pausa automática", gcAutoOn, 0},
		{"meus limites", gcShow, 0},
		// não são comandos: seguem para a Atendente
		{"limite", gcNone, 0},
		{"quero um limite de 15 reais no meu orçamento", gcNone, 0},
		{"limite 15 por semana", gcNone, 0},
		{"ativar", gcNone, 0},
		{"pausar", gcNone, 0},
		{"oi, tudo bem?", gcNone, 0},
	}
	for _, c := range cases {
		cmd, cents := parseGuardrailCommand(c.in)
		if cmd != c.cmd || cents != c.cents {
			t.Errorf("%q = (%v, %d), want (%v, %d)", c.in, cmd, cents, c.cmd, c.cents)
		}
	}
	// frase de limite com valor ilegível é reconhecida (para o cliente ser avisado), nunca vira limite
	if cmd, cents := parseGuardrailCommand("limite 0"); cmd != gcSetCost || cents >= 0 {
		t.Errorf("limite 0 = (%v, %d)", cmd, cents)
	}
}

func ptr(v int64) *int64 { return &v }

func at(h, m int) time.Time { return time.Date(2026, 10, 14, h, m, 0, 0, brt) }

// Os testes de regra usam gasto mínimo de R$ 30 e janela mínima de 24 h.
const (
	tMinSpend  = 3000
	tMinWindow = 24 * time.Hour
)

func eval(r store.GuardrailRule, d guardrailData) []guardrailViolation {
	return evaluateGuardrails(r, d, tMinSpend, tMinWindow)
}

func costData(now time.Time, ago time.Duration, spend, convs int64) guardrailData {
	act := now.Add(-ago)
	return guardrailData{Now: now, ActivatedAt: &act, WindowSince: brtDay(act), WindowSpend: spend, WindowConvs: convs, HaveWindow: true}
}

func TestEvaluateCostRule(t *testing.T) {
	rule := store.GuardrailRule{MaxCostPerConvCents: ptr(1500), AutoPause: true}
	now := at(12, 0)
	t.Run("violada: R$ 20 por conversa contra limite de R$ 15", func(t *testing.T) {
		vs := eval(rule, costData(now, 30*time.Hour, 6000, 3))
		if len(vs) != 1 || vs[0].Rule != store.GuardrailMaxCost || vs[0].ObservedCents != 2000 || vs[0].ThresholdCents != 1500 || vs[0].SpendCents != 6000 || vs[0].Conversations != 3 {
			t.Fatalf("%+v", vs)
		}
		for _, want := range []string{"R$ 60,00", "3 conversas", "R$ 20,00 por conversa", "R$ 15,00"} {
			if !strings.Contains(vs[0].Reason, want) {
				t.Errorf("motivo sem %q: %s", want, vs[0].Reason)
			}
		}
	})
	t.Run("não violada: R$ 12 por conversa", func(t *testing.T) {
		if vs := eval(rule, costData(now, 30*time.Hour, 6000, 5)); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("no limite exato não viola", func(t *testing.T) {
		if vs := eval(rule, costData(now, 30*time.Hour, 6000, 4)); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("pouco gasto: não julga", func(t *testing.T) {
		if vs := eval(rule, costData(now, 30*time.Hour, 2900, 1)); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("janela curta: não julga", func(t *testing.T) {
		if vs := eval(rule, costData(now, 23*time.Hour, 6000, 1)); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("sem conversa mas gasto acima do limite", func(t *testing.T) {
		vs := eval(rule, costData(now, 30*time.Hour, 4000, 0))
		if len(vs) != 1 || vs[0].ObservedCents != 4000 || !strings.Contains(vs[0].Reason, "nenhuma conversa") {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("sem conversa e gasto abaixo do mínimo: não julga", func(t *testing.T) {
		if vs := eval(rule, costData(now, 30*time.Hour, 2500, 0)); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("sem leitura da Meta: não julga", func(t *testing.T) {
		d := costData(now, 30*time.Hour, 6000, 1)
		d.HaveWindow = false
		if vs := eval(rule, d); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("sem data de ativação: não julga", func(t *testing.T) {
		d := costData(now, 30*time.Hour, 6000, 1)
		d.ActivatedAt = nil
		if vs := eval(rule, d); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
	t.Run("sem regra do cliente: nunca viola", func(t *testing.T) {
		if vs := eval(store.GuardrailRule{AutoPause: true}, costData(now, 30*time.Hour, 600000, 0)); len(vs) != 0 {
			t.Fatalf("%+v", vs)
		}
	})
}

func TestEvaluateDailyCap(t *testing.T) {
	rule := store.GuardrailRule{DailyCapCents: ptr(10000), AutoPause: true}
	now := at(15, 0)
	day := func(spend int64, have bool) guardrailData {
		return guardrailData{Now: now, TodaySpendCents: spend, HaveToday: have}
	}
	if vs := eval(rule, day(10000, true)); len(vs) != 1 || vs[0].Rule != store.GuardrailDailyCap || vs[0].ObservedCents != 10000 ||
		!vs[0].WindowStart.Equal(at(0, 0)) || !strings.Contains(vs[0].Reason, "R$ 100,00") {
		t.Fatalf("no teto: %+v", vs)
	}
	if vs := eval(rule, day(9999, true)); len(vs) != 0 {
		t.Fatalf("abaixo do teto: %+v", vs)
	}
	if vs := eval(rule, day(50000, false)); len(vs) != 0 {
		t.Fatalf("sem leitura da Meta não julga: %+v", vs)
	}
	// o teto vale mesmo logo depois de ativar: gastar o teto já é dado suficiente
	d := costData(now, time.Hour, 0, 0)
	d.TodaySpendCents, d.HaveToday = 12000, true
	if vs := eval(rule, d); len(vs) != 1 {
		t.Fatalf("teto logo após ativar: %+v", vs)
	}
}

func TestEvaluateBothRules(t *testing.T) {
	rule := store.GuardrailRule{DailyCapCents: ptr(10000), MaxCostPerConvCents: ptr(1500), AutoPause: true}
	now := at(12, 0)
	d := costData(now, 30*time.Hour, 20000, 2)
	d.TodaySpendCents, d.HaveToday = 10500, true
	vs := eval(rule, d)
	if len(vs) != 2 || vs[0].Rule != store.GuardrailDailyCap || vs[1].Rule != store.GuardrailMaxCost {
		t.Fatalf("%+v", vs)
	}
}

func TestQuietDelay(t *testing.T) {
	cases := []struct {
		now  time.Time
		want time.Duration
	}{
		{at(12, 0), 0},
		{at(8, 0), 0},
		{at(20, 59), 0},
		{at(21, 0), 11 * time.Hour},
		{at(23, 30), 8*time.Hour + 30*time.Minute},
		{at(2, 0), 6 * time.Hour},
		{at(7, 59), time.Minute},
	}
	for _, c := range cases {
		if got := quietDelay(c.now); got != c.want {
			t.Errorf("%s: delay = %v, want %v", c.now.Format("15:04"), got, c.want)
		}
	}
}

func TestGuardrailWindowSince(t *testing.T) {
	now := at(12, 0)
	if got := guardrailWindowSince(now, now.Add(-30*time.Hour)); got.Format("2006-01-02") != "2026-10-13" {
		t.Errorf("ativada ontem: %s", got)
	}
	if got := guardrailWindowSince(now, now.AddDate(0, 0, -20)); got.Format("2006-01-02") != "2026-10-08" {
		t.Errorf("ativada há 20 dias olha só 7: %s", got)
	}
}

func TestParseMoneyCents(t *testing.T) {
	for in, want := range map[string]int64{"15": 1500, "15,5": 1550, "15.50": 1550, "1.500": 150000, "1.500,00": 150000, "0,99": 99} {
		if got, ok := parseMoneyCents(in); !ok || got != want {
			t.Errorf("%q = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"0", "", "abc", "99999999999"} {
		if _, ok := parseMoneyCents(in); ok {
			t.Errorf("%q não deveria valer", in)
		}
	}
}

// ---------- diagnóstico ----------

func diagData() DiagnosticData {
	return DiagnosticData{AccountName: "Loja", Currency: "BRL",
		Since: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC), ActiveKnown: true}
}

func TestDiagnosticWithoutDataSaysSoAndInventsNothing(t *testing.T) {
	text, sum := buildDiagnostic(diagData())
	if !sum.NoData || !strings.Contains(text, "não encontrei nenhum anúncio com entrega") || !strings.Contains(text, "não inventar") {
		t.Fatalf("%s", text)
	}
	for _, bad := range []string{"R$ ", "Gasto", "Melhor", "conversas iniciadas", "Frequência"} {
		if strings.Contains(text, bad) {
			t.Errorf("diagnóstico sem dados não pode trazer %q: %s", bad, text)
		}
	}
}

func TestDiagnosticNoDataButStalledAds(t *testing.T) {
	d := diagData()
	d.Active = []meta.ActiveAd{{ID: "A1", Name: "Foto 1"}, {ID: "A2", Name: "Foto 2"}}
	text, sum := buildDiagnostic(d)
	if !sum.NoData || !strings.Contains(text, "Vi 2 anúncio(s) ligado(s) sem nenhuma exibição") {
		t.Fatalf("%s", text)
	}
}

func TestDiagnosticFullSummary(t *testing.T) {
	d := diagData()
	d.Total = meta.AdPerformance{SpendCents: 80000, Impressions: 90000, Reach: 30000, Conversations: 40, Frequency: 4.1}
	d.Ads = []meta.AdPerformance{
		{AdID: "A1", AdName: "Vídeo depoimento", SpendCents: 30000, Impressions: 40000, Conversations: 30, Frequency: 2.0}, // R$ 10,00
		{AdID: "A2", AdName: "Foto promoção", SpendCents: 30000, Impressions: 40000, Conversations: 10, Frequency: 4.5},    // R$ 30,00
		{AdID: "A3", AdName: "Carrossel", SpendCents: 20000, Impressions: 10000, Conversations: 0, Frequency: 1.1},         // desperdício
		{AdID: "A4", AdName: "Teste pequeno", SpendCents: 500, Impressions: 300, Conversations: 1, Frequency: 1.0},         // pouco gasto: fora da comparação
	}
	d.Active = []meta.ActiveAd{{ID: "A1", Name: "Vídeo depoimento"}, {ID: "A9", Name: "Anúncio parado"}}
	text, sum := buildDiagnostic(d)
	for _, want := range []string{
		"R$ 800,00", "Conversas iniciadas: 40", "Custo por conversa: R$ 20,00",
		"Melhor anúncio: *Vídeo depoimento*, R$ 10,00 por conversa",
		"Pior anúncio: *Carrossel*, gastou R$ 200,00 e não gerou nenhuma conversa",
		"4.1 vezes", "(alta)",
		"sem nenhuma exibição: 1 (Anúncio parado)",
		"O que eu notei:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("faltou %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Teste pequeno") {
		t.Errorf("anúncio com pouco gasto não entra no melhor/pior:\n%s", text)
	}
	if len(sum.Observations) != diagMaxObservations {
		t.Errorf("observações = %d, want %d: %v", len(sum.Observations), diagMaxObservations, sum.Observations)
	}
	if sum.LowData {
		t.Error("R$ 800 e 40 conversas não é pouco dado")
	}
	if strings.Contains(text, "pouco dado") {
		t.Errorf("sem ressalva quando há dado suficiente:\n%s", text)
	}
}

func TestDiagnosticLowDataCaveatAndNoComparison(t *testing.T) {
	d := diagData()
	d.Total = meta.AdPerformance{SpendCents: 4000, Impressions: 3000, Conversations: 2, Frequency: 1.3}
	d.Ads = []meta.AdPerformance{{AdID: "A1", AdName: "Único", SpendCents: 1500, Impressions: 3000, Conversations: 2}}
	text, sum := buildDiagnostic(d)
	if !sum.LowData || !strings.Contains(text, "Ressalva: é pouco dado (R$ 40,00 gastos e 2 conversas)") {
		t.Fatalf("sem ressalva:\n%s", text)
	}
	if !strings.Contains(text, "nenhum anúncio gastou o bastante") || strings.Contains(text, "Pior anúncio") {
		t.Errorf("com pouco gasto por anúncio não compara:\n%s", text)
	}
	if !strings.Contains(text, "Não achei nada fora do normal") {
		t.Errorf("sem observação forçada:\n%s", text)
	}
}

func TestDiagnosticSpendWithoutConversationsExplainsMetric(t *testing.T) {
	d := diagData()
	d.Total = meta.AdPerformance{SpendCents: 50000, Impressions: 30000}
	d.Ads = []meta.AdPerformance{{AdID: "A1", AdName: "Vendas", SpendCents: 50000, Impressions: 30000}}
	text, _ := buildDiagnostic(d)
	if !strings.Contains(text, "nenhuma conversa registrada") || !strings.Contains(text, "campanha de mensagem") {
		t.Fatalf("%s", text)
	}
}

func TestDiagnosticWithoutActiveListSkipsStalledPart(t *testing.T) {
	d := diagData()
	d.ActiveKnown = false
	d.Active = []meta.ActiveAd{{ID: "A9", Name: "Parado"}}
	d.Total = meta.AdPerformance{SpendCents: 20000, Impressions: 30000, Conversations: 20}
	d.Ads = []meta.AdPerformance{{AdID: "A1", AdName: "X", SpendCents: 20000, Impressions: 30000, Conversations: 20}}
	text, sum := buildDiagnostic(d)
	if strings.Contains(text, "sem nenhuma exibição") || sum.StalledAds != 0 {
		t.Fatalf("lista ilegível não pode virar afirmação:\n%s", text)
	}
}

func TestDiagnosticForeignCurrency(t *testing.T) {
	d := diagData()
	d.Currency = "USD"
	d.Total = meta.AdPerformance{SpendCents: 10000, Impressions: 1000, Conversations: 5}
	text, _ := buildDiagnostic(d)
	if !strings.Contains(text, "100.00 USD") || strings.Contains(text, "R$") {
		t.Fatalf("moeda da conta: %s", text)
	}
}
