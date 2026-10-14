// Package copylab é o laboratório de copy: checagens determinísticas (sem LLM) sobre o texto do anúncio.
//
// Regra do produto: a IA ajuda na copy, nunca na estratégia, e o cliente decide tudo. Este pacote
// só aponta riscos de política (Meta e conselhos profissionais), troca trechos quando existe um
// equivalente seguro e mecânico, e classifica o ângulo de cada variação. Nunca bloqueia publicação.
package copylab

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// RulesVersion identifica a lista de padrões de risco. Suba ao mudar qualquer regra.
const RulesVersion = "2026-10-14.1"

const (
	MaxHeadline = 40
	MaxText     = 500
)

type Level string

const (
	// Block: texto com alta chance de reprovação (Meta ou conselho profissional). Merece aviso forte.
	Block Level = "bloqueia"
	// Attention: pode ser reprovado ou soar mal; vale revisar.
	Attention Level = "atenção"
)

// Finding é um achado do checador.
type Finding struct {
	Level       Level  `json:"level"`
	Field       string `json:"field"`       // "headline" ou "text"
	Excerpt     string `json:"excerpt"`     // trecho original
	Reason      string `json:"reason"`      // por que é risco
	Alternative string `json:"alternative"` // sugestão segura
	Rule        string `json:"rule"`
}

type rule struct {
	id       string
	level    Level
	re       *regexp.Regexp // roda sobre o texto sem acento e minúsculo
	unless   *regexp.Regexp // se casar no texto todo (título + texto), a regra não vale
	reason   string
	alt      string
	replace  string   // troca mecânica segura; vazio = sem troca automática
	segments []string // vazio = todos; senão "advocacia" e/ou "saude"
}

func rx(p string) *regexp.Regexp { return regexp.MustCompile(p) }

const (
	segAdvocacia = "advocacia"
	segSaude     = "saude"
)

const (
	motivoMeta      = "A Meta reprova anúncios com essa linguagem. "
	motivoAdvocacia = "O Provimento 205/2021 da OAB restringe essa linguagem na publicidade de advocacia. "
	motivoSaude     = "Os conselhos de saúde (CFM, CFO, CFP, CFN) restringem essa linguagem na publicidade. "
)

