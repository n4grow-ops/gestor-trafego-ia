package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

// fakeOTP finge o Supabase Auth: o código certo é 123456.
type fakeOTP struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeOTP) SendOTP(_ context.Context, email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, email)
	return nil
}

func (f *fakeOTP) VerifyOTP(_ context.Context, email, code string) (signup.User, error) {
	if code != "123456" {
		return signup.User{}, signup.ErrUnauthorized
	}
	return signup.User{ID: "u-" + email, Email: email, VerifiedAt: time.Now()}, nil
}

type adminEnv struct {
	t       *testing.T
	pool    *pgxpool.Pool
	st      *store.Store
	h       http.Handler
	otp     *fakeOTP
	adminID int64
	staffID int64
	convID  int64
	orgID   int64
	admin   *httpapi.Admin
}

const adminHost = "admin.meuanuncio.io"

func newAdminEnv(t *testing.T, mode string) *adminEnv {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()
	e := &adminEnv{t: t, pool: pool, st: st, otp: &fakeOTP{}}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.EnsureAdmins(ctx, []string{"dono@meuanuncio.io"}))
	adm, _ := st.TeamMemberByEmail(ctx, "dono@meuanuncio.io")
	e.adminID = adm.ID
	staff, err := st.AddTeamMember(ctx, "marina@meuanuncio.io", "Marina", "staff", "dono@meuanuncio.io")
	must(err)
	e.staffID = staff.ID

	org, err := st.CreateOrganization(ctx, "Pizzaria Boa", "", 50, []string{"OUTCOME_ENGAGEMENT"})
	must(err)
	e.orgID = org
	mem, err := st.CreateMember(ctx, org, "Ana", "owner")
	must(err)
	must(st.LinkPhone(ctx, "waha", "5511988887777", mem))
	ident, err := st.FindIdentity(ctx, "waha", "5511988887777")
	must(err)
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(err)
	e.convID = conv.ID
	must(st.UpdateConversation(ctx, conv.ID, store.StateCollecting, contract.Fields{}))
	_, err = st.AddInbound(ctx, conv.ID, "text", "oi")
	must(err)

	a := &httpapi.Admin{
		Store: st, DB: pool, Mode: mode, SessionKey: []byte("chave-de-teste-com-mais-de-32-caracteres"),
		Secure: true, SignupBaseURL: "https://x.io/c", ChatIDForPhone: func(p string) string { return p + "@c.us" },
		Provider: "waha", USDBRL: 5, Limiter: httpapi.NewLimiter(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mode == "otp" {
		a.Auth = e.otp
	}
	e.admin = a
	e.h = httpapi.WithAdminHost(adminHost, httpapi.NewMux(pool, http.NotFoundHandler(), a.Register))
	return e
}

type session struct {
	cookie string
	csrf   string
}

type resp struct {
	code int
	body map[string]any
	raw  string
	hdr  http.Header
}

func (e *adminEnv) do(method, path string, s *session, body any, mod ...func(*http.Request)) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, "https://"+adminHost+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if s != nil {
		req.Header.Set("Cookie", s.cookie)
		if method != http.MethodGet {
			req.Header.Set("X-CSRF-Token", s.csrf)
			req.Header.Set("Origin", "https://"+adminHost)
		}
	}
	for _, m := range mod {
		m(req)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	r := resp{code: rec.Code, raw: rec.Body.String(), hdr: rec.Header()}
	json.Unmarshal(rec.Body.Bytes(), &r.body)
	return r
}

// login entra pelo código (modo otp) e devolve a sessão.
func (e *adminEnv) login(email string) *session {
	e.t.Helper()
	if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": email}); r.code != 200 {
		e.t.Fatalf("start = %d %s", r.code, r.raw)
	}
	r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": email, "code": "123456"})
	if r.code != 200 {
		e.t.Fatalf("verify = %d %s", r.code, r.raw)
	}
	return sessionFrom(e.t, r)
}

