package meta

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Leituras de saúde da conta para o escudo (internal/shield). Só leitura; nenhuma muda nada na Meta.

// AccountHealth é o estado da conta de anúncios: situação, motivo da desativação, moeda e business.
type AccountHealth struct {
	ID            string
	Name          string
	Currency      string
	Status        int // account_status: 1 ativa, 2 desativada, 3 pagamento pendente, 7 em análise de risco, 8 acerto pendente, 9 carência, 100 encerrando, 101 encerrada
	DisableReason int // disable_reason: 0 nenhum; ver shield para a tradução
	BusinessID    string
	BusinessName  string
}

// AccountHealth lê o estado da conta de anúncios.
func (c *Client) AccountHealth(ctx context.Context, token, adAccountID string) (AccountHealth, error) {
	acct := strings.TrimPrefix(strings.TrimSpace(adAccountID), "act_")
	var r struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Currency      string `json:"currency"`
		AccountStatus int    `json:"account_status"`
		DisableReason int    `json:"disable_reason"`
		Business      *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"business"`
	}
	q := url.Values{"fields": {"id,name,currency,account_status,disable_reason,business{id,name}"}}
	if err := c.call(ctx, http.MethodGet, "/act_"+acct, token, q, &r); err != nil {
		return AccountHealth{}, err
	}
	h := AccountHealth{ID: r.ID, Name: r.Name, Currency: r.Currency, Status: r.AccountStatus, DisableReason: r.DisableReason}
	if r.Business != nil {
		h.BusinessID, h.BusinessName = r.Business.ID, r.Business.Name
	}
	return h, nil
}

// BusinessVerification lê a verificação do business (verified, not_verified, pending, rejected…).
// Exige a permissão business_management: sem ela devolve erro, que quem chama trata como "não conferido".
func (c *Client) BusinessVerification(ctx context.Context, token, businessID string) (string, error) {
	var r struct {
		Verification string `json:"verification_status"`
	}
	q := url.Values{"fields": {"verification_status"}}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(strings.TrimSpace(businessID)), token, q, &r); err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(r.Verification)), nil
}

// PageAccess é o que o login do cliente enxerga da Página escolhida.
type PageAccess struct {
	Found       bool     // a Página ainda aparece nas Páginas do usuário
	Published   bool     // a Página está publicada (Página despublicada não veicula anúncio)
	Tasks       []string // papéis do usuário na Página (ADVERTISE, MANAGE, CREATE_CONTENT…)
	InstagramID string   // Instagram profissional vinculado à Página; vazio = nenhum
}

// CanAdvertise: o usuário pode anunciar pela Página. Sem a lista de papéis (a Meta não devolveu), conta como sim.
func (p PageAccess) CanAdvertise() bool {
	if len(p.Tasks) == 0 {
		return true
	}
	for _, t := range p.Tasks {
		if t == "ADVERTISE" || t == "MANAGE" {
			return true
		}
	}
	return false
}

// PageAccess lê a Página do cliente entre as Páginas do usuário (com papéis e Instagram vinculado).
func (c *Client) PageAccess(ctx context.Context, token, pageID string) (PageAccess, error) {
	var r struct {
		Data []struct {
			ID        string   `json:"id"`
			Tasks     []string `json:"tasks"`
			Published *bool    `json:"is_published"`
			Instagram *struct {
				ID string `json:"id"`
			} `json:"instagram_business_account"`
		} `json:"data"`
	}
	q := url.Values{"limit": {"100"}, "fields": {"id,tasks,is_published,instagram_business_account{id}"}}
	if err := c.call(ctx, http.MethodGet, "/me/accounts", token, q, &r); err != nil {
		return PageAccess{}, err
	}
	for _, p := range r.Data {
		if p.ID != pageID {
			continue
		}
		a := PageAccess{Found: true, Published: p.Published == nil || *p.Published, Tasks: p.Tasks}
		if p.Instagram != nil {
			a.InstagramID = p.Instagram.ID
		}
		return a, nil
	}
	return PageAccess{}, nil
}

// TokenInfo é a leitura do debug_token: se o token vale, as permissões dadas e até quando.
type TokenInfo struct {
	Valid     bool
	Scopes    []string  // vazio = a Meta não listou (não dá para concluir que faltam permissões)
	ExpiresAt time.Time // o menor entre a validade do token e a do acesso aos dados; zero = não vence
}

// HasScope diz se a permissão foi dada.
func (t TokenInfo) HasScope(s string) bool {
	for _, x := range t.Scopes {
		if x == s {
			return true
		}
	}
	return false
}

// TokenInfo confere o token do cliente com o token do app.
func (c *Client) TokenInfo(ctx context.Context, token string) (TokenInfo, error) {
	var out struct {
		Data struct {
			IsValid             bool     `json:"is_valid"`
			Scopes              []string `json:"scopes"`
			ExpiresAt           int64    `json:"expires_at"`
			DataAccessExpiresAt int64    `json:"data_access_expires_at"`
		} `json:"data"`
	}
	q := c.auth(c.AppID+"|"+c.AppSecret, url.Values{"input_token": {token}})
	if err := c.get(ctx, "/debug_token", q, &out); err != nil {
		return TokenInfo{}, err
	}
	t := TokenInfo{Valid: out.Data.IsValid, Scopes: out.Data.Scopes}
	var at int64
	for _, v := range []int64{out.Data.ExpiresAt, out.Data.DataAccessExpiresAt} {
		if v > 0 && (at == 0 || v < at) {
			at = v
		}
	}
	if at > 0 {
		t.ExpiresAt = time.Unix(at, 0).UTC()
	}
	return t, nil
}

// IsAuthError: o erro da Graph é de token inválido, vencido ou sem sessão (códigos 190 e 102).
func IsAuthError(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && (ae.Code == 190 || ae.Code == 102)
}
