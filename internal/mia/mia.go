// Package mia é a configuração editável da Mia (a Atendente) pelo painel: textos fixos, campos que ela
// coleta, persona por blocos, módulos por segmento, voz e funil por estado da conversa.
//
// As partes técnicas (formato JSON, intents, regras de segurança e de orçamento) continuam no código e
// não são editáveis: a configuração só preenche os blocos de conteúdo. Defaults() reproduz o
// comportamento de antes do painel.
package mia

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

// ErrInvalid: configuração fora das regras (Validate diz o quê).
var ErrInvalid = errors.New("mia: configuração inválida")

// Grupos de campos.
const (
	GroupCampaign = "campaign" // dados de uma campanha (contract.Fields)
	GroupProfile  = "profile"  // memória do negócio (contract.Profile)
)

// Field é um dado que a Mia coleta. Core = exigido pelo contrato da proposta: fica sempre ativo e obrigatório.
type Field struct {
	Key      string `json:"key"`
	Group    string `json:"group"`
	Label    string `json:"label"`
	Hint     string `json:"hint"` // como perguntar (dica para a IA)
	Active   bool   `json:"active"`
	Required bool   `json:"required"`
	Order    int    `json:"order"`
	Core     bool   `json:"core"`
}

// Persona são os blocos de texto da personalidade, montados nos prompts da Atendente e do Gestor.
type Persona struct {
	Identity      string `json:"identity"`
	Personality   string `json:"personality"`
	Communication string `json:"communication"`
	Never         string `json:"never"`
}

// Stage é uma etapa do funil, presa a um estado real da conversa. Várias etapas de coleta podem
// dividir o estado "collecting": vale a primeira (na ordem) que ainda tem campo faltando.
type Stage struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Objective    string   `json:"objective"`
	Instructions string   `json:"instructions"`
	Fields       []string `json:"fields"`
	Exit         string   `json:"exit"`
	Order        int      `json:"order"`
	Active       bool     `json:"active"`
}

// Segment é um módulo de conhecimento: entra no prompt só para clientes desse segmento.
type Segment struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
	Strategy string   `json:"strategy"`
	Active   bool     `json:"active"`
}

// Voice: voz das respostas em áudio. Name vazio = TTS_VOICE do ambiente.
type Voice struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Config é tudo o que o painel edita na aba Mia.
type Config struct {
	Texts    map[string]string `json:"texts"`
	Fields   []Field           `json:"fields"`
	Persona  Persona           `json:"persona"`
	Stages   []Stage           `json:"stages"`
	Segments []Segment         `json:"segments"`
	Voice    Voice             `json:"voice"`
}

// Chaves dos textos fixos e o que cada um exige.
const (
	TextFirstMessage = "first_message" // primeira mensagem (vai com o formulário de cadastro)
	TextSignupLink   = "signup_link"   // link da página de cadastro
	TextFlowFix      = "flow_fix"      // correção do formulário
	TextFlowRetry    = "flow_retry"    // formulário ilegível
	TextReminder1    = "reminder_1"    // 1º lembrete de cadastro
	TextReminder2    = "reminder_2"    // 2º lembrete de cadastro
	TextWelcome      = "welcome"       // boas-vindas depois do cadastro
)

// TextSpec descreve um texto editável: rótulo e marcadores obrigatórios.
type TextSpec struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Required []string `json:"required"`
	Optional []string `json:"optional"`
}

// TextSpecs na ordem da tela.
var TextSpecs = []TextSpec{
	{TextFirstMessage, "Primeira mensagem (apresentação, com o botão do cadastro)", nil, nil},
	{TextSignupLink, "Link da página de cadastro", []string{"{link}"}, nil},
	{TextFlowFix, "Correção do formulário de cadastro", []string{"{campos}"}, nil},
	{TextFlowRetry, "Formulário que não deu para ler", nil, nil},
	{TextReminder1, "1º lembrete de cadastro (3 h depois)", []string{"{link}"}, nil},
	{TextReminder2, "2º lembrete de cadastro (24 h depois)", []string{"{link}"}, nil},
	{TextWelcome, "Boas-vindas depois do cadastro", []string{"{email}"}, []string{"{nome}"}},
}

// Estados da conversa que o funil pode configurar (needs_human não: a IA fica calada).
var States = []string{"onboarding", "idle", "collecting", "awaiting_proposal", "awaiting_approval", "awaiting_creatives"}

// Voices são as vozes neurais pt-BR do Azure aceitas pelo TTS em uso (microsoft/mai-voice-2-flash).
var Voices = []string{
	"pt-BR-FranciscaNeural", "pt-BR-ThalitaNeural", "pt-BR-ThalitaMultilingualNeural", "pt-BR-BrendaNeural", "pt-BR-ElzaNeural",
	"pt-BR-GiovannaNeural", "pt-BR-LeilaNeural", "pt-BR-LeticiaNeural", "pt-BR-ManuelaNeural", "pt-BR-YaraNeural",
	"pt-BR-AntonioNeural", "pt-BR-DonatoNeural", "pt-BR-FabioNeural", "pt-BR-HumbertoNeural", "pt-BR-JulioNeural",
	"pt-BR-NicolauNeural", "pt-BR-ValerioNeural", "pt-BR-MacerioMultilingualNeural",
}

