# Conexão da conta Meta pelo WhatsApp — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** o cliente conecta a conta de anúncios Meta por um link enviado no WhatsApp (Facebook Login for Business) e recebe a confirmação e os avisos na conversa; a IA oferece a conexão quando a conta não está conectada.

**Architecture:** o worker (`engine`) envia o link `/m/{token}` (gatilho por texto, intent `connect_meta` da IA ou fim da prévia). A api (`httpapi.MetaConnect`) serve a página, recebe o retorno do Facebook, troca o código por token (`internal/meta`), cifra o token (`internal/secretbox`), grava a conexão (`store`) e enfileira a mensagem no WhatsApp (`engine.MetaConnectedJob`). Desautorização e exclusão de dados chegam por `signed_request`.

**Tech Stack:** Go 1.26, net/http ServeMux, pgx v5, html/template, crypto/aes + GCM, Graph API v23.0.

**Spec:** `docs/superpowers/specs/2026-09-25-conexao-meta-design.md`

## Global Constraints

- Módulo `github.com/n4grow-ops/gestor-trafego-ia`; comentários e mensagens em português do Brasil, no estilo do código vizinho.
- Testes com banco usam `TEST_DATABASE_URL='postgres://postgres:pg@localhost:55432/postgres?sslmode=disable'` (container `gti-pg`). Teste pulado (`SKIP`) conta como falha.
- Graph API `v23.0`; toda chamada com token leva `appsecret_proof` = HMAC-SHA256(token, app secret) em hex.
- Token Meta nunca vai para log, auditoria, histórico de mensagens ou IA.
- Link: 12 caracteres (`signup.NewToken`), só o hash no banco, validade 24 h, uso único, no máximo um novo a cada 10 min por identidade.
- Função desligada sem `META_APP_ID`, `META_APP_SECRET`, `META_LOGIN_CONFIG_ID`, `META_TOKEN_KEY` e `PUBLIC_BASE_URL`: nenhuma rota `/m/` ou `/meta/*`, nenhum gatilho no worker.
- Tabelas novas no schema do app, com RLS ligada (o teste `TestEveryTableHasRowLevelSecurity` cobre).
- Só o `owner` recebe link de conexão.

## Review Focus

- Cliente abre o link duas vezes ou volta do Facebook depois de já ter concluído: link usado → página "link já usado", sem gravar outra conexão (teste em Task 7).
- Cliente volta do Facebook num navegador diferente (sem cookie do nonce): 400 com página de erro e orientação para abrir o link de novo, sem trocar o código (teste em Task 7).
- Login concedido sem nenhuma conta de anúncios ou Página: página explica o que marcar e oferece tentar de novo com o mesmo link (teste em Task 7).
- Meta fora do ar ou código expirado na troca: página de erro amigável, link continua válido (teste em Task 7).
- Segunda conexão da mesma organização: a anterior vira `replaced`, token apagado, só uma `active` (teste em Task 3).

---

### Task 1: `internal/secretbox` — cifra do token

**Files:**
- Create: `internal/secretbox/secretbox.go`
- Test: `internal/secretbox/secretbox_test.go`

**Interfaces:**
- Produces: `secretbox.New(keyB64 string) (*Box, error)`; `(*Box).Seal(plain []byte) ([]byte, error)`; `(*Box).Open(sealed []byte) ([]byte, error)`; `secretbox.ErrOpen`.

- [ ] **Step 1: Write the failing test**

```go
package secretbox_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
)

func key(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := secretbox.New(key(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal([]byte("EAAB-token"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("EAAB-token")) {
		t.Fatal("texto aparece no cifrado")
	}
	again, _ := b.Seal([]byte("EAAB-token"))
	if bytes.Equal(sealed, again) {
		t.Fatal("nonce repetido: cifrados iguais")
	}
	plain, err := b.Open(sealed)
	if err != nil || string(plain) != "EAAB-token" {
		t.Fatalf("Open = %q, %v", plain, err)
	}
}

func TestOpenFailsWithWrongKeyOrTampering(t *testing.T) {
	a, _ := secretbox.New(key(t))
	other, _ := secretbox.New(key(t))
	sealed, _ := a.Seal([]byte("segredo"))
	if _, err := other.Open(sealed); !errors.Is(err, secretbox.ErrOpen) {
		t.Fatalf("chave errada: %v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := a.Open(sealed); !errors.Is(err, secretbox.ErrOpen) {
		t.Fatalf("adulterado: %v", err)
	}
	if _, err := a.Open([]byte("curto")); !errors.Is(err, secretbox.ErrOpen) {
		t.Fatalf("curto: %v", err)
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	for _, k := range []string{"", "nao-e-base64!", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := secretbox.New(k); err == nil {
			t.Errorf("chave %q aceita", k)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/secretbox/`
Expected: FAIL (pacote não existe).

- [ ] **Step 3: Write minimal implementation**

```go
// Package secretbox cifra segredos pequenos (tokens de terceiros) com AES-256-GCM.
// A chave fica só nas variáveis de ambiente: quem lê o banco sozinho não usa o segredo.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ErrOpen: cifrado adulterado, curto ou de outra chave.
var ErrOpen = errors.New("secretbox: não foi possível abrir o segredo")

type Box struct{ aead cipher.AEAD }

// New recebe a chave de 32 bytes em base64 padrão.
func New(keyB64 string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
	if err != nil || len(key) != 32 {
		return nil, errors.New("secretbox: a chave precisa ter 32 bytes em base64")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal devolve nonce aleatório + cifrado.
func (b *Box) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, ErrOpen
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/secretbox/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/secretbox
git commit -m "feat(meta): cifra AES-256-GCM para tokens de terceiros"
```

---

### Task 2: `internal/meta` — cliente da Graph API e `signed_request`

**Files:**
- Create: `internal/meta/meta.go`
- Test: `internal/meta/meta_test.go`

**Interfaces:**
- Produces:
  - `meta.New(appID, appSecret, configID, redirectURI string) *Client` (campos exportados `GraphURL`, `DialogURL`, `HTTP` para testes).
  - `(*Client).LoginURL(state string) string`
  - `(*Client).ExchangeCode(ctx context.Context, code string) (string, error)`
  - `(*Client).Assets(ctx context.Context, token string) (Assets, error)`
  - `meta.ParseSignedRequest(signed, appSecret string) (userID string, err error)`; `meta.ErrBadSignature`
  - Tipos: `AdAccount{ID, Name, Currency, Timezone, BusinessID string; Status, DisableReason int}`, `Page{ID, Name, InstagramID, InstagramUsername string; WhatsApp bool}`, `Assets{UserID string; AdAccounts []AdAccount; Pages []Page}` (todos com tags JSON snake_case, porque vão para `candidates` no banco).

- [ ] **Step 1: Write the failing test**

