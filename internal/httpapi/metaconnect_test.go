package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

const appSecret = "segredo-app"

type fakeMeta struct {
	assets   meta.Assets
	exchange error
	codes    []string
}

func (f *fakeMeta) LoginURL(state string) string {
	return "https://www.facebook.com/v23.0/dialog/oauth?state=" + url.QueryEscape(state)
}

func (f *fakeMeta) ExchangeCode(_ context.Context, code string) (string, error) {
	f.codes = append(f.codes, code)
	if f.exchange != nil {
		return "", f.exchange
	}
	return "TOKEN-" + code, nil
}

func (f *fakeMeta) Assets(context.Context, string) (meta.Assets, error) { return f.assets, nil }

type metaEnv struct {
	mux   http.Handler
	pool  *pgxpool.Pool
	st    *store.Store
	fake  *fakeMeta
	token string
	ident store.Identity
}

var (
	accA  = meta.AdAccount{ID: "act_1", Name: "Loja", Currency: "BRL", Timezone: "America/Sao_Paulo", Status: 1}
	accB  = meta.AdAccount{ID: "act_2", Name: "Loja 2", Currency: "USD", Timezone: "America/Los_Angeles", Status: 1}
	pageA = meta.Page{ID: "P1", Name: "Loja Page", InstagramUsername: "loja", WhatsApp: true}
)

func newMetaEnv(t *testing.T, assets meta.Assets) metaEnv {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	org, err := st.CreateOrganization(ctx, "Loja", "moda", 50, []string{"OUTCOME_ENGAGEMENT"})
	must(err)
	mem, err := st.CreateMember(ctx, org, "Ana", "owner")
	must(err)
	must(st.LinkPhone(ctx, "waha", "5511988887777", mem))
	ident, err := st.FindIdentity(ctx, "waha", "5511988887777")
	must(err)
	_, err = st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(err)
	raw, hash, err := signup.NewToken()
	must(err)
	must(st.CreateMetaLink(ctx, ident.ID, hash, time.Now().Add(time.Hour)))
	k := make([]byte, 32)
	rand.Read(k)
	box, err := secretbox.New(base64.StdEncoding.EncodeToString(k))
	must(err)
	fake := &fakeMeta{assets: assets}
	h := &httpapi.MetaConnect{Store: st, Meta: fake, Box: box, AppSecret: appSecret, BaseURL: "https://cadastro.teste",
		BotNumber: "5511900000000", Log: discard, Limiter: httpapi.NewLimiter()}
	return metaEnv{mux: httpapi.NewMux(pingOK{}, http.NotFoundHandler(), h.Register), pool: pool, st: st, fake: fake, token: raw, ident: ident}
}

