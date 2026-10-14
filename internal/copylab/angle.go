package copylab

import (
	"regexp"
	"strings"
)

// Ângulos de copy aceitos (ids ASCII, usados no campo "angle" do contrato).
const (
	AnglePreco      = "preco"
	AngleUrgencia   = "urgencia"
	AngleProva      = "prova_social"
	AngleDor        = "dor"
	AngleAutoridade = "autoridade"
	AngleNovidade   = "novidade"
)

// Angles lista os ângulos, na ordem de desempate da heurística.
var Angles = []string{AnglePreco, AngleUrgencia, AngleProva, AngleDor, AngleAutoridade, AngleNovidade}

// AngleLabel é o nome em português para o cliente.
var AngleLabel = map[string]string{
	AnglePreco: "preço", AngleUrgencia: "urgência", AngleProva: "prova social",
	AngleDor: "dor", AngleAutoridade: "autoridade", AngleNovidade: "novidade",
}

var angleRes = map[string]*regexp.Regexp{
	AnglePreco:      regexp.MustCompile(`r\$\s*\d|\d+\s*%\s*(off|de\s+desconto)|\bdescont\w+|\bpromocao\b|\ba\s+partir\s+de\b|\bbarat\w+|\beconomi\w+|\bparcel\w+|\bpreco\w*|\bsem\s+juros\b|\b\d+x\b|\bcabe\s+no\s+bolso\b`),
	AngleUrgencia:   regexp.MustCompile(`\bhoje\b|\bultim[oa]s?\s+(vagas?|unidades?|dias?|chance)|\bso\s+ate\b|\bate\s+(amanha|sexta|domingo|\d)|\bcorra\b|\baproveite\b|\bvagas?\s+limitad\w+|\btempo\s+limitado\b|\brestam\b|\bapenas\s+\d+|\bagora\b|\bnao\s+perca\b`),
	AngleProva:      regexp.MustCompile(`\bclientes?\b|\bavalia\w+|\bnota\s+\d|\d+\s*(mil\s+)?(pessoas|clientes|familias|pacientes|pedidos)|\bdepoimentos?\b|\brecomend\w+|\bmais\s+pedid\w+|\bfavorit\w+|\bestrelas?\b|\bqueridinh\w+|\bquem\s+(ja\s+)?(provou|experimentou|usou)\b`),
	AngleDor:        regexp.MustCompile(`\bcansad\w+|\bchega\s+de\b|\bproblemas?\b|\bdor\b|\bdores\b|\bpreocup\w+|\bsem\s+tempo\b|\bestress\w+|\bdificil\b|\bsofr\w+|\bdor\s+de\s+cabeca\b|\btrabalheira\b|\bcansa\b`),
	AngleAutoridade: regexp.MustCompile(`\banos\s+de\s+(experiencia|mercado|tradicao|estrada)|\bespecialista\w*|\bcertificad\w+|\bprofissionais?\b|\breferencia\b|\bdesde\s+(19|20)\d{2}\b|\bpremiad\w+|\btradicao\b|\bexperiencia\b|\bformad\w+|\bequipe\s+(especializada|qualificada)`),
	AngleNovidade:   regexp.MustCompile(`\bnov[oa]s?\b|\bnovidades?\b|\blancamento\b|\bchegou\b|\bchegaram\b|\bestreia\b|\binedit\w+|\bconheca\b|\bacabou\s+de\s+chegar\b`),
}

// Angle classifica o ângulo dominante da variação por heurística de palavras. Vazio = nenhum sinal.
// Empate vai para o primeiro da lista Angles.
func Angle(headline, text string) string {
	s := fold(headline + " " + text)
	best, bestN := "", 0
	for _, a := range Angles {
		if n := len(angleRes[a].FindAllStringIndex(s, -1)); n > bestN {
			best, bestN = a, n
		}
	}
	return best
}

// NormalizeAngle aceita rótulos em português ("preço", "Prova social") e devolve o id, ou vazio se inválido.
func NormalizeAngle(a string) string {
	s := strings.ReplaceAll(strings.TrimSpace(fold(a)), " ", "_")
	switch s {
	case "preco", "urgencia", "prova_social", "dor", "autoridade", "novidade":
		return s
	case "prova":
		return AngleProva
	}
	return ""
}
