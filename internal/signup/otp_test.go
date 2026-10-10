package signup_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
)

func TestSupabaseOTP(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != "pub" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "x", http.StatusBadRequest)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/auth/v1/otp":
			sent = body
			w.Write([]byte(`{}`))
		case "/auth/v1/verify":
			if body["type"] != "email" || body["email"] != "ana@meuanuncio.io" {
				http.Error(w, "x", http.StatusBadRequest)
				return
			}
			if body["token"] != "123456" {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"code":403,"error_code":"otp_expired","msg":"Token has expired or is invalid"}`))
				return
			}
			w.Write([]byte(`{"access_token":"jwt","user":{"id":"u1","email":"Ana@MeuAnuncio.io","email_confirmed_at":"2026-10-10T12:00:00Z"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := signup.NewSupabaseAuth(srv.URL, "pub")
	ctx := context.Background()

	if err := a.SendOTP(ctx, "ana@meuanuncio.io"); err != nil {
		t.Fatal(err)
	}
	if sent["email"] != "ana@meuanuncio.io" || sent["create_user"] != true {
		t.Fatalf("corpo do envio = %v", sent)
	}
	u, err := a.VerifyOTP(ctx, "ana@meuanuncio.io", "123456")
	if err != nil || u.ID != "u1" || u.Email != "ana@meuanuncio.io" || u.VerifiedAt.IsZero() {
		t.Fatalf("verificar = %+v %v", u, err)
	}
	if _, err := a.VerifyOTP(ctx, "ana@meuanuncio.io", "000000"); !errors.Is(err, signup.ErrUnauthorized) {
		t.Fatalf("código errado = %v", err)
	}
	if _, err := a.VerifyOTP(ctx, "ana@meuanuncio.io", ""); !errors.Is(err, signup.ErrUnauthorized) {
		t.Fatalf("código vazio = %v", err)
	}
}
