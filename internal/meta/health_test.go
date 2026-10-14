package meta_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

func TestAccountHealthReadsStatusReasonAndBusiness(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9", `{"id":"act_9","name":"Loja","currency":"BRL","account_status":2,"disable_reason":1,"business":{"id":"B1","name":"Loja LTDA"}}`)
	h, err := c.AccountHealth(context.Background(), tok, "9")
	if err != nil {
		t.Fatal(err)
	}
	if h.Status != 2 || h.DisableReason != 1 || h.Currency != "BRL" || h.BusinessID != "B1" || h.BusinessName != "Loja LTDA" {
		t.Fatalf("%+v", h)
	}
	fields := f.one("GET /act_9").Form.Get("fields")
	for _, want := range []string{"account_status", "disable_reason", "currency", "business"} {
		if !contains(fields, want) {
			t.Errorf("fields %q sem %s", fields, want)
		}
	}
}

func TestAccountHealthWithoutBusiness(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /act_9", `{"id":"act_9","currency":"USD","account_status":1}`)
	h, err := c.AccountHealth(context.Background(), tok, "act_9")
	if err != nil || h.BusinessID != "" || h.Currency != "USD" {
		t.Fatalf("%+v %v", h, err)
	}
}

func TestBusinessVerification(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /B1", `{"verification_status":"Verified","id":"B1"}`)
	v, err := c.BusinessVerification(context.Background(), tok, "B1")
	if err != nil || v != "verified" {
		t.Fatalf("v=%q err=%v", v, err)
	}
	f.onCode("GET /B2", 403, `{"error":{"message":"Permission","code":200}}`)
	if _, err := c.BusinessVerification(context.Background(), tok, "B2"); err == nil {
		t.Fatal("sem permissão deveria dar erro")
	}
}

func TestPageAccess(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /me/accounts", `{"data":[
		{"id":"P0","tasks":["ANALYZE"]},
		{"id":"P1","tasks":["ADVERTISE","CREATE_CONTENT"],"is_published":true,"instagram_business_account":{"id":"IG1"}},
		{"id":"P2","tasks":["ANALYZE"],"is_published":false}]}`)
	got, err := c.PageAccess(context.Background(), tok, "P1")
	if err != nil || !got.Found || !got.Published || !got.CanAdvertise() || got.InstagramID != "IG1" {
		t.Fatalf("%+v %v", got, err)
	}
	p2, _ := c.PageAccess(context.Background(), tok, "P2")
	if !p2.Found || p2.Published || p2.CanAdvertise() {
		t.Fatalf("P2 = %+v", p2)
	}
	gone, err := c.PageAccess(context.Background(), tok, "P9")
	if err != nil || gone.Found {
		t.Fatalf("P9 = %+v %v", gone, err)
	}
}

func TestPageAccessWithoutTasksListCountsAsAllowed(t *testing.T) {
	f, c := newFake(t)
	f.on("GET /me/accounts", `{"data":[{"id":"P1"}]}`)
	got, _ := c.PageAccess(context.Background(), tok, "P1")
	if !got.Found || !got.Published || !got.CanAdvertise() {
		t.Fatalf("%+v", got)
	}
}

func TestTokenInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("input_token") == "RUIM" {
			io.WriteString(w, `{"data":{"is_valid":false,"error":{"code":190}}}`)
			return
		}
		io.WriteString(w, `{"data":{"is_valid":true,"scopes":["ads_management","pages_show_list"],"expires_at":1797094838,"data_access_expires_at":1799711882}}`)
	}))
	defer srv.Close()
	c := meta.New("123", "segredo", "", "")
	c.GraphURL = srv.URL
	ti, err := c.TokenInfo(context.Background(), "BOM")
	if err != nil || !ti.Valid || !ti.HasScope("ads_management") || ti.HasScope("ads_read") || !ti.ExpiresAt.Equal(time.Unix(1797094838, 0).UTC()) {
		t.Fatalf("%+v %v", ti, err)
	}
	bad, err := c.TokenInfo(context.Background(), "RUIM")
	if err != nil || bad.Valid {
		t.Fatalf("%+v %v", bad, err)
	}
}

func TestIsAuthError(t *testing.T) {
	f, c := newFake(t)
	f.onCode("GET /act_9", 400, `{"error":{"message":"Session has expired","code":190}}`)
	_, err := c.AccountHealth(context.Background(), tok, "9")
	if !meta.IsAuthError(err) {
		t.Fatalf("err = %v", err)
	}
	f.onCode("GET /act_8", 500, `{"error":{"message":"boom","code":2}}`)
	_, err = c.AccountHealth(context.Background(), tok, "8")
	if err == nil || meta.IsAuthError(err) {
		t.Fatalf("err = %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
