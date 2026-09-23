package humanize_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/humanize"
)

func TestSplit(t *testing.T) {
	cases := map[string][]string{
		"":                           nil,
		"  oi  ":                     {"oi"},
		"Oi, Ana!\n\nQual a cidade?": {"Oi, Ana!", "Qual a cidade?"},
		"a\n \nb\n\nc":               {"a", "b", "c"},
		"a\n\nb\n\nc\n\nd\n\ne":      {"a", "b", "c\n\nd\n\ne"},
		"linha 1\nlinha 2":           {"linha 1\nlinha 2"},
	}
	for in, want := range cases {
		got := humanize.Split(in)
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("Split(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestTypingDelayBounds(t *testing.T) {
	if d := humanize.TypingDelay("ok", 0); d != humanize.MinTyping {
		t.Errorf("texto curto: %v; want %v", d, humanize.MinTyping)
	}
	for _, r := range []float64{0, 0.5, 1} {
		if d := humanize.TypingDelay(strings.Repeat("a", 2000), r); d != humanize.MaxTyping {
			t.Errorf("texto longo r=%v: %v; want %v", r, d, humanize.MaxTyping)
		}
	}
	// 100 runas, r=0.5: (1,2 s + 3,5 s) × 1,0 = 4,7 s
	if d := humanize.TypingDelay(strings.Repeat("é", 100), 0.5); d != 4700*time.Millisecond {
		t.Errorf("100 runas: %v; want 4.7s", d)
	}
}

func TestGap(t *testing.T) {
	if humanize.Gap(0) != 600*time.Millisecond || humanize.Gap(1) != 1400*time.Millisecond {
		t.Errorf("Gap fora dos limites: %v %v", humanize.Gap(0), humanize.Gap(1))
	}
}

func TestShouldVoice(t *testing.T) {
	if !humanize.ShouldVoice("Show! Em qual cidade fica a pizzaria?") {
		t.Error("resposta curta deveria virar áudio")
	}
	if humanize.ShouldVoice("   ") || humanize.ShouldVoice(strings.Repeat("a", humanize.MaxVoiceRunes+1)) {
		t.Error("vazio ou longo não vira áudio")
	}
}

func TestSpeechText(t *testing.T) {
	if got := humanize.SpeechText("Projeto *Pizzaria*\n\nQual o _público_?"); got != "Projeto Pizzaria. Qual o público?" {
		t.Errorf("SpeechText = %q", got)
	}
}
