package shield

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// rejectRule liga um trecho do motivo da Meta a uma explicação simples e ao que mudar no anúncio.
type rejectRule struct {
	key   string
	re    *regexp.Regexp
	plain string
	tip   string
}

func rr(key, pattern, plain, tip string) rejectRule {
	return rejectRule{key: key, re: regexp.MustCompile(pattern), plain: plain, tip: tip}
}

// As regras leem o motivo sem acento e em minúsculas (a Meta devolve em inglês ou português, conforme a conta).
var rejectRules = []rejectRule{
	rr("pessoal", `personal attribut|atributos pessoais|your (race|religion|age|gender|health|financ|sexual|disabilit)|sua (raca|religiao|idade|saude|situacao financeira)`,
		"o texto fala com a pessoa como se soubesse algo pessoal dela (idade, saúde, dinheiro, religião, vida íntima)",
		"fale do serviço, não da pessoa: troque \"Você está endividado?\" por \"Ajudamos a organizar as contas\""),
	rr("saude", `health|saude|weight loss|peso|emagrec|before and after|antes e depois|cosmetic|estetic|cirurgia`,
		"o anúncio promete resultado no corpo ou na saúde, ou usa antes e depois",
		"tire o antes e depois e a promessa de resultado; descreva o serviço sem prometer o que a pessoa vai conseguir"),
	rr("financeiro", `financ|credit|credito|emprestimo|loan|investiment|ganhe dinheiro|renda extra|get rich|cripto|crypto|apostas|gambling`,
		"o anúncio fala de dinheiro, crédito ou ganho de uma forma que a Meta restringe",
		"não prometa lucro, renda ou aprovação de crédito; mostre o serviço e deixe os números para a conversa"),
	rr("categoria", `special ad categor|categoria especial|housing|habitacao|moradia|imovel|imoveis|real estate|emprego|employment`,
		"anúncios de imóvel, crédito e emprego têm regras próprias na Meta (categoria especial)",
		"fale com a equipe: esses anúncios precisam ser declarados na categoria certa e não podem segmentar por idade, gênero ou CEP"),
	rr("texto_imagem", `text in image|too much text|texto na imagem|texto demais|muito texto|image text`,
		"a imagem tem texto demais",
		"deixe pouco texto na imagem (o texto principal vai na legenda) e use uma foto mais limpa"),
	rr("destino", `landing|destination|pagina de destino|destino|broken link|link quebrado|nao funciona|nao abre`,
		"o link ou a página de destino tem problema (não abre, não bate com o anúncio ou é vazia)",
		"confira que o link abre no celular e que a página mostra o mesmo que o anúncio promete"),
	rr("promessa", `misleading|enganos|false|falso|exaggerat|garantid|guarantee|milagr|100%|resultado garantido|sensational|clickbait`,
		"o texto tem promessa exagerada, garantia ou chamada que a Meta considera enganosa",
		"tire \"garantido\", \"100%\", \"milagre\" e afins; conte o que você faz, sem prometer resultado"),
	rr("adulto", `adult|sexual|nudity|nudez|erotic|sensual|suggestive|sugestiv`,
		"a imagem ou o texto foi lido como conteúdo adulto ou sugestivo",
		"troque a imagem por outra mais neutra, sem corpo em destaque nem poses sugestivas"),
	rr("restrito", `weapon|armas?|armamento|drug|droga|tobacco|tabaco|cigarro|vape|alcohol|alcool|bebida`,
		"o anúncio envolve um produto que a Meta proíbe ou restringe",
		"confirme com a equipe se o produto pode ser anunciado; se puder, mostre só o que for permitido"),
	rr("discriminacao", `discriminat|discrimin|hate|hateful|odio`,
		"o conteúdo foi lido como discriminatório",
		"revise o texto e as imagens e tire qualquer trecho que exclua ou ataque um grupo de pessoas"),
	rr("propriedade", `copyright|intellectual property|propriedade intelectual|trademark|marca registrada|direitos autorais`,
		"o anúncio usa marca, imagem ou música de terceiros",
		"use só imagens e músicas suas ou com licença, e não use o nome de outras marcas"),
}

