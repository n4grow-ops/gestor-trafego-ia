package meta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// defaultLeadQuestions são os campos do formulário quando o cliente não escolheu: o mínimo para retornar o contato.
var defaultLeadQuestions = []LeadFormQuestion{{Type: "FULL_NAME"}, {Type: "PHONE"}, {Type: "EMAIL"}}

// leadFormParams monta o pedido de criação do formulário do jeito que o cliente pediu. link é o destino do
// anúncio (site ou Página), usado no botão da tela final quando ele não deu outro.
func leadFormParams(in PublishInput, name, privacy, link string) (url.Values, []string) {
	spec := LeadFormSpec{}
	if in.Form != nil {
		spec = *in.Form
	}
	if u := strings.TrimSpace(spec.PrivacyURL); u != "" {
		privacy = u
	}
	qs := spec.Questions
	if len(qs) == 0 {
		qs = defaultLeadQuestions
	}
	questions := make([]map[string]any, 0, len(qs))
	custom := 0
	for _, q := range qs {
		item := map[string]any{"type": q.Type}
		if q.Type == "CUSTOM" {
			custom++
			key := fmt.Sprintf("pergunta_%d", custom)
			item["key"], item["label"] = key, q.Label
			if len(q.Options) > 0 {
				opts := make([]map[string]string, len(q.Options))
				for i, o := range q.Options {
					opts[i] = map[string]string{"value": o, "key": fmt.Sprintf("%s_opcao_%d", key, i+1)}
				}
				item["options"] = opts
			}
		}
		questions = append(questions, item)
	}
	params := url.Values{
		"name":                 {name},
		"locale":               {"PT_BR"},
		"questions":            {jsonParam(questions)},
		"privacy_policy":       {jsonParam(map[string]string{"url": privacy, "link_text": "Política de privacidade"})},
		"follow_up_action_url": {link},
	}
	if spec.Qualified {
		params.Set("is_optimized_for_quality", "true")
	}
	if spec.QuestionsHeadline != "" {
		params.Set("question_page_custom_headline", spec.QuestionsHeadline)
	}
	if spec.Headline != "" || spec.Intro != "" || len(spec.Bullets) > 0 {
		card := map[string]any{"title": firstNonEmpty(spec.Headline, "Cadastre-se"), "style": "PARAGRAPH_STYLE",
			"content": []string{firstNonEmpty(spec.Intro, spec.Headline)}}
		if len(spec.Bullets) > 0 {
			card["style"], card["content"] = "LIST_STYLE", spec.Bullets
		}
		if spec.IntroButton != "" {
			card["button_text"] = spec.IntroButton
		}
		params.Set("context_card", jsonParam(card))
	}
	var warnings []string
	if thanks, warn := thankYouPage(in, spec, link); thanks != nil {
		params.Set("thank_you_page", jsonParam(thanks))
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	return params, warnings
}

// thankYouPage monta a tela final; nil quando o cliente não personalizou (fica a padrão da Meta).
func thankYouPage(in PublishInput, spec LeadFormSpec, link string) (map[string]any, string) {
	action := spec.ButtonAction
	custom := spec.ThanksTitle != "" || spec.ThanksText != "" || spec.ButtonText != "" || spec.ButtonURL != "" ||
		spec.ButtonPhone != "" || (action != "" && action != "site")
	if !custom {
		return nil, ""
	}
	page := map[string]any{"title": firstNonEmpty(spec.ThanksTitle, "Obrigado!")}
	if spec.ThanksText != "" {
		page["body"] = spec.ThanksText
	}
	var warn string
	phone := digitsOnly(firstNonEmpty(spec.ButtonPhone, in.WhatsAppNumber))
	if (action == "whatsapp" || action == "call") && phone == "" {
		warn = "botão da tela final sem número: virou botão para o site"
		action = "site"
	}
	switch action {
	case "none":
		page["button_type"] = "NONE"
	case "whatsapp":
		// Link do WhatsApp em vez do botão WHATSAPP da Meta: funciona com qualquer número, sem vínculo na Página.
		page["button_type"], page["website_url"] = "VIEW_WEBSITE", "https://wa.me/"+phone
		page["button_text"] = firstNonEmpty(spec.ButtonText, "Chamar no WhatsApp")
	case "call":
		page["button_type"], page["business_phone_number"] = "CALL_BUSINESS", "+"+phone
		page["button_text"] = firstNonEmpty(spec.ButtonText, "Ligar agora")
	default:
		page["button_type"], page["website_url"] = "VIEW_WEBSITE", firstNonEmpty(spec.ButtonURL, link)
		page["button_text"] = firstNonEmpty(spec.ButtonText, "Ver site")
	}
	return page, warn
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := strings.TrimLeft(b.String(), "0") // "011 9..." = DDD com zero de discagem
	if len(d) == 10 || len(d) == 11 { // número brasileiro sem DDI
		d = "55" + d
	}
	return d
}

// leadFormName identifica o formulário da publicação. Formulário personalizado leva uma marca do conteúdo:
// a Meta não aceita dois com o mesmo nome e um ajuste do cliente precisa virar formulário novo.
func leadFormName(in PublishInput) string {
	name := in.Name + " - formulário"
	if in.Form != nil {
		h := sha256.Sum256([]byte(jsonParam(in.Form)))
		name += " " + hex.EncodeToString(h[:3])
	}
	return name
}

// ensureLeadForm devolve o formulário instantâneo da campanha na Página do cliente. Tentativa anterior
// da mesma publicação (mesmo nome) é reaproveitada: a Meta não aceita dois formulários com o mesmo nome.
func (c *Client) ensureLeadForm(ctx context.Context, token string, in PublishInput, link string) (string, []string, error) {
	page := strings.TrimSpace(in.PageID)
	privacy := strings.TrimSpace(in.FormPrivacyURL)
	if in.Form != nil && strings.TrimSpace(in.Form.PrivacyURL) != "" {
		privacy = strings.TrimSpace(in.Form.PrivacyURL)
	}
	if privacy == "" {
		return "", nil, &PublishError{Step: "leadform", Message: "sem URL de política de privacidade para o formulário", UserMessage: genericUserMessage}
	}
	pageToken, err := c.pageToken(ctx, token, page)
	if err != nil {
		return "", nil, err
	}
	name := leadFormName(in)

	var existing struct {
		Data []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(page)+"/leadgen_forms", pageToken,
		url.Values{"fields": {"id,name,status"}, "limit": {"100"}}, &existing); err == nil {
		for _, f := range existing.Data {
			if f.Name == name && f.ID != "" && !strings.EqualFold(f.Status, "ARCHIVED") {
				return f.ID, nil, nil
			}
		}
	}

	params, warnings := leadFormParams(in, name, privacy, link)
	var created struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/"+url.PathEscape(page)+"/leadgen_forms", pageToken, params, &created); err != nil {
		return "", nil, err
	}
	if created.ID == "" {
		return "", nil, errors.New("meta: leadgen_forms sem id")
	}
	return created.ID, warnings, nil
}

// pageToken troca o token do usuário pelo da Página (criar formulário exige o token da Página).
func (c *Client) pageToken(ctx context.Context, token, page string) (string, error) {
	var r struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(page), token, url.Values{"fields": {"access_token"}}, &r); err != nil {
		return "", err
	}
	if r.AccessToken == "" {
		return "", errors.New("meta: página sem access_token")
	}
	return r.AccessToken, nil
}