// start abre o link e devolve o nonce do cookie.
func (e metaEnv) start(t *testing.T) string {
	t.Helper()
	rec := e.req("GET", "/m/"+e.token, "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Conectar com Facebook") {
		t.Fatalf("página: %d %s", rec.Code, rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self';") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cabeçalhos: %q %q", csp, rec.Header().Get("Cache-Control"))
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "meta_nonce" && c.HttpOnly && c.Secure && c.Path == "/meta" {
			if !strings.Contains(rec.Body.String(), url.QueryEscape(e.token+"."+c.Value)) {
				t.Fatal("state do login sem token.nonce")
			}
			return c.Value
		}
	}
	t.Fatal("sem cookie do nonce")
	return ""
}

func (e metaEnv) req(method, target, body, nonce string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if nonce != "" {
		req.AddCookie(&http.Cookie{Name: "meta_nonce", Value: nonce})
	}
	req.Header.Set("X-Forwarded-For", "200.1.2.3")
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e metaEnv) callback(nonce, code string) *httptest.ResponseRecorder {
	return e.req("GET", "/meta/callback?code="+code+"&state="+url.QueryEscape(e.token+"."+nonce), "", nonce)
}

func (e metaEnv) count(t *testing.T, sql string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMetaSingleAccountConnectsDirectly(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA}, Pages: []meta.Page{pageA}})
	nonce := e.start(t)
	rec := e.callback(nonce, "c1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "conta conectada") || !strings.Contains(rec.Body.String(), "wa.me/5511900000000") {
		t.Fatalf("retorno: %d %s", rec.Code, rec.Body.String())
	}
	c, ok, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID)
	if !ok || c.AdAccountID != "act_1" {
		t.Fatalf("conexão = %+v", c)
	}
	if e.count(t, `SELECT count(*) FROM meta_connections WHERE token_ciphertext IS NOT NULL AND position('TOKEN-c1' in encode(token_ciphertext, 'escape')) = 0`) != 1 {
		t.Fatal("token não está cifrado")
	}
	if e.count(t, `SELECT count(*) FROM outbox_jobs WHERE payload->>'text' LIKE 'Conectado%'`) != 1 {
		t.Fatal("confirmação não foi para a fila")
	}
	if e.count(t, `SELECT count(*) FROM audit_events WHERE action = 'meta_connected' AND data::text NOT LIKE '%TOKEN%'`) != 1 {
		t.Fatal("sem auditoria ou auditoria com token")
	}
	// Link usado: nem a página nem um novo retorno funcionam.
	if rec := e.req("GET", "/m/"+e.token, "", ""); rec.Code != 410 {
		t.Fatalf("link usado: %d", rec.Code)
	}
	if rec := e.callback(nonce, "c2"); rec.Code != 410 || len(e.fake.codes) != 1 {
		t.Fatalf("retorno repetido: %d, trocas %d", rec.Code, len(e.fake.codes))
	}
}

func TestMetaSeveralAccountsAskForChoice(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA, accB}, Pages: []meta.Page{pageA}})
	nonce := e.start(t)
	rec := e.callback(nonce, "c1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Loja 2") || !strings.Contains(rec.Body.String(), `name="nonce" value="`+nonce+`"`) {
		t.Fatalf("escolha: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID); ok {
		t.Fatal("conectou antes da escolha")
	}
	if body := rec.Body.String(); strings.Contains(body, `value="act_1" required checked`) || strings.Contains(body, `value="act_2" required checked`) {
		t.Fatal("com várias contas nenhuma pode vir marcada")
	}
	if !strings.Contains(rec.Body.String(), `value="P1" required checked`) {
		t.Fatal("Página única deve vir marcada")
	}
	bad := url.Values{"token": {e.token}, "nonce": {nonce}, "ad_account": {"act_999"}, "page": {"P1"}}.Encode()
	if rec := e.req("POST", "/meta/escolha", bad, nonce); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Loja 2") ||
		!strings.Contains(rec.Body.String(), "Escolha uma conta de anúncios") {
		t.Fatalf("conta fora da lista volta para a escolha: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.req("POST", "/meta/escolha", url.Values{"token": {e.token}, "nonce": {nonce}, "page": {"P1"}}.Encode(), nonce); rec.Code != 400 {
		t.Fatalf("sem conta escolhida: %d", rec.Code)
	}
	good := url.Values{"token": {e.token}, "nonce": {nonce}, "ad_account": {"act_2"}, "page": {"P1"}}.Encode()
	if rec := e.req("POST", "/meta/escolha", good, ""); rec.Code != 400 {
		t.Fatalf("sem cookie: %d", rec.Code)
	}
	if rec := e.req("POST", "/meta/escolha", good, nonce); rec.Code != 200 || !strings.Contains(rec.Body.String(), "conta conectada") {
		t.Fatalf("escolha válida: %d %s", rec.Code, rec.Body.String())
	}
	c, _, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID)
	if c.AdAccountID != "act_2" || c.Currency != "USD" {
		t.Fatalf("conectada = %+v", c)
	}
}

