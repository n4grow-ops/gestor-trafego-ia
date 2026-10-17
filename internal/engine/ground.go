package engine

import (
	"strings"
	"unicode"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// numberWords cobre orçamento dito por extenso ("cinquenta por dia") e em áudio transcrito.
var numberWords = []string{"dez", "vinte", "trinta", "quarenta", "cinquenta", "sessenta", "setenta", "oitenta",
	"noventa", "cem", "cento", "duzentos", "trezentos", "quinhentos", "mil", "quinze", "doze", "vinte e cinco"}

// groundFields tira da resposta da IA o orçamento e o local que não aparecem na mensagem do cliente.
// O modelo às vezes "completa" a campanha com valor inventado ("R$ 50", "cidade onde fica a pizzaria"),
// e campanha completa dispara a proposta: o dado precisa ter vindo do cliente.
// known são lugares que o cliente já deu antes (cidade e área da ficha do negócio).
func groundFields(f contract.Fields, message string, known ...string) (contract.Fields, []string) {
	msg := normalizeName(message)
	places := normalizeName(message + " " + strings.Join(known, " "))
	var dropped []string
	if f.DailyBudgetBRL != nil && !mentionsNumber(msg) {
		f.DailyBudgetBRL = nil
		dropped = append(dropped, "daily_budget_brl")
	}
	if f.Location != nil && strings.TrimSpace(*f.Location) != "" && !mentionsPlace(places, normalizeName(*f.Location)) {
		f.Location = nil
		dropped = append(dropped, "location")
	}
	return f, dropped
}

func mentionsNumber(msg string) bool {
	if strings.ContainsFunc(msg, unicode.IsDigit) {
		return true
	}
	for _, w := range numberWords {
		if containsWord(msg, w) {
			return true
		}
	}
	return false
}

// mentionsPlace: alguma palavra significativa do local (3+ letras, fora "cidade", "regiao"...) está na mensagem.
func mentionsPlace(msg, loc string) bool {
	skip := map[string]bool{"cidade": true, "regiao": true, "bairro": true, "estado": true, "raio": true, "km": true,
		"onde": true, "fica": true, "dos": true, "das": true, "com": true, "sem": true, "toda": true, "todo": true}
	for _, w := range strings.FieldsFunc(loc, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if len([]rune(w)) >= 3 && !skip[w] && containsWord(msg, w) {
			return true
		}
	}
	return false
}

func containsWord(s, w string) bool {
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if t == w || (len(w) >= 5 && strings.HasPrefix(t, w)) {
			return true
		}
	}
	return strings.Contains(s, w) && strings.Contains(w, " ") // expressão com espaço ("vinte e cinco")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
