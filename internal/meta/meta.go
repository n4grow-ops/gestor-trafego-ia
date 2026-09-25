// Package meta fala com a Graph API da Meta: login do cliente (Facebook Login for Business),
// leitura das contas liberadas e o signed_request da desautorização.
package meta

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const version = "v23.0"

// ErrBadSignature: signed_request malformado ou com assinatura que não bate.
var ErrBadSignature = errors.New("meta: signed_request inválido")

type Client struct {
	AppID, AppSecret, ConfigID, RedirectURI string
	GraphURL                                string
	DialogURL                               string
	HTTP                                    *http.Client
}

func New(appID, appSecret, configID, redirectURI string) *Client {
	return &Client{AppID: appID, AppSecret: appSecret, ConfigID: configID, RedirectURI: redirectURI,
		GraphURL: "https://graph.facebook.com", DialogURL: "https://www.facebook.com", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

type AdAccount struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Currency      string `json:"currency"`
	Timezone      string `json:"timezone"`
	BusinessID    string `json:"business_id"`
	Status        int    `json:"status"`
	DisableReason int    `json:"disable_reason"`
}

type Page struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	InstagramID       string `json:"instagram_id,omitempty"`
	InstagramUsername string `json:"instagram_username,omitempty"`
	WhatsApp          bool   `json:"whatsapp"`
}

// Assets é o que o cliente liberou no login.
type Assets struct {
	UserID     string      `json:"user_id"`
	AdAccounts []AdAccount `json:"ad_accounts"`
	Pages      []Page      `json:"pages"`
}

// LoginURL abre o Facebook Login for Business com a configuração do app.
func (c *Client) LoginURL(state string) string {
	q := url.Values{"client_id": {c.AppID}, "config_id": {c.ConfigID}, "redirect_uri": {c.RedirectURI},
		"response_type": {"code"}, "override_default_response_type": {"true"}, "state": {state}}
	return c.DialogURL + "/" + version + "/dialog/oauth?" + q.Encode()
}

// ExchangeCode troca o código do retorno pelo token do cliente.
func (c *Client) ExchangeCode(ctx context.Context, code string) (string, error) {
	var out struct {
		AccessToken string `json:"access_token"`
	}
	q := url.Values{"client_id": {c.AppID}, "client_secret": {c.AppSecret}, "redirect_uri": {c.RedirectURI}, "code": {code}}
	if err := c.get(ctx, "/oauth/access_token", q, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("meta: troca do código sem token")
	}
	return out.AccessToken, nil
}

// Assets lê o usuário, as contas de anúncios e as Páginas (com Instagram e WhatsApp vinculados).
func (c *Client) Assets(ctx context.Context, token string) (Assets, error) {
	var a Assets
	var me struct {
		ID string `json:"id"`
	}
	if err := c.get(ctx, "/me", c.auth(token, url.Values{"fields": {"id"}}), &me); err != nil {
		return a, err
	}
	a.UserID = me.ID
	var accounts struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Status        int    `json:"account_status"`
			Currency      string `json:"currency"`
			Timezone      string `json:"timezone_name"`
			DisableReason int    `json:"disable_reason"`
			Business      struct {
				ID string `json:"id"`
			} `json:"business"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/me/adaccounts", c.auth(token, url.Values{"limit": {"100"},
		"fields": {"id,name,account_status,currency,timezone_name,disable_reason,business"}}), &accounts); err != nil {
		return a, err
	}
	for _, x := range accounts.Data {
		a.AdAccounts = append(a.AdAccounts, AdAccount{ID: x.ID, Name: x.Name, Currency: x.Currency, Timezone: x.Timezone,
			BusinessID: x.Business.ID, Status: x.Status, DisableReason: x.DisableReason})
	}
	var pages struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Instagram struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"instagram_business_account"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/me/accounts", c.auth(token, url.Values{"limit": {"100"},
		"fields": {"id,name,instagram_business_account{id,username}"}}), &pages); err != nil {
		return a, err
	}
	for _, x := range pages.Data {
		a.Pages = append(a.Pages, Page{ID: x.ID, Name: x.Name, InstagramID: x.Instagram.ID, InstagramUsername: x.Instagram.Username,
			WhatsApp: c.pageWhatsApp(ctx, token, x.ID)})
	}
	return a, nil
}

// pageWhatsApp diz se a Página tem número de WhatsApp vinculado. Erro (campo sem permissão) conta como não.
func (c *Client) pageWhatsApp(ctx context.Context, token, pageID string) bool {
	var p struct {
		WhatsApp string `json:"whatsapp_number"`
	}
	if err := c.get(ctx, "/"+url.PathEscape(pageID), c.auth(token, url.Values{"fields": {"whatsapp_number"}}), &p); err != nil {
		return false
	}
	return strings.TrimSpace(p.WhatsApp) != ""
}

func (c *Client) auth(token string, q url.Values) url.Values {
	m := hmac.New(sha256.New, []byte(c.AppSecret))
	m.Write([]byte(token))
	q.Set("access_token", token)
	q.Set("appsecret_proof", hex.EncodeToString(m.Sum(nil)))
	return q
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.GraphURL+"/"+version+path+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("meta: %s: requisição inválida", path)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// A URL tem token e segredo: o erro vai sem ela.
		return fmt.Errorf("meta: %s: falha de rede", path)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("meta: %s: resposta incompleta", path)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return fmt.Errorf("meta: %s: HTTP %d código %d: %s", path, resp.StatusCode, e.Error.Code, e.Error.Message)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("meta: %s: %w", path, err)
	}
	return nil
}

// ParseSignedRequest confere o signed_request (desautorização e exclusão de dados) e devolve o user_id.
func ParseSignedRequest(signed, appSecret string) (string, error) {
	sig64, payload, ok := strings.Cut(strings.TrimSpace(signed), ".")
	if !ok || sig64 == "" || payload == "" {
		return "", ErrBadSignature
	}
	sig, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(sig64, "="))
	if err != nil {
		return "", ErrBadSignature
	}
	m := hmac.New(sha256.New, []byte(appSecret))
	m.Write([]byte(payload))
	if !hmac.Equal(sig, m.Sum(nil)) {
		return "", ErrBadSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		return "", ErrBadSignature
	}
	var p struct {
		Algorithm string `json:"algorithm"`
		UserID    string `json:"user_id"`
	}
	if json.Unmarshal(raw, &p) != nil || !strings.EqualFold(p.Algorithm, "HMAC-SHA256") || p.UserID == "" {
		return "", ErrBadSignature
	}
	return p.UserID, nil
}