func TestMetaCallbackRejectsMissingCookieWithoutExchange(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA}, Pages: []meta.Page{pageA}})
	nonce := e.start(t)
	rec := e.req("GET", "/meta/callback?code=c1&state="+url.QueryEscape(e.token+"."+nonce), "", "")
	if rec.Code != 400 || len(e.fake.codes) != 0 || !strings.Contains(rec.Body.String(), "abre o link de novo") {
		t.Fatalf("sem cookie: %d trocas %d %s", rec.Code, len(e.fake.codes), rec.Body.String())
	}
}

func TestMetaCallbackCancelledAndErrors(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1"})
	nonce := e.start(t)
	rec := e.req("GET", "/meta/callback?error=access_denied&state="+url.QueryEscape(e.token+"."+nonce), "", nonce)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cancelada") || !strings.Contains(rec.Body.String(), "/m/"+e.token) {
		t.Fatalf("cancelado: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.callback(nonce, "c1")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pelo menos uma conta de anúncios e uma Página") || !strings.Contains(rec.Body.String(), "/m/"+e.token) {
		t.Fatalf("sem ativos: %d %s", rec.Code, rec.Body.String())
	}
	e.fake.exchange = errors.New("meta: code expirado")
	rec = e.callback(nonce, "c2")
	if rec.Code != 502 || !strings.Contains(rec.Body.String(), "/m/"+e.token) {
		t.Fatalf("meta fora: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.st.MetaLink(context.Background(), signup.HashToken(e.token)); err != nil {
		t.Fatal("link deixou de valer depois de erro")
	}
}

func TestMetaInvalidLinkPage(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{})
	if rec := e.req("GET", "/m/naoexiste1234", "", ""); rec.Code != 410 || !strings.Contains(rec.Body.String(), "Voltar pro WhatsApp") {
		t.Fatalf("link inválido: %d", rec.Code)
	}
}

func signedReq(userID, key string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(`{"algorithm":"HMAC-SHA256","user_id":"` + userID + `"}`))
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(p))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)) + "." + p
}

func TestMetaDeauthorizeAndDeletion(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA}, Pages: []meta.Page{pageA}})
	e.callback(e.start(t), "c1")
	if rec := e.req("POST", "/meta/deauthorize", "signed_request="+signedReq("U1", "chave-errada"), ""); rec.Code != 400 {
		t.Fatalf("assinatura inválida: %d", rec.Code)
	}
	if rec := e.req("POST", "/meta/deauthorize", "signed_request="+signedReq("U1", appSecret), ""); rec.Code != 200 {
		t.Fatalf("deauthorize: %d", rec.Code)
	}
	if _, ok, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID); ok {
		t.Fatal("continua conectada")
	}
	if e.count(t, `SELECT count(*) FROM outbox_jobs WHERE payload->>'text' LIKE '%desconectada%'`) != 1 {
		t.Fatal("sem aviso de desconexão")
	}
	rec := e.req("POST", "/meta/exclusao-dados", "signed_request="+signedReq("U1", appSecret), "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"confirmation_code"`) || !strings.Contains(rec.Body.String(), "https://cadastro.teste/meta/exclusao/") {
		t.Fatalf("exclusão: %d %s", rec.Code, rec.Body.String())
	}
}

type slowMeta struct{ fakeMeta }

func (s *slowMeta) Assets(ctx context.Context, _ string) (meta.Assets, error) {
	<-ctx.Done()
	return meta.Assets{}, ctx.Err()
}

func TestMetaCallbackHasDeadline(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{})
	slow := &slowMeta{}
	h := &httpapi.MetaConnect{Store: e.st, Meta: slow, Box: nil, AppSecret: appSecret, BaseURL: "https://cadastro.teste",
		BotNumber: "5511900000000", Log: discard, Timeout: 50 * time.Millisecond}
	e.mux = httpapi.NewMux(pingOK{}, http.NotFoundHandler(), h.Register)
	nonce := e.start(t)
	begin := time.Now()
	rec := e.callback(nonce, "c1")
	if rec.Code != 502 || time.Since(begin) > 5*time.Second {
		t.Fatalf("sem prazo: %d em %s", rec.Code, time.Since(begin))
	}
}

