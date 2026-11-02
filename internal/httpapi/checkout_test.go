package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/asaas"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/paylink"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

const (
	testCard   = "4111111111111111" // Visa de teste, passa no Luhn
	testCVV    = "987"
	testOrigin = "https://cadastro.meuanuncio.io"
)

// fakeCheckoutAsaas é o Asaas de mentira do checkout.
type fakeCheckoutAsaas struct {
	mu          sync.Mutex
	payDelay    time.Duration
	payment     asaas.Payment
	payErr      error
	payResult   asaas.CardResult
	payCalls    int
	lastIP      string
	lastCard    asaas.CreditCard
	lastPayment string   // cobrança que o checkout mandou cobrar
	calls       []string // toda chamada que muda algo no Asaas
	billingSets []string
	subCards    []string // sub|token|ip
}

func (f *fakeCheckoutAsaas) GetPayment(_ context.Context, id string) (asaas.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.payment, nil
}
func (f *fakeCheckoutAsaas) PixQRCode(_ context.Context, id string) (asaas.PixQRCode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.payment.BillingType != asaas.BillingPix {
		return asaas.PixQRCode{}, &asaas.APIError{Status: 400, Message: "invalid_action Esta cobrança não permite pagamentos via Pix."}
	}
	return asaas.PixQRCode{EncodedImage: "iVBORw0KGgo=", Payload: "00020101021226820014br.gov.bcb.pix", ExpirationDate: "2027-11-02 23:59:59"}, nil
}
func (f *fakeCheckoutAsaas) SetPaymentBillingType(_ context.Context, id, bt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.billingSets = append(f.billingSets, id+"="+bt)
	f.calls = append(f.calls, "billing:"+id+"="+bt)
	f.payment.BillingType = bt
	return nil
}
func (f *fakeCheckoutAsaas) PayWithCard(_ context.Context, id string, card asaas.CreditCard, _ asaas.CardHolder, ip string) (asaas.CardResult, error) {
	time.Sleep(f.payDelay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payCalls++
	f.lastIP, f.lastCard, f.lastPayment = ip, card, id
	f.calls = append(f.calls, "pay:"+id)
	if f.payErr != nil {
		return asaas.CardResult{}, f.payErr
	}
	res := f.payResult
	// O Asaas cobra o valor da cobrança dele, nunca um valor vindo do front.
	if res.ID == "" {
		res.ID = id
	}
	if res.Value == 0 {
		res.Value = f.payment.Value
	}
	if asaas.Paid(res.Status) {
		f.payment.Status = res.Status
	}
	return res, nil
}
func (f *fakeCheckoutAsaas) SetSubscriptionCard(_ context.Context, sub, token, ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subCards = append(f.subCards, sub+"|"+token+"|"+ip)
	f.calls = append(f.calls, "subcard:"+sub)
	return nil
}

type checkoutEnv struct {
	t     *testing.T
	pool  *pgxpool.Pool
	st    *store.Store
	fake  *fakeCheckoutAsaas
	h     *httpapi.Checkout
	mux   *http.ServeMux
	logs  *bytes.Buffer
	orgID int64
	token string
	now   time.Time
}

// newCheckoutEnv: organização com dono (CPF), assinatura pending no Asaas (sub_1) e um link válido para pay_1.
func newCheckoutEnv(t *testing.T, limiter bool) *checkoutEnv {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()
	org, err := st.CreateOrganization(ctx, "Pizzaria Boa", "restaurante", 100, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	mem, err := st.CreateMember(ctx, org, "Ana Souza", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkPhone(ctx, "openwa", "5511988887777", mem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE members SET cpf = '52998224725', email = 'ana@pizzaria.com' WHERE id = $1`, mem); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, provider, provider_ref, pending_plan, pending_method)
		VALUES ($1, 'starter', 'pending', 'asaas', 'sub_1', 'starter', 'pix')`, org); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC)
	raw, hash, err := paylink.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateCheckoutLink(ctx, store.NewCheckoutLink{OrgID: org, TokenHash: hash, AsaasSub: "sub_1", AsaasPayment: "pay_1",
		AmountCents: 39700, DueDate: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), ExpiresAt: now.Add(96 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	fake := &fakeCheckoutAsaas{payment: asaas.Payment{ID: "pay_1", Status: "PENDING", BillingType: asaas.BillingPix, Value: 397, DueDate: "2026-11-02"},
		payResult: asaas.CardResult{Status: "CONFIRMED", Last4: "1111", Brand: "VISA", CardToken: "tok_abc"}}
	h := &httpapi.Checkout{Store: st, Asaas: fake, Origin: testOrigin, Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug,
		// sem horário: o teste procura dígitos do cartão no log
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		}})),
		Now: func() time.Time { return now }}
	if limiter {
		h.Limiter = httpapi.NewLimiter()
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return &checkoutEnv{t: t, pool: pool, st: st, fake: fake, h: h, mux: mux, logs: logs, orgID: org, token: raw, now: now}
}

func (e *checkoutEnv) do(method, path string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	req.Host = "cadastro.meuanuncio.io"
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 177.10.20.30")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	e.mux.ServeHTTP(rec, req)
	return rec
}

// csrf lê o token CSRF da página, como o navegador faria.
func (e *checkoutEnv) csrf() string {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil)
	b := rec.Body.String()
	i := strings.Index(b, `data-csrf="`)
	if i < 0 {
		e.t.Fatalf("página sem csrf: %d", rec.Code)
	}
	b = b[i+len(`data-csrf="`):]
	return b[:strings.IndexByte(b, '"')]
}

