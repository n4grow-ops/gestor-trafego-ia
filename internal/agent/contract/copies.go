package contract

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Limites da copy na proposta (os mesmos que o Gestor segue).
const (
	MaxHeadline = 40
	MaxText     = 500
)

// CleanCopies deixa só os textos que o cliente de fato fechou: sem texto vazio, sem repetição e no
// máximo MaxCopies. Título e texto acima do limite são cortados no fim da última palavra que cabe.
func CleanCopies(in []Copy) []Copy {
	var out []Copy
	for _, c := range in {
		c.Headline = clipWords(c.Headline, MaxHeadline)
		c.Text = clipWords(c.Text, MaxText)
		c.CTA = strings.ToUpper(strings.TrimSpace(c.CTA))
		if !slices.Contains(allowedCTAs, c.CTA) {
			c.CTA = ""
		}
		c.Angle = ""
		if c.Headline == "" && c.Text == "" {
			continue
		}
		if slices.ContainsFunc(out, func(o Copy) bool { return o.Headline == c.Headline && o.Text == c.Text }) {
			continue
		}
		out = append(out, c)
		if len(out) == MaxCopies {
			break
		}
	}
	return out
}

// ApplyClientCopies troca as variações do Gestor pelos textos do cliente. O que ele não escreveu
// (título ou texto) e a chamada sem escolha vêm da versão do Gestor na mesma posição (ou da primeira).
func ApplyClientCopies(client, gestor []Copy) []Copy {
	client = CleanCopies(client)
	if len(client) == 0 {
		return gestor
	}
	out := make([]Copy, len(client))
	for i, c := range client {
		var g Copy
		switch {
		case i < len(gestor):
			g = gestor[i]
		case len(gestor) > 0:
			g = gestor[0]
		}
		if c.Headline == "" {
			c.Headline = g.Headline
		}
		if c.Text == "" {
			c.Text = g.Text
		}
		if c.CTA == "" {
			c.CTA = g.CTA
		}
		out[i] = c
	}
	return out
}

func clipWords(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	if i := strings.LastIndexAny(string(r), " \n"); i > n/2 {
		return strings.TrimSpace(string(r)[:i])
	}
	return strings.TrimSpace(string(r))
}