func sessionFrom(t *testing.T, r resp) *session {
	t.Helper()
	c := r.hdr.Get("Set-Cookie")
	if !strings.HasPrefix(c, "__Host-ma_admin=") || !strings.Contains(c, "HttpOnly") || !strings.Contains(c, "Secure") ||
		!strings.Contains(c, "SameSite=Strict") || !strings.Contains(c, "Path=/") {
		t.Fatalf("cookie = %q", c)
	}
	csrf, _ := r.body["csrf"].(string)
	if csrf == "" {
		t.Fatalf("sem csrf: %s", r.raw)
	}
	return &session{cookie: strings.Split(c, ";")[0], csrf: csrf}
}

func TestAdminHostRouting(t *testing.T) {
	e := newAdminEnv(t, "otp")
	get := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "https://"+host+path, nil)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct{ host, path string }{
		{adminHost, "/"}, {"ADMIN.meuanuncio.io:443", "/"}, {adminHost, "/admin/"},
	} {
		rec := get(c.host, c.path)
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "script-src 'self'") ||
			rec.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("%s%s = %d %v", c.host, c.path, rec.Code, rec.Header())
		}
	}
	// Com o host do painel configurado, /admin não existe em nenhum outro host (nem a API, nem os arquivos).
	for _, c := range []struct{ method, host, path string }{
		{"GET", "api.meuanuncio.io", "/admin"}, {"GET", "api.meuanuncio.io", "/admin/"}, {"GET", "localhost:8080", "/admin/"},
		{"GET", "meuanuncio.io", "/admin/static/admin.js"}, {"GET", "api.meuanuncio.io", "/admin/api/session"},
		{"POST", "api.meuanuncio.io", "/admin/api/login/start"}, {"GET", "admin.meuanuncio.io.evil.com", "/admin/"},
	} {
		req := httptest.NewRequest(c.method, "https://"+c.host+c.path, strings.NewReader(`{"email":"dono@meuanuncio.io"}`))
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "admin.js") {
			t.Errorf("%s %s%s fora do host do painel = %d", c.method, c.host, c.path, rec.Code)
		}
	}
	if len(e.otp.sent) != 0 {
		t.Errorf("código enviado por fora do host do painel: %v", e.otp.sent)
	}
	if rec := get(adminHost, "/static/admin.css"); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("css no host do painel = %d", rec.Code)
	}
	if rec := get(adminHost, "/healthz"); rec.Code != 200 || rec.Body.String() != "ok" {
		t.Errorf("healthz no host do painel = %d %q", rec.Code, rec.Body.String())
	}
	// Fora do host do painel, a raiz não é o painel; e o webhook não responde no host do painel.
	if rec := get("api.meuanuncio.io", "/"); strings.Contains(rec.Body.String(), "admin.js") {
		t.Error("painel respondendo na raiz de outro host")
	}
	req := httptest.NewRequest("POST", "https://"+adminHost+"/webhooks/whatsapp", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("webhook no host do painel = %d", rec.Code)
	}
}

