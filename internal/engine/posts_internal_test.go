package engine

import (
	"slices"
	"testing"
)

func TestWantsPosts(t *testing.T) {
	yes := []string{"POSTS", "meus posts", "quero usar um post que já tenho", "dá pra impulsionar um post da minha página?",
		"pode pegar uma publicação do meu insta", "usa um post do instagram", "quero turbinar um reels", "mostra meus posts"}
	no := []string{"pronto", "apaga a 2", "vou postar amanhã", "o post fica bonito com essa foto?", "2", "manda o post pra mim depois que publicar"}
	for _, s := range yes {
		if !wantsPosts(s) {
			t.Errorf("wantsPosts(%q) = false", s)
		}
	}
	for _, s := range no {
		if wantsPosts(s) {
			t.Errorf("wantsPosts(%q) = true", s)
		}
	}
}

func TestParsePostPick(t *testing.T) {
	cases := []struct {
		in    string
		picks []int
		named bool
	}{
		{"POST 2", []int{2}, true},
		{"posts 1 e 3", []int{1, 3}, true},
		{"o 2", []int{2}, false},
		{"usa o 1 e o 3, por favor", []int{1, 3}, false},
		{"quero o primeiro", []int{1}, false},
		{"2", []int{2}, false},
		{"apaga a 2", nil, false},
		{"tenho 2 fotos", nil, false},
		{"pronto", nil, false},
	}
	for _, c := range cases {
		picks, named := parsePostPick(c.in)
		if !slices.Equal(picks, c.picks) || named != c.named {
			t.Errorf("parsePostPick(%q) = %v %v, quero %v %v", c.in, picks, named, c.picks, c.named)
		}
	}
}
