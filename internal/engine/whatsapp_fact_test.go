package engine

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Caso real (Jonathan, 24/10): sem número lido, a Mia inventou um. O contexto diz o que se sabe e proíbe citar número.
func TestWhatsAppFact(t *testing.T) {
	if f := whatsAppFact(store.MetaConnection{}); !strings.Contains(f, "não cite número") {
		t.Fatalf("sem número = %q", f)
	}
	if f := whatsAppFact(store.MetaConnection{WhatsAppNumbers: []string{"+55 49 99999-1111"}}); !strings.Contains(f, "+55 49 99999-1111") {
		t.Fatalf("um número = %q", f)
	}
	f := whatsAppFact(store.MetaConnection{WhatsAppNumbers: []string{"+55 49 99999-1111", "+55 49 98888-2222"}})
	if !strings.Contains(f, "99999-1111, +55 49 98888-2222") || !strings.Contains(f, "cite todos") {
		t.Fatalf("dois números = %q", f)
	}
}
