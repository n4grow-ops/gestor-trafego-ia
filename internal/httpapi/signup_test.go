package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

type fakeAuth struct{}

func (fakeAuth) GetUser(_ context.Context, token string) (signup.User, error) {
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
	mux   http.Handler
	pool  *pgxpool.Pool
	token string
	ident store.Identity
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
	s := &httpapi.Signup{Store: st, Auth: fakeAuth{}, CNPJ: fakeCNPJ{}, SupabaseURL: "https://sb.test", SupabaseKey: "pub",
		BotNumber: "5511900000000", TermsVersion: "2026-09-23", Log: discard}
	return signupEnv{mux: httpapi.NewMux(pingOK{}, http.NotFoundHandler(), s.Register), pool: pool, token: raw, ident: ident}
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
"objective":"mensagens","monthly_budget":"500_1500","ads_experience":"nunca","meta_ad_account":"nao_sei","approver":"self",
"cnpj":"11.222.333/0001-81","accept_terms":true,"plan":"free"}`

func TestSignupPageAndTerms(t *testing.T) {
	e := newSignupEnv(t)
	rec := e.do("GET", "/cadastro/"+e.token, "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "supabase-js") || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("página: %d %q", rec.Code, rec.Header().Get("Referrer-Policy"))
	}
	for _, p := range []string{"/termos", "/privacidade"} {
		if rec := e.do("GET", p, "", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "LGPD") {
			t.Errorf("%s: %d", p, rec.Code)
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
	if status != "complete" || email != "ana@mail.com" || ip != "200.1.2.3" {
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
	body := strings.Replace(validBody, `"plan":"free"`, `"plan":"pro"`, 1)
	if rec := e.do("POST", "/api/cadastro/"+e.token, body, "sessao-ok"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("plano pago: %d %s", rec.Code, rec.Body)
	}
}
