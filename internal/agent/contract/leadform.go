package contract

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// LeadForm é o formulário instantâneo da Meta do jeito que o cliente pediu: tipo, abertura, perguntas e
// tela final. Tudo opcional; o que ele não disse fica no padrão (nome, telefone e e-mail, sem abertura).
type LeadForm struct {
	// Kind: "volume" (mais cadastros, preenchimento rápido) ou "qualified" (mais qualificados: a pessoa
	// revisa os dados antes de enviar). Vazio = volume.
	Kind string `json:"kind,omitempty"`
	// Abertura (tela antes das perguntas). Bullets viram lista; senão Intro vira parágrafo.
	Headline string   `json:"headline,omitempty"`
	Intro    string   `json:"intro,omitempty"`
	Bullets  []string `json:"bullets,omitempty"`
	// IntroButton: texto do botão da abertura (ex.: "Quero participar").
	IntroButton string `json:"intro_button,omitempty"`
	// QuestionsHeadline: título da página das perguntas.
	QuestionsHeadline string         `json:"questions_headline,omitempty"`
	Questions         []LeadQuestion `json:"questions,omitempty"`
	// Tela final (depois de enviar).
	ThanksTitle string `json:"thanks_title,omitempty"`
	ThanksText  string `json:"thanks_text,omitempty"`
	// ButtonAction: "site", "whatsapp", "call" ou "none". Vazio = site (o destino do anúncio).
	ButtonAction string `json:"button_action,omitempty"`
	ButtonText   string `json:"button_text,omitempty"`
	ButtonURL    string `json:"button_url,omitempty"`
	ButtonPhone  string `json:"button_phone,omitempty"`
	// PrivacyURL: política de privacidade do próprio cliente. Vazio = a do Meu Anúncio.
	PrivacyURL string `json:"privacy_url,omitempty"`
	// Clear: partes que o cliente mandou tirar ("intro", "thanks", "button", "questions_headline").
	Clear []string `json:"clear,omitempty"`
}

// LeadQuestion é uma pergunta do formulário. Kind é um tipo pronto da Meta (preenchido sozinho com os
// dados do perfil) ou "custom" (pergunta do cliente; com Options vira múltipla escolha).
type LeadQuestion struct {
	Kind    string   `json:"kind"`
	Label   string   `json:"label,omitempty"`
	Options []string `json:"options,omitempty"`
}

// Tipos prontos que o cliente pode pedir, com o tipo da Meta e como aparecem no resumo.
var leadQuestionKinds = map[string]struct{ Meta, Label string }{
	"full_name":  {"FULL_NAME", "nome completo"},
	"first_name": {"FIRST_NAME", "primeiro nome"},
	"last_name":  {"LAST_NAME", "sobrenome"},
	"phone":      {"PHONE", "telefone"},
	"whatsapp":   {"WHATSAPP_NUMBER", "WhatsApp"},
	"email":      {"EMAIL", "e-mail"},
	"work_email": {"WORK_EMAIL", "e-mail profissional"},
	"city":       {"CITY", "cidade"},
	"state":      {"STATE", "estado"},
	"zip":        {"ZIP", "CEP"},
	"address":    {"STREET_ADDRESS", "endereço"},
	"birthdate":  {"DOB", "data de nascimento"},
	"gender":     {"GENDER", "gênero"},
	"cpf":        {"ID_CPF", "CPF"},
	"job_title":  {"JOB_TITLE", "cargo"},
	"company":    {"COMPANY_NAME", "empresa"},
	"work_phone": {"WORK_PHONE_NUMBER", "telefone comercial"},
	"marital":    {"RELATIONSHIP_STATUS", "estado civil"},
	"education":  {"EDUCATION_LEVEL", "escolaridade"},
	"website":    {"WEBSITE", "site"},
	"date_time":  {"DATE_TIME", "data e horário de agendamento"},
	"custom":     {"CUSTOM", ""},
}