func cardBody(number, cvv string) string {
	b, _ := json.Marshal(map[string]any{"number": number, "name": "Ana Souza", "expiry": "12/30", "cvv": cvv,
		"holder": map[string]string{"name": "Ana Souza", "cpfCnpj": "529.982.247-25", "email": "ana@pizzaria.com",
			"postalCode": "01310-100", "addressNumber": "100", "phone": "(11) 98888-7777"}})
	return string(b)
}

func (e *checkoutEnv) payCard(csrf, number, cvv string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/pagar/"+e.token+"/cartao", strings.NewReader(cardBody(number, cvv)),
		map[string]string{"Content-Type": "application/json", "Origin": testOrigin, "X-CSRF-Token": csrf})
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("json: %v: %s", err, rec.Body.String())
	}
	return m
}

func TestCheckoutPageOpenWithBrandAndHeaders(t *testing.T) {
	e := newCheckoutEnv(t, false)
	rec := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	b := rec.Body.String()
	for _, want := range []string{"Plano Intermediário", "R$ 397,00", "02/11/2026", "/static/logo.svg", "/static/pagar.js",
		`value="Ana Souza"`, `value="52998224725"`, `value="ana@pizzaria.com"`, `value="11988887777"`} {
		if !strings.Contains(b, want) {
			t.Errorf("página sem %q", want)
		}
	}
	if strings.Contains(b, e.token) {
		t.Error("o token não deve ir para dentro do HTML")
	}
	h := rec.Header()
	for k, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY",
		"X-Content-Type-Options": "nosniff"} {
		if h.Get(k) != want {
			t.Errorf("%s = %q", k, h.Get(k))
		}
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "frame-ancestors 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP sem %q: %s", want, csp)
		}
	}
}

func TestCheckoutInvalidUnknownAndExpiredLookTheSame(t *testing.T) {
	e := newCheckoutEnv(t, false)
	unknown, _, _ := paylink.New()
	bodies := map[string]string{}
	for name, tok := range map[string]string{"curto": "abc", "desconhecido": unknown, "lixo": strings.Repeat("-", 24)} {
		rec := e.do(http.MethodGet, "/pagar/"+tok, nil, nil)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Esse link não vale mais") {
			t.Errorf("%s: %d", name, rec.Code)
		}
		bodies[name] = rec.Body.String()
	}
	e.h.Now = func() time.Time { return e.now.Add(97 * time.Hour) }
	rec := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil)
	if rec.Code != http.StatusNotFound || rec.Body.String() != bodies["desconhecido"] {
		t.Fatalf("vencido: %d, igual ao desconhecido = %v", rec.Code, rec.Body.String() == bodies["desconhecido"])
	}
	if rec := e.do(http.MethodGet, "/api/pagar/"+e.token+"/status", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("status vencido = %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/api/pagar/"+e.token+"/pix", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("pix vencido = %d", rec.Code)
	}
}