// Sem host do painel configurado (desenvolvimento local), o painel continua em /admin.
func TestAdminWithoutHostServesUnderPrefix(t *testing.T) {
	e := newAdminEnv(t, "dev")
	h := httpapi.WithAdminHost("", httpapi.NewMux(e.pool, http.NotFoundHandler(), e.admin.Register))
	for _, path := range []string{"/admin/", "/admin/static/admin.js", "/admin/api/session"} {
		req := httptest.NewRequest("GET", "http://localhost:8080"+path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("local %s = %d", path, rec.Code)
		}
	}
	req := httptest.NewRequest("GET", "http://localhost:8080/admin", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/admin/" {
		t.Errorf("/admin sem barra = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAdminAnonymousIsRefused(t *testing.T) {
	e := newAdminEnv(t, "otp")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/me"}, {"GET", "/api/overview"}, {"GET", "/api/clients"}, {"GET", "/api/billing"}, {"GET", "/api/team"},
		{"GET", "/api/conversations"}, {"POST", "/api/conversations/1/takeover"}, {"POST", "/api/clients/1/reset"}, {"GET", "/api/audit"},
	} {
		if r := e.do(c.method, c.path, nil, map[string]string{}); r.code != http.StatusUnauthorized {
			t.Errorf("%s %s anônimo = %d", c.method, c.path, r.code)
		}
	}
	if r := e.do("GET", "/api/session", nil, nil); r.code != 200 || r.body["mode"] != "otp" {
		t.Errorf("session = %d %s", r.code, r.raw)
	}
	// Cookie adulterado.
	s := e.login("marina@meuanuncio.io")
	forged := &session{cookie: s.cookie[:len(s.cookie)-2] + "xx", csrf: s.csrf}
	if r := e.do("GET", "/api/me", forged, nil); r.code != http.StatusUnauthorized {
		t.Errorf("cookie adulterado = %d", r.code)
	}
}

func TestAdminOTPLogin(t *testing.T) {
	e := newAdminEnv(t, "otp")
	// Quem não é da equipe recebe a mesma resposta, mas nenhum código sai.
	if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": "intruso@mail.com"}); r.code != 200 {
		t.Fatalf("start de fora = %d", r.code)
	}
	if len(e.otp.sent) != 0 {
		t.Fatalf("código enviado para quem não é da equipe: %v", e.otp.sent)
	}
	if r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": "intruso@mail.com", "code": "123456"}); r.code != http.StatusUnauthorized {
		t.Fatalf("verify de fora = %d", r.code)
	}
	if r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": "marina@meuanuncio.io", "code": "000000"}); r.code != http.StatusUnauthorized {
		t.Fatalf("código errado = %d", r.code)
	}
	s := e.login("Marina@MeuAnuncio.io")
	if len(e.otp.sent) != 1 || e.otp.sent[0] != "marina@meuanuncio.io" {
		t.Fatalf("envios = %v", e.otp.sent)
	}
	me := e.do("GET", "/api/me", s, nil)
	if me.code != 200 || me.body["role"] != "staff" || me.body["email"] != "marina@meuanuncio.io" {
		t.Fatalf("me = %d %s", me.code, me.raw)
	}
	// O login de desenvolvimento não existe no modo de produção.
	if r := e.do("POST", "/api/login/dev", nil, map[string]string{"email": "dono@meuanuncio.io"}); r.code != http.StatusNotFound {
		t.Fatalf("login dev em produção = %d", r.code)
	}
	// Limite: o mesmo e-mail não pede código sem parar.
	limited := false
	for i := 0; i < 5; i++ {
		if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": "dono@meuanuncio.io"}); r.code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Fatal("sem limite no pedido de código")
	}
}

func TestAdminDevLogin(t *testing.T) {
	e := newAdminEnv(t, "dev")
	if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": "dono@meuanuncio.io"}); r.code != http.StatusNotFound {
		t.Fatalf("otp no modo dev = %d", r.code)
	}
	if r := e.do("POST", "/api/login/dev", nil, map[string]string{"email": "intruso@mail.com"}); r.code != http.StatusUnauthorized {
		t.Fatalf("dev de fora = %d", r.code)
	}
	r := e.do("POST", "/api/login/dev", nil, map[string]string{"email": "dono@meuanuncio.io"})
	if r.code != 200 || r.body["role"] != "admin" {
		t.Fatalf("dev = %d %s", r.code, r.raw)
	}
}

