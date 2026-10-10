package signup

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Cadastro pelo formulário do WhatsApp (WhatsApp Flows). O formulário é estático: a resposta chega
// no webhook com o flow_token, que é o mesmo token do link de cadastro.

// TermsVersion é a versão dos Termos e da Política de Privacidade gravada em cada aceite (página e formulário).
const TermsVersion = "2026-09-23"

// FlowFirstScreen é a tela que abre primeiro.
const FlowFirstScreen = "DADOS"

// ErrFlowToken: resposta sem flow_token (não veio de um formulário nosso).
var ErrFlowToken = errors.New("signup: resposta do formulário sem flow_token")

// FlowSubmission é a resposta do formulário: Form cru (Normalize roda depois, como na página).
type FlowSubmission struct {
	Token string
	Email string
	Form  Form
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// flexBool aceita true ou "true": o formulário pode devolver o aceite como texto.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	*b = flexBool(s == "true")
	return nil
}

// ParseFlowResponse lê o response_json do formulário.
func ParseFlowResponse(raw []byte) (FlowSubmission, error) {
	var r struct {
		FlowToken      string   `json:"flow_token"`
		FullName       string   `json:"full_name"`
		Email          string   `json:"email"`
		Documento      string   `json:"documento"`
		BusinessName   string   `json:"business_name"`
		Segment        string   `json:"segment"`
		SegmentOther   string   `json:"segment_other"`
		City           string   `json:"city"`
		UF             string   `json:"uf"`
		Website        string   `json:"website"`
		Instagram      string   `json:"instagram"`
		Objectives     []string `json:"objectives"`
		MonthlyBudget  string   `json:"monthly_budget"`
		AdsExperience  string   `json:"ads_experience"`
		MetaAdAccount  string   `json:"meta_ad_account"`
		Approver       string   `json:"approver"`
		ApproverName   string   `json:"approver_name"`
		ReferralSource string   `json:"referral_source"`
		AcceptTerms    flexBool `json:"accept_terms"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return FlowSubmission{}, fmt.Errorf("signup: resposta do formulário inválida: %w", err)
	}
	if strings.TrimSpace(r.FlowToken) == "" {
		return FlowSubmission{}, ErrFlowToken
	}
	email := strings.ToLower(strings.TrimSpace(r.Email))
	if !emailRe.MatchString(email) || len(email) > longMax {
		return FlowSubmission{}, &ValidationError{Fields: map[string]string{"email": "e-mail inválido"}}
	}
	f := Form{
		FullName: r.FullName, BusinessName: r.BusinessName, Segment: r.Segment, SegmentOther: r.SegmentOther,
		City: r.City, UF: r.UF, Website: r.Website, Instagram: r.Instagram, Objectives: r.Objectives,
		MonthlyBudget: r.MonthlyBudget, AdsExperience: r.AdsExperience, MetaAdAccount: r.MetaAdAccount,
		Approver: r.Approver, ApproverName: r.ApproverName, ReferralSource: r.ReferralSource, AcceptTerms: bool(r.AcceptTerms),
	}
	// Um campo só para o documento: 14 dígitos é CNPJ; o resto é tratado como CPF (e validado como tal).
	if doc := strings.TrimSpace(r.Documento); doc != "" {
		if len(OnlyDigits(doc)) == 14 {
			f.CNPJ = doc
		} else {
			f.CPF = doc
		}
	}
	return FlowSubmission{Token: strings.TrimSpace(r.FlowToken), Email: email, Form: f}, nil
}

// flowTitle corta o rótulo no limite da Meta para opções (30 caracteres).
var flowShort = map[string]string{"engajamento": "Seguidores e engajamento"}

func flowOptions(opts []Option) []map[string]string {
	out := make([]map[string]string, len(opts))
	for i, o := range opts {
		title := o.Label
		if s, ok := flowShort[o.Code]; ok {
			title = s
		}
		if r := []rune(title); len(r) > 30 {
			title = string(r[:30])
		}
		out[i] = map[string]string{"id": o.Code, "title": title}
	}
	return out
}

func flowUFs() []map[string]string {
	out := make([]map[string]string, len(UFs))
	for i, uf := range UFs {
		out[i] = map[string]string{"id": uf, "title": uf}
	}
	return out
}

type obj = map[string]any

func text(name, label string, required bool, inputType string) obj {
	return obj{"type": "TextInput", "name": name, "label": label, "required": required, "input-type": inputType}
}

func next(screen string) obj {
	return obj{"type": "Footer", "label": "Continuar", "on-click-action": obj{
		"name": "navigate", "next": obj{"type": "screen", "name": screen}, "payload": obj{},
	}}
}

func ref(screen, field string) string { return "${screen." + screen + ".form." + field + "}" }

func screen(id, title string, terminal bool, children ...obj) obj {
	s := obj{"id": id, "title": title, "data": obj{}, "layout": obj{"type": "SingleColumnLayout", "children": children}}
	if terminal {
		s["terminal"] = true
		s["success"] = true
	}
	return s
}

// FlowJSON monta o formulário a partir das listas do cadastro (fonte única com a página).
func FlowJSON(termsURL, privacyURL string) ([]byte, error) {
	payload := obj{
		"full_name": ref("DADOS", "full_name"), "email": ref("DADOS", "email"), "documento": ref("DADOS", "documento"),
		"business_name": ref("NEGOCIO", "business_name"), "segment": ref("NEGOCIO", "segment"),
		"segment_other": ref("NEGOCIO", "segment_other"), "city": ref("NEGOCIO", "city"), "uf": ref("NEGOCIO", "uf"),
		"website": ref("NEGOCIO", "website"), "instagram": ref("NEGOCIO", "instagram"),
		"objectives": ref("ANUNCIOS", "objectives"), "monthly_budget": ref("ANUNCIOS", "monthly_budget"),
		"ads_experience": ref("ANUNCIOS", "ads_experience"), "meta_ad_account": ref("ANUNCIOS", "meta_ad_account"),
		"approver": ref("ANUNCIOS", "approver"), "approver_name": ref("ANUNCIOS", "approver_name"),
		"referral_source": "${form.referral_source}", "accept_terms": "${form.accept_terms}",
	}
	doc := obj{
		"version": "6.3",
		"screens": []obj{
			screen("DADOS", "Seus dados", false,
				obj{"type": "TextBody", "text": "Leva uns 2 minutos. Assim que terminar, a gente continua a conversa."},
				text("full_name", "Nome completo", true, "text"),
				text("email", "E-mail", true, "email"),
				text("documento", "CPF ou CNPJ (opcional)", false, "text"),
				next("NEGOCIO"),
			),
			screen("NEGOCIO", "Seu negócio", false,
				text("business_name", "Nome do negócio", true, "text"),
				obj{"type": "Dropdown", "name": "segment", "label": "Segmento", "required": true, "data-source": flowOptions(Segments)},
				text("segment_other", "Se escolheu Outro, qual segmento?", false, "text"),
				text("city", "Cidade", true, "text"),
				obj{"type": "Dropdown", "name": "uf", "label": "UF", "required": true, "data-source": flowUFs()},
				text("website", "Site (opcional)", false, "text"),
				text("instagram", "Instagram (opcional)", false, "text"),
				next("ANUNCIOS"),
			),
			screen("ANUNCIOS", "Seus anúncios", false,
				obj{"type": "CheckboxGroup", "name": "objectives", "label": "O que você quer com os anúncios?", "required": true,
					"min-selected-items": 1, "data-source": flowOptions(Objectives)},
				obj{"type": "RadioButtonsGroup", "name": "monthly_budget", "label": "Quanto pretende investir por mês?", "required": true, "data-source": flowOptions(MonthlyBudgets)},
				obj{"type": "RadioButtonsGroup", "name": "ads_experience", "label": "Você já anuncia?", "required": true, "data-source": flowOptions(AdsExperiences)},
				obj{"type": "RadioButtonsGroup", "name": "meta_ad_account", "label": "Tem conta de anúncios no Facebook/Instagram?", "required": true, "data-source": flowOptions(MetaAdAccounts)},
				obj{"type": "RadioButtonsGroup", "name": "approver", "label": "Quem aprova os gastos com anúncios?", "required": true, "data-source": flowOptions(Approvers)},
				text("approver_name", "Se for outra pessoa, nome dela", false, "text"),
				next("CONCLUIR"),
			),
			screen("CONCLUIR", "Concluir", true,
				text("referral_source", "Como conheceu a gente? (opcional)", false, "text"),
				obj{"type": "TextBody", "text": "Você começa no plano Free, sem custo."},
				obj{"type": "EmbeddedLink", "text": "Ler os Termos de Uso", "on-click-action": obj{"name": "open_url", "url": termsURL}},
				obj{"type": "EmbeddedLink", "text": "Ler a Política de Privacidade", "on-click-action": obj{"name": "open_url", "url": privacyURL}},
				obj{"type": "OptIn", "name": "accept_terms", "label": "Li e aceito os Termos e a Política de Privacidade", "required": true},
				obj{"type": "Footer", "label": "Concluir cadastro", "on-click-action": obj{"name": "complete", "payload": payload}},
			),
		},
	}
	return json.Marshal(doc)
}
