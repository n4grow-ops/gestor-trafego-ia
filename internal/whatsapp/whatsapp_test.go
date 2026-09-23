package whatsapp_test

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

func TestNormalizePhone(t *testing.T) {
	ok := map[string]string{
		"+55 (11) 98888-7777": "5511988887777",
		"551188887777":        "5511988887777", // celular sem o nono dígito
		"551133334444":        "551133334444",  // fixo fica igual
		"14155552671":         "14155552671",   // fora do Brasil fica igual
	}
	for in, want := range ok {
		got, err := whatsapp.NormalizePhone(in)
		if err != nil || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"123", "", "1234567890123456"} {
		if _, err := whatsapp.NormalizePhone(in); err == nil {
			t.Errorf("NormalizePhone(%q) deveria falhar", in)
		}
	}
}

// O destino do operador precisa ser exatamente o número registrado no WhatsApp:
// Digits não insere o nono dígito, ao contrário de NormalizePhone.
func TestDigitsKeepsNumberAsTyped(t *testing.T) {
	for in, want := range map[string]string{
		"+55 (11) 8888-7777":  "551188887777",
		"+55 (11) 98888-7777": "5511988887777",
		"":                    "",
	} {
		if got := whatsapp.Digits(in); got != want {
			t.Errorf("Digits(%q) = %q, want %q", in, got, want)
		}
	}
}