func TestAdminPermissions(t *testing.T) {
	e := newAdminEnv(t, "otp")
	staff := e.login("marina@meuanuncio.io")
	admin := e.login("dono@meuanuncio.io")
	org := strconv.FormatInt(e.orgID, 10)

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/billing", nil},
		{"POST", "/api/payments", map[string]any{"org_id": e.orgID, "amount_cents": 100, "method": "pix", "status": "paid", "date": "2026-10-01"}},
		{"GET", "/api/team", nil},
		{"POST", "/api/team", map[string]string{"email": "novo@meuanuncio.io", "role": "admin"}},
		{"DELETE", "/api/team/" + strconv.FormatInt(e.adminID, 10), nil},
		{"GET", "/api/clients/" + org + "/reset", nil},
		{"POST", "/api/clients/" + org + "/reset", map[string]string{"confirm": "ZERAR"}},
	} {
		if r := e.do(c.method, c.path, staff, c.body); r.code != http.StatusForbidden {
			t.Errorf("staff %s %s = %d %s", c.method, c.path, r.code, r.raw)
		}
	}
	for _, path := range []string{"/api/overview", "/api/clients", "/api/clients/" + org, "/api/conversations", "/api/whatsapp", "/api/audit"} {
		if r := e.do("GET", path, staff, nil); r.code != 200 {
			t.Errorf("staff GET %s = %d %s", path, r.code, r.raw)
		}
	}
	if r := e.do("GET", "/api/billing?month=2026-10", admin, nil); r.code != 200 || r.body["month"] != "2026-10" {
		t.Errorf("admin faturamento = %d %s", r.code, r.raw)
	}
	if r := e.do("GET", "/api/team", admin, nil); r.code != 200 {
		t.Errorf("admin equipe = %d", r.code)
	}
	// Staff bloqueia e desbloqueia (não é ação restrita).
	if r := e.do("POST", "/api/clients/"+org+"/block", staff, nil); r.code != 200 {
		t.Errorf("staff bloquear = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/clients/"+org+"/unblock", staff, nil); r.code != 200 {
		t.Errorf("staff desbloquear = %d", r.code)
	}
	// Staff vê só as próprias ações no registro; admin vê todas.
	mine := e.do("GET", "/api/audit", staff, nil)
	for _, it := range mine.body["entries"].([]any) {
		if it.(map[string]any)["actor_email"] != "marina@meuanuncio.io" {
			t.Fatalf("staff vendo ação de outro: %v", it)
		}
	}
	all := e.do("GET", "/api/audit", admin, nil)
	if len(all.body["entries"].([]any)) <= len(mine.body["entries"].([]any)) {
		t.Errorf("admin deve ver mais ações: %d x %d", len(all.body["entries"].([]any)), len(mine.body["entries"].([]any)))
	}

	// Equipe: o admin adiciona e remove; não remove a si mesmo.
	r := e.do("POST", "/api/team", admin, map[string]string{"email": "Pedro@MeuAnuncio.io", "name": "Pedro", "role": "staff"})
	if r.code != http.StatusCreated || r.body["email"] != "pedro@meuanuncio.io" {
		t.Fatalf("adicionar = %d %s", r.code, r.raw)
	}
	pedro := strconv.FormatFloat(r.body["id"].(float64), 'f', 0, 64)
	if r := e.do("DELETE", "/api/team/"+strconv.FormatInt(e.adminID, 10), admin, nil); r.code != http.StatusConflict {
		t.Errorf("remover a si mesmo = %d", r.code)
	}
	if r := e.do("DELETE", "/api/team/"+pedro, admin, nil); r.code != 200 {
		t.Errorf("remover = %d %s", r.code, r.raw)
	}
	// Quem sai da equipe perde a sessão na hora.
	if r := e.do("DELETE", "/api/team/"+strconv.FormatInt(e.staffID, 10), admin, nil); r.code != 200 {
		t.Fatalf("remover marina = %d", r.code)
	}
	if r := e.do("GET", "/api/me", staff, nil); r.code != http.StatusUnauthorized {
		t.Errorf("sessão de quem saiu = %d", r.code)
	}
}