func TestCheckoutPixQRAndStatusAfterWebhook(t *testing.T) {
	e := newCheckoutEnv(t, false)
	rec := e.do(http.MethodGet, "/api/pagar/"+e.token+"/pix", nil, nil)
	m := decode(t, rec)
	if rec.Code != http.StatusOK || m["payload"] != "00020101021226820014br.gov.bcb.pix" || m["image"] != "data:image/png;base64,iVBORw0KGgo=" {
		t.Fatalf("pix = %d %v", rec.Code, m)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("resposta do Pix sem no-store")
	}
	if m := decode(t, e.do(http.MethodGet, "/api/pagar/"+e.token+"/status", nil, nil)); m["status"] != "pending" {
		t.Fatalf("antes = %v", m)
	}
	// O webhook que já existe marca a cobrança paga; a página vê pelo status.
	ev := store.AsaasEvent{ID: "evt_1", Event: "PAYMENT_RECEIVED", Payment: &store.AsaasPayment{ID: "pay_1", Subscription: "sub_1",
		Value: 397, BillingType: "PIX", Status: "RECEIVED", DueDate: "2026-11-02", PaymentDate: "2026-11-02"}}
	if _, err := e.st.ApplyAsaasEvent(context.Background(), ev, engine.BillingNotifyJob); err != nil {
		t.Fatal(err)
	}
	if m := decode(t, e.do(http.MethodGet, "/api/pagar/"+e.token+"/status", nil, nil)); m["status"] != "paid" {
		t.Fatalf("depois = %v", m)
	}
	if rec := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil); !strings.Contains(rec.Body.String(), "Pagamento confirmado ✅") {
		t.Fatal("página paga sem a confirmação")
	}
}

func TestCheckoutPixOnCardChargeSwitchesOnlyThePayment(t *testing.T) {
	e := newCheckoutEnv(t, false)
	e.fake.payment.BillingType = asaas.BillingCard
	rec := e.do(http.MethodGet, "/api/pagar/"+e.token+"/pix", nil, nil)
	if rec.Code != http.StatusOK || len(e.fake.billingSets) != 1 || e.fake.billingSets[0] != "pay_1=PIX" {
		t.Fatalf("pix em cobrança de cartão: %d %v", rec.Code, e.fake.billingSets)
	}
}

func TestCheckoutCardPaysAndMovesSubscriptionToCard(t *testing.T) {
	e := newCheckoutEnv(t, false)
	rec := e.payCard(e.csrf(), testCard, testCVV)
	if m := decode(t, rec); rec.Code != http.StatusOK || m["status"] != "paid" {
		t.Fatalf("cartão = %d %v", rec.Code, m)
	}
	f := e.fake
	if f.payCalls != 1 || f.lastIP != "177.10.20.30" || f.lastCard.Number != testCard || f.lastCard.ExpiryYear != "2030" || f.lastCard.HolderName != "ANA SOUZA" {
		t.Fatalf("cobrança = %d chamadas, ip %q, cartão %+v", f.payCalls, f.lastIP, f.lastCard)
	}
	if len(f.subCards) != 1 || f.subCards[0] != "sub_1|tok_abc|177.10.20.30" {
		t.Fatalf("cartão na assinatura = %v", f.subCards)
	}
	if m := decode(t, e.do(http.MethodGet, "/api/pagar/"+e.token+"/status", nil, nil)); m["status"] != "paid" {
		t.Fatalf("status = %v", m)
	}
	// Segundo envio (duplo clique): não cobra de novo.
	if rec := e.payCard(e.csrf(), testCard, testCVV); rec.Code != http.StatusOK || f.payCalls != 1 {
		t.Fatalf("segunda cobrança: %d, chamadas %d", rec.Code, f.payCalls)
	}
	assertNoCardInLogs(t, e)
}

func TestCheckoutCardAlreadyPaidAtAsaasDoesNotCharge(t *testing.T) {
	e := newCheckoutEnv(t, false)
	e.fake.payment.Status = "RECEIVED"
	if rec := e.payCard(e.csrf(), testCard, testCVV); rec.Code != http.StatusOK || e.fake.payCalls != 0 {
		t.Fatalf("já paga: %d, chamadas %d", rec.Code, e.fake.payCalls)
	}
}