var rules = []rule{
	// Promessa de resultado garantido.
	{id: "promessa_garantida", level: Block,
		re:      rx(`\b(resultados?|sucesso|lucro|retorno|vendas|aprovacao|emagrecimento)\s+(100%\s*)?garantid[oa]s?\b`),
		reason:  motivoMeta + "Promessa de resultado garantido.",
		alt:     "Fale do que você faz e do cuidado no atendimento, sem prometer resultado.",
		replace: "acompanhamento de perto"},
	{id: "cem_garantido", level: Block,
		re:      rx(`\b100%\s*garantid[oa]s?\b`),
		reason:  motivoMeta + "Garantia absoluta de resultado.",
		alt:     "Troque por algo que você pode cumprir (ex.: \"com compromisso\").",
		replace: "com compromisso"},
	{id: "garantimos_resultado", level: Block,
		re:      rx(`\bgarantimos\s+(o\s+|os\s+|a\s+|as\s+|seu\s+|sua\s+)?(resultados?|sucesso|lucro|retorno|vendas)\b`),
		reason:  motivoMeta + "Promessa de resultado garantido.",
		alt:     "Diga como você trabalha (ex.: \"trabalhamos com dedicação\").",
		replace: "trabalhamos com dedicação"},
	{id: "garantia_resultado", level: Block,
		re:     rx(`\bgarantia\s+de\s+(resultados?|sucesso|lucro|cura)\b`),
		reason: motivoMeta + "Promessa de resultado garantido.",
		alt:    "Descreva o serviço sem prometer o resultado."},

	// Antes e depois.
	{id: "antes_depois", level: Block,
		re:     rx(`\bantes\s*(e|x|/|&)\s*(o\s+)?depois\b`),
		reason: motivoMeta + "Comparação de antes e depois (corpo, saúde, finanças) é restrita.",
		alt:    "Mostre o ambiente, o serviço ou o produto, sem comparação de antes e depois."},

	// Atributos pessoais.
	{id: "atributo_pessoal", level: Block,
		re:     rx(`\bvoce\s+(esta|ta|tem|sofre\s+de|possui|ainda\s+(esta|tem))\s+(acima\s+do\s+peso|gord[oa]|obes[oa]|endividad\w+|desempregad\w+|deprimid\w+|depressiv\w+|doente|calv\w+|careca|sozinh[oa]|falid[oa]|com\s+(depressao|ansiedade|diabetes|cancer|dividas?|celulite|acne|obesidade|doenca\w*|problemas?\s+de\s+saude|sobrepeso))\b`),
		reason: motivoMeta + "Afirma algo pessoal e sensível sobre quem lê (saúde, peso, finanças).",
		alt:    "Fale do assunto em geral (ex.: \"Para quem quer cuidar do peso\"), sem apontar a pessoa."},
	{id: "atributo_pessoal_sua", level: Block,
		re:     rx(`\b(sua|seu)\s+(doenca|obesidade|depressao|ansiedade|divida|dividas|calvicie|impotencia|disfuncao|sobrepeso)\b`),
		reason: motivoMeta + "Atribui uma condição pessoal a quem lê.",
		alt:    "Fale da condição de forma geral, sem \"sua/seu\"."},

	// Saúde, emagrecimento, renda.
	{id: "perda_peso_numero", level: Block,
		re:     rx(`\b(perca|emagreca|elimine|queime)\s+(ate\s+)?\d+\s*(kg|quilos?)\b`),
		reason: motivoMeta + "Promessa de perda de peso com número.",
		alt:    "Fale do acompanhamento ou do método, sem prometer quilos."},
	{id: "cura", level: Block,
		re:     rx(`\b(curamos|cura\s+(definitiva|garantida|total|rapida)|curar\s+(o\s+|a\s+|seu\s+|sua\s+)?(cancer|diabetes|depressao|ansiedade|calvicie))\b`),
		reason: motivoMeta + "Promessa de cura.",
		alt:    "Descreva o atendimento e o cuidado, sem prometer cura."},
	{id: "renda_numero", level: Block,
		re:     rx(`\b(ganhe|fature|lucre|receba)\b.{0,20}r\$\s*[\d.,]+\s*(por|ao|/)\s*(dia|semana|mes)\b`),
		reason: motivoMeta + "Promessa de renda com valor.",
		alt:    "Fale do produto ou serviço, sem prometer ganho financeiro."},
	{id: "dinheiro_facil", level: Block,
		re:     rx(`\b(dinheiro|lucro|renda)\s+(facil|rapido)\b|\b(ficar|fique)\s+rico\b|\benriquec\w+`),
		reason: motivoMeta + "Promessa de dinheiro fácil ou enriquecimento.",
		alt:    "Fale do produto ou serviço, sem prometer ganho financeiro."},
	{id: "renda_extra", level: Attention,
		re:     rx(`\b(renda\s+extra|ganhar\s+dinheiro|liberdade\s+financeira|trabalhe\s+de\s+casa)\b`),
		reason: "Anúncios sobre renda são analisados com rigor pela Meta.",
		alt:    "Seja específico sobre o que a pessoa recebe, sem insinuar ganho."},
	{id: "emagrecimento", level: Attention,
		re:     rx(`\b(emagrec\w+|perda\s+de\s+peso|queima\s+de\s+gordura)\b`),
		reason: "Temas de peso e emagrecimento passam por revisão rigorosa da Meta.",
		alt:    "Evite números, prazos e comparação; foque no acompanhamento."},
	{id: "sem_efeito", level: Attention,
		re:     rx(`\b(sem\s+efeitos?\s+colaterais?|sem\s+dor|indolor|milagr\w+|rejuvenesc\w+)\b`),
		reason: "Alegação de saúde que pode ser vista como exagerada.",
		alt:    "Descreva o procedimento de forma neutra."},

	// Grátis sem condição clara.
	{id: "gratis_enganoso", level: Attention,
		re:     rx(`\b(gratis|gratuit[oa]s?|de\s+graca)\b`),
		unless: rx(`\b(na\s+compra|para\s+quem|mediante|a\s+partir\s+de|primeir[oa]|brinde|frete|teste|orcamento|avaliacao|diagnostico|condico\w*|ate\s+\d|consulta|nas?\s+(compras?|primeir\w+))\b|\*`),
		reason: "\"Grátis\" sem condição clara pode ser considerado enganoso.",
		alt:    "Diga o que é grátis e em que condição (ex.: \"orçamento grátis\", \"frete grátis acima de R$ 80\")."},

	// Pontuação exagerada.
	{id: "pontuacao", level: Attention,
		re:     rx(`[!?]{3,}`),
		reason: "Pontuação exagerada reduz a aprovação e a credibilidade.",
		alt:    "Use no máximo uma exclamação."},

	// Advocacia (OAB).
	{id: "adv_exito", level: Block, segments: []string{segAdvocacia},
		re:     rx(`\b(causa\s+ganha|exito\s+garantido|100%\s+de\s+exito|ganhamos\s+(\d+|todas|quase\s+todas)|sucesso\s+nas\s+causas|vitoria\s+garantida)\b`),
		reason: motivoAdvocacia + "Promessa ou divulgação de êxito.",
		alt:    "Apresente a área de atuação e como é o atendimento."},
	{id: "adv_mercantil", level: Block, segments: []string{segAdvocacia},
		re:     rx(`\b(promocao|desconto|gratis|gratuit[oa]|brinde|sorteio|cashback|oferta\s+imperdivel|black\s+friday|primeira\s+consulta)\b`),
		reason: motivoAdvocacia + "Promoção, desconto ou gratuidade é captação mercantilizada.",
		alt:    "Informe a área de atuação e o canal de contato, sem preço ou benefício."},
	{id: "adv_superlativo", level: Block, segments: []string{segAdvocacia},
		re:     rx(`\b(melhor|maior|numero\s*1|n[o]\s*1|lider|numero\s+um)\b.{0,15}\b(advogad\w+|escritorio|advocacia)\b`),
		reason: motivoAdvocacia + "Autopromoção com superlativo.",
		alt:    "Fale de tempo de atuação e áreas atendidas, sem comparar."},

	// Saúde (CFM, CFO, CFP, CFN).
	{id: "saude_promocao", level: Block, segments: []string{segSaude},
		re:     rx(`\b(promocao|desconto|sorteio|brinde|cashback|oferta\s+imperdivel|black\s+friday|\d+\s*%\s*off)\b`),
		reason: motivoSaude + "Promoção, desconto e sorteio de consulta ou procedimento.",
		alt:    "Apresente o serviço e como agendar, sem preço promocional."},
	{id: "saude_superlativo", level: Block, segments: []string{segSaude},
		re:     rx(`\b(melhor|maior|numero\s*1|n[o]\s*1|lider|numero\s+um|unico)\b.{0,15}\b(medic\w+|dentista|clinica|psicolog\w+|nutri\w+|fisioterap\w+|tratamento)\b`),
		reason: motivoSaude + "Autopromoção com superlativo.",
		alt:    "Fale da formação, da especialidade e do atendimento."},
	{id: "saude_resultado", level: Block, segments: []string{segSaude},
		re:     rx(`\b(resultados?\s+(imediat\w+|rapid\w+|comprovad\w+|garantid\w+)|diagnostico\s+(gratis|gratuito)|sem\s+dor|indolor|sem\s+efeitos?\s+colaterais?)\b`),
		reason: motivoSaude + "Promessa de resultado ou ausência de dor e efeitos.",
		alt:    "Descreva o procedimento e o cuidado de forma neutra."},
}