func TestMetaNoAssetsKeepsNoToken(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1"})
	e.callback(e.start(t), "c1")
	if n := e.count(t, `SELECT count(*) FROM meta_connections WHERE token_ciphertext IS NOT NULL`); n != 0 {
		t.Fatal("login sem ativos guardou token")
	}
}

func TestMetaDeletionAfterDeauthorizeShowsStatus(t *testing.T) {
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: []meta.AdAccount{accA}, Pages: []meta.Page{pageA}})
	e.callback(e.start(t), "c1")
	e.req("POST", "/meta/deauthorize", "signed_request="+signedReq("U1", appSecret), "")
	rec := e.req("POST", "/meta/exclusao-dados", "signed_request="+signedReq("U1", appSecret), "")
	i := strings.Index(rec.Body.String(), "/meta/exclusao/")
	if i < 0 {
		t.Fatalf("sem url: %s", rec.Body.String())
	}
	path := rec.Body.String()[i:]
	path = path[:strings.IndexAny(path, `"`)]
	if st := e.req("GET", path, "", ""); st.Code != 200 || !strings.Contains(st.Body.String(), "Dados excluídos") {
		t.Fatalf("status da exclusão: %d", st.Code)
	}
}

func TestMetaChoiceListHelpsFindAccount(t *testing.T) {
	accs := []meta.AdAccount{
		{ID: "act_30", Name: "Zeta Antiga", Currency: "BRL", Status: 2, BusinessName: "Portfólio Z"},
		{ID: "act_10", Name: "beta loja", Currency: "BRL", Status: 1, BusinessName: "Clínica Sorriso"},
		{ID: "act_20", Name: "Alfa", Currency: "USD", Status: 3},
		{ID: "act_40", Name: "", Currency: "BRL", Status: 1},
	}
	e := newMetaEnv(t, meta.Assets{UserID: "U1", AdAccounts: accs,
		Pages: []meta.Page{pageA, {ID: "P2", Name: "Outra"}, {ID: "P3", Name: "C"}, {ID: "P4", Name: "D"}, {ID: "P5", Name: "E"}}})
	nonce := e.start(t)
	rec := e.callback(nonce, "c1")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("escolha: %d %s", rec.Code, body)
	}
	// Ativas primeiro (ordem alfabética), depois as com problema.
	order := []string{`value="act_10"`, `value="act_40"`, `value="act_20"`, `value="act_30"`}
	last := -1
	for _, v := range order {
		i := strings.Index(body, v)
		if i < 0 || i < last {
			t.Fatalf("ordem errada em %s: %s", v, body)
		}
		last = i
	}
	for _, want := range []string{"Nº 10 · Clínica Sorriso · BRL", "Conta 40", "Pagamento pendente", "Desativada",
		`data-search="beta loja 10 clínica sorriso brl"`, `class="pick-search"`, "/static/meta.js", "@loja", "Sem Instagram ligado"} {
		if !strings.Contains(body, want) {
			t.Fatalf("faltou %q: %s", want, body)
		}
	}
	if strings.Contains(body, " checked") {
		t.Fatal("várias opções: nada vem marcado")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("CSP = %q", csp)
	}
	good := url.Values{"token": {e.token}, "nonce": {nonce}, "ad_account": {"act_10"}, "page": {"P2"}}.Encode()
	if rec := e.req("POST", "/meta/escolha", good, nonce); rec.Code != 200 {
		t.Fatalf("escolha válida: %d", rec.Code)
	}
	if c, _, _ := e.st.ActiveMeta(context.Background(), e.ident.OrgID); c.AdAccountID != "act_10" {
		t.Fatalf("conectada = %+v", c)
	}
}