func TestCheckoutCardRequiresCSRFAndOrigin(t *testing.T) {
	e := newCheckoutEnv(t, false)
	csrf := e.csrf()
	cases := map[string]map[string]string{
		"sem csrf":        {"Content-Type": "application/json", "Origin": testOrigin},
		"csrf errado":     {"Content-Type": "application/json", "Origin": testOrigin, "X-CSRF-Token": strings.Repeat("0", 64)},
		"outra origem":    {"Content-Type": "application/json", "Origin": "https://evil.example", "X-CSRF-Token": csrf},
		"sem origem":      {"Content-Type": "application/json", "X-CSRF-Token": csrf},
		"cross-site":      {"Content-Type": "application/json", "X-CSRF-Token": csrf, "Sec-Fetch-Site": "cross-site"},
		"form (sem json)": {"Content-Type": "application/x-www-form-urlencoded", "Origin": testOrigin, "X-CSRF-Token": csrf},
	}
	for name, hdr := range cases {
		rec := e.do(http.MethodPost, "/api/pagar/"+e.token+"/cartao", strings.NewReader(cardBody(testCard, testCVV)), hdr)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if e.fake.payCalls != 0 {
		t.Fatalf("cobrou sem CSRF: %d", e.fake.payCalls)
	}
}

func TestCheckoutCardValidation(t *testing.T) {
	e := newCheckoutEnv(t, false)
	csrf := e.csrf()
	for number, field := range map[string]string{"4111111111111112": "number", "4444444444444444": "number", "411111": "number"} {
		rec := e.payCard(csrf, number, testCVV)
		if m := decode(t, rec); rec.Code != http.StatusBadRequest || m["field"] != field {
			t.Errorf("%s: %d %v", number, rec.Code, m)
		}
	}
	if rec := e.payCard(csrf, testCard, "12"); decode(t, rec)["field"] != "cvv" {
		t.Error("CVV curto passou")
	}
	if rec := e.payCard(csrf, "378282246310005", "123"); decode(t, rec)["field"] != "cvv" {
		t.Error("Amex com CVV de 3 dígitos passou")
	}
	if e.fake.payCalls != 0 {
		t.Fatalf("cobrou com dado inválido: %d", e.fake.payCalls)
	}
	// Sandbox aceita o cartão de teste do Asaas (4444…, fora do Luhn).
	e.h.Sandbox = true
	if rec := e.payCard(csrf, "4444444444444444", testCVV); rec.Code != http.StatusOK {
		t.Fatalf("cartão de teste no sandbox = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCheckoutCardLocksAfterRepeatedDeclines(t *testing.T) {
	e := newCheckoutEnv(t, false)
	csrf := e.csrf()
	// O erro do Asaas traz dígitos: não podem ir crus para o log nem para o cliente.
	e.fake.payErr = &asaas.APIError{Method: "POST", Path: "/payments/pay_1/payWithCreditCard", Status: 400,
		Message: "invalid_action Transação não autorizada para o cartão " + testCard}
	for i := 1; i <= 5; i++ {
		rec := e.payCard(csrf, testCard, testCVV)
		m := decode(t, rec)
		if rec.Code != http.StatusPaymentRequired || m["locked"] != (i == 5) {
			t.Fatalf("tentativa %d: %d %v", i, rec.Code, m)
		}
		if strings.Contains(rec.Body.String(), "autorizada") || strings.Contains(rec.Body.String(), "invalid_action") {
			t.Fatal("erro do Asaas vazou para o cliente")
		}
	}
	e.fake.payErr = nil
	if rec := e.payCard(csrf, testCard, testCVV); rec.Code != http.StatusTooManyRequests || e.fake.payCalls != 5 {
		t.Fatalf("bloqueado: %d, chamadas %d", rec.Code, e.fake.payCalls)
	}
	if rec := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil); !strings.Contains(rec.Body.String(), `data-locked="1"`) {
		t.Error("página não mostra o bloqueio")
	}
	// Passado o bloqueio, volta a aceitar.
	e.h.Now = func() time.Time { return time.Now().Add(31 * time.Minute) }
	if _, err := e.pool.Exec(context.Background(), `UPDATE checkout_links SET expires_at = now() + interval '5 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE checkout_links SET locked_until = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if rec := e.payCard(e.csrf(), testCard, testCVV); rec.Code != http.StatusOK {
		t.Fatalf("depois do bloqueio = %d %s", rec.Code, rec.Body.String())
	}
	assertNoCardInLogs(t, e)
}

func TestCheckoutAsaasOutageIsGenericAndChecksPayment(t *testing.T) {
	e := newCheckoutEnv(t, false)
	e.fake.payErr = errors.New("asaas: Post \"https://api.asaas.com/v3/payments/pay_1/payWithCreditCard\": context deadline exceeded")
	rec := e.payCard(e.csrf(), testCard, testCVV)
	if m := decode(t, rec); rec.Code != http.StatusBadGateway || m["error"] != "indisponivel" {
		t.Fatalf("queda = %d %v", rec.Code, m)
	}
	var fails int
	if err := e.pool.QueryRow(context.Background(), `SELECT failed_attempts FROM checkout_links`).Scan(&fails); err != nil || fails != 0 {
		t.Fatalf("queda do Asaas contou como recusa: %d %v", fails, err)
	}
}

func TestCheckoutRateLimitPerToken(t *testing.T) {
	e := newCheckoutEnv(t, true)
	csrf := e.csrf()
	e.fake.payErr = &asaas.APIError{Status: 400, Message: "invalid_action recusado"}
	// A consulta de status da página (a cada 5 s) não gasta a cota do cartão.
	for range 20 {
		e.do(http.MethodGet, "/api/pagar/"+e.token+"/status", nil, nil)
	}
	codes := []int{}
	for i := 0; i < 6; i++ {
		codes = append(codes, e.payCard(csrf, testCard, testCVV).Code)
	}
	if codes[0] != http.StatusPaymentRequired || codes[5] != http.StatusTooManyRequests {
		t.Fatalf("sem limite por token: %v", codes)
	}
	if e.fake.payCalls > 5 {
		t.Fatalf("chamadas ao Asaas = %d", e.fake.payCalls)
	}
}

// assertNoCardInLogs: número completo e CVV nunca aparecem no log; o final do cartão pode.
func assertNoCardInLogs(t *testing.T, e *checkoutEnv) {
	t.Helper()
	logs := e.logs.String()
	if strings.Contains(logs, testCard) || strings.Contains(logs, "ccv") || strings.Contains(logs, "cvv="+testCVV) || strings.Contains(logs, testCVV) {
		t.Fatalf("dado de cartão no log:\n%s", logs)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM checkout_links l WHERE l::text LIKE '%'||$1||'%'`, testCard).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cartão no banco: %d %v", n, err)
	}
}

func TestCheckoutConcurrentSubmitsChargeOnce(t *testing.T) {
	e := newCheckoutEnv(t, false)
	csrf := e.csrf()
	e.fake.payDelay = 300 * time.Millisecond
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.payCard(csrf, testCard, testCVV).Code
		}()
	}
	wg.Wait()
	if e.fake.payCalls != 1 {
		t.Fatalf("cobranças = %d (códigos %v)", e.fake.payCalls, codes)
	}
}

