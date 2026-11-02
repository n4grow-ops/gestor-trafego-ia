// Package paylink gera e confere o token do link de pagamento (/pagar/{token}).
package paylink

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const (
	alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	// 24 caracteres de 62 = ~142 bits: impossível de adivinhar, mesmo sem o limite de tentativas.
	Length = 24
)

// New gera o token: raw vai na URL, hash vai para o banco.
func New() (raw, hash string, err error) {
	out := make([]byte, 0, Length)
	buf := make([]byte, 48)
	for len(out) < Length {
		if _, err := rand.Read(buf); err != nil {
			return "", "", fmt.Errorf("paylink: %w", err)
		}
		for _, b := range buf {
			// descarta 248..255 para não enviesar o alfabeto de 62 (248 = 4 × 62)
			if b < 248 && len(out) < Length {
				out = append(out, alphabet[int(b)%len(alphabet)])
			}
		}
	}
	raw = string(out)
	return raw, Hash(raw), nil
}

// Hash é o SHA-256 em hex do token.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Valid diz se o texto tem cara de token (tamanho e alfabeto); o resto é o banco que decide.
func Valid(raw string) bool {
	if len(raw) != Length {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}
