package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

type fakeAuth struct{ calls *int }

func (a fakeAuth) GetUser(_ context.Context, token string) (signup.User, error) {
	if a.calls != nil {
		*a.calls++
	}
	if token != "sessao-ok" {
		return signup.User{}, signup.ErrUnauthorized
	}
	return signup.User{ID: "7c9e6679-7425-40de-944b-e07fc1f90ae7", Email: "ana@mail.com", Name: "Ana Souza", VerifiedAt: time.Now()}, nil
}

type fakeCNPJ struct{}

func (fakeCNPJ) LookupCNPJ(_ context.Context, cnpj string) (signup.Company, error) {
	if !signup.ValidCNPJ(cnpj) {
		return signup.Company{}, signup.ErrInvalid
	}
	if signup.OnlyDigits(cnpj) != "11222333000181" {
		return signup.Company{}, signup.ErrNotFound
	}
	return signup.Company{LegalName: "PIZZARIA BOA LTDA", TradeName: "PIZZARIA BOA", City: "CAMPINAS", UF: "SP"}, nil
}

type signupEnv struct {
	mux       http.Handler
	authCalls *int
	pool      *pgxpool.Pool
	token     string
	ident     store.Identity
}

func newSignupEnv(t *testing.T) signupEnv {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()
	org, err := st.CreateOrganization(ctx, "Cliente 7777", "", 50, []string{"OUTCOME_ENGAGEMENT"})
	if err != nil {
		t.Fatal(err)
	}
	mem, err := st.CreateMember(ctx, org, "Cliente", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkPhone(ctx, "waha", "5511988887777", mem); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRegistrationStatus(ctx, org, "pending"); err != nil {
		t.Fatal(err)
	}
	ident, err := st.FindIdentity(ctx, "waha", "5511988887777")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us"); err != nil {
		t.Fatal(err)
	}
	raw, hash, err := signup.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSignupLink(ctx, ident.ID, hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := &httpapi.Signup{Store: st, Auth: fakeAuth{calls: &calls}, CNPJ: fakeCNPJ{}, SupabaseURL: "https://sb.test", SupabaseKey: "pub",
		BotNumber: "5511900000000", TermsVersion: "2026-09-23", Log: discard, Limiter: httpapi.NewLimiter()}
	return signupEnv{mux: httpapi.NewMux(pingOK{}, http.NotFoundHandler(), s.Register), authCalls: &calls, pool: pool, token: raw, ident: ident}
}

type pingOK struct{}

func (pingOK) Ping(context.Context) error { return nil }

func (e signupEnv) do(method, target, body, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("X-Forwarded-For", "200.1.2.3, 10.0.0.1")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

const validBody = `{"full_name":"Ana Souza","business_name":"Pizzaria Boa","segment":"restaurante","city":"Campinas","uf":"SP",
"objectives":["mensagens","leads"],"monthly_budget":"500_1500","ads_experience":"nunca","meta_ad_account":"nao_sei","approver":"self",
"cnpj":"11.222.333/0001-81","accept_terms":true,"plan":"free"}`

func TestSignupPageAndTerms(t *testing.T) {
	e := newSignupEnv(t)
	if rec := e.do("GET", "/c/"+e.token, "", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "supabase.js") {
		t.Fatalf("link curto: %d", rec.Code)
	}
	rec := e.do("GET", "/cadastro/"+e.token, "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `src="/static/supabase.js"`) || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("página: %d %q", rec.Code, rec.Header().Get("Referrer-Policy"))
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self' https://sb.test") {
		t.Fatalf("CSP = %q", csp)
	}
	if strings.Contains(rec.Body.String(), "cdn.jsdelivr") {
		t.Fatal("script de terceiros na página")
	}
	if rec := e.do("GET", "/static/supabase.js", "", ""); rec.Code != 200 || rec.Body.Len() < 100000 {
		t.Fatalf("supabase.js: %d %d", rec.Code, rec.Body.Len())
	}
	for _, p := range []string{"/termos", "/privacidade"} {
		if rec := e.do("GET", p, "", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "LGPD") {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestHomePage(t *testing.T) {
	e := newSignupEnv(t)
	rec := e.do("GET", "/", "", "")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "meuanuncio.io") || !strings.Contains(body, "https://wa.me/5511900000000?text=") || strings.Contains(body, "{{WHATSAPP") {
		t.Fatalf("início: %d %s", rec.Code, body)
	}
	if !strings.Contains(body, "+55 11 90000-0000") {
		t.Error("início sem o número do WhatsApp formatado")
	}
	// A página roda JS próprio (demonstração e contadores), nunca inline nem de terceiros.
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "style-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP = %q", csp)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, ` style="`) {
		t.Error("script ou style inline no início (a CSP bloqueia)")
	}
	for p, ct := range map[string]string{
		"/static/logo.png": "image/png", "/static/icon.png": "image/png", "/favicon.ico": "image/png",
		"/static/logo.svg": "image/svg+xml", "/static/simbolo.svg": "image/svg+xml", "/static/favicon.svg": "image/svg+xml",
		"/static/apple-touch-icon.png": "image/png",
		"/static/urbanist.woff2":       "font/woff2", "/static/figtree.woff2": "font/woff2",
		"/static/inicio.css": "text/css; charset=utf-8", "/static/inicio.js": "text/javascript; charset=utf-8",
	} {
		if rec := e.do("GET", p, "", ""); rec.Code != 200 || rec.Header().Get("Content-Type") != ct {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	for _, href := range []string{`href="/termos"`, `href="/privacidade"`, `src="/static/logo.svg"`} {
		if !strings.Contains(body, href) {
			t.Errorf("início sem %s", href)
		}
	}
	if rec := e.do("GET", "/nao-existe", "", ""); rec.Code != 404 {
		t.Fatalf("rota desconhecida: %d", rec.Code)
	}
}

// Todo arquivo /static/... citado nas páginas públicas precisa ter rota.
func TestPublicPagesAssetsResolve(t *testing.T) {
	e := newSignupEnv(t)
	ref := regexp.MustCompile(`(?:href|src)="(/static/[^"?]+)`)
	for _, page := range []string{"web/inicio.html", "web/cadastro.html", "web/meta.html", "web/termos.html", "web/privacidade.html", "web/inicio.css", "web/cadastro.css"} {
		b, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		found := ref.FindAllStringSubmatch(string(b), -1)
		for _, m := range regexp.MustCompile(`url\("(/static/[^"?]+)`).FindAllStringSubmatch(string(b), -1) {
			found = append(found, m)
		}
		for _, m := range found {
			if rec := e.do("GET", m[1], "", ""); rec.Code != 200 {
				t.Errorf("%s cita %s: %d", page, m[1], rec.Code)
			}
		}
	}
}

func TestSignupConfig(t *testing.T) {
	e := newSignupEnv(t)
	rec := e.do("GET", "/api/cadastro/"+e.token, "", "")
	if rec.Code != 200 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Prefill  store.SignupPrefill `json:"prefill"`
		Plans    []store.Plan        `json:"plans"`
		Options  map[string]any      `json:"options"`
		Supabase struct{ URL, Key string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Prefill.Phone != "5511988887777" || len(body.Plans) != 3 || body.Options["segments"] == nil || body.Supabase.Key != "pub" {
		t.Fatalf("config = %+v", body)
	}
	if rec := e.do("GET", "/api/cadastro/nao-existe", "", ""); rec.Code != http.StatusGone {
		t.Fatalf("link ruim: %d", rec.Code)
	}
}

func TestSignupCNPJLookup(t *testing.T) {
	e := newSignupEnv(t)
	rec := e.do("GET", "/api/cadastro/"+e.token+"/cnpj/11222333000181", "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "PIZZARIA BOA LTDA") {
		t.Fatalf("cnpj: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/cadastro/"+e.token+"/cnpj/33000167000101", "", ""); rec.Code != 404 {
		t.Fatalf("não encontrado: %d", rec.Code)
	}
	if rec := e.do("GET", "/api/cadastro/nao-existe/cnpj/11222333000181", "", ""); rec.Code != http.StatusGone {
		t.Fatalf("sem link válido não consulta: %d", rec.Code)
	}
}

func TestSignupCNPJLookupIsRateLimited(t *testing.T) {
	e := newSignupEnv(t)
	last := 0
	for range 12 {
		last = e.do("GET", "/api/cadastro/"+e.token+"/cnpj/11222333000181", "", "").Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("12ª consulta = %d; want 429", last)
	}
}

func TestSignupSubmitChecksLinkBeforeSupabase(t *testing.T) {
	e := newSignupEnv(t)
	if rec := e.do("POST", "/api/cadastro/nao-existe", validBody, "sessao-ok"); rec.Code != http.StatusGone {
		t.Fatalf("link ruim: %d", rec.Code)
	}
	if *e.authCalls != 0 {
		t.Fatal("chamou o Supabase com link inválido")
	}
}

func TestSignupSubmit(t *testing.T) {
	e := newSignupEnv(t)
	target := "/api/cadastro/" + e.token
	if rec := e.do("POST", target, validBody, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sem sessão: %d", rec.Code)
	}
	if rec := e.do("POST", target, validBody, "sessao-ruim"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sessão ruim: %d", rec.Code)
	}
	bad := strings.Replace(validBody, `"accept_terms":true`, `"accept_terms":false`, 1)
	if rec := e.do("POST", target, bad, "sessao-ok"); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "accept_terms") {
		t.Fatalf("inválido: %d %s", rec.Code, rec.Body)
	}
	rec := e.do("POST", target, validBody, "sessao-ok")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "https://wa.me/5511900000000") {
		t.Fatalf("concluir: %d %s", rec.Code, rec.Body)
	}
	var status, email, ip string
	if err := e.pool.QueryRow(context.Background(), `SELECT o.registration_status, m.email, c.ip FROM organizations o
		JOIN members m ON m.organization_id = o.id JOIN consents c ON c.member_id = m.id LIMIT 1`).Scan(&status, &email, &ip); err != nil {
		t.Fatal(err)
	}
	// O IP é o que o proxy (último da lista) viu, não o que o cliente escreveu no cabeçalho.
	if status != "complete" || email != "ana@mail.com" || ip != "10.0.0.1" {
		t.Fatalf("gravado: %s %s %s", status, email, ip)
	}
	var welcome int
	e.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_jobs WHERE kind = 'whatsapp.send' AND payload->>'text' LIKE '%Ana%'`).Scan(&welcome)
	if welcome != 1 {
		t.Fatalf("boas-vindas = %d", welcome)
	}
	if rec := e.do("POST", target, validBody, "sessao-ok"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "wa.me") {
		t.Fatalf("repetido: %d %s", rec.Code, rec.Body)
	}
}

func TestSignupRejectsUnavailablePlan(t *testing.T) {
	e := newSignupEnv(t)
	if _, err := e.pool.Exec(context.Background(), `UPDATE plans SET available = false WHERE code = 'pro'`); err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(validBody, `"plan":"free"`, `"plan":"pro"`, 1)
	if rec := e.do("POST", "/api/cadastro/"+e.token, body, "sessao-ok"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("plano pago: %d %s", rec.Code, rec.Body)
	}
}
