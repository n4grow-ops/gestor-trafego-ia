package httpapi_test

// Segurança do checkout: o front não decide valor, plano, cobrança nem resultado. Quem altera a requisição
// (Burp, DevTools) não paga menos nem libera o plano.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/asaas"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/paylink"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func (e *checkoutEnv) postRaw(csrf, body string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/pagar/"+e.token+"/cartao", strings.NewReader(body),
		map[string]string{"Content-Type": "application/json", "Origin": testOrigin, "X-CSRF-Token": csrf})
}

func (e *checkoutEnv) subStatus() string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(), `SELECT status FROM subscriptions WHERE organization_id = $1`, e.orgID).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *checkoutEnv) linkPaid() bool {
	e.t.Helper()
	var paid bool
	if err := e.pool.QueryRow(context.Background(), `SELECT card_paid_at IS NOT NULL FROM checkout_links WHERE organization_id = $1`, e.orgID).Scan(&paid); err != nil {
		e.t.Fatal(err)
	}
	return paid
}

// 1. value/amount/plan/payment_id/customer/billingType/status no POST: recusados. A cobrança é sempre a do
// token, no valor dela (o pedido ao Asaas nem tem campo de valor).
func TestSecurityTamperedFieldsAreRefusedAndChargeComesFromToken(t *testing.T) {
	e := newCheckoutEnv(t, false)
	csrf := e.csrf()
	base := cardBody(testCard, testCVV)
	tampered := map[string]string{
		"value+plan+payment_id": strings.TrimSuffix(base, "}") + `,"value":0.01,"plan":"pro","payment_id":"pay_outra_org"}`,
		"amount":                strings.TrimSuffix(base, "}") + `,"amount":1}`,
		"customer+billingType+status": strings.TrimSuffix(base, "}") +
			`,"customer":"cus_x","billingType":"PIX","status":"RECEIVED"}`,
		"value no titular":      strings.Replace(base, `"holder":{`, `"holder":{"value":0.01,`, 1),
		"segundo objeto colado": base + `{"value":0.01}`,
	}
	for name, body := range tampered {
		if rec := e.postRaw(csrf, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if e.fake.payCalls != 0 || e.linkPaid() {
		t.Fatalf("pedido adulterado cobrou (%d) ou marcou pago", e.fake.payCalls)
	}
	rec := e.postRaw(csrf, base)
	if rec.Code != http.StatusOK || e.fake.lastPayment != "pay_1" || !e.linkPaid() {
		t.Fatalf("pedido limpo: %d, cobrança %q", rec.Code, e.fake.lastPayment)
	}
}

// 2. Só o webhook autenticado ativa a assinatura. Status forjado, parâmetro de URL, rota inventada e a
// resposta do POST do cartão não ativam nada.
func TestSecurityOnlyAuthenticatedWebhookActivates(t *testing.T) {
	e := newCheckoutEnv(t, false)
	forged := []struct{ method, path, body string }{
		{http.MethodPost, "/api/pagar/" + e.token + "/status", `{"status":"paid"}`},
		{http.MethodPut, "/api/pagar/" + e.token + "/status", `{"status":"paid"}`},
		{http.MethodGet, "/api/pagar/" + e.token + "/status?status=paid&success=true", ""},
		{http.MethodGet, "/pagar/" + e.token + "?success=true&status=paid", ""},
		{http.MethodPost, "/pagar/" + e.token, `success=true`},
		{http.MethodPost, "/api/pagar/" + e.token + "/sucesso", `{}`},
	}
	for _, f := range forged {
		rec := e.do(f.method, f.path, strings.NewReader(f.body), map[string]string{"Content-Type": "application/json", "Origin": testOrigin})
		if strings.Contains(rec.Body.String(), `"status":"paid"`) || strings.Contains(rec.Body.String(), `data-state="paid"`) {
			t.Errorf("%s %s respondeu pago", f.method, f.path)
		}
	}
	if s := e.subStatus(); s != "pending" || e.linkPaid() {
		t.Fatalf("forjado ativou: %s", s)
	}
	// Cartão aprovado e conferido: o link mostra pago, mas a assinatura só ativa com o webhook.
	if rec := e.payCard(e.csrf(), testCard, testCVV); rec.Code != http.StatusOK {
		t.Fatalf("cartão = %d", rec.Code)
	}
	if s := e.subStatus(); s != "pending" {
		t.Fatalf("resposta do POST ativou: %s", s)
	}
	hook := &httpapi.AsaasWebhook{Store: e.st, Token: "segredo", Log: e.h.Log}
	mux := http.NewServeMux()
	hook.Register(mux)
	body := `{"id":"evt_1","event":"PAYMENT_CONFIRMED","payment":{"id":"pay_1","subscription":"sub_1","value":397,"billingType":"CREDIT_CARD",
		"status":"CONFIRMED","dueDate":"2026-11-02","confirmedDate":"2026-11-02"}}`
	send := func(tok string) int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/asaas", strings.NewReader(body))
		if tok != "" {
			req.Header.Set("asaas-access-token", tok)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, tok := range []string{"", "errado"} {
		if code := send(tok); code != http.StatusUnauthorized {
			t.Fatalf("webhook com token %q = %d", tok, code)
		}
	}
	if s := e.subStatus(); s != "pending" {
		t.Fatalf("webhook sem token ativou: %s", s)
	}
	if send("segredo"); e.subStatus() != "active" {
		t.Fatalf("webhook autenticado não ativou: %s", e.subStatus())
	}
}

// 3. O servidor confere cobrança e valor devolvidos pelo Asaas antes de mostrar sucesso.
func TestSecurityAsaasResponseMustMatchTokenCharge(t *testing.T) {
	for name, res := range map[string]asaas.CardResult{
		"valor menor":    {Status: "CONFIRMED", Value: 0.01, CardToken: "tok"},
		"outra cobrança": {Status: "CONFIRMED", ID: "pay_outra", CardToken: "tok"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newCheckoutEnv(t, false)
			e.fake.payResult = res
			if m := decode(t, e.payCard(e.csrf(), testCard, testCVV)); m["status"] == "paid" {
				t.Fatalf("divergente virou pago: %v", m)
			}
			if e.linkPaid() || len(e.fake.subCards) != 0 {
				t.Fatalf("divergente marcou pago ou trocou o cartão da assinatura: %v", e.fake.subCards)
			}
			if !strings.Contains(e.logs.String(), "ALERTA cobrança divergente") {
				t.Fatal("sem alerta no log")
			}
		})
	}
	// Cobrança alterada no Asaas (valor diferente do token): nem tenta cobrar, nem gera Pix.
	e := newCheckoutEnv(t, false)
	e.fake.payment.Value = 1
	if rec := e.payCard(e.csrf(), testCard, testCVV); rec.Code != http.StatusConflict || e.fake.payCalls != 0 {
		t.Fatalf("cobrança divergente: %d, chamadas %d", rec.Code, e.fake.payCalls)
	}
	if rec := e.do(http.MethodGet, "/api/pagar/"+e.token+"/pix", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("Pix de cobrança divergente = %d", rec.Code)
	}
}

// 4. A consulta de status só lê: nada muda no banco e nada é chamado no Asaas.
func TestSecurityStatusIsReadOnly(t *testing.T) {
	e := newCheckoutEnv(t, false)
	snapshot := func() string {
		var s string
		if err := e.pool.QueryRow(context.Background(), `SELECT (SELECT jsonb_agg(to_jsonb(l)) FROM checkout_links l)::text ||
			(SELECT jsonb_agg(to_jsonb(s)) FROM subscriptions s)::text || (SELECT count(*) FROM payments)::text`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	for range 30 {
		e.do(http.MethodGet, "/api/pagar/"+e.token+"/status", nil, nil)
	}
	if after := snapshot(); after != before {
		t.Fatalf("status mudou o banco:\n%s\n%s", before, after)
	}
	if len(e.fake.calls) != 0 {
		t.Fatalf("status chamou o Asaas: %v", e.fake.calls)
	}
}

// 5. IDOR: o token só enxerga e só cobra a cobrança da própria organização; pago e vencido não reabrem.
func TestSecurityTokenIsBoundToItsOrgCharge(t *testing.T) {
	e := newCheckoutEnv(t, false)
	ctx := context.Background()
	orgB, err := e.st.CreateOrganization(ctx, "Outra Loja", "varejo", 100, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, provider, provider_ref, pending_plan)
		VALUES ($1, 'pro', 'pending', 'asaas', 'sub_B', 'pro')`, orgB); err != nil {
		t.Fatal(err)
	}
	rawB, hashB, _ := paylink.New()
	if err := e.st.CreateCheckoutLink(ctx, store.NewCheckoutLink{OrgID: orgB, TokenHash: hashB, AsaasSub: "sub_B", AsaasPayment: "pay_B",
		AmountCents: 99700, DueDate: e.now, ExpiresAt: e.now.Add(72 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if b := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil).Body.String(); strings.Contains(b, "997") || strings.Contains(b, "Avançado") {
		t.Fatal("página de A mostra dados de B")
	}
	tokA := e.token
	csrfA := e.csrf()
	e.token = rawB
	csrfB := e.csrf()
	e.token = tokA
	// CSRF de B não vale no link de A; pedir a cobrança de B no corpo é recusado.
	if rec := e.postRaw(csrfB, cardBody(testCard, testCVV)); rec.Code != http.StatusForbidden {
		t.Fatalf("CSRF de B no link de A = %d", rec.Code)
	}
	if rec := e.postRaw(csrfA, strings.TrimSuffix(cardBody(testCard, testCVV), "}")+`,"payment_id":"pay_B"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("payment_id de B = %d", rec.Code)
	}
	if rec := e.payCard(csrfA, testCard, testCVV); rec.Code != http.StatusOK || e.fake.lastPayment != "pay_1" {
		t.Fatalf("link de A cobrou %q", e.fake.lastPayment)
	}
	for _, call := range e.fake.calls {
		if strings.Contains(call, "pay_B") || strings.Contains(call, "sub_B") {
			t.Fatalf("mexeu na cobrança de B: %v", e.fake.calls)
		}
	}
	// Pago não reabre: nem cartão, nem Pix.
	calls := e.fake.payCalls
	if m := decode(t, e.payCard(csrfA, testCard, testCVV)); m["status"] != "paid" || e.fake.payCalls != calls {
		t.Fatalf("pago reabriu: %v", m)
	}
	if m := decode(t, e.do(http.MethodGet, "/api/pagar/"+e.token+"/pix", nil, nil)); m["status"] != "paid" || m["payload"] != nil {
		t.Fatalf("Pix de link pago: %v", m)
	}
	// Vencido não reabre (B, nunca pago).
	e.h.Now = func() time.Time { return e.now.Add(73 * time.Hour) }
	if rec := e.do(http.MethodGet, "/pagar/"+rawB, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("vencido reabriu: %d", rec.Code)
	}
	e.token = rawB
	if rec := e.postRaw(csrfB, cardBody(testCard, testCVV)); rec.Code != http.StatusNotFound {
		t.Fatalf("cartão em link vencido = %d", rec.Code)
	}
}

// 6. Troca de forma: o front só escolhe a rota (pix ou cartão); billingType e valor são do servidor.
func TestSecurityMethodSwitchIsServerSide(t *testing.T) {
	e := newCheckoutEnv(t, false)
	e.fake.payment.BillingType = asaas.BillingCard
	rec := e.do(http.MethodGet, "/api/pagar/"+e.token+"/pix?billingType=BOLETO&value=0.01&payment_id=pay_B", nil, nil)
	if rec.Code != http.StatusOK || len(e.fake.billingSets) != 1 || e.fake.billingSets[0] != "pay_1=PIX" {
		t.Fatalf("troca para Pix: %d %v", rec.Code, e.fake.billingSets)
	}
	if e.fake.payment.Value != 397 {
		t.Fatalf("valor mudou: %v", e.fake.payment.Value)
	}
}

// 7. Duas réplicas da api (dois handlers, mesmo banco) recebendo o mesmo pedido juntas: uma cobrança só.
func TestSecurityConcurrentChargeAcrossReplicas(t *testing.T) {
	e := newCheckoutEnv(t, false)
	e.fake.payDelay = 300 * time.Millisecond
	other := &httpapi.Checkout{Store: e.st, Asaas: e.fake, Origin: testOrigin, Log: e.h.Log, Now: e.h.Now}
	mux2 := http.NewServeMux()
	other.Register(mux2)
	csrfOf := func(m *http.ServeMux) string {
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pagar/"+e.token, nil))
		b := rec.Body.String()
		i := strings.Index(b, `data-csrf="`) + len(`data-csrf="`)
		return b[i : i+64]
	}
	tokens := map[*http.ServeMux]string{e.mux: csrfOf(e.mux), mux2: csrfOf(mux2)}
	var wg sync.WaitGroup
	for _, m := range []*http.ServeMux{e.mux, mux2, e.mux, mux2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/pagar/"+e.token+"/cartao", strings.NewReader(cardBody(testCard, testCVV)))
			req.Host = "cadastro.meuanuncio.io"
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", testOrigin)
			req.Header.Set("X-CSRF-Token", tokens[m])
			m.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()
	if e.fake.payCalls != 1 {
		t.Fatalf("cobranças = %d", e.fake.payCalls)
	}
}
