// Package httpapi expõe o webhook do WhatsApp, o health check e o cadastro do cliente. O webhook só
// grava no inbox; o processamento é do worker.
package httpapi

import (
	"context"
	"crypto/subtle"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

// NewMux monta as rotas; extra registra rotas adicionais (ex.: cadastro).
func NewMux(db Pinger, webhook http.Handler, extra ...func(*http.ServeMux)) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			http.Error(w, "banco indisponível", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok")
	})
	mux.Handle("POST /webhooks/whatsapp", webhook)
	for _, register := range extra {
		register(mux)
	}
	return mux
}

type Webhook struct {
	Store  *store.Store
	WA     whatsapp.Provider
	Secret string
	Log    *slog.Logger
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Secret == "" {
		http.Error(w, "webhook desativado", http.StatusServiceUnavailable)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(h.Secret)) != 1 {
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		http.Error(w, "corpo inválido", http.StatusBadRequest)
		return
	}
	if cp, isCP := h.WA.(whatsapp.ConnectionParser); isCP {
		if ev, isConn := cp.ParseConnection(body); isConn {
			h.connection(w, r, ev)
			return
		}
	}
	in, ok, err := h.WA.ParseWebhook(body)
	if err != nil {
		h.Log.Warn("webhook inválido", "erro", err)
		http.Error(w, "payload inválido", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		io.WriteString(w, `{"status":"ignored"}`)
		return
	}
	inserted, err := h.Store.InsertInbox(r.Context(), h.WA.Name(), in.ExternalID, body)
	if err != nil {
		h.Log.Error("falha ao gravar webhook", "erro", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	}
	if !inserted {
		io.WriteString(w, `{"status":"duplicate"}`)
		return
	}
	io.WriteString(w, `{"status":"queued"}`)
}

// connection grava o aviso de queda ou volta do número. O alerta ativo é do admin, que lê
// whatsapp_status: avisar pelo WhatsApp não adianta quando é ele que caiu.
func (h *Webhook) connection(w http.ResponseWriter, r *http.Request, ev whatsapp.ConnectionEvent) {
	changed, err := h.Store.SetWhatsAppStatus(r.Context(), h.WA.Name(), ev)
	if err != nil {
		h.Log.Error("falha ao gravar estado do WhatsApp", "erro", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	}
	switch {
	case changed && !ev.Connected:
		h.Log.Warn("WhatsApp do bot desconectado", "provider", h.WA.Name(), "codigo", ev.Code, "motivo", ev.Reason)
	case changed:
		h.Log.Info("WhatsApp do bot conectado", "provider", h.WA.Name())
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"status":"connection"}`)
}
