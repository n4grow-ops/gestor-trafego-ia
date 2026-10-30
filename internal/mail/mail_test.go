package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResendSend(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("pedido: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer srv.Close()
	r := NewResend("k", "Avisos <a@x.io>")
	r.BaseURL = srv.URL
	if err := r.Send(context.Background(), []string{"b@x.io"}, "Assunto", "Texto"); err != nil {
		t.Fatal(err)
	}
	if got["subject"] != "Assunto" || got["from"] != "Avisos <a@x.io>" {
		t.Fatalf("corpo: %v", got)
	}
	if err := r.Send(context.Background(), nil, "x", "y"); err != nil {
		t.Fatal(err)
	}
}
