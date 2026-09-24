package signup_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
)

var ctx = context.Background()

func TestTokenHash(t *testing.T) {
	raw, hash, err := signup.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 12 || len(hash) != 64 || signup.HashToken(raw) != hash {
		t.Fatalf("raw=%q (%d) hash=%q", raw, len(raw), hash)
	}
	for _, r := range raw {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			t.Fatalf("caractere fora do alfabeto curto: %q", raw)
		}
	}
	seen := map[string]bool{}
	for range 2000 {
		r, _, _ := signup.NewToken()
		if seen[r] {
			t.Fatal("tokens repetidos")
		}
		seen[r] = true
	}
}

func TestLookupCNPJ(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/cnpj/v1/11222333000181" {
			io.WriteString(w, `{"razao_social":"PIZZARIA BOA LTDA","nome_fantasia":"PIZZARIA BOA","municipio":"CAMPINAS","uf":"SP","cnae_fiscal_descricao":"Restaurantes e similares","descricao_situacao_cadastral":"ATIVA"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	api := &signup.BrasilAPI{BaseURL: srv.URL, HTTP: srv.Client()}
	c, err := api.LookupCNPJ(ctx, "11.222.333/0001-81")
	if err != nil {
		t.Fatal(err)
	}
	if c.LegalName != "PIZZARIA BOA LTDA" || c.TradeName != "PIZZARIA BOA" || c.City != "CAMPINAS" || c.UF != "SP" || c.Activity == "" || c.Status != "ATIVA" {
		t.Fatalf("empresa = %+v", c)
	}
	if _, err := api.LookupCNPJ(ctx, "33.000.167/0001-01"); !errors.Is(err, signup.ErrNotFound) {
		t.Fatalf("não encontrado: %v", err)
	}
	if _, err := api.LookupCNPJ(ctx, "123"); !errors.Is(err, signup.ErrInvalid) {
		t.Fatalf("inválido: %v", err)
	}
}

func TestSupabaseGetUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/user" || r.Header.Get("apikey") != "pub" {
			t.Errorf("rota/apikey: %s %q", r.URL.Path, r.Header.Get("apikey"))
		}
		switch r.Header.Get("Authorization") {
		case "Bearer ok":
			io.WriteString(w, `{"id":"7c9e6679-7425-40de-944b-e07fc1f90ae7","email":"Ana@Mail.com","email_confirmed_at":"2026-09-23T10:00:00Z","user_metadata":{"full_name":"Ana Souza"}}`)
		case "Bearer unconfirmed":
			io.WriteString(w, `{"id":"7c9e6679-7425-40de-944b-e07fc1f90ae8","email":"b@mail.com","email_confirmed_at":null}`)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	auth := &signup.SupabaseAuth{URL: srv.URL, Key: "pub", HTTP: srv.Client()}
	u, err := auth.GetUser(ctx, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != "7c9e6679-7425-40de-944b-e07fc1f90ae7" || u.Email != "ana@mail.com" || u.Name != "Ana Souza" || u.VerifiedAt.IsZero() {
		t.Fatalf("usuário = %+v", u)
	}
	if _, err := auth.GetUser(ctx, "unconfirmed"); !errors.Is(err, signup.ErrUnauthorized) {
		t.Fatalf("sem e-mail confirmado: %v", err)
	}
	if _, err := auth.GetUser(ctx, "bad"); !errors.Is(err, signup.ErrUnauthorized) {
		t.Fatalf("token ruim: %v", err)
	}
	if _, err := auth.GetUser(ctx, ""); !errors.Is(err, signup.ErrUnauthorized) {
		t.Fatalf("token vazio: %v", err)
	}
}