// fold deixa o texto minúsculo e sem acento, preservando a quantidade de runas (índices alinham).
func fold(s string) string {
	r := []rune(s)
	for i, c := range r {
		c = unicode.ToLower(c)
		switch c {
		case 'á', 'à', 'â', 'ã', 'ä':
			c = 'a'
		case 'é', 'è', 'ê', 'ë':
			c = 'e'
		case 'í', 'ì', 'î', 'ï':
			c = 'i'
		case 'ó', 'ò', 'ô', 'õ', 'ö':
			c = 'o'
		case 'ú', 'ù', 'û', 'ü':
			c = 'u'
		case 'ç':
			c = 'c'
		case 'ñ':
			c = 'n'
		case 'º', '°':
			c = 'o'
		}
		r[i] = c
	}
	return string(r)
}

// segmentKind reconhece o segmento a partir do texto livre (nome do segmento, nicho).
func segmentKind(segment string) string {
	s := fold(segment)
	switch {
	case regexp.MustCompile(`advoc|juridic|direito|oab`).MatchString(s):
		return segAdvocacia
	case regexp.MustCompile(`saude|clinic|medic|dentist|odont|psicolog|nutri|fisioterap|dermat|fonoaudi|estetic|harmoniz|terapi`).MatchString(s):
		return segSaude
	}
	return ""
}