func TestAdminCSRF(t *testing.T) {
	e := newAdminEnv(t, "otp")
	s := e.login("marina@meuanuncio.io")
	path := "/api/conversations/" + strconv.FormatInt(e.convID, 10) + "/takeover"
	noToken := e.do("POST", path, s, nil, func(r *http.Request) { r.Header.Del("X-CSRF-Token") })
	wrong := e.do("POST", path, s, nil, func(r *http.Request) { r.Header.Set("X-CSRF-Token", "outro") })
	foreign := e.do("POST", path, s, nil, func(r *http.Request) { r.Header.Set("Origin", "https://malicioso.com") })
	crossSite := e.do("POST", path, s, nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") })
	for name, r := range map[string]resp{"sem token": noToken, "token errado": wrong, "outra origem": foreign, "cross-site": crossSite} {
		if r.code != http.StatusForbidden {
			t.Errorf("%s = %d", name, r.code)
		}
	}
	if c, _ := e.st.GetConversation(context.Background(), e.convID); c.State != store.StateCollecting {
		t.Fatalf("ação passou sem CSRF: %q", c.State)
	}
	if r := e.do("POST", path, s, nil); r.code != 200 {
		t.Fatalf("com token = %d %s", r.code, r.raw)
	}
}

func TestAdminConversationFlow(t *testing.T) {
	e := newAdminEnv(t, "otp")
	s := e.login("marina@meuanuncio.io")
	base := "/api/conversations/" + strconv.FormatInt(e.convID, 10)
	ctx := context.Background()

	if r := e.do("POST", base+"/send", s, map[string]string{"text": "oi"}); r.code != http.StatusConflict || r.body["error"] != "ia_atendendo" {
		t.Fatalf("enviar com a IA atendendo = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", base+"/takeover", s, nil); r.code != 200 {
		t.Fatalf("assumir = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", base+"/send", s, map[string]string{"text": "  "}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("texto vazio = %d", r.code)
	}
	if r := e.do("POST", base+"/send", s, map[string]string{"text": "Oi Ana, aqui é a Marina!"}); r.code != http.StatusAccepted {
		t.Fatalf("enviar = %d %s", r.code, r.raw)
	}
	var payload string
	if err := e.pool.QueryRow(ctx, `SELECT payload::text FROM outbox_jobs WHERE kind = 'whatsapp.send'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"author": "Marina"`) || !strings.Contains(payload, `"chat_id": "5511988887777@c.us"`) {
		t.Fatalf("job = %s", payload)
	}
	th := e.do("GET", base, s, nil)
	thread := th.body["thread"].(map[string]any)
	if th.code != 200 || thread["state"] != "needs_human" || thread["human_by"] != "marina@meuanuncio.io" || len(thread["pending"].([]any)) != 1 {
		t.Fatalf("conversa = %d %s", th.code, th.raw)
	}
	if th.body["window_open"] != true {
		t.Fatalf("janela = %v", th.body["window_open"])
	}
	inbox := e.do("GET", "/api/conversations?filter=humano", s, nil)
	if len(inbox.body["items"].([]any)) != 1 {
		t.Fatalf("caixa humano = %s", inbox.raw)
	}
	if r := e.do("POST", base+"/release", s, nil); r.code != 200 || r.body["status"] != "collecting" {
		t.Fatalf("devolver = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/conversations/99999/takeover", s, nil); r.code != http.StatusNotFound {
		t.Fatalf("conversa inexistente = %d", r.code)
	}

	log, err := e.st.AdminLog(ctx, store.AdminLogFilter{TargetType: "conversation", TargetID: strconv.FormatInt(e.convID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, l := range log {
		actions = append(actions, l.Action)
		if l.ActorEmail != "marina@meuanuncio.io" || l.TargetLabel != "Pizzaria Boa" {
			t.Errorf("registro = %+v", l)
		}
	}
	if strings.Join(actions, ",") != "conversation_release,conversation_send,conversation_takeover" {
		t.Fatalf("ações registradas = %v", actions)
	}
}

func TestAdminClientActions(t *testing.T) {
	e := newAdminEnv(t, "otp")
	admin := e.login("dono@meuanuncio.io")
	ctx := context.Background()
	org := strconv.FormatInt(e.orgID, 10)

	if r := e.do("POST", "/api/clients/"+org+"/signup-link", admin, nil); r.code != http.StatusConflict || r.body["error"] != "cadastro_concluido" {
		t.Fatalf("link para quem já tem cadastro = %d %s", r.code, r.raw)
	}
	if err := e.st.SetRegistrationStatus(ctx, e.orgID, "pending"); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/clients/"+org+"/signup-link", admin, nil); r.code != 200 {
		t.Fatalf("reenviar link = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/payments", admin, map[string]any{"org_id": e.orgID, "amount_cents": 9700, "method": "pix", "status": "paid", "date": "2026-10-05"}); r.code != http.StatusUnprocessableEntity || r.body["error"] != "sem_assinatura" {
		t.Fatalf("pagamento sem assinatura = %d %s", r.code, r.raw)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'starter', 'active')`, e.orgID); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/payments", admin, map[string]any{"org_id": e.orgID, "amount_cents": 9700, "method": "pix", "status": "paid", "date": "2026-10-05"}); r.code != http.StatusCreated {
		t.Fatalf("pagamento = %d %s", r.code, r.raw)
	}
	if r := e.do("GET", "/api/clients/"+org+"/reset", admin, nil); r.code != 200 || r.body["applied"] != false {
		t.Fatalf("prévia de zerar = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/clients/"+org+"/reset", admin, map[string]string{"confirm": "sim"}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("zerar sem confirmação = %d", r.code)
	}
	if r := e.do("POST", "/api/clients/"+org+"/reset", admin, map[string]string{"confirm": "ZERAR"}); r.code != 200 || r.body["applied"] != true {
		t.Fatalf("zerar = %d %s", r.code, r.raw)
	}
	if r := e.do("GET", "/api/clients/"+org, admin, nil); r.code != http.StatusNotFound {
		t.Fatalf("cliente depois de zerar = %d", r.code)
	}
	log, _ := e.st.AdminLog(ctx, store.AdminLogFilter{TargetType: "organization", TargetID: org})
	var actions []string
	for _, l := range log {
		actions = append(actions, l.Action)
	}
	if strings.Join(actions, ",") != "client_reset,payment_record,client_signup_link" {
		t.Fatalf("ações registradas = %v", actions)
	}
}

func (e *adminEnv) auditActions(email string) []string {
	e.t.Helper()
	log, err := e.st.AdminLog(context.Background(), store.AdminLogFilter{ActorEmail: email})
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, l := range log {
		out = append(out, l.Action)
	}
	return out
}

func TestAdminLoginBruteForceLocks(t *testing.T) {
	e := newAdminEnv(t, "otp")
	e.admin.Limiter = nil // só a trava do banco
	const email = "marina@meuanuncio.io"
	if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": email}); r.code != 200 {
		t.Fatalf("start = %d", r.code)
	}
	for i := 0; i < 5; i++ {
		if r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": email, "code": "00000" + strconv.Itoa(i)}); r.code != http.StatusUnauthorized {
			t.Fatalf("tentativa %d = %d %s", i, r.code, r.raw)
		}
	}
	// Com 5 falhas, nem o código certo entra: e-mail bloqueado.
	if r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": email, "code": "123456"}); r.code != http.StatusTooManyRequests {
		t.Fatalf("código certo depois de 5 falhas = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": email}); r.code != http.StatusTooManyRequests {
		t.Fatalf("novo código durante o bloqueio = %d", r.code)
	}
	acts := strings.Join(e.auditActions(email), ",")
	if strings.Count(acts, "login_failed") != 5 || strings.Count(acts, "login_locked") != 1 || strings.Contains(acts, "login,") {
		t.Fatalf("registro = %s", acts)
	}
	// Passado o bloqueio, o código antigo continua sem valer; com um código novo, entra.
	if _, err := e.pool.Exec(context.Background(), `UPDATE admin_login_state SET locked_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": email, "code": "123456"}); r.code != http.StatusUnauthorized {
		t.Fatalf("código antigo depois do bloqueio = %d %s", r.code, r.raw)
	}
	e.login(email)
	// Quem não é da equipe recebe as mesmas respostas e também fica bloqueado.
	if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": "intruso@mail.com"}); r.code != 200 {
		t.Fatalf("start intruso = %d", r.code)
	}
	for i := 0; i < 5; i++ {
		if r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": "intruso@mail.com", "code": "123456"}); r.code != http.StatusUnauthorized {
			t.Fatalf("intruso %d = %d", i, r.code)
		}
	}
	if r := e.do("POST", "/api/login/verify", nil, map[string]string{"email": "intruso@mail.com", "code": "123456"}); r.code != http.StatusTooManyRequests {
		t.Fatalf("intruso bloqueado = %d", r.code)
	}
}

func TestAdminLoginCodeEmailLimit(t *testing.T) {
	e := newAdminEnv(t, "otp")
	e.admin.Limiter = nil // só o teto por hora do banco
	for i := 0; i < 5; i++ {
		if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": "dono@meuanuncio.io"}); r.code != 200 {
			t.Fatalf("pedido %d = %d", i, r.code)
		}
	}
	if r := e.do("POST", "/api/login/start", nil, map[string]string{"email": "dono@meuanuncio.io"}); r.code != http.StatusTooManyRequests {
		t.Fatalf("sexto código na hora = %d", r.code)
	}
	if len(e.otp.sent) != 5 {
		t.Fatalf("e-mails enviados = %d", len(e.otp.sent))
	}
}
