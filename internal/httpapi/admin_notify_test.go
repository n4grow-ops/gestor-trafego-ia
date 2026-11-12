package httpapi_test

import (
	"bytes"
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

	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/notify"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fakeHook é o app de notificação: guarda o que chegou e responde o status pedido.
type fakeHook struct {
	mu     sync.Mutex
	status int
	bodies []string
	srv    *httptest.Server
}

func newFakeHook(t *testing.T) *fakeHook {
	h := &fakeHook{status: 200}
	h.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		defer h.mu.Unlock()
		h.bodies = append(h.bodies, string(b))
		w.WriteHeader(h.status)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func notifyEnv(t *testing.T) (*adminEnv, *fakeHook) {
	e := newAdminEnv(t, "otp")
	hook := newFakeHook(t)
	e.admin.NotifySender = &notify.Sender{AllowLocal: true, Transport: hook.srv.Client().Transport}
	return e, hook
}

func TestNotifyCRUDAndMask(t *testing.T) {
	e, hook := notifyEnv(t)
	admin := e.login("dono@meuanuncio.io")
	staff := e.login("marina@meuanuncio.io")
	secretURL := hook.srv.URL + "/segredo-abc123/notifications/Venda"

	// Equipe (não administrador) não vê nem mexe.
	if r := e.do("GET", "/api/notifications", staff, nil); r.code != http.StatusForbidden {
		t.Fatalf("staff lista = %d", r.code)
	}
	if r := e.do("POST", "/api/notifications", staff, map[string]any{"name": "x", "kind": "pushcut", "url": secretURL, "events": []string{}, "active": true}); r.code != http.StatusForbidden {
		t.Fatalf("staff cria = %d", r.code)
	}
	// Sem sessão.
	if r := e.do("GET", "/api/notifications", nil, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("sem sessão = %d", r.code)
	}

	body := map[string]any{"name": "  iPhone   do Gui ", "kind": "pushcut", "url": secretURL, "events": []string{"venda.nova", "venda.nova", "cliente.novo"},
		"active": true, "title_tpl": "Venda {valor}", "text_tpl": "{nome} comprou"}
	r := e.do("POST", "/api/notifications", admin, body)
	if r.code != http.StatusCreated {
		t.Fatalf("cria = %d %s", r.code, r.raw)
	}
	if strings.Contains(r.raw, "segredo") || r.body["url"] != nil {
		t.Fatalf("resposta vaza a URL: %s", r.raw)
	}
	if r.body["name"] != "iPhone do Gui" || r.body["url_masked"] != "https://"+strings.TrimPrefix(hook.srv.URL, "https://")+"/…enda" {
		t.Fatalf("nome/máscara = %v %v", r.body["name"], r.body["url_masked"])
	}
	if evs, _ := r.body["events"].([]any); len(evs) != 2 {
		t.Fatalf("eventos sem repetição = %v", r.body["events"])
	}
	id := strconv.FormatInt(int64(r.body["id"].(float64)), 10)

	// Listagem: mascarada, com o catálogo de eventos.
	r = e.do("GET", "/api/notifications", admin, nil)
	if r.code != 200 || strings.Contains(r.raw, "segredo") {
		t.Fatalf("lista = %d %s", r.code, r.raw)
	}
	if evs, _ := r.body["events"].([]any); len(evs) < 4 {
		t.Fatalf("catálogo = %v", r.body["events"])
	}

	// Edição sem URL mantém a guardada.
	r = e.do("PUT", "/api/notifications/"+id, admin, map[string]any{"name": "iPhone", "kind": "pushcut", "url": "", "events": []string{"venda.nova"}, "active": true})
	if r.code != 200 || r.body["name"] != "iPhone" {
		t.Fatalf("edita = %d %s", r.code, r.raw)
	}
	n, _ := strconv.ParseInt(id, 10, 64)
	d, err := e.st.NotifyDestination(context.Background(), n)
	if err != nil || d.URL != secretURL {
		t.Fatalf("URL mantida = %v", err)
	}

	// Liga e desliga.
	if r := e.do("POST", "/api/notifications/"+id+"/active", admin, map[string]bool{"active": false}); r.code != 200 || r.body["active"] != false {
		t.Fatalf("desliga = %d %s", r.code, r.raw)
	}

	// Inválidos.
	for name, b := range map[string]map[string]any{
		"sem url":         {"name": "x", "kind": "pushcut", "url": "", "events": []string{}, "active": true},
		"tipo":            {"name": "x", "kind": "sms", "url": secretURL, "events": []string{}, "active": true},
		"evento":          {"name": "x", "kind": "pushcut", "url": secretURL, "events": []string{"teste"}, "active": true},
		"nome":            {"name": "   ", "kind": "pushcut", "url": secretURL, "events": []string{}, "active": true},
		"http":            {"name": "x", "kind": "webhook", "url": "http://example.com/x", "events": []string{}, "active": true},
		"modelo comprido": {"name": "x", "kind": "webhook", "url": secretURL, "events": []string{}, "active": true, "text_tpl": strings.Repeat("a", 1001)},
	} {
		if r := e.do("POST", "/api/notifications", admin, b); r.code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s", name, r.code, r.raw)
		}
	}
	// Campo a mais é recusado.
	if r := e.do("POST", "/api/notifications", admin, map[string]any{"name": "x", "kind": "pushcut", "url": secretURL, "events": []string{}, "active": true, "id": 99}); r.code != http.StatusBadRequest {
		t.Fatalf("campo a mais = %d", r.code)
	}
	// Sem CSRF.
	if r := e.do("DELETE", "/api/notifications/"+id, &session{cookie: admin.cookie, csrf: "x"}, nil); r.code != http.StatusForbidden {
		t.Fatalf("sem csrf = %d", r.code)
	}

	// Registro de ações sem a URL.
	var raw string
	if err := e.pool.QueryRow(context.Background(), `SELECT string_agg(data::text, ' ') FROM admin_audit_log WHERE action LIKE 'notify_%'`).Scan(&raw); err != nil || strings.Contains(raw, "segredo") {
		t.Fatalf("registro = %q %v", raw, err)
	}

	// Apaga.
	if r := e.do("DELETE", "/api/notifications/"+id, admin, nil); r.code != 200 {
		t.Fatalf("apaga = %d %s", r.code, r.raw)
	}
	if r := e.do("DELETE", "/api/notifications/"+id, admin, nil); r.code != http.StatusNotFound {
		t.Fatalf("apaga de novo = %d", r.code)
	}
}

func TestNotifySaveBlocksSSRF(t *testing.T) {
	e := newAdminEnv(t, "otp") // Sender padrão: sem exceção para rede local
	admin := e.login("dono@meuanuncio.io")
	for _, u := range []string{
		"https://127.0.0.1/hook", "https://localhost/hook", "https://10.0.0.1/hook", "https://192.168.0.10/hook",
		"https://169.254.169.254/latest/meta-data", "https://[::1]/hook", "https://[fd00::1]/hook", "https://0.0.0.0/hook",
	} {
		r := e.do("POST", "/api/notifications", admin, map[string]any{"name": "x", "kind": "webhook", "url": u, "events": []string{}, "active": true})
		if r.code != http.StatusUnprocessableEntity || !strings.Contains(r.raw, "bloqueado") {
			t.Errorf("%s = %d %s", u, r.code, r.raw)
		}
	}
	r := e.do("POST", "/api/notifications", admin, map[string]any{"name": "x", "kind": "pushcut", "url": "https://example.com/notifications/X", "events": []string{}, "active": true})
	if r.code != http.StatusUnprocessableEntity || !strings.Contains(r.raw, "api.pushcut.io") {
		t.Fatalf("host do pushcut = %d %s", r.code, r.raw)
	}
}

func TestNotifyTestButtonAndHistory(t *testing.T) {
	e, hook := notifyEnv(t)
	admin := e.login("dono@meuanuncio.io")
	r := e.do("POST", "/api/notifications", admin, map[string]any{"name": "Android", "kind": "macrodroid", "url": hook.srv.URL + "/abc/venda",
		"events": []string{"venda.nova"}, "active": true, "title_tpl": "Teste {nome}", "text_tpl": ""})
	if r.code != http.StatusCreated {
		t.Fatalf("cria = %d %s", r.code, r.raw)
	}
	id := strconv.FormatInt(int64(r.body["id"].(float64)), 10)

	r = e.do("POST", "/api/notifications/"+id+"/test", admin, nil)
	if r.code != 200 || r.body["ok"] != true || r.body["status_code"] != float64(200) {
		t.Fatalf("teste = %d %s", r.code, r.raw)
	}
	if len(hook.bodies) != 1 || !strings.Contains(hook.bodies[0], `"title":"Teste Ana Souza"`) {
		t.Fatalf("chegou = %q", hook.bodies)
	}
	hook.status = 404
	r = e.do("POST", "/api/notifications/"+id+"/test", admin, nil)
	if r.code != 200 || r.body["ok"] != false || r.body["error"] != "HTTP 404" {
		t.Fatalf("teste com erro = %d %s", r.code, r.raw)
	}
	r = e.do("GET", "/api/notifications/"+id+"/deliveries", admin, nil)
	list, _ := r.body["deliveries"].([]any)
	if r.code != 200 || len(list) != 2 {
		t.Fatalf("histórico = %d %s", r.code, r.raw)
	}
	if first := list[0].(map[string]any); first["ok"] != false || first["event"] != "teste" {
		t.Fatalf("mais nova primeiro = %v", first)
	}
	// Status da última entrega na listagem.
	r = e.do("GET", "/api/notifications", admin, nil)
	dests := r.body["destinations"].([]any)
	if last := dests[0].(map[string]any)["last_delivery"].(map[string]any); last["status_code"] != float64(404) {
		t.Fatalf("última = %v", last)
	}
	if r := e.do("GET", "/api/notifications/999/deliveries", admin, nil); r.code != http.StatusNotFound {
		t.Fatalf("inexistente = %d", r.code)
	}
}

func TestSaleHookToken(t *testing.T) {
	e, _ := notifyEnv(t)
	admin := e.login("dono@meuanuncio.io")
	ctx := context.Background()
	if _, err := e.st.CreateNotifyDestination(ctx, store.NotifyDestinationInput{Name: "v", Kind: "webhook", URL: "https://x.example.com/",
		Events: []string{"venda.nova"}, Active: true}); err != nil {
		t.Fatal(err)
	}
	hook := &httpapi.SaleHook{Store: e.st, Limiter: httpapi.NewLimiter(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := httpapi.NewMux(e.pool, http.NotFoundHandler(), hook.Register)
	post := func(token string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "https://cadastro.meuanuncio.io/api/hooks/venda", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	sale := map[string]any{"nome": "Ana", "whatsapp": "+55 (11) 98888-7777", "valor": 97.5, "produto": "Curso", "origem": "hotmart"}

	// Sem token gerado: nada passa.
	if rec := post("mav_qualquer", sale); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sem token gerado = %d", rec.Code)
	}
	// Gerar é só para administrador; o token aparece uma vez e o banco guarda o hash.
	staff := e.login("marina@meuanuncio.io")
	if r := e.do("POST", "/api/notifications/sale-token", staff, nil); r.code != http.StatusForbidden {
		t.Fatalf("staff gera = %d", r.code)
	}
	r := e.do("POST", "/api/notifications/sale-token", admin, nil)
	token, _ := r.body["token"].(string)
	if r.code != http.StatusCreated || !strings.HasPrefix(token, "mav_") || len(token) < 40 {
		t.Fatalf("gera = %d %s", r.code, r.raw)
	}
	var stored string
	if err := e.pool.QueryRow(ctx, `SELECT token_hash FROM notify_sale_token`).Scan(&stored); err != nil || stored == token || strings.Contains(stored, token[4:]) {
		t.Fatalf("guardado em claro: %v", err)
	}
	if l := e.do("GET", "/api/notifications", admin, nil); strings.Contains(l.raw, token) || !strings.Contains(l.raw, token[len(token)-4:]) {
		t.Fatalf("listagem mostra o token de novo ou sem final: %s", l.raw)
	}

	// Inválidos.
	for _, tk := range []string{"", "mav_errado", token + "x", strings.ToUpper(token)} {
		if rec := post(tk, sale); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q = %d", tk, rec.Code)
		}
	}
	// Válido: vira job do evento venda.nova, com o valor formatado.
	rec := post(token, sale)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("válido = %d %s", rec.Code, rec.Body)
	}
	var payload string
	if err := e.pool.QueryRow(ctx, `SELECT payload::text FROM outbox_jobs WHERE kind = 'notify.deliver' ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"valor": "R$ 97,50"`) || !strings.Contains(payload, `"whatsapp": "5511988887777"`) || !strings.Contains(payload, `"event": "venda.nova"`) {
		t.Fatalf("payload = %s", payload)
	}
	// Valor inválido e corpo com campo a mais.
	if rec := post(token, map[string]any{"valor": -1}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("valor negativo = %d", rec.Code)
	}
	if rec := post(token, map[string]any{"nome": "x", "admin": true}); rec.Code != http.StatusBadRequest {
		t.Fatalf("campo a mais = %d", rec.Code)
	}
	// Token novo: o antigo para de valer.
	r = e.do("POST", "/api/notifications/sale-token", admin, nil)
	if rec := post(token, sale); r.code != http.StatusCreated || rec.Code != http.StatusUnauthorized {
		t.Fatalf("antigo depois de trocar = %d", rec.Code)
	}
}

func TestNotifyAdminAssets(t *testing.T) {
	e := newAdminEnv(t, "otp")
	page := e.do("GET", "/", nil, nil)
	if !strings.Contains(page.raw, `src="static/notificacoes.js"`) || !strings.Contains(page.raw, `href="#/notificacoes"`) || !strings.Contains(page.raw, `id="i-bell"`) {
		t.Fatal("página sem a aba Notificações")
	}
	js := e.do("GET", "/static/notificacoes.js", nil, nil)
	if js.code != 200 || !strings.Contains(js.hdr.Get("Content-Type"), "javascript") || !strings.Contains(js.raw, "views.notificacoes") {
		t.Fatalf("script = %d", js.code)
	}
}
