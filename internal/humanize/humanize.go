// Package humanize guarda as regras que deixam a resposta com cara de gente: balões curtos,
// tempo de digitação e quando responder em áudio. Não faz I/O; a aleatoriedade vem de fora.
package humanize

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxParts      = 3
	MaxVoiceRunes = 600
	MinTyping     = 1500 * time.Millisecond
	MaxTyping     = 7 * time.Second
)

var blankLine = regexp.MustCompile(`\n[ \t]*\n`)

// Split corta a resposta em balões nas linhas em branco; o que passar de MaxParts vai no último.
func Split(reply string) []string {
	var parts []string
	for _, p := range blankLine.Split(strings.TrimSpace(reply), -1) {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) > MaxParts {
		parts = append(parts[:MaxParts-1], strings.Join(parts[MaxParts-1:], "\n\n"))
	}
	return parts
}

// TypingDelay é quanto tempo mostrar "digitando…" antes de um balão. r em [0, 1] dá a variação.
func TypingDelay(text string, r float64) time.Duration {
	base := 1200*time.Millisecond + time.Duration(utf8.RuneCountInString(text))*35*time.Millisecond
	d := time.Duration(float64(base) * (0.85 + 0.3*r))
	return min(max(d, MinTyping), MaxTyping)
}

// Gap é a pausa entre dois balões seguidos.
func Gap(r float64) time.Duration {
	return 600*time.Millisecond + time.Duration(r*float64(800*time.Millisecond))
}

// ChainDuration é o tempo máximo que os balões de uma resposta levam para sair (pior caso).
func ChainDuration(text string) time.Duration {
	parts := Split(text)
	var d time.Duration
	for i, p := range parts {
		d += TypingDelay(p, 1)
		if i > 0 {
			d += Gap(1)
		}
	}
	return d
}

// ShouldVoice diz se o texto cabe numa mensagem de voz.
func ShouldVoice(text string) bool {
	n := utf8.RuneCountInString(strings.TrimSpace(text))
	return n > 0 && n <= MaxVoiceRunes
}

var markup = strings.NewReplacer("*", "", "_", "", "~", "")

// SpeechText prepara o texto para o TTS: sem marcação do WhatsApp e com os balões virando frases.
func SpeechText(text string) string {
	parts := Split(markup.Replace(text))
	for i, p := range parts {
		p = strings.Join(strings.Fields(p), " ")
		if last, _ := utf8.DecodeLastRuneInString(p); !strings.ContainsRune(".!?", last) {
			p += "."
		}
		parts[i] = p
	}
	return strings.Join(parts, " ")
}
