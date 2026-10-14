package engine

import "testing"

func TestParseCopyVariations(t *testing.T) {
	cases := map[string]int{
		"quero 3 variações do texto": 3,
		"me dá duas opções de copy":  2,
		"pode ser 9 versões":         5, // teto = MaxCopies
		"quero anunciar pizza":       0,
		"20 por dia":                 0,
		"cinco textos diferentes":    5,
	}
	for in, want := range cases {
		if got := parseCopyVariations(in); got != want {
			t.Errorf("%q = %d, quer %d", in, got, want)
		}
	}
}
