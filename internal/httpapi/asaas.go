package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// AsaasWebhook recebe os eventos de cobrança do Asaas (POST /webhooks/asaas). Sempre 200 depois de
// gravar: em erro o Asaas pausa a fila. Erro de banco = 500, para ele tentar de novo.
type AsaasWebhook struct {
	Store *store.Store
	Token string // ASAAS_WEBHOOK_TOKEN; vazio = desligado (503)
	Log   *slog.Logger
	// Subscriptions troca a forma da assinatura no Asaas (checkout próprio: cobrança de cartão paga por Pix
	// passa a assinatura para Pix). nil = sem troca.
	Subscriptions interface {
		SetSubscriptionBillingType(ctx context.Context, subID, billingType string) error
	}
}

func (h *AsaasWebhook) Register(mux *http.ServeMux) {
	mux.Handle("POST /webhooks/asaas", h)
}

func (h *AsaasWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Token == "" {
		http.Error(w, "webhook desativado", http.StatusServiceUnavailable)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("asaas-access-token")), []byte(h.Token)) != 1 {
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}
	var ev store.AsaasEvent
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&ev); err != nil || ev.ID == "" || ev.Event == "" {
		// Corpo que nunca vai servir: 200 para não travar a fila do Asaas.
		h.Log.Warn("webhook do asaas ilegível", "erro", err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	applied, err := h.Store.ApplyAsaasEvent(r.Context(), ev, engine.BillingNotifyJob)
	if err != nil {
		h.Log.Error("falha ao gravar evento do asaas", "evento", ev.Event, "id", ev.ID, "erro", err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	}
	status := "duplicate"
	if applied {
		status = "applied"
		h.Log.Info("evento do asaas", "evento", ev.Event, "id", ev.ID)
		h.followPix(r.Context(), ev)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// followPix: cobrança de cartão trocada para Pix no checkout e paga por Pix. A assinatura passa para Pix,
// para os próximos meses seguirem a forma que o cliente usou. Falha só fica no log: o pagamento já valeu.
func (h *AsaasWebhook) followPix(ctx context.Context, ev store.AsaasEvent) {
	if h.Subscriptions == nil || ev.Payment == nil || ev.Payment.BillingType != "PIX" ||
		(ev.Event != "PAYMENT_RECEIVED" && ev.Event != "PAYMENT_CONFIRMED") {
		return
	}
	sub, err := h.Store.CheckoutPixSwitchedSub(ctx, ev.Payment.ID)
	if err != nil || sub == "" {
		if err != nil {
			h.Log.Error("checkout: falha ao ler a troca para Pix", "pagamento", ev.Payment.ID, "erro", err)
		}
		return
	}
	if err := h.Subscriptions.SetSubscriptionBillingType(ctx, sub, "PIX"); err != nil {
		h.Log.Error("checkout: assinatura não passou para Pix", "assinatura", sub, "asaas", safeAsaasErr(err))
		return
	}
	h.Log.Info("checkout: assinatura passou para Pix", "assinatura", sub)
}