type fakeSubs struct{ calls []string }

func (f *fakeSubs) SetSubscriptionBillingType(_ context.Context, sub, bt string) error {
	f.calls = append(f.calls, sub+"="+bt)
	return nil
}

func TestCheckoutCardChargePaidByPixMovesSubscriptionToPix(t *testing.T) {
	e := newCheckoutEnv(t, false)
	subs := &fakeSubs{}
	hook := &httpapi.AsaasWebhook{Store: e.st, Token: "tok", Log: e.h.Log, Subscriptions: subs}
	mux := http.NewServeMux()
	hook.Register(mux)
	post := func(id, payment, billing string) {
		body := `{"id":"` + id + `","event":"PAYMENT_RECEIVED","payment":{"id":"` + payment + `","subscription":"sub_1","value":397,
			"billingType":"` + billing + `","status":"RECEIVED","dueDate":"2026-11-02","paymentDate":"2026-11-02"}}`
		req := httptest.NewRequest(http.MethodPost, "/webhooks/asaas", strings.NewReader(body))
		req.Header.Set("asaas-access-token", "tok")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("webhook = %d", rec.Code)
		}
	}
	// Pix pago sem troca na página: a assinatura fica como está.
	post("evt_a", "pay_0", "PIX")
	if len(subs.calls) != 0 {
		t.Fatalf("trocou sem a página pedir: %v", subs.calls)
	}
	// Cobrança de cartão; o cliente abre o Pix na página e paga por Pix.
	e.fake.payment.BillingType = asaas.BillingCard
	if rec := e.do(http.MethodGet, "/api/pagar/"+e.token+"/pix", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("pix = %d", rec.Code)
	}
	post("evt_b", "pay_1", "PIX")
	if len(subs.calls) != 1 || subs.calls[0] != "sub_1=PIX" {
		t.Fatalf("assinatura = %v", subs.calls)
	}
	// Evento repetido não troca de novo.
	post("evt_b", "pay_1", "PIX")
	if len(subs.calls) != 1 {
		t.Fatalf("repetido trocou de novo: %v", subs.calls)
	}
}

func TestCheckoutPageOpensOnChosenMethod(t *testing.T) {
	e := newCheckoutEnv(t, false)
	if b := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil).Body.String(); !strings.Contains(b, `data-method="pix"`) {
		t.Fatal("link de Pix sem data-method=pix")
	}
	if _, err := e.pool.Exec(context.Background(), `UPDATE checkout_links SET method = 'card'`); err != nil {
		t.Fatal(err)
	}
	if b := e.do(http.MethodGet, "/pagar/"+e.token, nil, nil).Body.String(); !strings.Contains(b, `data-method="card"`) {
		t.Fatal("link de cartão sem data-method=card")
	}
}
