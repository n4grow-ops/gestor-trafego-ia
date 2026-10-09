package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/waha"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

const msg = `{"id":"evt","timestamp":1,"session":"default","engine":"GOWS","event":"message","payload":{"id":"m1","from":"5511988887777@c.us","body":"oi"},"environment":{}}`

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newWebhook(t *testing.T, secret string) (*httpapi.Webhook, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	return &httpapi.Webhook{Store: store.New(pool), WA: waha.New("http://nao-usado", "k", "default"), Secret: secret, Log: discard}, pool
}

func post(h http.Handler, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)))
	return rec
}

func inboxCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM inbox_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWebhookRejectsWrongToken(t *testing.T) {
	wh, pool := newWebhook(t, "segredo")
	for _, target := range []string{"/webhooks/whatsapp", "/webhooks/whatsapp?token=errado", "/webhooks/whatsapp?token=segred"} {
		if rec := post(wh, target, msg); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: code = %d", target, rec.Code)
		}
	}
	if inboxCount(t, pool) != 0 {
		t.Error("nada deveria ser gravado")
	}
}

func TestWebhookDisabledWithoutSecret(t *testing.T) {
	wh, pool := newWebhook(t, "")
	if rec := post(wh, "/webhooks/whatsapp?token=", msg); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rec.Code)
	}
	if inboxCount(t, pool) != 0 {
		t.Error("nada deveria ser gravado")
	}
}

func TestWebhookQueuesAndDeduplicates(t *testing.T) {
	wh, pool := newWebhook(t, "segredo")
	rec := post(wh, "/webhooks/whatsapp?token=segredo", msg)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "queued") {
		t.Fatalf("1º: %d %s", rec.Code, rec.Body)
	}
	rec = post(wh, "/webhooks/whatsapp?token=segredo", msg)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("2º: %d %s", rec.Code, rec.Body)
	}
	if inboxCount(t, pool) != 1 {
		t.Errorf("inbox = %d", inboxCount(t, pool))
	}
}

func TestWebhookIgnoresOtherEvents(t *testing.T) {
	wh, pool := newWebhook(t, "segredo")
	rec := post(wh, "/webhooks/whatsapp?token=segredo", `{"event":"message.ack","payload":{}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ignored") || inboxCount(t, pool) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestWebhookRejectsInvalidJSON(t *testing.T) {
	wh, _ := newWebhook(t, "segredo")
	if rec := post(wh, "/webhooks/whatsapp?token=segredo", "{nope"); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", rec.Code)
	}
}

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func TestHealth(t *testing.T) {
	get := func(p pinger) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		httpapi.NewMux(p, http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		return rec
	}
	if rec := get(pinger{}); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("saudável: %d %q", rec.Code, rec.Body)
	}
	if rec := get(pinger{errors.New("fora")}); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("banco fora: %d", rec.Code)
	}
}

func TestMuxRoutesWebhookOnlyOnPost(t *testing.T) {
	wh, _ := newWebhook(t, "segredo")
	mux := httpapi.NewMux(pinger{}, wh)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/whatsapp?token=segredo", strings.NewReader(msg)))
	if rec.Code != 200 {
		t.Errorf("POST: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webhooks/whatsapp?token=segredo", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}

func TestWebhookWameConnectionAndDuplicate(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool)
	wh := &httpapi.Webhook{Store: st, WA: wame.New("http://nao-usado", "k", ""), Secret: "segredo", Log: discard}
	conn := `{"object":"wame","entry":[{"changes":[{"field":"connection","value":{"connection":{"status":"close","code":1004,"reason":"disconnected"}}}]}]}`
	rec := post(wh, "/webhooks/whatsapp?token=segredo", conn)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "connection") {
		t.Fatalf("conexão: %d %s", rec.Code, rec.Body)
	}
	s, err := st.WhatsAppStatus(context.Background(), "wame")
	if err != nil || s.Connected || s.Code != 1004 {
		t.Fatalf("estado: %+v %v", s, err)
	}
	if n := inboxCount(t, pool); n != 0 {
		t.Fatalf("aviso de conexão não vai para o inbox: %d", n)
	}
	m := `{"object":"wame","entry":[{"changes":[{"field":"messages","value":{"messages":[{"from":"5511999998888","chat_type":"individual","id":"W1","type":"text","text":{"body":"oi"}}]}}]}]}`
	if rec := post(wh, "/webhooks/whatsapp?token=segredo", m); !strings.Contains(rec.Body.String(), "queued") {
		t.Fatalf("1ª: %s", rec.Body)
	}
	if rec := post(wh, "/webhooks/whatsapp?token=segredo", m); !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("2ª: %s", rec.Body)
	}
	status := `{"object":"wame","entry":[{"changes":[{"field":"messages","value":{"statuses":[{"id":"W1","status":"read"}]}}]}]}`
	if rec := post(wh, "/webhooks/whatsapp?token=segredo", status); !strings.Contains(rec.Body.String(), "ignored") {
		t.Fatalf("status de entrega: %s", rec.Body)
	}
	if rec := post(wh, "/webhooks/whatsapp?token=errado", conn); rec.Code != http.StatusUnauthorized {
		t.Fatalf("segredo errado: %d", rec.Code)
	}
}

func TestWebhookWameSplitsBatchedMessages(t *testing.T) {
	pool := testdb.New(t)
	wh := &httpapi.Webhook{Store: store.New(pool), WA: wame.New("http://nao-usado", "k", ""), Secret: "segredo", Log: discard}
	two := `{"object":"wame","entry":[{"id":"x","changes":[{"field":"messages","value":{"messages":[` +
		`{"from":"5511999998888","id":"A","type":"text","text":{"body":"um"}},` +
		`{"from":"5511999998888","id":"B","type":"text","text":{"body":"dois"}}]}}]}]}`
	if rec := post(wh, "/webhooks/whatsapp?token=segredo", two); rec.Code != 200 || !strings.Contains(rec.Body.String(), "queued") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if n := inboxCount(t, pool); n != 2 {
		t.Fatalf("inbox = %d, want 2", n)
	}
}
