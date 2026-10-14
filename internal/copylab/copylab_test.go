package copylab_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/copylab"
)

func rules(fs []copylab.Finding) map[string]copylab.Level {
	m := map[string]copylab.Level{}
	for _, f := range fs {
		m[f.Rule] = f.Level
	}
	return m
}

func TestCheckFlagsRisks(t *testing.T) {
	cases := []struct {
		name, headline, text, segment, rule string
		level                               copylab.Level
	}{
		{"resultado garantido", "Resultado garantido", "Fale com a gente", "", "promessa_garantida", copylab.Block},
		{"100% garantido", "Qualidade 100% garantida", "Peça já", "", "cem_garantido", copylab.Block},
		{"garantimos", "Seu site novo", "Garantimos o resultado em 30 dias.", "", "garantimos_resultado", copylab.Block},
		{"antes e depois", "Veja o antes e depois", "Transformação real", "", "antes_depois", copylab.Block},
		{"antes x depois", "Antes x depois", "", "", "antes_depois", copylab.Block},
		{"atributo pessoal", "Você está acima do peso?", "Conheça nosso plano", "", "atributo_pessoal", copylab.Block},
		{"sua doença", "Cuide da sua doença", "", "", "atributo_pessoal_sua", copylab.Block},
		{"perca kg", "Perca 10 kg em um mês", "", "", "perda_peso_numero", copylab.Block},
		{"cura", "Cura definitiva da ansiedade", "", "", "cura", copylab.Block},
		{"renda", "Ganhe R$ 500 por dia", "Comece hoje", "", "renda_numero", copylab.Block},
		{"dinheiro fácil", "Dinheiro fácil em casa", "", "", "dinheiro_facil", copylab.Block},
		{"renda extra", "Renda extra para você", "", "", "renda_extra", copylab.Attention},
		{"emagrecimento", "Emagrecimento com acompanhamento", "", "", "emagrecimento", copylab.Attention},
		{"grátis sem condição", "Tudo grátis", "Venha conhecer", "", "gratis_enganoso", copylab.Attention},
		{"pontuação", "Aproveite!!!", "", "", "pontuacao", copylab.Attention},
		{"caixa alta", "SUPER PROMOÇÃO IMPERDÍVEL", "", "", "caixa_alta", copylab.Attention},
		{"emojis", "Pizza", "🍕🍕🍕🍕 muito boa 🔥", "", "emojis", copylab.Attention},
		{"emoji no título", "Pizza 🍕🔥", "Peça agora", "", "emojis", copylab.Attention},
		{"título longo", strings.Repeat("a", 41), "ok", "", "limite_headline", copylab.Block},
		{"texto longo", "ok", strings.Repeat("a", 501), "", "limite_text", copylab.Block},
		{"advocacia êxito", "Escritório de direito", "Êxito garantido na sua causa", "Advocacia", "adv_exito", copylab.Block},
		{"advocacia promoção", "Advogado trabalhista", "Primeira consulta grátis", "escritório de advocacia", "adv_mercantil", copylab.Block},
		{"advocacia superlativo", "O melhor advogado da cidade", "", "advocacia", "adv_superlativo", copylab.Block},
		{"saúde promoção", "Clínica odontológica", "Desconto em limpeza", "Clínica odontológica", "saude_promocao", copylab.Block},
		{"saúde sem dor", "Procedimento sem dor", "Agende", "saúde", "saude_resultado", copylab.Block},
		{"saúde superlativo", "A melhor clínica da região", "", "clínica", "saude_superlativo", copylab.Block},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rules(copylab.Check(c.headline, c.text, c.segment))
			if lv, ok := got[c.rule]; !ok || lv != c.level {
				t.Errorf("regra %s nível %q esperada; achados: %v", c.rule, c.level, got)
			}
		})
	}
}

func TestCheckNoFalsePositives(t *testing.T) {
	cases := []struct{ name, headline, text, segment string }{
		{"resultado neutro", "Veja o resultado do jogo", "O resultado da rodada sai hoje à noite.", ""},
		{"resultado de exame", "Resultado do exame online", "Receba o resultado pelo WhatsApp.", "clínica"},
		{"garantia de produto", "Garantia de 12 meses", "Troca em até 7 dias.", ""},
		{"garantimos entrega", "Pizza quentinha", "Garantimos a entrega em 40 minutos.", ""},
		{"grátis com condição", "Frete grátis", "Frete grátis acima de R$ 80 na compra.", ""},
		{"orçamento grátis", "Orçamento grátis", "Peça o seu", ""},
		{"você neutro", "Você merece um café", "Você tem fome? Venha provar.", ""},
		{"antes neutro", "Abrimos antes das 8h", "Venha depois do trabalho também.", ""},
		{"promoção fora de saúde", "Promoção de pizza", "Desconto de 10% hoje", "pizzaria"},
		{"cura da pizza", "Massa curada por 48h", "Fermentação natural", ""},
		{"caixa alta curta", "Pizza SIM", "Peça pelo WhatsApp e receba rápido em casa.", ""},
		{"um emoji", "Pizza 🍕", "Peça pelo WhatsApp 😋", ""},
		{"peso neutro", "Balança de precisão", "Peso exato para sua cozinha.", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := copylab.Check(c.headline, c.text, c.segment); len(got) != 0 {
				t.Errorf("sem achados esperado, veio %+v", got)
			}
		})
	}
}

