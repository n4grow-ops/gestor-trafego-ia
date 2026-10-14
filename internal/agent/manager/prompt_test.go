package manager

import (
	"strings"
	"testing"
)

func TestSystemPromptKeepsClientInControl(t *testing.T) {
	p := systemPrompt("")
	for _, want := range []string{"vale exatamente como veio", "NUNCA entram na campanha", "Se quiser, dá para", "copy_variations", "sem pedido, gere 1 variação", "interests vazio"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt sem %q", want)
		}
	}
	for _, bad := range []string{"recomende a estratégia", "de 1 a 3 variações"} {
		if strings.Contains(p, bad) {
			t.Errorf("prompt ainda contém %q", bad)
		}
	}
}

func TestSystemPromptAsksDifferentAnglesAndNoInvention(t *testing.T) {
	p := systemPrompt("")
	for _, want := range []string{`"angle"`, "prova_social", "ângulo diferente", "não invente preço", "antes e depois", "específica do negócio"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt sem %q", want)
		}
	}
}
