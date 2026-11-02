package paylink

import "testing"

func TestNewIsRandomAndHashed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		raw, hash, err := New()
		if err != nil {
			t.Fatal(err)
		}
		if !Valid(raw) {
			t.Fatalf("token inválido: %q", raw)
		}
		if hash != Hash(raw) || len(hash) != 64 || hash == raw {
			t.Fatalf("hash = %q", hash)
		}
		if seen[raw] {
			t.Fatal("token repetido")
		}
		seen[raw] = true
	}
}

func TestValidRejectsGuesses(t *testing.T) {
	for _, s := range []string{"", "abc", "aaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaa/.",
		"aaaaaaaaaaaaaaaaaaaaaa%0", "ççaaaaaaaaaaaaaaaaaaaa"} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true", s)
		}
	}
}