func TestSegmentRulesOnlyApplyToSegment(t *testing.T) {
	h, tx := "Desconto na primeira consulta", "Agende"
	if _, ok := rules(copylab.Check(h, tx, "pizzaria"))["adv_mercantil"]; ok {
		t.Error("regra de advocacia vazou para outro segmento")
	}
	if _, ok := rules(copylab.Check(h, tx, "pizzaria"))["saude_promocao"]; ok {
		t.Error("regra de saúde vazou para outro segmento")
	}
	if got := rules(copylab.Check(h, tx, "Saúde e bem-estar")); got["saude_promocao"] != copylab.Block {
		t.Errorf("saúde deveria bloquear desconto: %v", got)
	}
}

func TestFindingHasAllFields(t *testing.T) {
	fs := copylab.Check("Resultado garantido", "", "")
	if len(fs) == 0 {
		t.Fatal("sem achados")
	}
	f := fs[0]
	if f.Level != copylab.Block || f.Excerpt != "Resultado garantido" || f.Reason == "" || f.Alternative == "" || f.Field != "headline" {
		t.Errorf("achado incompleto: %+v", f)
	}
}

func TestBlocksComeFirst(t *testing.T) {
	fs := copylab.Check("Renda extra", "Antes e depois incrível", "")
	if len(fs) < 2 || fs[0].Level != copylab.Block {
		t.Errorf("bloqueia deveria vir primeiro: %+v", fs)
	}
}

func TestFixReplacesMechanically(t *testing.T) {
	h, tx, changed := copylab.Fix("Resultado garantido", "Garantimos o resultado em 30 dias. Peça já.", "")
	if !changed {
		t.Fatal("deveria trocar")
	}
	if h != "Acompanhamento de perto" {
		t.Errorf("headline = %q", h)
	}
	if !strings.Contains(tx, "trabalhamos com dedicação") && !strings.Contains(tx, "Trabalhamos com dedicação") {
		t.Errorf("text = %q", tx)
	}
	if fs := copylab.Check(h, tx, ""); copylab.HasBlock(fs) {
		t.Errorf("ainda bloqueia depois da troca: %+v", fs)
	}
}

func TestFixKeepsWhenNothingMechanical(t *testing.T) {
	h, tx, changed := copylab.Fix("Veja o antes e depois", "Transformação real", "")
	if changed || h != "Veja o antes e depois" || tx != "Transformação real" {
		t.Errorf("não deveria alterar: %q %q %v", h, tx, changed)
	}
}

func TestFixDiscardsReplacementOverLimit(t *testing.T) {
	in := "Resultado garantido na sua loja hoje!!" // 38 caracteres; a troca passaria de 40
	if n := len([]rune(in)); n != 38 {
		t.Fatalf("caso inválido: %d", n)
	}
	h, _, changed := copylab.Fix(in, "ok", "")
	if changed || h != in {
		t.Errorf("troca que estoura o limite deveria ser descartada: %q %v", h, changed)
	}
}

func TestApplyAdjustsAndLabels(t *testing.T) {
	in := []contract.Copy{
		{Headline: "Resultado garantido", Text: "Peça agora", CTA: "LEARN_MORE", Angle: "Prova social"},
		{Headline: "Pizza nova", Text: "Chegou o sabor novo", CTA: "LEARN_MORE", Angle: "xyz"},
		{Headline: "Pizza", Text: "Peça", CTA: "LEARN_MORE"},
	}
	res := copylab.Apply(in, "")
	if len(res.Adjusted) != 1 || res.Adjusted[0] != 0 {
		t.Errorf("adjusted = %v", res.Adjusted)
	}
	if res.Copies[0].Headline == in[0].Headline {
		t.Error("headline deveria ter sido trocada")
	}
	if in[0].Headline != "Resultado garantido" {
		t.Error("entrada foi alterada")
	}
	if res.Copies[0].Angle != copylab.AngleProva {
		t.Errorf("angle 0 = %q", res.Copies[0].Angle)
	}
	if res.Copies[1].Angle != copylab.AngleNovidade {
		t.Errorf("angle inválido deveria virar heurística: %q", res.Copies[1].Angle)
	}
}

func TestApplySingleCopyKeepsAngleEmpty(t *testing.T) {
	res := copylab.Apply([]contract.Copy{{Headline: "Pizza nova", Text: "Chegou", CTA: "LEARN_MORE"}}, "")
	if res.Copies[0].Angle != "" {
		t.Errorf("1 variação não precisa de ângulo: %q", res.Copies[0].Angle)
	}
}

func TestAngle(t *testing.T) {
	cases := map[string][2]string{
		copylab.AnglePreco:      {"Pizza a partir de R$ 29", "Parcele em 3x sem juros"},
		copylab.AngleUrgencia:   {"Só até domingo", "Últimas vagas, aproveite hoje"},
		copylab.AngleProva:      {"Mais de 500 clientes", "Avaliação nota 4,9, o mais pedido"},
		copylab.AngleDor:        {"Cansado de perder tempo?", "Chega de trabalheira com a limpeza"},
		copylab.AngleAutoridade: {"15 anos de experiência", "Equipe especializada e certificada"},
		copylab.AngleNovidade:   {"Novidade na cidade", "Lançamento: chegou o sabor novo"},
	}
	for want, c := range cases {
		if got := copylab.Angle(c[0], c[1]); got != want {
			t.Errorf("Angle(%q, %q) = %q, quer %q", c[0], c[1], got, want)
		}
	}
	if got := copylab.Angle("Pizza", "Peça pelo WhatsApp"); got != "" {
		t.Errorf("sem sinal deveria ser vazio, veio %q", got)
	}
}

func TestNormalizeAngle(t *testing.T) {
	for in, want := range map[string]string{"preço": "preco", "Urgência": "urgencia", "prova social": "prova_social", "dor": "dor", "x": "", "": ""} {
		if got := copylab.NormalizeAngle(in); got != want {
			t.Errorf("NormalizeAngle(%q) = %q, quer %q", in, got, want)
		}
	}
}