```go
package meta_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

const secret = "segredo-do-app"

func proof(token string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

func TestLoginURL(t *testing.T) {
	c := meta.New("123", secret, "cfg9", "https://x.test/meta/callback")
	u, err := url.Parse(c.LoginURL("tok.nonce"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "www.facebook.com" || u.Path != "/v23.0/dialog/oauth" || q.Get("client_id") != "123" || q.Get("config_id") != "cfg9" ||
		q.Get("redirect_uri") != "https://x.test/meta/callback" || q.Get("response_type") != "code" ||
		q.Get("override_default_response_type") != "true" || q.Get("state") != "tok.nonce" {
		t.Fatalf("url = %s", u)
	}
}

func graph(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path == "/v23.0/oauth/access_token" {
			if q.Get("code") == "ruim" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"message":"code expirado","code":100}}`))
				return
			}
			if q.Get("client_secret") != secret || q.Get("redirect_uri") != "https://x.test/meta/callback" {
				t.Errorf("troca sem segredo/redirect: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"access_token":"TOKEN1","token_type":"bearer"}`))
			return
		}
		if q.Get("access_token") != "TOKEN1" || q.Get("appsecret_proof") != proof("TOKEN1") {
			t.Errorf("chamada sem token/proof: %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/v23.0/me":
			w.Write([]byte(`{"id":"U1","name":"Ana"}`))
		case "/v23.0/me/adaccounts":
			w.Write([]byte(`{"data":[{"id":"act_1","name":"Loja","account_status":1,"currency":"BRL","timezone_name":"America/Sao_Paulo","disable_reason":0,"business":{"id":"B1"}}]}`))
		case "/v23.0/me/accounts":
			w.Write([]byte(`{"data":[{"id":"P1","name":"Loja Page","instagram_business_account":{"id":"IG1","username":"loja"}},{"id":"P2","name":"Outra"}]}`))
		case "/v23.0/P1":
			w.Write([]byte(`{"id":"P1","whatsapp_number":"+5511999990000"}`))
		case "/v23.0/P2":
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"sem permissão","code":10}}`))
		default:
			t.Errorf("caminho inesperado %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
}

func client(srv *httptest.Server) *meta.Client {
	c := meta.New("123", secret, "cfg9", "https://x.test/meta/callback")
	c.GraphURL = srv.URL
	return c
}

func TestExchangeCodeAndAssets(t *testing.T) {
	srv := graph(t)
	defer srv.Close()
	c := client(srv)
	tok, err := c.ExchangeCode(context.Background(), "bom")
	if err != nil || tok != "TOKEN1" {
		t.Fatalf("troca = %q, %v", tok, err)
	}
	a, err := c.Assets(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if a.UserID != "U1" || len(a.AdAccounts) != 1 || a.AdAccounts[0].BusinessID != "B1" || a.AdAccounts[0].Currency != "BRL" || a.AdAccounts[0].Status != 1 {
		t.Fatalf("contas = %+v", a)
	}
	if len(a.Pages) != 2 || a.Pages[0].InstagramUsername != "loja" || !a.Pages[0].WhatsApp || a.Pages[1].WhatsApp {
		t.Fatalf("páginas = %+v", a.Pages)
	}
}

func TestExchangeCodeErrorHasNoSecret(t *testing.T) {
	srv := graph(t)
	defer srv.Close()
	_, err := client(srv).ExchangeCode(context.Background(), "ruim")
	if err == nil || !strings.Contains(err.Error(), "code expirado") || strings.Contains(err.Error(), secret) {
		t.Fatalf("erro = %v", err)
	}
}

func signed(payload, key string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(payload))
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)) + "." + p
}

func TestParseSignedRequest(t *testing.T) {
	uid, err := meta.ParseSignedRequest(signed(`{"algorithm":"HMAC-SHA256","user_id":"U1"}`, secret), secret)
	if err != nil || uid != "U1" {
		t.Fatalf("uid = %q, %v", uid, err)
	}
	for _, s := range []string{
		signed(`{"algorithm":"HMAC-SHA256","user_id":"U1"}`, "outra-chave"),
		signed(`{"algorithm":"HMAC-SHA256"}`, secret),
		signed(`{"algorithm":"MD5","user_id":"U1"}`, secret),
		"sem-ponto", "",
	} {
		if _, err := meta.ParseSignedRequest(s, secret); !errors.Is(err, meta.ErrBadSignature) {
			t.Errorf("%q aceito: %v", s, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/meta/`
Expected: FAIL (pacote não existe).

- [ ] **Step 3: Write minimal implementation**

```go
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
		return fmt.Errorf("meta: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// A URL tem token e segredo: o erro vai sem ela.
		return fmt.Errorf("meta: %s: falha de rede", path)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("meta: %s: %w", path, err)
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/meta/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/meta
git commit -m "feat(meta): cliente da Graph API (login, contas, páginas) e signed_request"
```

---

### Task 3: migration 0010 + `store` da conexão + reset do contato

**Files:**
- Create: `internal/db/migrations/0010_meta_conexao.sql`
- Create: `internal/store/metaconnect.go`
- Test: `internal/store/metaconnect_test.go`
- Modify: `scripts/resetcontato/main.go` (lista `steps`, antes de `signup_links`)

**Interfaces:**
- Consumes: `meta.AdAccount`, `meta.Page` (Task 2); `store.JobSpec`, `enqueue` (existentes).
- Produces:
  - `store.ErrMetaLinkInvalid`
  - `(*Store).CreateMetaLink(ctx, identityID int64, tokenHash string, expiresAt time.Time) error`
  - `(*Store).MetaLinkSentWithin(ctx, identityID int64, within time.Duration) (bool, error)`
  - `store.MetaLink{ID, IdentityID, OrgID int64}`; `(*Store).MetaLink(ctx, tokenHash string) (MetaLink, error)` (inválido/usado/expirado → `ErrMetaLinkInvalid`)
  - `store.MetaCandidates{AdAccounts []meta.AdAccount; Pages []meta.Page}`
  - `(*Store).SavePendingMeta(ctx, link MetaLink, metaUserID string, sealedToken []byte, c MetaCandidates) (int64, error)`
  - `(*Store).PendingMeta(ctx, linkID int64) (int64, MetaCandidates, error)` (mais recente pendente do link; nenhum → `ErrMetaLinkInvalid`)
  - `store.MetaChoice{ConnectionID, LinkID int64; Account meta.AdAccount; Page meta.Page; Message func(convID int64, chatID string) JobSpec}`; `(*Store).CompleteMeta(ctx, MetaChoice) (orgID int64, err error)`
  - `store.MetaConnection{ID int64; AdAccountID, AdAccountName, Currency, PageID, PageName, InstagramUsername string; PageWhatsApp bool}`; `(*Store).ActiveMeta(ctx, orgID int64) (MetaConnection, bool, error)`
  - `(*Store).RevokeMeta(ctx, metaUserID, deletionCode string, notify func(convID int64, chatID string) JobSpec) (int, error)` (quantas conexões revogadas)
  - `(*Store).MetaDeletionDone(ctx, code string) (bool, error)`

- [ ] **Step 1: Write the migration**

`internal/db/migrations/0010_meta_conexao.sql`:

```sql
-- Conexão da conta de anúncios Meta do cliente (Facebook Login for Business).
CREATE TABLE meta_connect_links (
  id bigserial PRIMARY KEY,
  identity_id bigint NOT NULL REFERENCES whatsapp_identities(id),
  token_hash text NOT NULL UNIQUE,
  expires_at timestamptz NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX meta_connect_links_identity_idx ON meta_connect_links (identity_id, created_at DESC);
ALTER TABLE meta_connect_links ENABLE ROW LEVEL SECURITY;

CREATE TABLE meta_connections (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  connected_by bigint NOT NULL REFERENCES whatsapp_identities(id),
  link_id bigint NOT NULL REFERENCES meta_connect_links(id),
  meta_user_id text NOT NULL,
  business_id text,
  ad_account_id text,
  ad_account_name text,
  currency text,
  timezone_name text,
  account_status int,
  page_id text,
  page_name text,
  instagram_id text,
  instagram_username text,
  page_whatsapp boolean NOT NULL DEFAULT false,
  -- Token do cliente cifrado (AES-256-GCM, chave META_TOKEN_KEY). NULL depois de substituído ou revogado.
  token_ciphertext bytea,
  -- Contas e Páginas liberadas no login, para a escolha; limpo na conclusão.
  candidates jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active', 'replaced', 'revoked')),
  deletion_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  connected_at timestamptz,
  revoked_at timestamptz
);
CREATE UNIQUE INDEX meta_connections_active_idx ON meta_connections (organization_id) WHERE status = 'active';
CREATE INDEX meta_connections_user_idx ON meta_connections (meta_user_id);
CREATE INDEX meta_connections_link_idx ON meta_connections (link_id, created_at DESC);
CREATE INDEX meta_connections_deletion_idx ON meta_connections (deletion_code) WHERE deletion_code IS NOT NULL;
ALTER TABLE meta_connections ENABLE ROW LEVEL SECURITY;
```

- [ ] **Step 2: Write the failing test**

`internal/store/metaconnect_test.go` (pacote `store_test`; confira o nome do helper de setup usado em `internal/store/signup_test.go` e reaproveite; abaixo, a versão autocontida):

```go
package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

type metaEnv struct {
	st    *store.Store
	ident store.Identity
	ctx   context.Context
}

func newMetaEnv(t *testing.T) metaEnv {
	t.Helper()
	ctx := context.Background()
	st := store.New(testdb.New(t))
	org, err := st.CreateOrganization(ctx, "Loja", "moda", 100, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	mem, err := st.CreateMember(ctx, org, "Ana", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkPhone(ctx, "waha", "5511988887777", mem); err != nil {
		t.Fatal(err)
	}
	ident, err := st.FindIdentity(ctx, "waha", "5511988887777")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us"); err != nil {
		t.Fatal(err)
	}
	return metaEnv{st: st, ident: ident, ctx: ctx}
}

func (e metaEnv) link(t *testing.T, hash string, expires time.Duration) store.MetaLink {
	t.Helper()
	if err := e.st.CreateMetaLink(e.ctx, e.ident.ID, hash, time.Now().Add(expires)); err != nil {
		t.Fatal(err)
	}
	l, err := e.st.MetaLink(e.ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

var (
	acc  = meta.AdAccount{ID: "act_1", Name: "Loja", Currency: "BRL", Timezone: "America/Sao_Paulo", BusinessID: "B1", Status: 1}
	page = meta.Page{ID: "P1", Name: "Loja Page", InstagramID: "IG1", InstagramUsername: "loja", WhatsApp: true}
)

func msg(convID int64, chatID string) store.JobSpec {
	return store.JobSpec{Kind: "send", Payload: map[string]any{"conversation_id": convID, "chat_id": chatID, "text": "ok"}}
}

func TestMetaLinkValidity(t *testing.T) {
	e := newMetaEnv(t)
	l := e.link(t, "h1", time.Hour)
	if l.IdentityID != e.ident.ID || l.OrgID != e.ident.OrgID {
		t.Fatalf("link = %+v", l)
	}
	if err := e.st.CreateMetaLink(e.ctx, e.ident.ID, "h2", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"h2", "nao-existe"} {
		if _, err := e.st.MetaLink(e.ctx, h); !errors.Is(err, store.ErrMetaLinkInvalid) {
			t.Errorf("%s: %v", h, err)
		}
	}
	recent, err := e.st.MetaLinkSentWithin(e.ctx, e.ident.ID, 10*time.Minute)
	if err != nil || !recent {
		t.Fatalf("recente = %v, %v", recent, err)
	}
}

func TestCompleteMetaActivatesAndReplaces(t *testing.T) {
	e := newMetaEnv(t)
	l1 := e.link(t, "h1", time.Hour)
	id1, err := e.st.SavePendingMeta(e.ctx, l1, "U1", []byte("cifrado1"), store.MetaCandidates{AdAccounts: []meta.AdAccount{acc}, Pages: []meta.Page{page}})
	if err != nil {
		t.Fatal(err)
	}
	gotID, cands, err := e.st.PendingMeta(e.ctx, l1.ID)
	if err != nil || gotID != id1 || len(cands.AdAccounts) != 1 || cands.Pages[0].InstagramUsername != "loja" {
		t.Fatalf("pendente = %d %+v %v", gotID, cands, err)
	}
	org, err := e.st.CompleteMeta(e.ctx, store.MetaChoice{ConnectionID: id1, LinkID: l1.ID, Account: acc, Page: page, Message: msg})
	if err != nil || org != e.ident.OrgID {
		t.Fatalf("conclusão = %d, %v", org, err)
	}
	if _, err := e.st.MetaLink(e.ctx, "h1"); !errors.Is(err, store.ErrMetaLinkInvalid) {
		t.Fatal("link continua valendo depois de usado")
	}
	if _, err := e.st.CompleteMeta(e.ctx, store.MetaChoice{ConnectionID: id1, LinkID: l1.ID, Account: acc, Page: page, Message: msg}); !errors.Is(err, store.ErrMetaLinkInvalid) {
		t.Fatalf("segunda conclusão: %v", err)
	}
	c, ok, err := e.st.ActiveMeta(e.ctx, e.ident.OrgID)
	if err != nil || !ok || c.AdAccountID != "act_1" || c.PageName != "Loja Page" || c.InstagramUsername != "loja" || !c.PageWhatsApp || c.Currency != "BRL" {
		t.Fatalf("ativa = %+v %v %v", c, ok, err)
	}

	l2 := e.link(t, "h3", time.Hour)
	id2, _ := e.st.SavePendingMeta(e.ctx, l2, "U1", []byte("cifrado2"), store.MetaCandidates{AdAccounts: []meta.AdAccount{acc}, Pages: []meta.Page{page}})
	acc2 := acc
	acc2.ID, acc2.Name = "act_2", "Loja 2"
	if _, err := e.st.CompleteMeta(e.ctx, store.MetaChoice{ConnectionID: id2, LinkID: l2.ID, Account: acc2, Page: page, Message: msg}); err != nil {
		t.Fatal(err)
	}
	c, _, _ = e.st.ActiveMeta(e.ctx, e.ident.OrgID)
	if c.AdAccountID != "act_2" {
		t.Fatalf("ativa = %+v", c)
	}
	var replaced int
	var token []byte
	if err := e.st.Pool().QueryRow(e.ctx, `SELECT count(*) FROM meta_connections WHERE status = 'replaced' AND token_ciphertext IS NULL`).Scan(&replaced); err != nil || replaced != 1 {
		t.Fatalf("substituídas = %d, %v", replaced, err)
	}
	_ = token
	var jobs int
	e.st.Pool().QueryRow(e.ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = 'send'`).Scan(&jobs)
	if jobs != 2 {
		t.Fatalf("mensagens na fila = %d", jobs)
	}
}