// Apelidos comuns que a IA pode mandar no lugar do tipo exato.
var leadQuestionAliases = map[string]string{
	"name": "full_name", "nome": "full_name", "telefone": "phone", "celular": "phone", "e-mail": "email",
	"mail": "email", "cidade": "city", "estado": "state", "cep": "zip", "endereco": "address", "endereço": "address",
	"dob": "birthdate", "nascimento": "birthdate", "genero": "gender", "gênero": "gender", "cargo": "job_title",
	"empresa": "company", "whatsapp_number": "whatsapp", "agendamento": "date_time", "pergunta": "custom",
	"multiple_choice": "custom", "text": "custom", "short_answer": "custom",
}

// Limites da Meta (e de bom senso para não estourar a tela do celular).
const (
	MaxLeadQuestions    = 15
	MaxLeadOptions      = 20
	maxLeadShortText    = 60
	maxLeadLabel        = 200
	maxLeadLongText     = 500
	maxLeadBullets      = 5
	maxLeadOptionLength = 80
)

// DefaultLeadQuestions é o formulário quando o cliente não escolheu as perguntas.
func DefaultLeadQuestions() []LeadQuestion {
	return []LeadQuestion{{Kind: "full_name"}, {Kind: "phone"}, {Kind: "email"}}
}

// LeadQuestionMetaType devolve o tipo da Meta da pergunta ("" se desconhecido).
func LeadQuestionMetaType(kind string) string { return leadQuestionKinds[kind].Meta }

// LeadQuestionText descreve a pergunta em português para o resumo.
func LeadQuestionText(q LeadQuestion) string {
	if q.Kind != "custom" {
		return leadQuestionKinds[q.Kind].Label
	}
	if len(q.Options) > 0 {
		return q.Label + " (opções: " + strings.Join(q.Options, " / ") + ")"
	}
	return q.Label + " (resposta livre)"
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}

func normKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if a, ok := leadQuestionAliases[k]; ok {
		return a
	}
	return k
}