// Text devolve o texto com os marcadores {chave} trocados pelos valores.
func (c Config) Text(key string, vars map[string]string) string {
	t := c.Texts[key]
	if t == "" {
		t = Defaults().Texts[key]
	}
	for k, v := range vars {
		t = strings.ReplaceAll(t, "{"+k+"}", v)
	}
	return t
}

// fieldValues lê os valores preenchidos de Fields ou Profile pela chave JSON.
func fieldValues(v any) map[string]bool {
	raw, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(raw, &m)
	out := map[string]bool{}
	for k, val := range m {
		switch x := val.(type) {
		case string:
			out[k] = strings.TrimSpace(x) != ""
		case float64:
			out[k] = x > 0
		case nil:
		default:
			out[k] = true
		}
	}
	return out
}

func (c Config) ordered(group string) []Field {
	var out []Field
	for _, f := range c.Fields {
		if f.Group == group && f.Active {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// CampaignMissing lista, na ordem configurada, as chaves obrigatórias da campanha ainda vazias.
// Os campos core entram sempre: a proposta não sai sem eles.
func (c Config) CampaignMissing(f contract.Fields) []string {
	have := fieldValues(f)
	var out []string
	for _, fl := range c.ordered(GroupCampaign) {
		if (fl.Required || fl.Core) && !have[fl.Key] {
			out = append(out, fl.Key)
		}
	}
	return out
}

// ProfileMissing lista os rótulos dos itens obrigatórios da ficha do negócio ainda vazios.
func (c Config) ProfileMissing(p contract.Profile) []string {
	have := fieldValues(p)
	var out []string
	for _, fl := range c.ordered(GroupProfile) {
		if fl.Required && !have[fl.Key] {
			out = append(out, fl.Label)
		}
	}
	return out
}

// ProfileComplete diz se a ficha tem tudo o que é obrigatório (fim do onboarding).
func (c Config) ProfileComplete(p contract.Profile) bool { return len(c.ProfileMissing(p)) == 0 }

// Label devolve o rótulo configurado do campo (ou a própria chave).
func (c Config) Label(key string) string {
	for _, f := range c.Fields {
		if f.Key == key {
			return f.Label
		}
	}
	return key
}

// StageFor escolhe a etapa ativa do estado. Em coleta, a primeira etapa (na ordem) com campo vazio;
// se todas estiverem cheias, a última.
func (c Config) StageFor(state string, f contract.Fields) *Stage {
	var cands []Stage
	for _, s := range c.Stages {
		if s.Active && s.State == state {
			cands = append(cands, s)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Order < cands[j].Order })
	if state == "collecting" && len(cands) > 1 {
		have := fieldValues(f)
		for _, s := range cands {
			for _, k := range s.Fields {
				if !have[k] {
					return &s
				}
			}
		}
	}
	if state == "collecting" {
		return &cands[len(cands)-1]
	}
	return &cands[0]
}

// SegmentFor acha o módulo do segmento do cliente: nome ou palavra-chave contida no segmento/nicho.
func (c Config) SegmentFor(segment, niche string) *Segment {
	hay := strings.ToLower(normalize(segment + " | " + niche))
	if strings.TrimSpace(strings.Trim(hay, "| ")) == "" {
		return nil
	}
	for _, s := range c.Segments {
		if !s.Active {
			continue
		}
		for _, k := range append([]string{s.Name}, s.Keywords...) {
			if k = strings.ToLower(normalize(strings.TrimSpace(k))); k != "" && strings.Contains(hay, k) {
				s := s
				return &s
			}
		}
	}
	return nil
}

// normalize tira acentos comuns para casar "clínica" com "clinica".
func normalize(s string) string {
	return strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "é", "e", "ê", "e", "í", "i", "ó", "o", "ô", "o", "õ", "o",
		"ú", "u", "ç", "c", "Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ç", "c").Replace(s)
}

var slug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// knownField diz se a chave existe no grupo (group vazio = em qualquer grupo).
func knownField(group, key string) bool {
	for _, f := range Defaults().Fields {
		if f.Key == key && (group == "" || f.Group == group) {
			return true
		}
	}
	return false
}

// Normalize completa o que faltar com os padrões, trava os campos core e ordena as listas.
func (c Config) Normalize() Config {
	d := Defaults()
	out := c
	out.Texts = map[string]string{}
	for k, v := range d.Texts {
		out.Texts[k] = v
	}
	for k, v := range c.Texts {
		if _, ok := d.Texts[k]; ok && strings.TrimSpace(v) != "" {
			out.Texts[k] = v
		}
	}
	// Campos: os do padrão, com o que veio por cima (campos desconhecidos são descartados).
	given := map[string]Field{}
	for _, f := range c.Fields {
		given[f.Group+"/"+f.Key] = f
	}
	out.Fields = nil
	for _, df := range d.Fields {
		f := df
		if g, ok := given[df.Group+"/"+df.Key]; ok {
			f.Label, f.Hint, f.Active, f.Required, f.Order = g.Label, g.Hint, g.Active, g.Required, g.Order
			if strings.TrimSpace(f.Label) == "" {
				f.Label = df.Label
			}
		}
		if f.Core {
			f.Active, f.Required = true, true
		}
		out.Fields = append(out.Fields, f)
	}
	sort.SliceStable(out.Fields, func(i, j int) bool {
		if out.Fields[i].Group != out.Fields[j].Group {
			return out.Fields[i].Group < out.Fields[j].Group
		}
		return out.Fields[i].Order < out.Fields[j].Order
	})
	if strings.TrimSpace(out.Persona.Identity) == "" {
		out.Persona = d.Persona
	}
	if out.Stages == nil {
		out.Stages = d.Stages
	}
	sort.SliceStable(out.Stages, func(i, j int) bool { return out.Stages[i].Order < out.Stages[j].Order })
	if out.Segments == nil {
		out.Segments = []Segment{}
	}
	return out
}

// Validate confere marcadores, tamanhos, chaves e estados. Devolve todos os problemas juntos.
func (c Config) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	long := func(s string, n int) bool { return utf8.RuneCountInString(s) > n }
	for _, spec := range TextSpecs {
		t := c.Texts[spec.Key]
		if strings.TrimSpace(t) == "" {
			add("texto %q vazio", spec.Label)
			continue
		}
		if long(t, 1000) {
			add("texto %q passa de 1000 caracteres", spec.Label)
		}
		for _, r := range spec.Required {
			if !strings.Contains(t, r) {
				add("texto %q precisa do marcador %s", spec.Label, r)
			}
		}
	}
	for _, f := range c.Fields {
		if !knownField(f.Group, f.Key) {
			add("campo desconhecido %q", f.Key)
		}
		if strings.TrimSpace(f.Label) == "" || long(f.Label, 120) {
			add("campo %q: rótulo vazio ou com mais de 120 caracteres", f.Key)
		}
		if long(f.Hint, 300) {
			add("campo %q: dica com mais de 300 caracteres", f.Key)
		}
		if f.Core && (!f.Active || !f.Required) {
			add("campo %q é exigido pela proposta e não pode ser desligado", f.Key)
		}
	}
	if strings.TrimSpace(c.Persona.Identity) == "" {
		add("a identidade da Mia não pode ficar vazia")
	}
	for name, v := range map[string]string{"identidade": c.Persona.Identity, "personalidade": c.Persona.Personality,
		"comunicação": c.Persona.Communication, "o que nunca fazer": c.Persona.Never} {
		if long(v, 4000) {
			add("bloco %q passa de 4000 caracteres", name)
		}
	}
	seen := map[string]bool{}
	for _, s := range c.Stages {
		if !slug.MatchString(s.Key) || seen["s/"+s.Key] {
			add("etapa com chave inválida ou repetida %q", s.Key)
		}
		seen["s/"+s.Key] = true
		if strings.TrimSpace(s.Name) == "" || long(s.Name, 60) {
			add("etapa %q: nome vazio ou longo", s.Key)
		}
		valid := false
		for _, st := range States {
			valid = valid || st == s.State
		}
		if !valid {
			add("etapa %q: estado %q não existe no engine", s.Key, s.State)
		}
		for _, k := range s.Fields {
			if !knownField("", k) {
				add("etapa %q: campo desconhecido %q", s.Key, k)
			}
		}
		if long(s.Instructions, 2000) || long(s.Objective, 300) || long(s.Exit, 300) {
			add("etapa %q: texto longo demais", s.Key)
		}
	}
	for _, s := range c.Segments {
		if !slug.MatchString(s.Key) || seen["g/"+s.Key] {
			add("segmento com chave inválida ou repetida %q", s.Key)
		}
		seen["g/"+s.Key] = true
		if strings.TrimSpace(s.Name) == "" || long(s.Name, 60) {
			add("segmento %q: nome vazio ou longo", s.Key)
		}
		if long(s.Strategy, 4000) || len(s.Keywords) > 20 {
			add("segmento %q: estratégia ou palavras-chave demais", s.Key)
		}
	}
	if c.Voice.Name != "" {
		ok := false
		for _, v := range Voices {
			ok = ok || v == c.Voice.Name
		}
		if !ok {
			add("voz %q não está na lista de vozes pt-BR", c.Voice.Name)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(errs, "; "))
	}
	return nil
}