func TestRevokeMeta(t *testing.T) {
	e := newMetaEnv(t)
	l := e.link(t, "h1", time.Hour)
	id, _ := e.st.SavePendingMeta(e.ctx, l, "U9", []byte("cifrado"), store.MetaCandidates{AdAccounts: []meta.AdAccount{acc}, Pages: []meta.Page{page}})
	if _, err := e.st.CompleteMeta(e.ctx, store.MetaChoice{ConnectionID: id, LinkID: l.ID, Account: acc, Page: page, Message: msg}); err != nil {
		t.Fatal(err)
	}
	n, err := e.st.RevokeMeta(e.ctx, "U9", "COD123", msg)
	if err != nil || n != 1 {
		t.Fatalf("revogadas = %d, %v", n, err)
	}
	if _, ok, _ := e.st.ActiveMeta(e.ctx, e.ident.OrgID); ok {
		t.Fatal("continua ativa")
	}
	done, err := e.st.MetaDeletionDone(e.ctx, "COD123")
	if err != nil || !done {
		t.Fatalf("exclusão = %v, %v", done, err)
	}
	if done, _ := e.st.MetaDeletionDone(e.ctx, "OUTRO"); done {
		t.Fatal("código desconhecido dado como feito")
	}
	if n, _ := e.st.RevokeMeta(e.ctx, "U9", "", msg); n != 0 {
		t.Fatalf("revogou de novo: %d", n)
	}
}
```

Se `store.Store` não tiver `Pool()`, adicione em `internal/store/store.go`: `func (s *Store) Pool() *pgxpool.Pool { return s.pool }` (confira antes com `grep -n "func (s \*Store) Pool" internal/store/*.go`; se já existir outro acesso ao pool nos testes do pacote, use-o e remova o `_ = token`).

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'Meta'`
Expected: FAIL (funções não existem).

- [ ] **Step 4: Write the implementation**

`internal/store/metaconnect.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// ErrMetaLinkInvalid: link de conexão inexistente, expirado ou já usado.
var ErrMetaLinkInvalid = errors.New("store: link de conexão inválido ou expirado")

// CreateMetaLink guarda o hash de um link de conexão da conta Meta para a identidade.
func (s *Store) CreateMetaLink(ctx context.Context, identityID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO meta_connect_links (identity_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		identityID, tokenHash, expiresAt)
	return err
}

// MetaLinkSentWithin diz se a identidade recebeu link de conexão dentro do intervalo (relógio do banco).
func (s *Store) MetaLinkSentWithin(ctx context.Context, identityID int64, within time.Duration) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meta_connect_links
		WHERE identity_id = $1 AND created_at > now() - $2 * interval '1 millisecond')`, identityID, within.Milliseconds()).Scan(&ok)
	return ok, err
}

type MetaLink struct {
	ID         int64
	IdentityID int64
	OrgID      int64
}

// MetaLink confere o link: existe, não foi usado e não expirou.
func (s *Store) MetaLink(ctx context.Context, tokenHash string) (MetaLink, error) {
	var l MetaLink
	err := s.pool.QueryRow(ctx, `SELECT l.id, l.identity_id, m.organization_id
		FROM meta_connect_links l
		JOIN whatsapp_identities wi ON wi.id = l.identity_id
		JOIN members m ON m.id = wi.member_id
		WHERE l.token_hash = $1 AND l.used_at IS NULL AND l.expires_at > now()`, tokenHash).Scan(&l.ID, &l.IdentityID, &l.OrgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetaLink{}, ErrMetaLinkInvalid
	}
	return l, err
}

// MetaCandidates são as contas e Páginas liberadas no login, guardadas para a escolha.
type MetaCandidates struct {
	AdAccounts []meta.AdAccount `json:"ad_accounts"`
	Pages      []meta.Page      `json:"pages"`
}

// SavePendingMeta grava o retorno do login (token já cifrado) à espera da escolha da conta e da Página.
func (s *Store) SavePendingMeta(ctx context.Context, l MetaLink, metaUserID string, sealedToken []byte, c MetaCandidates) (int64, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO meta_connections (organization_id, connected_by, link_id, meta_user_id, token_ciphertext, candidates)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, l.OrgID, l.IdentityID, l.ID, metaUserID, sealedToken, raw).Scan(&id)
	return id, err
}

// PendingMeta devolve a conexão pendente mais recente do link, com as opções da escolha.
func (s *Store) PendingMeta(ctx context.Context, linkID int64) (int64, MetaCandidates, error) {
	var id int64
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id, candidates FROM meta_connections WHERE link_id = $1 AND status = 'pending'
		ORDER BY created_at DESC, id DESC LIMIT 1`, linkID).Scan(&id, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, MetaCandidates{}, ErrMetaLinkInvalid
	}
	if err != nil {
		return 0, MetaCandidates{}, err
	}
	var c MetaCandidates
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, MetaCandidates{}, fmt.Errorf("store: %w", err)
	}
	return id, c, nil
}

// MetaChoice fecha a conexão com a conta e a Página escolhidas.
type MetaChoice struct {
	ConnectionID int64
	LinkID       int64
	Account      meta.AdAccount
	Page         meta.Page
	// Message monta a confirmação no WhatsApp, enfileirada na mesma transação.
	Message func(convID int64, chatID string) JobSpec
}

// CompleteMeta ativa a conexão numa transação: link usado, conexão ativa anterior substituída
// (token apagado), escolha gravada e confirmação na fila. Devolve a organização.
func (s *Store) CompleteMeta(ctx context.Context, ch MetaChoice) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE meta_connect_links SET used_at = now() WHERE id = $1 AND used_at IS NULL AND expires_at > now()`, ch.LinkID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrMetaLinkInvalid
	}
	var orgID, identityID int64
	err = tx.QueryRow(ctx, `SELECT organization_id, connected_by FROM meta_connections WHERE id = $1 AND link_id = $2 AND status = 'pending'`,
		ch.ConnectionID, ch.LinkID).Scan(&orgID, &identityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrMetaLinkInvalid
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE meta_connections SET status = 'replaced', token_ciphertext = NULL
		WHERE organization_id = $1 AND status = 'active'`, orgID); err != nil {
		return 0, err
	}
	a, p := ch.Account, ch.Page
	if _, err := tx.Exec(ctx, `UPDATE meta_connections SET status = 'active', connected_at = now(), candidates = '{}',
			business_id = nullif($2, ''), ad_account_id = $3, ad_account_name = $4, currency = $5, timezone_name = $6, account_status = $7,
			page_id = $8, page_name = $9, instagram_id = nullif($10, ''), instagram_username = nullif($11, ''), page_whatsapp = $12
		WHERE id = $1`, ch.ConnectionID, a.BusinessID, a.ID, a.Name, a.Currency, a.Timezone, a.Status,
		p.ID, p.Name, p.InstagramID, p.InstagramUsername, p.WhatsApp); err != nil {
		return 0, err
	}
	var convID int64
	var chatID string
	err = tx.QueryRow(ctx, `SELECT id, chat_id FROM conversations WHERE identity_id = $1`, identityID).Scan(&convID, &chatID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if err == nil && chatID != "" && ch.Message != nil {
		if err := enqueue(ctx, tx, ch.Message(convID, chatID)); err != nil {
			return 0, err
		}
	}
	return orgID, tx.Commit(ctx)
}

// MetaConnection é a conexão ativa, sem o token.
type MetaConnection struct {
	ID                int64
	AdAccountID       string
	AdAccountName     string
	Currency          string
	PageID            string
	PageName          string
	InstagramUsername string
	PageWhatsApp      bool
}

// ActiveMeta devolve a conexão ativa da organização, se houver.
func (s *Store) ActiveMeta(ctx context.Context, orgID int64) (MetaConnection, bool, error) {
	var c MetaConnection
	err := s.pool.QueryRow(ctx, `SELECT id, ad_account_id, coalesce(ad_account_name, ''), coalesce(currency, ''), page_id, coalesce(page_name, ''),
			coalesce(instagram_username, ''), page_whatsapp
		FROM meta_connections WHERE organization_id = $1 AND status = 'active'`, orgID).
		Scan(&c.ID, &c.AdAccountID, &c.AdAccountName, &c.Currency, &c.PageID, &c.PageName, &c.InstagramUsername, &c.PageWhatsApp)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetaConnection{}, false, nil
	}
	return c, err == nil, err
}

// RevokeMeta desliga as conexões do usuário Meta (removeu o app ou pediu exclusão): token e opções
// apagados, código da exclusão gravado e aviso no WhatsApp para cada conexão que estava ativa.
func (s *Store) RevokeMeta(ctx context.Context, metaUserID, deletionCode string, notify func(convID int64, chatID string) JobSpec) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `UPDATE meta_connections mc SET status = 'revoked', token_ciphertext = NULL, candidates = '{}',
			revoked_at = now(), deletion_code = coalesce(nullif($2, ''), mc.deletion_code)
		FROM meta_connections old
		WHERE old.id = mc.id AND mc.meta_user_id = $1 AND mc.status IN ('active', 'pending')
		RETURNING old.status, mc.connected_by`, metaUserID, deletionCode)
	if err != nil {
		return 0, err
	}
	type revoked struct {
		status     string
		identityID int64
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (revoked, error) {
		var v revoked
		return v, r.Scan(&v.status, &v.identityID)
	})
	if err != nil {
		return 0, err
	}
	for _, v := range list {
		if v.status != "active" || notify == nil {
			continue
		}
		var convID int64
		var chatID string
		err := tx.QueryRow(ctx, `SELECT id, chat_id FROM conversations WHERE identity_id = $1`, v.identityID).Scan(&convID, &chatID)
		if errors.Is(err, pgx.ErrNoRows) || chatID == "" {
			continue
		}
		if err != nil {
			return 0, err
		}
		if err := enqueue(ctx, tx, notify(convID, chatID)); err != nil {
			return 0, err
		}
	}
	return len(list), tx.Commit(ctx)
}

// MetaDeletionDone diz se o pedido de exclusão com esse código já foi atendido.
func (s *Store) MetaDeletionDone(ctx context.Context, code string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meta_connections WHERE deletion_code = $1 AND status = 'revoked')`, code).Scan(&ok)
	return ok, err
}
```

Observação do `RevokeMeta`: o `UPDATE … FROM meta_connections old WHERE old.id = mc.id` devolve em `old.status` o status **antes** da atualização (a junção lê o snapshot do início do comando). Assim só as conexões que estavam `active` geram aviso.

- [ ] **Step 5: Update the reset script**

Em `scripts/resetcontato/main.go`, na lista `steps`, antes da linha de `signup_links`:

```go
		{"meta_connections", `DELETE FROM meta_connections WHERE organization_id = ANY($1)`},
		{"meta_connect_links", `DELETE FROM meta_connect_links WHERE identity_id IN (SELECT w.id FROM whatsapp_identities w JOIN members m ON m.id = w.member_id WHERE m.organization_id = ANY($1))`},
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/store/ ./internal/db/ && go build ./scripts/...`
Expected: PASS (inclui `TestEveryTableHasRowLevelSecurity` com as tabelas novas).

- [ ] **Step 7: Commit**

```bash
git add internal/db/migrations/0010_meta_conexao.sql internal/store scripts/resetcontato/main.go
git commit -m "feat(meta): tabelas e store da conexão da conta de anúncios"
```

---

### Task 4: configuração

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: campos `MetaAppID`, `MetaAppSecret`, `MetaLoginConfigID`, `MetaTokenKey string`; `(Config).MetaConnectEnabled() bool`.

- [ ] **Step 1: Write the failing test** (acrescente ao arquivo de teste existente)

```go
func TestMetaConnectEnabled(t *testing.T) {
	env := map[string]string{"PUBLIC_BASE_URL": "https://cadastro.x/", "META_APP_ID": "1", "META_APP_SECRET": "s",
		"META_LOGIN_CONFIG_ID": "c", "META_TOKEN_KEY": "k"}
	get := func(k string) string { return env[k] }
	c := config.Load(get)
	if !c.MetaConnectEnabled() || c.MetaAppID != "1" || c.MetaAppSecret != "s" || c.MetaLoginConfigID != "c" || c.MetaTokenKey != "k" {
		t.Fatalf("config = %+v", c)
	}
	for _, k := range []string{"PUBLIC_BASE_URL", "META_APP_ID", "META_APP_SECRET", "META_LOGIN_CONFIG_ID", "META_TOKEN_KEY"} {
		old := env[k]
		env[k] = ""
		if config.Load(get).MetaConnectEnabled() {
			t.Errorf("ligado sem %s", k)
		}
		env[k] = old
	}
}
```

(Se o arquivo de teste estiver no pacote `config` e não `config_test`, troque `config.Load` por `Load`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL (campos não existem).

- [ ] **Step 3: Implement**

No `struct Config`, depois de `ReportPhone string`:

```go

	// Conexão da conta de anúncios do cliente (Facebook Login for Business).
	MetaAppID         string
	MetaAppSecret     string
	MetaLoginConfigID string
	MetaTokenKey      string // 32 bytes em base64: cifra os tokens dos clientes
```

Depois de `SignupEnabled`:

```go
// MetaConnectEnabled diz se a conexão da conta Meta está configurada.
func (c Config) MetaConnectEnabled() bool {
	return c.PublicBaseURL != "" && c.MetaAppID != "" && c.MetaAppSecret != "" && c.MetaLoginConfigID != "" && c.MetaTokenKey != ""
}
```

Em `Load`, depois de `ReportPhone: …`:

```go
		MetaAppID:              getenv("META_APP_ID"),
		MetaAppSecret:          getenv("META_APP_SECRET"),
		MetaLoginConfigID:      getenv("META_LOGIN_CONFIG_ID"),
		MetaTokenKey:           getenv("META_TOKEN_KEY"),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(meta): variáveis da conexão com a Meta"
```

---

### Task 5: Atendente — intent `connect_meta` e estado da conexão no prompt

**Files:**
- Modify: `internal/agent/contract/contract.go` (constantes e `Valid`)
- Modify: `internal/agent/attendant/attendant.go` (`Input`, `userPrompt`)
- Modify: `internal/agent/attendant/prompt.go` (lista de intents + regra)
- Test: `internal/agent/attendant/attendant_test.go`, `internal/agent/contract/contract_test.go`

**Interfaces:**
- Produces: `contract.IntentConnectMeta Intent = "connect_meta"`; `attendant.Input.Meta string` — `""` (função desligada: nada no prompt), `"não conectada"` ou `"conectada: {conta} · Página {página}"`.

- [ ] **Step 1: Write the failing tests**

Em `internal/agent/contract/contract_test.go`:

```go
func TestConnectMetaIntentIsValid(t *testing.T) {
	if !contract.IntentConnectMeta.Valid() || contract.IntentConnectMeta != "connect_meta" {
		t.Fatal("connect_meta precisa ser intent válida")
	}
}
```

Em `internal/agent/attendant/attendant_test.go` (use o fake de LLM que os testes do arquivo já usam; o padrão é `llm.Fake` com resposta fixa e leitura do último pedido — confira o nome do método que devolve as mensagens enviadas, por exemplo `f.Requests[len(f.Requests)-1]`, e ajuste):

```go
func TestPromptCarriesMetaConnection(t *testing.T) {
	f := &llm.Fake{}
	f.Responses = append(f.Responses, `{"intent":"connect_meta","reply":"Te mando o link agora."}`)
	a := &attendant.Agent{LLM: f, Model: "m"}
	turn, _, err := a.Reply(context.Background(), attendant.Input{OrgName: "Loja", State: "idle", Message: "como publico?", Meta: "não conectada"})
	if err != nil || turn.Intent != contract.IntentConnectMeta {
		t.Fatalf("turn = %+v, %v", turn, err)
	}
	req := f.Requests[len(f.Requests)-1]
	sys, user := req.Messages[0].Content, req.Messages[1].Content
	if !strings.Contains(user, "Conta Meta: não conectada") {
		t.Fatalf("prompt sem estado da conta: %s", user)
	}
	if !strings.Contains(sys, "connect_meta") || !strings.Contains(sys, "Conta Meta") {
		t.Fatal("system prompt sem a regra da conexão")
	}
	f.Responses = append(f.Responses, `{"intent":"question","reply":"oi"}`)
	a.Reply(context.Background(), attendant.Input{OrgName: "Loja", State: "idle", Message: "oi"})
	if strings.Contains(f.Requests[len(f.Requests)-1].Messages[1].Content, "Conta Meta") {
		t.Fatal("função desligada não deve aparecer no prompt")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/...`
Expected: FAIL (`IntentConnectMeta` e `Input.Meta` não existem).

- [ ] **Step 3: Implement**

`contract.go`: acrescente `IntentConnectMeta Intent = "connect_meta"` ao bloco `const` e a `Valid()` (`…, IntentProjectStatus, IntentConnectMeta:`).

`attendant.go`, em `Input`, depois de `Voice bool …`:

```go
	// Meta: estado da conta de anúncios ("não conectada" ou "conectada: …"); vazio = conexão desligada.
	Meta string
```

Em `userPrompt`, logo depois do `fmt.Fprintf(&b, "Estado da conversa: …")`:

```go
	if in.Meta != "" {
		fmt.Fprintf(&b, "Conta Meta: %s\n", in.Meta)
	}
```

`prompt.go`: na lista de intents, depois de `- project_status: …`:

```
- connect_meta: o cliente quer conectar a conta de anúncios do Facebook/Instagram, pediu o link de conexão ou aceitou a sua oferta de conectar
```

E, no fim das regras (antes da linha sobre `<cliente>`):

```
Conta Meta: se aparecer "Conta Meta: não conectada" e o assunto for publicar, anunciar, quando a campanha vai ao ar ou o status de um projeto pronto, avise em uma frase que falta conectar a conta de anúncios do Facebook/Instagram e ofereça mandar o link. Não repita a oferta se você já ofereceu nas últimas 6 mensagens do histórico. Se o cliente aceitar ou pedir, use intent connect_meta e diga só que o link vai em seguida: nunca escreva link. Se aparecer "Conta Meta: conectada", pode dizer qual conta está conectada quando perguntarem. Se não aparecer "Conta Meta", não fale de conexão.
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent
git commit -m "feat(meta): Atendente sabe da conexão da conta e pede o link (connect_meta)"
```

---

### Task 6: engine — envio do link, gatilhos e mensagens

**Files:**
- Create: `internal/engine/metaconnect.go`
- Modify: `internal/engine/engine.go` (campo `MetaConnectBaseURL`; `dispatch`; `converse`: `Meta` no `attendant.Input` e `case contract.IntentConnectMeta`)
- Modify: `internal/engine/signup.go` (regex de `redactLinks` inclui `m`)
- Modify: `internal/engine/preview.go` (fim de `sendPreview`)
- Modify: `internal/engine/messages.go` (mensagens novas)
- Test: `internal/engine/metaconnect_test.go`

**Interfaces:**
- Consumes: `store.CreateMetaLink`, `store.MetaLinkSentWithin`, `store.ActiveMeta` (Task 3); `contract.IntentConnectMeta`, `attendant.Input.Meta` (Task 5); `signup.NewToken`.
- Produces:
  - `Engine.MetaConnectBaseURL string` (ex.: `https://cadastro.meuanuncio.io/m`; vazio = desligado)
  - `engine.MetaSummary{AccountName, Currency, Timezone, PageName, Instagram string; AccountStatus, DisableReason int; PageWhatsApp bool}`
  - `engine.MetaConnectedJob(convID int64, chatID string, s MetaSummary) store.JobSpec`
  - `engine.MetaRevokedJob(convID int64, chatID string) store.JobSpec`

- [ ] **Step 1: Write the failing tests**

`internal/engine/metaconnect_test.go`:

```go
package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const metaBase = "https://cadastro.teste/m"

func newMetaHarness(t *testing.T, role string) *harness {
	h := newHarness(t, role)
	h.eng.MetaConnectBaseURL = metaBase
	return h
}

func (h *harness) sentText() string {
	h.t.Helper()
	return strings.Join(h.wa.to(chatID), "\n")
}

func TestConnectMetaCommandSendsLinkWithoutAI(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.say("quero conectar a conta de anúncios")
	if h.att.Calls() != 0 {
		t.Fatal("comando não deve chamar a IA")
	}
	if !strings.Contains(h.sentText(), metaBase+"/") {
		t.Fatalf("sem link: %q", h.sentText())
	}
	if n := h.count(`SELECT count(*) FROM meta_connect_links`); n != 1 {
		t.Fatalf("links = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE body LIKE '%/m/[link]%'`); n != 1 {
		t.Fatal("link não foi mascarado no histórico")
	}
	h.say("conectar meta")
	if n := h.count(`SELECT count(*) FROM meta_connect_links`); n != 1 {
		t.Fatal("reenviou link em menos de 10 minutos")
	}
	if !strings.Contains(h.sentText(), "há pouco") {
		t.Fatalf("sem aviso de link recente: %q", h.sentText())
	}
}

func TestConnectMetaOnlyForOwner(t *testing.T) {
	h := newMetaHarness(t, "member")
	h.say("conectar facebook")
	if h.count(`SELECT count(*) FROM meta_connect_links`) != 0 || !strings.Contains(h.sentText(), "responsável") {
		t.Fatalf("membro recebeu link: %q", h.sentText())
	}
}

func TestConnectMetaCommandIgnoredWhenDisabled(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Posso ajudar?"}`)
	h.say("conectar meta")
	if h.att.Calls() != 1 || h.count(`SELECT count(*) FROM meta_connect_links`) != 0 {
		t.Fatal("desligado: vai para a IA e não gera link")
	}
}

func TestAttendantConnectMetaIntentSendsReplyThenLink(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.att.Responses = append(h.att.Responses, `{"intent":"connect_meta","reply":"Claro! Te mando o link pra conectar."}`)
	h.say("pode mandar o negócio do face")
	txt := h.sentText()
	if !strings.Contains(txt, "Claro!") || !strings.Contains(txt, metaBase+"/") {
		t.Fatalf("enviado = %q", txt)
	}
	if strings.Index(txt, "Claro!") > strings.Index(txt, metaBase) {
		t.Fatal("link veio antes da resposta")
	}
	if !strings.Contains(h.att.Requests[len(h.att.Requests)-1].Messages[1].Content, "Conta Meta: não conectada") {
		t.Fatal("IA não soube que a conta não está conectada")
	}
}

func TestAttendantSeesConnectedAccount(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.connectMeta()
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Tá conectada sim."}`)
	h.say("minha conta tá ligada?")
	if !strings.Contains(h.att.Requests[len(h.att.Requests)-1].Messages[1].Content, "Conta Meta: conectada: Loja") {
		t.Fatal("IA não recebeu a conta conectada")
	}
}

// connectMeta grava uma conexão ativa direto pelo store.
func (h *harness) connectMeta() {
	h.t.Helper()
	ident, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	h.must(h.st.CreateMetaLink(ctx, ident.ID, "hash-teste", time.Now().Add(time.Hour)))
	l, err := h.st.MetaLink(ctx, "hash-teste")
	h.must(err)
	acc := meta.AdAccount{ID: "act_1", Name: "Loja", Currency: "BRL", Status: 1}
	pg := meta.Page{ID: "P1", Name: "Loja Page"}
	id, err := h.st.SavePendingMeta(ctx, l, "U1", []byte("x"), store.MetaCandidates{AdAccounts: []meta.AdAccount{acc}, Pages: []meta.Page{pg}})
	h.must(err)
	_, err = h.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id, LinkID: l.ID, Account: acc, Page: pg})
	h.must(err)
}

func TestMetaConnectedMessage(t *testing.T) {
	job := engine.MetaConnectedJob(1, chatID, engine.MetaSummary{AccountName: "Conta 1", Currency: "USD", Timezone: "America/Los_Angeles",
		AccountStatus: 2, PageName: "Clara Luz"})
	text := strings.Join(append([]string{job.Payload.(engine.SendJob).Text}, job.Payload.(engine.SendJob).Next...), "\n")
	for _, want := range []string{"Conectado", "Conta 1", "Clara Luz", "sem Instagram", "USD", "desativada", "WhatsApp Business"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q em %q", want, text)
		}
	}
	ok := engine.MetaConnectedJob(1, chatID, engine.MetaSummary{AccountName: "Loja", Currency: "BRL", Timezone: "America/Sao_Paulo",
		AccountStatus: 1, PageName: "Loja", Instagram: "loja", PageWhatsApp: true})
	okText := strings.Join(append([]string{ok.Payload.(engine.SendJob).Text}, ok.Payload.(engine.SendJob).Next...), "\n")
	if strings.Contains(okText, "WhatsApp Business") || strings.Contains(okText, "moeda") || !strings.Contains(okText, "@loja") {
		t.Fatalf("sem avisos esperado: %q", okText)
	}
}
```

E um teste do gatilho depois da prévia, acrescentado ao arquivo de testes da prévia (siga o teste existente que dispara `JobCreativesPreview` e confere `h.wa.sentImages()`; confira com `grep -n "sentImages" internal/engine/*_test.go` qual função monta o rascunho aprovado com anúncios e reaproveite):

```go
func TestPreviewAsksForMetaConnection(t *testing.T) {
	// Monte o cenário igual ao teste existente da prévia, com h.eng.MetaConnectBaseURL = metaBase,
	// rode o job da prévia e confira:
	//   strings.Contains(h.sentText(), metaBase+"/") == true
	// Depois h.connectMeta(), rode de novo a prévia (outro rascunho) e confira que não saiu link novo:
	//   h.count(`SELECT count(*) FROM meta_connect_links WHERE token_hash <> 'hash-teste'`) == 1
}
```

O executor substitui os comentários pelo corpo real, copiando o arranjo do teste existente da prévia (mesmo arquivo, mesmo helper).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/engine/ -run 'Meta'`
Expected: FAIL (campo `MetaConnectBaseURL`, `MetaSummary` e `MetaConnectedJob` não existem).

- [ ] **Step 3: Implement messages**

Em `internal/engine/messages.go`, no bloco de constantes:

```go
	msgMetaLink        = "Pra eu publicar seus anúncios, conecta sua conta de anúncios do Facebook/Instagram por aqui:\n\n%s\n\nVocê entra com o seu Facebook e escolhe a conta e a Página. Leva 1 minuto e o link vale por 24 horas."
	msgMetaLinkRecent  = "Te mandei o link de conexão há pouco, é só abrir ele 🙂 Se não achar, me pede de novo daqui a uns minutos."
	msgMetaOwnerOnly   = "Quem conecta a conta de anúncios é o responsável pela conta aqui no meuanuncio. Pede pra ele me mandar \"conectar meta\" 🙂"
	msgMetaConnected   = "Conectado ✅\n\nConta de anúncios: %s (%s)\nPágina: %s\nInstagram: %s"
	msgMetaWarnStatus  = "⚠️ Essa conta de anúncios está %s na Meta. Enquanto isso, nada vai ao ar: dá uma olhada no Gerenciador de Anúncios."
	msgMetaWarnLocale  = "⚠️ Essa conta está em %s e no fuso %s. Funciona, mas a cobrança e os relatórios ficam nessa moeda e nesse horário. Se preferir reais e horário de Brasília, dá pra criar uma conta nova e conectar de novo."
	msgMetaWarnWA      = "⚠️ Sua Página ainda não tem WhatsApp vinculado, e anúncio que leva pro WhatsApp precisa disso. No WhatsApp Business: Configurações → Ferramentas comerciais → Facebook e Instagram → entra com o Facebook e escolhe a Página. Depois me avisa aqui."
	msgMetaRevoked     = "Sua conta de anúncios foi desconectada do meuanuncio (o acesso foi removido no Facebook). Se quiser conectar de novo, é só me mandar \"conectar meta\"."
```

- [ ] **Step 4: Implement `internal/engine/metaconnect.go`**

```go
package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const (
	metaLinkTTL    = 24 * time.Hour
	metaLinkResend = 10 * time.Minute
)

// metaConnectAsk reconhece o pedido direto de conexão, sem gastar IA.
var metaConnectAsk = regexp.MustCompile(`(?i)\bconectar\s+(a\s+)?(meta|facebook|face|conta\s+de\s+an[uú]ncios?)\b`)

// sendMetaLink manda o link de conexão da conta de anúncios; lead é a resposta da IA que vem antes (pode ser vazia).
func (e *Engine) sendMetaLink(ctx context.Context, ident store.Identity, conv store.Conversation, lead string) error {
	if ident.Role != "owner" {
		return e.send(ctx, conv, join(lead, msgMetaOwnerOnly))
	}
	recent, err := e.Store.MetaLinkSentWithin(ctx, ident.ID, metaLinkResend)
	if err != nil {
		return err
	}
	if recent {
		return e.send(ctx, conv, join(lead, msgMetaLinkRecent))
	}
	raw, hash, err := signup.NewToken()
	if err != nil {
		return err
	}
	if err := e.Store.CreateMetaLink(ctx, ident.ID, hash, e.now().Add(metaLinkTTL)); err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "meta_link_sent", map[string]any{"identity_id": ident.ID})
	return e.send(ctx, conv, join(lead, fmt.Sprintf(msgMetaLink, strings.TrimRight(e.MetaConnectBaseURL, "/")+"/"+raw)))
}

// metaStatus é o estado da conta que vai para a IA; vazio com a conexão desligada.
func (e *Engine) metaStatus(ctx context.Context, orgID int64) (string, error) {
	if e.MetaConnectBaseURL == "" {
		return "", nil
	}
	c, ok, err := e.Store.ActiveMeta(ctx, orgID)
	if err != nil || !ok {
		return "não conectada", err
	}
	return fmt.Sprintf("conectada: %s · Página %s", c.AdAccountName, c.PageName), nil
}

// MetaSummary é o que a confirmação da conexão mostra.
type MetaSummary struct {
	AccountName   string
	Currency      string
	Timezone      string
	AccountStatus int
	DisableReason int
	PageName      string
	Instagram     string
	PageWhatsApp  bool
}

// MetaConnectedJob é a confirmação no WhatsApp, enfileirada pela api quando a conexão fecha.
func MetaConnectedJob(convID int64, chatID string, s MetaSummary) store.JobSpec {
	ig := "sem Instagram vinculado"
	if s.Instagram != "" {
		ig = "@" + s.Instagram
	}
	parts := []string{fmt.Sprintf(msgMetaConnected, s.AccountName, s.Currency, s.PageName, ig)}
	if s.AccountStatus != 1 {
		parts = append(parts, fmt.Sprintf(msgMetaWarnStatus, accountStatusText(s.AccountStatus)))
	}
	if s.Currency != "BRL" || (s.Timezone != "" && s.Timezone != "America/Sao_Paulo") {
		parts = append(parts, fmt.Sprintf(msgMetaWarnLocale, s.Currency, s.Timezone))
	}
	if !s.PageWhatsApp {
		parts = append(parts, msgMetaWarnWA)
	}
	return humanJob(0, store.Conversation{ID: convID, ChatID: chatID}, join(parts...), false)
}

// MetaRevokedJob avisa que o cliente tirou o acesso do app no Facebook.
func MetaRevokedJob(convID int64, chatID string) store.JobSpec {
	return humanJob(0, store.Conversation{ID: convID, ChatID: chatID}, msgMetaRevoked, false)
}

// accountStatusText traduz o account_status da Marketing API.
func accountStatusText(status int) string {
	switch status {
	case 2:
		return "desativada"
	case 3:
		return "com pagamento pendente"
	case 7:
		return "em análise de risco"
	case 8:
		return "aguardando acerto de pagamento"
	case 9:
		return "em período de carência"
	case 100, 101:
		return "fechada"
	default:
		return fmt.Sprintf("com restrição (status %d)", status)
	}
}
```

Confira que `join` (usado em `creative.go`) aceita strings vazias e as ignora; se não ignorar, filtre `lead == ""` antes de chamar (`if lead == "" { … }`).

- [ ] **Step 5: Wire into engine.go, signup.go and preview.go**

`engine.go`, no `struct Engine`, depois de `ReportPhone string`:

```go

	// Conexão da conta Meta: base do link (…/m). Vazio = desligada.
	MetaConnectBaseURL string
```

`dispatch`, logo depois de `case store.StateNeedsHuman: return nil` — mova para antes do `switch` um bloco que só roda fora de atendimento humano:

```go
	if e.MetaConnectBaseURL != "" && conv.State != store.StateNeedsHuman && metaConnectAsk.MatchString(text) {
		return e.sendMetaLink(ctx, ident, conv, "")
	}
```

`converse`: antes da chamada `e.Attendant.Reply`:

```go
	metaState, err := e.metaStatus(ctx, ident.OrgID)
	if err != nil {
		return err
	}
```

e no `attendant.Input{…}` acrescente `Meta: metaState,`. No `switch turn.Intent`, depois de `case contract.IntentProjectStatus: …`:

```go
	case contract.IntentConnectMeta:
		if e.MetaConnectBaseURL == "" {
			return e.speak(ctx, ident, conv, turn.Reply)
		}
		return e.sendMetaLink(ctx, ident, conv, turn.Reply)
```

`signup.go`: troque a regex para `` `/(cadastro|c|m)/[A-Za-z0-9_-]{10,}` ``.

`preview.go`, no fim de `sendPreview`, troque o `return e.Store.AddMessage(…)` por:

```go
	if err := e.Store.AddMessage(ctx, conv.ID, "out", "image", "[prévia do anúncio no Instagram enviada]"); err != nil {
		return err
	}
	if e.MetaConnectBaseURL == "" {
		return nil
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	if _, ok, err := e.Store.ActiveMeta(ctx, ident.OrgID); err != nil || ok {
		return err
	}
	return e.sendMetaLink(ctx, ident, conv, "")
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/engine/`
Expected: PASS (todos, inclusive os antigos).

- [ ] **Step 7: Commit**

```bash
git add internal/engine
git commit -m "feat(meta): link de conexão no WhatsApp (comando, IA e depois da prévia) e mensagens"
```

---

### Task 7: api — página do link, retorno do Facebook, escolha, desautorização e exclusão

**Files:**
- Create: `internal/httpapi/metaconnect.go`
- Create: `internal/httpapi/web/meta.html`
- Test: `internal/httpapi/metaconnect_test.go`

**Interfaces:**
- Consumes: `meta.Assets`, `meta.ParseSignedRequest` (Task 2); `secretbox.Box` (Task 1); store da Task 3; `engine.MetaConnectedJob`, `engine.MetaRevokedJob`, `engine.MetaSummary` (Task 6); `clientIP`, `writeJSON`, `Limiter` (existentes).
- Produces: `httpapi.MetaAPI` (interface) e `httpapi.MetaConnect{Store, Meta, Box, AppSecret, BaseURL, BotNumber, Log, Limiter}` com `Register(*http.ServeMux)`.

- [ ] **Step 1: Write the page template**

`internal/httpapi/web/meta.html` (usa o CSS do cadastro):

```html
<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Conectar conta de anúncios · meuanuncio.io</title>
<link rel="icon" href="/static/favicon.png">
<link rel="stylesheet" href="/static/cadastro.css">
</head>
<body>
<main class="card">
  <img src="/static/logo.png" alt="meuanuncio.io" class="logo">
  {{if eq .State "start"}}
    <h1>Conectar sua conta de anúncios</h1>
    <p>Você entra com o seu Facebook e escolhe a conta de anúncios, a Página e o Instagram que o meuanuncio vai usar para publicar seus anúncios.</p>
    <p><a class="button" href="{{.LoginURL}}">Conectar com Facebook</a></p>
    <p class="muted">A gente só cria e acompanha anúncios que você aprovar no WhatsApp. Dá para remover o acesso a qualquer momento nas configurações do Facebook.</p>
  {{else if eq .State "choose"}}
    <h1>Escolha a conta e a Página</h1>
    <form method="post" action="/meta/escolha">
      <input type="hidden" name="token" value="{{.Token}}">
      <input type="hidden" name="nonce" value="{{.Nonce}}">
      <fieldset><legend>Conta de anúncios</legend>
        {{range $i, $a := .AdAccounts}}<label><input type="radio" name="ad_account" value="{{$a.ID}}" {{if eq $i 0}}checked{{end}}> {{$a.Name}} ({{$a.Currency}})</label>{{end}}
      </fieldset>
      <fieldset><legend>Página</legend>
        {{range $i, $p := .Pages}}<label><input type="radio" name="page" value="{{$p.ID}}" {{if eq $i 0}}checked{{end}}> {{$p.Name}}{{if $p.InstagramUsername}} · @{{$p.InstagramUsername}}{{end}}</label>{{end}}
      </fieldset>
      <button class="button" type="submit">Conectar</button>
    </form>
  {{else if eq .State "done"}}
    <h1>Pronto, conta conectada ✅</h1>
    <p>Pode voltar pro WhatsApp: já te mandei o resumo por lá.</p>
    <p><a class="button" href="{{.WhatsApp}}">Voltar pro WhatsApp</a></p>
  {{else}}
    <h1>{{.Title}}</h1>
    <p>{{.Message}}</p>
    {{if .Retry}}<p><a class="button" href="{{.Retry}}">Tentar de novo</a></p>{{end}}
    <p><a class="button secondary" href="{{.WhatsApp}}">Voltar pro WhatsApp</a></p>
  {{end}}
</main>
</body>
</html>
```

Confira em `internal/httpapi/web/cadastro.css` os nomes de classe existentes (`card`, `button`, `muted`, `secondary`, `logo`); troque pelos que o CSS já tem.

- [ ] **Step 2: Write the failing tests**

`internal/httpapi/metaconnect_test.go`:

```go
package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

const appSecret = "segredo-app"

type fakeMeta struct {
	assets   meta.Assets
	exchange error
	codes    []string
}

func (f *fakeMeta) LoginURL(state string) string {
	return "https://www.facebook.com/v23.0/dialog/oauth?state=" + url.QueryEscape(state)
}
func (f *fakeMeta) ExchangeCode(_ context.Context, code string) (string, error) {
	f.codes = append(f.codes, code)
	if f.exchange != nil {
		return "", f.exchange
	}
	return "TOKEN-" + code, nil
}
func (f *fakeMeta) Assets(context.Context, string) (meta.Assets, error) { return f.assets, nil }

type metaEnv struct {
	mux   http.Handler
	pool  *pgxpool.Pool
	st    *store.Store
	fake  *fakeMeta
	token string
	ident store.Identity
}

var (
	accA  = meta.AdAccount{ID: "act_1", Name: "Loja", Currency: "BRL", Timezone: "America/Sao_Paulo", Status: 1}
	accB  = meta.AdAccount{ID: "act_2", Name: "Loja 2", Currency: "USD", Timezone: "America/Los_Angeles", Status: 1}
	pageA = meta.Page{ID: "P1", Name: "Loja Page", InstagramUsername: "loja", WhatsApp: true}
)

func newMetaEnv(t *testing.T, assets meta.Assets) metaEnv {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()
	org, err := st.CreateOrganization(ctx, "Loja", "moda", 50, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	mem, _ := st.CreateMember(ctx, org, "Ana", "owner")
	st.LinkPhone(ctx, "waha", "5511988887777", mem)
	ident, _ := st.FindIdentity(ctx, "waha", "5511988887777")
	st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	raw, hash, _ := signup.NewToken()
	if err := st.CreateMetaLink(ctx, ident.ID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	k := make([]byte, 32)
	rand.Read(k)
	box, _ := secretbox.New(base64.StdEncoding.EncodeToString(k))
	fake := &fakeMeta{assets: assets}
	h := &httpapi.MetaConnect{Store: st, Meta: fake, Box: box, AppSecret: appSecret, BaseURL: "https://cadastro.teste",
		BotNumber: "5511900000000", Log: discard, Limiter: httpapi.NewLimiter()}
	return metaEnv{mux: httpapi.NewMux(pingOK{}, http.NotFoundHandler(), h.Register), pool: pool, st: st, fake: fake, token: raw, ident: ident}
}

// start abre o link e devolve o nonce do cookie.
func (e metaEnv) start(t *testing.T) string {
	t.Helper()
	rec := e.req("GET", "/m/"+e.token, "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Conectar com Facebook") {
		t.Fatalf("página: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "meta_nonce" && c.HttpOnly && c.Secure && c.Path == "/meta" {
			if !strings.Contains(rec.Body.String(), url.QueryEscape(e.token+"."+c.Value)) {
				t.Fatal("state do login sem token.nonce")
			}
			return c.Value
		}
	}
	t.Fatal("sem cookie do nonce")
	return ""
}

func (e metaEnv) req(method, target, body, nonce string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if nonce != "" {
		req.AddCookie(&http.Cookie{Name: "meta_nonce", Value: nonce})
	}
	req.Header.Set("X-Forwarded-For", "200.1.2.3")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e metaEnv) callback(nonce, code string) *httptest.ResponseRecorder {
	return e.req("GET", "/meta/callback?code="+code+"&state="+url.QueryEscape(e.token+"."+nonce), "", nonce)
}

func (e metaEnv) count(t *testing.T, sql string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMetaSingleAccountConnectsDirectly(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA}, Pages: []meta.Page{pageA}})
	nonce := e.start(t)
	rec := e.callback(nonce, "c1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "conta conectada") || !strings.Contains(rec.Body.String(), "wa.me/5511900000000") {
		t.Fatalf("retorno: %d %s", rec.Code, rec.Body.String())
	}
	c, ok, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID)
	if !ok || c.AdAccountID != "act_1" {
		t.Fatalf("conexão = %+v", c)
	}
	if e.count(t, `SELECT count(*) FROM meta_connections WHERE token_ciphertext IS NOT NULL AND position('TOKEN-c1' in encode(token_ciphertext, 'escape')) = 0`) != 1 {
		t.Fatal("token não está cifrado")
	}
	if e.count(t, `SELECT count(*) FROM outbox_jobs WHERE payload->>'text' LIKE 'Conectado%'`) != 1 {
		t.Fatal("confirmação não foi para a fila")
	}
	if e.count(t, `SELECT count(*) FROM audit_events WHERE action = 'meta_connected' AND data::text NOT LIKE '%TOKEN%'`) != 1 {
		t.Fatal("sem auditoria ou auditoria com token")
	}
	// Link usado: nem a página nem um novo retorno funcionam.
	if rec := e.req("GET", "/m/"+e.token, "", ""); rec.Code != 410 {
		t.Fatalf("link usado: %d", rec.Code)
	}
	if rec := e.callback(nonce, "c2"); rec.Code != 410 || len(e.fake.codes) != 1 {
		t.Fatalf("retorno repetido: %d, trocas %d", rec.Code, len(e.fake.codes))
	}
}

func TestMetaSeveralAccountsAskForChoice(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA, accB}, Pages: []meta.Page{pageA}})
	nonce := e.start(t)
	rec := e.callback(nonce, "c1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Loja 2") || !strings.Contains(rec.Body.String(), `name="nonce" value="`+nonce+`"`) {
		t.Fatalf("escolha: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID); ok {
		t.Fatal("conectou antes da escolha")
	}
	bad := url.Values{"token": {e.token}, "nonce": {nonce}, "ad_account": {"act_999"}, "page": {"P1"}}.Encode()
	if rec := e.req("POST", "/meta/escolha", bad, nonce); rec.Code != 400 {
		t.Fatalf("conta fora da lista: %d", rec.Code)
	}
	noCookie := url.Values{"token": {e.token}, "nonce": {nonce}, "ad_account": {"act_2"}, "page": {"P1"}}.Encode()
	if rec := e.req("POST", "/meta/escolha", noCookie, ""); rec.Code != 400 {
		t.Fatalf("sem cookie: %d", rec.Code)
	}
	if rec := e.req("POST", "/meta/escolha", noCookie, nonce); rec.Code != 200 || !strings.Contains(rec.Body.String(), "conta conectada") {
		t.Fatalf("escolha válida: %d %s", rec.Code, rec.Body.String())
	}
	c, _, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID)
	if c.AdAccountID != "act_2" || c.Currency != "USD" {
		t.Fatalf("conectada = %+v", c)
	}
}

func TestMetaCallbackRejectsMissingCookieWithoutExchange(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA}, Pages: []meta.Page{pageA}})
	nonce := e.start(t)
	rec := e.req("GET", "/meta/callback?code=c1&state="+url.QueryEscape(e.token+"."+nonce), "", "")
	if rec.Code != 400 || len(e.fake.codes) != 0 || !strings.Contains(rec.Body.String(), "abre o link de novo") {
		t.Fatalf("sem cookie: %d trocas %d %s", rec.Code, len(e.fake.codes), rec.Body.String())
	}
}

func TestMetaCallbackCancelledAndErrors(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1"})
	nonce := e.start(t)
	rec := e.req("GET", "/meta/callback?error=access_denied&state="+url.QueryEscape(e.token+"."+nonce), "", nonce)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cancelada") || !strings.Contains(rec.Body.String(), "/m/"+e.token) {
		t.Fatalf("cancelado: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.callback(nonce, "c1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pelo menos uma conta de anúncios e uma Página") || !strings.Contains(rec.Body.String(), "/m/"+e.token) {
		t.Fatalf("sem ativos: %d %s", rec.Code, rec.Body.String())
	}
	e.fake.exchange = errors.New("meta: code expirado")
	rec = e.callback(nonce, "c2")
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "/m/"+e.token) {
		t.Fatalf("meta fora: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.st.MetaLink(context.Background(), signup.HashToken(e.token)); err != nil {
		t.Fatal("link deixou de valer depois de erro")
	}
}

func TestMetaInvalidLinkPage(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{})
	if rec := e.req("GET", "/m/naoexiste1234", "", ""); rec.Code != 410 || !strings.Contains(rec.Body.String(), "Voltar pro WhatsApp") {
		t.Fatalf("link inválido: %d", rec.Code)
	}
}

func signedReq(userID, key string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(`{"algorithm":"HMAC-SHA256","user_id":"` + userID + `"}`))
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)) + "." + p
}

func TestMetaDeauthorizeAndDeletion(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA}, Pages: []meta.Page{pageA}})
	e.callback(e.start(t), "c1")
	if rec := e.req("POST", "/meta/deauthorize", "signed_request="+signedReq("U1", "chave-errada"), ""); rec.Code != 400 {
		t.Fatalf("assinatura inválida: %d", rec.Code)
	}
	if rec := e.req("POST", "/meta/deauthorize", "signed_request="+signedReq("U1", appSecret), ""); rec.Code != 200 {
		t.Fatalf("deauthorize: %d", rec.Code)
	}
	if _, ok, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID); ok {
		t.Fatal("continua conectada")
	}
	if e.count(t, `SELECT count(*) FROM outbox_jobs WHERE payload->>'text' LIKE '%desconectada%'`) != 1 {
		t.Fatal("sem aviso de desconexão")
	}
	rec := e.req("POST", "/meta/exclusao-dados", "signed_request="+signedReq("U1", appSecret), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"confirmation_code"`) || !strings.Contains(rec.Body.String(), "https://cadastro.teste/meta/exclusao/") {
		t.Fatalf("exclusão: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/httpapi/ -run 'Meta'`
Expected: FAIL (`MetaConnect` não existe).

- [ ] **Step 4: Implement `internal/httpapi/metaconnect.go`**

```go
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// MetaAPI é a parte da Graph API que a conexão usa.
type MetaAPI interface {
	LoginURL(state string) string
	ExchangeCode(ctx context.Context, code string) (string, error)
	Assets(ctx context.Context, token string) (meta.Assets, error)
}

// MetaConnect serve o link de conexão da conta de anúncios, o retorno do Facebook e os avisos da Meta.
type MetaConnect struct {
	Store     *store.Store
	Meta      MetaAPI
	Box       *secretbox.Box
	AppSecret string
	BaseURL   string // PUBLIC_BASE_URL, sem barra no fim
	BotNumber string
	Log       *slog.Logger
	Limiter   *Limiter
}

const metaCookie = "meta_nonce"

var metaPage = template.Must(template.ParseFS(web, "web/meta.html"))

type metaView struct {
	State, Title, Message, Retry, LoginURL, Token, Nonce, WhatsApp string
	AdAccounts                                                     []meta.AdAccount
	Pages                                                          []meta.Page
}

func (m *MetaConnect) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /m/{token}", m.limit(30, m.start))
	mux.HandleFunc("GET /meta/callback", m.limit(30, m.callback))
	mux.HandleFunc("POST /meta/escolha", m.limit(20, m.choose))
	mux.HandleFunc("POST /meta/deauthorize", m.limit(60, m.deauthorize))
	mux.HandleFunc("POST /meta/exclusao-dados", m.limit(60, m.deletion))
	mux.HandleFunc("GET /meta/exclusao/{code}", m.limit(60, m.deletionStatus))
}

func (m *MetaConnect) limit(perMinute int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.Limiter != nil && !m.Limiter.Allow("meta:"+clientIP(r), perMinute) {
			http.Error(w, "muitas tentativas", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func (m *MetaConnect) start(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("token")
	if _, err := m.Store.MetaLink(r.Context(), signup.HashToken(raw)); err != nil {
		m.fail(w, r, err, http.StatusGone, "Link expirado ou já usado", "Esse link de conexão não vale mais. Me pede outro no WhatsApp mandando \"conectar meta\".", "")
		return
	}
	nonce, err := randomHex(16)
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", "Tenta abrir o link de novo em instantes.", "")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: metaCookie, Value: nonce, Path: "/meta", MaxAge: 900, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	m.render(w, http.StatusOK, metaView{State: "start", LoginURL: m.Meta.LoginURL(raw + "." + nonce)})
}

func (m *MetaConnect) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, nonce, _ := strings.Cut(q.Get("state"), ".")
	retry := m.BaseURL + "/m/" + raw
	if q.Get("error") != "" {
		m.render(w, http.StatusOK, metaView{State: "error", Title: "Conexão cancelada",
			Message: "Tudo bem, nada foi conectado. Quando quiser, é só tentar de novo.", Retry: retry, WhatsApp: m.whatsapp()})
		return
	}
	if !m.nonceOK(r, nonce) {
		m.fail(w, r, errors.New("nonce ausente ou diferente"), http.StatusBadRequest, "Não deu pra confirmar",
			"Parece que o login voltou em outro navegador. Volta no WhatsApp e abre o link de novo neste mesmo navegador.", "")
		return
	}
	link, err := m.Store.MetaLink(r.Context(), signup.HashToken(raw))
	if err != nil {
		m.fail(w, r, err, http.StatusGone, "Link expirado ou já usado", "Esse link de conexão não vale mais. Me pede outro no WhatsApp mandando \"conectar meta\".", "")
		return
	}
	token, err := m.Meta.ExchangeCode(r.Context(), q.Get("code"))
	if err != nil {
		m.fail(w, r, err, http.StatusBadGateway, "A Meta não respondeu", "Não consegui confirmar com o Facebook agora. Tenta de novo em instantes.", retry)
		return
	}
	assets, err := m.Meta.Assets(r.Context(), token)
	if err != nil {
		m.fail(w, r, err, http.StatusBadGateway, "A Meta não respondeu", "Não consegui ler suas contas no Facebook agora. Tenta de novo em instantes.", retry)
		return
	}
	sealed, err := m.Box.Seal([]byte(token))
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", "Tenta de novo em instantes.", retry)
		return
	}
	cands := store.MetaCandidates{AdAccounts: assets.AdAccounts, Pages: assets.Pages}
	connID, err := m.Store.SavePendingMeta(r.Context(), link, assets.UserID, sealed, cands)
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", "Tenta de novo em instantes.", retry)
		return
	}
	if len(assets.AdAccounts) == 0 || len(assets.Pages) == 0 {
		m.render(w, http.StatusOK, metaView{State: "error", Title: "Faltou escolher no Facebook",
			Message: "No login, marque pelo menos uma conta de anúncios e uma Página para o meuanuncio. Clique em tentar de novo e confira as opções.",
			Retry: retry, WhatsApp: m.whatsapp()})
		return
	}
	if len(assets.AdAccounts) == 1 && len(assets.Pages) == 1 {
		m.complete(w, r, link, connID, assets.AdAccounts[0], assets.Pages[0])
		return
	}
	m.render(w, http.StatusOK, metaView{State: "choose", Token: raw, Nonce: nonce, AdAccounts: assets.AdAccounts, Pages: assets.Pages})
}

func (m *MetaConnect) choose(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulário inválido", http.StatusBadRequest)
		return
	}
	raw := r.PostFormValue("token")
	if !m.nonceOK(r, r.PostFormValue("nonce")) {
		m.fail(w, r, errors.New("nonce ausente ou diferente"), http.StatusBadRequest, "Não deu pra confirmar",
			"Volta no WhatsApp e abre o link de novo neste mesmo navegador.", "")
		return
	}
	link, err := m.Store.MetaLink(r.Context(), signup.HashToken(raw))
	if err != nil {
		m.fail(w, r, err, http.StatusGone, "Link expirado ou já usado", "Me pede outro no WhatsApp mandando \"conectar meta\".", "")
		return
	}
	connID, cands, err := m.Store.PendingMeta(r.Context(), link.ID)
	if err != nil {
		m.fail(w, r, err, http.StatusGone, "Link expirado ou já usado", "Me pede outro no WhatsApp mandando \"conectar meta\".", "")
		return
	}
	var acc *meta.AdAccount
	for i := range cands.AdAccounts {
		if cands.AdAccounts[i].ID == r.PostFormValue("ad_account") {
			acc = &cands.AdAccounts[i]
		}
	}
	var page *meta.Page
	for i := range cands.Pages {
		if cands.Pages[i].ID == r.PostFormValue("page") {
			page = &cands.Pages[i]
		}
	}
	if acc == nil || page == nil {
		m.fail(w, r, errors.New("escolha fora da lista"), http.StatusBadRequest, "Escolha inválida", "Volta e escolhe uma conta e uma Página da lista.", m.BaseURL+"/m/"+raw)
		return
	}
	m.complete(w, r, link, connID, *acc, *page)
}

func (m *MetaConnect) complete(w http.ResponseWriter, r *http.Request, link store.MetaLink, connID int64, acc meta.AdAccount, page meta.Page) {
	summary := engine.MetaSummary{AccountName: acc.Name, Currency: acc.Currency, Timezone: acc.Timezone, AccountStatus: acc.Status,
		DisableReason: acc.DisableReason, PageName: page.Name, Instagram: page.InstagramUsername, PageWhatsApp: page.WhatsApp}
	orgID, err := m.Store.CompleteMeta(r.Context(), store.MetaChoice{ConnectionID: connID, LinkID: link.ID, Account: acc, Page: page,
		Message: func(convID int64, chatID string) store.JobSpec { return engine.MetaConnectedJob(convID, chatID, summary) }})
	if errors.Is(err, store.ErrMetaLinkInvalid) {
		m.fail(w, r, err, http.StatusGone, "Link expirado ou já usado", "Me pede outro no WhatsApp mandando \"conectar meta\".", "")
		return
	}
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", "Tenta de novo em instantes.", m.BaseURL+"/m/")
		return
	}
	if err := m.Store.Audit(r.Context(), orgID, "identity:"+itoa(link.IdentityID), "meta_connected",
		map[string]any{"ad_account_id": acc.ID, "page_id": page.ID}); err != nil {
		m.Log.Error("falha ao auditar conexão meta", "erro", err)
	}
	http.SetCookie(w, &http.Cookie{Name: metaCookie, Value: "", Path: "/meta", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	m.render(w, http.StatusOK, metaView{State: "done", WhatsApp: m.whatsapp()})
}

func (m *MetaConnect) deauthorize(w http.ResponseWriter, r *http.Request) {
	userID, ok := m.signedUser(w, r)
	if !ok {
		return
	}
	if _, err := m.Store.RevokeMeta(r.Context(), userID, "", engine.MetaRevokedJob); err != nil {
		m.Log.Error("falha ao revogar conexão meta", "erro", err)
		http.Error(w, "erro", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (m *MetaConnect) deletion(w http.ResponseWriter, r *http.Request) {
	userID, ok := m.signedUser(w, r)
	if !ok {
		return
	}
	code, _, err := signup.NewToken()
	if err == nil {
		_, err = m.Store.RevokeMeta(r.Context(), userID, code, engine.MetaRevokedJob)
	}
	if err != nil {
		m.Log.Error("falha na exclusão de dados meta", "erro", err)
		http.Error(w, "erro", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": m.BaseURL + "/meta/exclusao/" + code, "confirmation_code": code})
}

func (m *MetaConnect) deletionStatus(w http.ResponseWriter, r *http.Request) {
	done, err := m.Store.MetaDeletionDone(r.Context(), r.PathValue("code"))
	if err != nil || !done {
		m.render(w, http.StatusNotFound, metaView{State: "error", Title: "Pedido não encontrado",
			Message: "Não achamos um pedido de exclusão com esse código.", WhatsApp: m.whatsapp()})
		return
	}
	m.render(w, http.StatusOK, metaView{State: "error", Title: "Dados excluídos",
		Message: "O acesso do meuanuncio à sua conta foi removido e o token de acesso foi apagado.", WhatsApp: m.whatsapp()})
}

func (m *MetaConnect) signedUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulário inválido", http.StatusBadRequest)
		return "", false
	}
	userID, err := meta.ParseSignedRequest(r.PostFormValue("signed_request"), m.AppSecret)
	if err != nil {
		http.Error(w, "assinatura inválida", http.StatusBadRequest)
		return "", false
	}
	return userID, true
}

func (m *MetaConnect) nonceOK(r *http.Request, nonce string) bool {
	c, err := r.Cookie(metaCookie)
	return err == nil && nonce != "" && c.Value == nonce
}

func (m *MetaConnect) fail(w http.ResponseWriter, r *http.Request, err error, status int, title, msg, retry string) {
	m.Log.Warn("conexão meta", "caminho", r.URL.Path, "status", status, "erro", err)
	m.render(w, status, metaView{State: "error", Title: title, Message: msg, Retry: retry, WhatsApp: m.whatsapp()})
}

func (m *MetaConnect) render(w http.ResponseWriter, status int, v metaView) {
	if v.WhatsApp == "" {
		v.WhatsApp = m.whatsapp()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'none'; style-src 'self'; img-src 'self' data:; "+
		"frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.WriteHeader(status)
	if err := metaPage.Execute(w, v); err != nil {
		m.Log.Error("falha ao montar página meta", "erro", err)
	}
}

func (m *MetaConnect) whatsapp() string { return "https://wa.me/" + signup.OnlyDigits(m.BotNumber) }

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
```

Acrescente `"strconv"` aos imports. O `Retry` do erro 500 em `complete` deve ser `m.BaseURL + "/m/"` + token; como `complete` não recebe o token, passe `""` (sem botão de tentar de novo) — ajuste a chamada para `""`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/httpapi/`
Expected: PASS (todos, inclusive os do cadastro).

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi
git commit -m "feat(meta): página do link, retorno do Facebook, escolha, desautorização e exclusão de dados"
```

---

### Task 8: ligar na api e no worker + documentação

**Files:**
- Modify: `cmd/api/main.go`
- Modify: `cmd/worker/main.go`
- Modify: `README.md` (seção de variáveis de ambiente; confira o nome da seção com `grep -n "PUBLIC_BASE_URL" README.md`)

**Interfaces:**
- Consumes: `config.MetaConnectEnabled` (Task 4); `meta.New` (Task 2); `secretbox.New` (Task 1); `httpapi.MetaConnect` (Task 7); `Engine.MetaConnectBaseURL` (Task 6).

- [ ] **Step 1: api**

Em `cmd/api/main.go`, depois do bloco `if cfg.SignupEnabled() { … }`:

```go
	if cfg.MetaConnectEnabled() {
		box, err := secretbox.New(cfg.MetaTokenKey)
		if err != nil {
			return err
		}
		mc := &httpapi.MetaConnect{
			Store: st, Meta: meta.New(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaLoginConfigID, cfg.PublicBaseURL+"/meta/callback"),
			Box: box, AppSecret: cfg.MetaAppSecret, BaseURL: cfg.PublicBaseURL, BotNumber: cfg.BotWhatsAppNumber,
			Log: log, Limiter: httpapi.NewLimiter(),
		}
		routes = append(routes, mc.Register)
	}
	log.Info("api configurada", "cadastro", cfg.SignupEnabled(), "conexao_meta", cfg.MetaConnectEnabled())
```

(substitui a linha `log.Info("api configurada", "cadastro", cfg.SignupEnabled())`). Imports: `internal/meta`, `internal/secretbox`.

- [ ] **Step 2: worker**

Em `cmd/worker/main.go`, depois do bloco `if cfg.SignupEnabled() { … }`:

```go
	if cfg.MetaConnectEnabled() {
		eng.MetaConnectBaseURL = cfg.PublicBaseURL + "/m"
	}
```

E acrescente `"conexao_meta", eng.MetaConnectBaseURL != ""` ao `log.Info("worker iniciado", …)`.

- [ ] **Step 3: README**

Na seção de variáveis, acrescente:

```markdown
### Conexão da conta Meta (api e worker)

| Variável | O que é |
|---|---|
| `META_APP_ID` | ID do app `meuanuncio.io` no developers.facebook.com |
| `META_APP_SECRET` | Chave secreta do app (confere `signed_request` e assina as chamadas) |
| `META_LOGIN_CONFIG_ID` | ID da configuração do Facebook Login for Business |
| `META_TOKEN_KEY` | 32 bytes em base64 que cifram os tokens dos clientes (`openssl rand -base64 32`). Trocar a chave invalida as conexões existentes |

Sem as quatro (e `PUBLIC_BASE_URL`), a conexão fica desligada. No app Meta: redirect `PUBLIC_BASE_URL/meta/callback`, URL de desautorização `PUBLIC_BASE_URL/meta/deauthorize` e URL de exclusão de dados `PUBLIC_BASE_URL/meta/exclusao-dados`.
```

- [ ] **Step 4: Build and full test run**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: tudo PASS, nenhum SKIP nos pacotes com banco.

- [ ] **Step 5: Commit**

```bash
git add cmd README.md
git commit -m "feat(meta): liga a conexão da conta Meta na api e no worker"
```

---

## Validação ao vivo (depois do merge no deploy, com o usuário)

1. Gerar `META_TOKEN_KEY` (`openssl rand -base64 32`) e setar as quatro variáveis na api e no worker do Easypanel; redeploy.
2. No app Meta: URL de desautorização e de exclusão de dados (Facebook Login for Business → Configurações).
3. Número de teste com cadastro completo manda "conectar meta"; abrir o link no celular; concluir com a conta do usuário.
4. Conferir no WhatsApp a confirmação com os avisos (USD/Los Angeles e Página sem WhatsApp esperados para `act_3538929729609697` / Clara Luz).
5. Conferir o tipo do token salvo: script temporário que abre o token com a chave e chama `debug_token` (esperado `SYSTEM_USER`); apagar o script depois.