func (r rule) appliesTo(kind string) bool {
	if len(r.segments) == 0 {
		return true
	}
	for _, s := range r.segments {
		if s == kind {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}

// runeSpan converte um intervalo de bytes do texto sem acento em um intervalo de runas.
func runeSpan(folded string, start, end int) (int, int) {
	return utf8.RuneCountInString(folded[:start]), utf8.RuneCountInString(folded[:end])
}

// Check roda todas as regras sobre o título e o texto. segment é texto livre (nome do segmento e/ou
// nicho); quando reconhecido (advocacia, saúde), entram as regras dos conselhos profissionais.
func Check(headline, text, segment string) []Finding {
	kind := segmentKind(segment)
	all := fold(headline + " " + text)
	var out []Finding
	for _, f := range []struct{ name, s string }{{"headline", headline}, {"text", text}} {
		if strings.TrimSpace(f.s) == "" {
			continue
		}
		folded := fold(f.s)
		orig := []rune(f.s)
		for _, r := range rules {
			if !r.appliesTo(kind) {
				continue
			}
			if r.unless != nil && r.unless.MatchString(all) {
				continue
			}
			loc := r.re.FindStringIndex(folded)
			if loc == nil {
				continue
			}
			a, b := runeSpan(folded, loc[0], loc[1])
			out = append(out, Finding{Level: r.level, Field: f.name, Excerpt: clip(string(orig[a:b]), 60),
				Reason: r.reason, Alternative: r.alt, Rule: r.id})
		}
		out = append(out, formatFindings(f.name, f.s)...)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(headline)); n > MaxHeadline {
		out = append(out, Finding{Level: Block, Field: "headline", Excerpt: clip(headline, 60),
			Reason: "O título passa de 40 caracteres e a Meta corta ou reprova.", Alternative: "Encurte o título para até 40 caracteres.", Rule: "limite_headline"})
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(text)); n > MaxText {
		out = append(out, Finding{Level: Block, Field: "text", Excerpt: clip(text, 60),
			Reason: "O texto passa de 500 caracteres.", Alternative: "Encurte o texto para até 500 caracteres.", Rule: "limite_text"})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Level == Block && out[j].Level != Block })
	return out
}

// HasBlock diz se algum achado é de nível bloqueia.
func HasBlock(fs []Finding) bool {
	for _, f := range fs {
		if f.Level == Block {
			return true
		}
	}
	return false
}

func isEmoji(r rune) bool {
	return (r >= 0x1F300 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF) || (r >= 0x1F000 && r <= 0x1F2FF) || r == 0x2B50 || r == 0x2B55
}

// formatFindings cobre caixa-alta e emojis em excesso (regras de forma, não de vocabulário).
func formatFindings(field, s string) []Finding {
	var out []Finding
	letters, upper, emojis := 0, 0, 0
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		case isEmoji(r):
			emojis++
		}
	}
	if letters >= 10 && float64(upper)/float64(letters) > 0.6 {
		out = append(out, Finding{Level: Attention, Field: field, Excerpt: clip(s, 40),
			Reason: "Texto quase todo em caixa-alta é lido como grito e a Meta pode reprovar.", Alternative: "Use caixa-alta só em uma palavra de destaque.", Rule: "caixa_alta"})
	}
	maxEmoji := 3
	if field == "headline" {
		maxEmoji = 1
	}
	if emojis > maxEmoji {
		out = append(out, Finding{Level: Attention, Field: field, Excerpt: clip(s, 40),
			Reason: "Emojis em excesso parecem spam.", Alternative: "Deixe no máximo um emoji no título e três no texto.", Rule: "emojis"})
	}
	return out
}

