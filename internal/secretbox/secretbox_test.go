package secretbox_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
)

func key(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := secretbox.New(key(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal([]byte("EAAB-token"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("EAAB-token")) {
		t.Fatal("texto aparece no cifrado")
	}
	again, _ := b.Seal([]byte("EAAB-token"))
	if bytes.Equal(sealed, again) {
		t.Fatal("nonce repetido: cifrados iguais")
	}
	plain, err := b.Open(sealed)
	if err != nil || string(plain) != "EAAB-token" {
		t.Fatalf("Open = %q, %v", plain, err)
	}
}

func TestOpenFailsWithWrongKeyOrTampering(t *testing.T) {
	a, _ := secretbox.New(key(t))
	other, _ := secretbox.New(key(t))
	sealed, _ := a.Seal([]byte("segredo"))
	if _, err := other.Open(sealed); !errors.Is(err, secretbox.ErrOpen) {
		t.Fatalf("chave errada: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := a.Open(sealed); !errors.Is(err, secretbox.ErrOpen) {
		t.Fatalf("adulterado: %v", err)
	}
	if _, err := a.Open([]byte("curto")); !errors.Is(err, secretbox.ErrOpen) {
		t.Fatalf("curto: %v", err)
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	for _, k := range []string{"", "nao-e-base64!", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := secretbox.New(k); err == nil {
			t.Errorf("chave %q aceita", k)
		}
	}
}
