package mia

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// MaxStrategyRunes é o teto da estratégia de um segmento (Validate e playbooks usam o mesmo).
const MaxStrategyRunes = 6000

// maxKeywords é o teto de palavras-chave por segmento.
const maxKeywords = 20

// "all:" inclui o .gitkeep, então a pasta vazia também compila.
//
//go:embed all:playbooks
var playbookFS embed.FS

// playbookFile é o formato de cada internal/mia/playbooks/<key>.json.
type playbookFile struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
	Strategy string   `json:"strategy"`
}

var (
	playbooksOnce sync.Once
	playbooksList []Segment
)

// loadPlaybooks lê todos os JSON embutidos. Devolve o que estiver válido e um erro agregando o que
// não estiver (o teste exige erro nulo; em produção o inválido só fica de fora).
func loadPlaybooks() ([]Segment, error) {
	entries, err := playbookFS.ReadDir("playbooks")
	if err != nil {
		return nil, err
	}
	var out []Segment
	var errs []string
	seen := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := playbookFS.ReadFile(path.Join("playbooks", e.Name()))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		var p playbookFile
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			errs = append(errs, fmt.Sprintf("%s: JSON inválido: %v", e.Name(), err))
			continue
		}
		if problem := checkPlaybook(e.Name(), p); problem != "" {
			errs = append(errs, problem)
			continue
		}
		if prev, dup := seen[p.Key]; dup {
			errs = append(errs, fmt.Sprintf("%s: key %q repetida (já em %s)", e.Name(), p.Key, prev))
			continue
		}
		seen[p.Key] = e.Name()
		out = append(out, Segment{Key: p.Key, Name: strings.TrimSpace(p.Name), Keywords: p.Keywords,
			Strategy: strings.TrimSpace(p.Strategy), Active: true, Builtin: true})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if len(errs) > 0 {
		return out, fmt.Errorf("playbooks inválidos: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

// checkPlaybook devolve o problema do arquivo ("" = ok): campos obrigatórios, slug e limites.
func checkPlaybook(file string, p playbookFile) string {
	switch {
	case !slug.MatchString(p.Key):
		return fmt.Sprintf("%s: key %q não é um slug válido", file, p.Key)
	case p.Key+".json" != file:
		return fmt.Sprintf("%s: o arquivo deve se chamar %s.json", file, p.Key)
	case strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > 60:
		return fmt.Sprintf("%s: name vazio ou com mais de 60 caracteres", file)
	case len(p.Keywords) == 0:
		return fmt.Sprintf("%s: keywords vazio", file)
	case len(p.Keywords) > maxKeywords:
		return fmt.Sprintf("%s: %d keywords (máximo %d)", file, len(p.Keywords), maxKeywords)
	case strings.TrimSpace(p.Strategy) == "":
		return fmt.Sprintf("%s: strategy vazia", file)
	case utf8.RuneCountInString(p.Strategy) > MaxStrategyRunes:
		return fmt.Sprintf("%s: strategy passa de %d caracteres", file, MaxStrategyRunes)
	}
	for _, k := range p.Keywords {
		if strings.TrimSpace(k) == "" {
			return fmt.Sprintf("%s: keyword vazia", file)
		}
	}
	return ""
}

// Playbooks devolve cópias dos segmentos padrão do sistema (playbooks de nicho embutidos), ordenados
// por chave. Todos vêm Active=true e Builtin=true.
func Playbooks() []Segment {
	playbooksOnce.Do(func() { playbooksList, _ = loadPlaybooks() })
	out := make([]Segment, len(playbooksList))
	for i, s := range playbooksList {
		s.Keywords = append([]string(nil), s.Keywords...)
		out[i] = s
	}
	return out
}

// PlaybookFor devolve o padrão do sistema para a chave (ok=false se não for playbook). Serve ao
// "restaurar padrão" de um segmento.
func PlaybookFor(key string) (Segment, bool) {
	for _, s := range Playbooks() {
		if s.Key == key {
			return s, true
		}
	}
	return Segment{}, false
}

// mergePlaybooks acrescenta os playbooks cuja Key ainda não existe e marca Builtin nos que têm a
// chave de um playbook. Nunca altera o conteúdo de um segmento já existente (a edição do admin vale).
func mergePlaybooks(segs []Segment) []Segment {
	pbs := Playbooks()
	isBuiltin := map[string]bool{}
	for _, p := range pbs {
		isBuiltin[p.Key] = true
	}
	have := map[string]bool{}
	out := make([]Segment, 0, len(segs)+len(pbs))
	for _, s := range segs {
		s.Builtin = isBuiltin[s.Key]
		have[s.Key] = true
		out = append(out, s)
	}
	for _, p := range pbs {
		if !have[p.Key] {
			out = append(out, p)
		}
	}
	return out
}