// Explanation é a reprovação traduzida: as causas reconhecidas, o que mudar e, se houver, a sugestão de texto.
type Explanation struct {
	Causes []string // causas em português simples (uma por regra reconhecida)
	Tips   []string // o que mudar, na mesma ordem
	Known  bool     // pelo menos uma causa reconhecida
	// SuggestedHeadline e SuggestedText vêm do ajuste de copy (copylab), quando ligado e quando mudou algo.
	SuggestedHeadline string
	SuggestedText     string
}

// First é a primeira causa, "" se nenhuma.
func (e Explanation) First() string {
	if len(e.Causes) > 0 {
		return e.Causes[0]
	}
	return ""
}

var foldTransformer = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

func fold(s string) string {
	out, _, err := transform.String(foldTransformer, strings.ToLower(s))
	if err != nil {
		return strings.ToLower(s)
	}
	return out
}

// ExplainRejection traduz o motivo da reprovação (texto da Meta, em qualquer idioma) para português simples.
// Motivo vazio ou desconhecido vira uma explicação genérica honesta; nunca inventa a causa.
func ExplainRejection(reasons []string) Explanation {
	text := fold(strings.Join(reasons, " "))
	var ex Explanation
	for _, r := range rejectRules {
		if len(ex.Causes) >= 3 {
			break
		}
		if r.re.MatchString(text) {
			ex.Causes = append(ex.Causes, r.plain)
			ex.Tips = append(ex.Tips, r.tip)
		}
	}
	ex.Known = len(ex.Causes) > 0
	if !ex.Known {
		ex.Causes = []string{"a Meta apontou violação das políticas de publicidade, mas não disse exatamente qual trecho"}
		ex.Tips = []string{"revise o texto e a imagem procurando promessa de resultado, texto demais na imagem e dados pessoais; se o anúncio parecer correto, peça nova análise pelo Gerenciador de Anúncios"}
	}
	return ex
}

// Text monta a explicação para a Mia mandar. Vazio quando não há o que acrescentar.
func (e Explanation) Text() string {
	var b strings.Builder
	b.WriteString("Em português simples: ")
	if e.Known {
		b.WriteString("a Meta entendeu que ")
	}
	for i, c := range e.Causes {
		if i > 0 {
			b.WriteString("; além disso, ")
		}
		b.WriteString(c)
	}
	b.WriteString(".\n\n*O que mudar:*")
	for _, t := range e.Tips {
		b.WriteString("\n• " + capitalize(t) + ".")
	}
	if e.SuggestedText != "" || e.SuggestedHeadline != "" {
		b.WriteString("\n\n*Sugestão de texto:*")
		if e.SuggestedHeadline != "" {
			b.WriteString("\nTítulo: " + e.SuggestedHeadline)
		}
		if e.SuggestedText != "" {
			b.WriteString("\nTexto: " + e.SuggestedText)
		}
	}
	return b.String()
}

func capitalize(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

// CopyFixer é o ajuste de copy (internal/copylab.Fix entra aqui): recebe título, texto e segmento do
// anúncio e devolve a versão ajustada, e se mudou algo. Fica como interface para o escudo não depender do
// pacote enquanto ele muda; nil = sem sugestão de texto.
type CopyFixer func(headline, text, segment string) (string, string, bool)

// ExplainWithCopy traduz o motivo e, com um CopyFixer e o texto que foi ao ar, sugere o ajuste do texto.
func ExplainWithCopy(ctx context.Context, reasons []string, fix CopyFixer, headline, text, segment string) Explanation {
	ex := ExplainRejection(reasons)
	if fix == nil || (strings.TrimSpace(headline) == "" && strings.TrimSpace(text) == "") || ctx.Err() != nil {
		return ex
	}
	if h, t, changed := fix(headline, text, segment); changed {
		ex.SuggestedHeadline, ex.SuggestedText = h, t
	}
	return ex
}