// Fix troca os trechos de nível bloqueia que têm equivalente mecânico seguro. Devolve os textos novos
// e se algo mudou. Uma troca que estourasse o limite de caracteres é descartada.
func Fix(headline, text, segment string) (string, string, bool) {
	kind := segmentKind(segment)
	fix := func(s string, limit int) (string, bool) {
		cur, changed := s, false
		for _, r := range rules {
			if r.level != Block || r.replace == "" || !r.appliesTo(kind) {
				continue
			}
			if next, ok := replaceAll(cur, r); ok {
				cur, changed = next, true
			}
		}
		if changed && utf8.RuneCountInString(strings.TrimSpace(cur)) > limit && utf8.RuneCountInString(strings.TrimSpace(s)) <= limit {
			return s, false
		}
		return cur, changed
	}
	h, hc := fix(headline, MaxHeadline)
	t, tc := fix(text, MaxText)
	return h, t, hc || tc
}

func replaceAll(s string, r rule) (string, bool) {
	folded := fold(s)
	locs := r.re.FindAllStringIndex(folded, -1)
	if len(locs) == 0 {
		return s, false
	}
	orig := []rune(s)
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		a, e := runeSpan(folded, loc[0], loc[1])
		b.WriteString(string(orig[last:a]))
		rep := r.replace
		if a < len(orig) && unicode.IsUpper(orig[a]) {
			rr := []rune(rep)
			rr[0] = unicode.ToUpper(rr[0])
			rep = string(rr)
		}
		b.WriteString(rep)
		last = e
	}
	b.WriteString(string(orig[last:]))
	return b.String(), true
}

// Result é o que Apply devolve.
type Result struct {
	Copies   []contract.Copy
	Adjusted []int // índices (base 0) das variações trocadas por versão segura
}

// Apply roda o checador nas variações. Onde houver achado "bloqueia" com troca mecânica, substitui o
// texto. Também normaliza o ângulo: ângulo ausente ou inválido vira o da heurística quando há mais de
// uma variação. Não muda estratégia e não impede nada: o que não tem troca segura fica para o resumo.
func Apply(copies []contract.Copy, segment string) Result {
	res := Result{Copies: make([]contract.Copy, len(copies))}
	copy(res.Copies, copies)
	for i, c := range res.Copies {
		if HasBlock(Check(c.Headline, c.Text, segment)) {
			h, t, changed := Fix(c.Headline, c.Text, segment)
			if changed {
				res.Copies[i].Headline, res.Copies[i].Text = h, t
				res.Adjusted = append(res.Adjusted, i)
			}
		}
		if len(copies) > 1 {
			res.Copies[i].Angle = NormalizeAngle(res.Copies[i].Angle)
			if res.Copies[i].Angle == "" {
				res.Copies[i].Angle = Angle(res.Copies[i].Headline, res.Copies[i].Text)
			}
		} else {
			res.Copies[i].Angle = NormalizeAngle(res.Copies[i].Angle)
		}
	}
	return res
}
