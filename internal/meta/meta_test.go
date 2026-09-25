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
