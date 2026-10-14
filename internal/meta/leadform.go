package meta

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// leadFormQuestions são os campos do formulário instantâneo: o mínimo para o cliente retornar o contato.
var leadFormQuestions = []map[string]string{{"type": "FULL_NAME"}, {"type": "PHONE"}, {"type": "EMAIL"}}

// ensureLeadForm devolve o formulário instantâneo da campanha na Página do cliente. Tentativa anterior
// da mesma publicação (mesmo nome) é reaproveitada: a Meta não aceita dois formulários com o mesmo nome.
func (c *Client) ensureLeadForm(ctx context.Context, token string, in PublishInput) (string, error) {
	page := strings.TrimSpace(in.PageID)
	privacy := strings.TrimSpace(in.FormPrivacyURL)
	if privacy == "" {
		return "", &PublishError{Step: "leadform", Message: "sem URL de política de privacidade para o formulário", UserMessage: genericUserMessage}
	}
	pageToken, err := c.pageToken(ctx, token, page)
	if err != nil {
		return "", err
	}
	name := in.Name + " - formulário"

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
				return f.ID, nil
			}
		}
	}

	var created struct {
		ID string `json:"id"`
	}
	err = c.call(ctx, http.MethodPost, "/"+url.PathEscape(page)+"/leadgen_forms", pageToken, url.Values{
		"name":                 {name},
		"locale":               {"PT_BR"},
		"questions":            {jsonParam(leadFormQuestions)},
		"privacy_policy":       {jsonParam(map[string]string{"url": privacy, "link_text": "Política de privacidade"})},
		"follow_up_action_url": {privacy},
	}, &created)
	if err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("meta: leadgen_forms sem id")
	}
	return created.ID, nil
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
