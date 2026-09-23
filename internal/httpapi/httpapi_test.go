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
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/openwa"
)

const msg = `{"event":"onMessage","data":{"id":"m1","from":"5511988887777@c.us","type":"chat","body":"oi"}}`

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newWebhook(t *testing.T, secret string) (*httpapi.Webhook, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	return &httpapi.Webhook{Store: store.New(pool), WA: openwa.New("http://nao-usado", "k"), Secret: secret, Log: discard}, pool
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
	for _, target := range []string{"/webhooks/openwa", "/webhooks/openwa?token=errado", "/webhooks/openwa?token=segred"} {
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
	if rec := post(wh, "/webhooks/openwa?token=", msg); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", rec.Code)
	}
	if inboxCount(t, pool) != 0 {
		t.Error("nada deveria ser gravado")
	}
}

func TestWebhookQueuesAndDeduplicates(t *testing.T) {
	wh, pool := newWebhook(t, "segredo")
	rec := post(wh, "/webhooks/openwa?token=segredo", msg)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "queued") {
		t.Fatalf("1º: %d %s", rec.Code, rec.Body)
	}
	rec = post(wh, "/webhooks/openwa?token=segredo", msg)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("2º: %d %s", rec.Code, rec.Body)
	}
	if inboxCount(t, pool) != 1 {
		t.Errorf("inbox = %d", inboxCount(t, pool))
	}
}

func TestWebhookIgnoresOtherEvents(t *testing.T) {
	wh, pool := newWebhook(t, "segredo")
	rec := post(wh, "/webhooks/openwa?token=segredo", `{"event":"onAck","data":{}}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ignored") || inboxCount(t, pool) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestWebhookRejectsInvalidJSON(t *testing.T) {
	wh, _ := newWebhook(t, "segredo")
	if rec := post(wh, "/webhooks/openwa?token=segredo", "{nope"); rec.Code != http.StatusBadRequest {
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
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/openwa?token=segredo", strings.NewReader(msg)))
	if rec.Code != 200 {
		t.Errorf("POST: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/webhooks/openwa?token=segredo", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}