// normalizeQuestions deixa só perguntas válidas: tipo conhecido, pergunta própria com texto, sem tipo pronto
// repetido e no máximo MaxLeadQuestions.
func normalizeQuestions(in []LeadQuestion) []LeadQuestion {
	var out []LeadQuestion
	seen := map[string]bool{}
	for _, q := range in {
		k := normKind(q.Kind)
		if _, ok := leadQuestionKinds[k]; !ok {
			// Tipo desconhecido com texto: vira pergunta própria em vez de sumir.
			if strings.TrimSpace(q.Label) == "" {
				continue
			}
			k = "custom"
		}
		nq := LeadQuestion{Kind: k}
		if k == "custom" {
			nq.Label = clip(q.Label, maxLeadLabel)
			if nq.Label == "" {
				continue
			}
			for _, o := range q.Options {
				if o = clip(o, maxLeadOptionLength); o != "" && !slices.Contains(nq.Options, o) && len(nq.Options) < MaxLeadOptions {
					nq.Options = append(nq.Options, o)
				}
			}
			if len(nq.Options) == 1 {
				nq.Options = nil // múltipla escolha com uma opção só não faz sentido: vira resposta livre
			}
			if seen["custom:"+strings.ToLower(nq.Label)] {
				continue
			}
			seen["custom:"+strings.ToLower(nq.Label)] = true
		} else {
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		out = append(out, nq)
		if len(out) == MaxLeadQuestions {
			break
		}
	}
	return out
}

// Normalize devolve o formulário pronto para a Meta: textos aparados no limite, tipos e ações conhecidos,
// perguntas válidas (vazio = padrão) e Clear já aplicado (não sobra).
func (f LeadForm) Normalize() LeadForm {
	out := LeadForm{
		Headline:          clip(f.Headline, maxLeadShortText),
		Intro:             clip(f.Intro, maxLeadLongText),
		IntroButton:       clip(f.IntroButton, 30),
		QuestionsHeadline: clip(f.QuestionsHeadline, maxLeadShortText),
		ThanksTitle:       clip(f.ThanksTitle, maxLeadShortText),
		ThanksText:        clip(f.ThanksText, maxLeadLongText),
		ButtonText:        clip(f.ButtonText, 30),
		ButtonURL:         strings.TrimSpace(f.ButtonURL),
		ButtonPhone:       strings.TrimSpace(f.ButtonPhone),
		PrivacyURL:        strings.TrimSpace(f.PrivacyURL),
		Questions:         normalizeQuestions(f.Questions),
	}
	switch strings.ToLower(strings.TrimSpace(f.Kind)) {
	case "qualified", "qualificado", "higher_intent", "more_qualified":
		out.Kind = "qualified"
	default:
		out.Kind = "volume"
	}
	for _, b := range f.Bullets {
		if b = clip(b, maxLeadShortText*2); b != "" && len(out.Bullets) < maxLeadBullets {
			out.Bullets = append(out.Bullets, b)
		}
	}
	switch strings.ToLower(strings.TrimSpace(f.ButtonAction)) {
	case "whatsapp", "wa":
		out.ButtonAction = "whatsapp"
	case "call", "ligar", "phone", "telefone":
		out.ButtonAction = "call"
	case "none", "nenhum", "sem botão":
		out.ButtonAction = "none"
	default:
		out.ButtonAction = "site"
	}
	if !strings.HasPrefix(out.ButtonURL, "https://") && !strings.HasPrefix(out.ButtonURL, "http://") {
		out.ButtonURL = ""
	}
	if !strings.HasPrefix(out.PrivacyURL, "https://") && !strings.HasPrefix(out.PrivacyURL, "http://") {
		out.PrivacyURL = ""
	}
	if len(out.Questions) == 0 {
		out.Questions = DefaultLeadQuestions()
	}
	return out
}

// HasIntro diz se o cliente pediu tela de abertura.
func (f LeadForm) HasIntro() bool {
	return f.Headline != "" || f.Intro != "" || len(f.Bullets) > 0
}

// HasThanks diz se o cliente personalizou a tela final.
func (f LeadForm) HasThanks() bool {
	return f.ThanksTitle != "" || f.ThanksText != "" || f.ButtonText != "" || f.ButtonURL != "" ||
		f.ButtonPhone != "" || (f.ButtonAction != "" && f.ButtonAction != "site")
}

// MergeLeadForm aplica o que o cliente disse agora (o) sobre o formulário já combinado (f). Texto em branco
// não apaga; perguntas, quando vêm, substituem a lista inteira; Clear tira a parte citada.
func MergeLeadForm(f, o *LeadForm) *LeadForm {
	if o == nil {
		return f
	}
	out := LeadForm{}
	if f != nil {
		out = *f
		out.Bullets = slices.Clone(f.Bullets)
		out.Questions = slices.Clone(f.Questions)
	}
	set := func(dst *string, v string) {
		if strings.TrimSpace(v) != "" {
			*dst = v
		}
	}
	for _, c := range o.Clear {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "intro":
			out.Headline, out.Intro, out.Bullets, out.IntroButton = "", "", nil, ""
		case "thanks":
			out.ThanksTitle, out.ThanksText, out.ButtonAction, out.ButtonText, out.ButtonURL, out.ButtonPhone = "", "", "", "", "", ""
		case "button":
			out.ButtonAction, out.ButtonText, out.ButtonURL, out.ButtonPhone = "none", "", "", ""
		case "questions_headline":
			out.QuestionsHeadline = ""
		case "questions":
			out.Questions = nil
		case "privacy_url":
			out.PrivacyURL = ""
		}
	}
	set(&out.Kind, o.Kind)
	set(&out.Headline, o.Headline)
	set(&out.Intro, o.Intro)
	set(&out.IntroButton, o.IntroButton)
	set(&out.QuestionsHeadline, o.QuestionsHeadline)
	set(&out.ThanksTitle, o.ThanksTitle)
	set(&out.ThanksText, o.ThanksText)
	set(&out.ButtonAction, o.ButtonAction)
	set(&out.ButtonText, o.ButtonText)
	set(&out.ButtonURL, o.ButtonURL)
	set(&out.ButtonPhone, o.ButtonPhone)
	set(&out.PrivacyURL, o.PrivacyURL)
	if len(o.Bullets) > 0 {
		out.Bullets = slices.Clone(o.Bullets)
	}
	if len(o.Questions) > 0 {
		out.Questions = slices.Clone(o.Questions)
	}
	return &out
}
