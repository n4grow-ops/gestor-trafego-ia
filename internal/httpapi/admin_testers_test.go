package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fakeTesters guarda a fila em memória (o store real dos testadores é de outra frente).
type fakeTesters struct {
	mu         sync.Mutex
	reqs       []store.TesterRequestView
	jobs       []store.JobSpec
	enqueueErr error
	listLimit  int // >0: a lista corta como o LIMIT do store
}

func (f *fakeTesters) TesterRequestByID(_ context.Context, id int64) (store.TesterRequestView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		if r.ID == id {
			return r, nil
		}
	}
	return store.TesterRequestView{}, store.ErrNotFound
}

func (f *fakeTesters) ListTesterRequests(_ context.Context, status string) ([]store.TesterRequestView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.TesterRequestView
	for _, r := range f.reqs {
		open := r.Status == store.TesterRequested || r.Status == store.TesterAdded
		if (status == "" && open) || r.Status == status {
			out = append(out, r)
		}
	}
	if f.listLimit > 0 && len(out) > f.listLimit {
		out = out[:f.listLimit]
	}
	return out, nil
}

func (f *fakeTesters) MarkTesterAdded(_ context.Context, id int64, by string) (store.TesterRequestView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.reqs {
		if f.reqs[i].ID == id && f.reqs[i].Status == store.TesterRequested {
			now := time.Now()
			f.reqs[i].Status, f.reqs[i].AddedAt, f.reqs[i].AddedBy = store.TesterAdded, &now, by
			return f.reqs[i], nil
		}
	}
	return store.TesterRequestView{}, store.ErrNotFound
}

func (f *fakeTesters) MarkTesterAddedForConversation(_ context.Context, convID int64, by string) (store.TesterRequestView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if convID == 404 {
		return store.TesterRequestView{}, store.ErrNotFound
	}
	now := time.Now()
	v := store.TesterRequestView{TesterRequest: store.TesterRequest{ID: 100 + convID, ConversationID: convID, ProfileURL: store.TesterFromPanel,
		RefKind: "username", RefValue: store.TesterFromPanel, Status: store.TesterAdded, AddedAt: &now, AddedBy: by}, MemberName: "Ana", OrgName: "Pizzaria Boa"}
	f.reqs = append(f.reqs, v)
	return v, nil
}

func (f *fakeTesters) Enqueue(_ context.Context, j store.JobSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enqueueErr != nil {
		return f.enqueueErr
	}
	f.jobs = append(f.jobs, j)
	return nil
}

func (f *fakeTesters) jobIDs(t *testing.T) []int64 {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []int64
	for _, j := range f.jobs {
		if j.Kind != "tester.added" {
			t.Fatalf("job = %q", j.Kind)
		}
		raw, _ := json.Marshal(j.Payload)
		var p struct {
			RequestID int64 `json:"request_id"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.RequestID)
	}
	return ids
}

func newTesterEnv(t *testing.T) (*adminEnv, *fakeTesters) {
	e := newAdminEnv(t, "otp")
	tv := func(id int64, status, kind, val, url string) store.TesterRequestView {
		return store.TesterRequestView{TesterRequest: store.TesterRequest{ID: id, OrgID: e.orgID, RefKind: kind, RefValue: val,
			ProfileURL: url, Status: status, RequestedAt: time.Now().Add(-time.Hour)}, MemberName: "Ana", OrgName: "Pizzaria Boa", Phone: "5511988887777"}
	}
	f := &fakeTesters{reqs: []store.TesterRequestView{
		tv(1, store.TesterRequested, "id", "100012345", "https://www.facebook.com/profile.php?id=100012345"),
		tv(2, store.TesterAdded, "username", "ana.pizza", "javascript:alert(1)"),
		tv(3, store.TesterAccepted, "username", "outro", "https://evil.com/x"),
	}}
	e.admin.Testers = f
	e.admin.MetaAppID = "987654"
	return e, f
}

func TestTestersListAndCounts(t *testing.T) {
	e, _ := newTesterEnv(t)
	staff := e.login("marina@meuanuncio.io")

	r := e.do("GET", "/api/testers", staff, nil)
	if r.code != 200 || r.body["meta_app_id"] != "987654" {
		t.Fatalf("lista = %d %s", r.code, r.raw)
	}
	list, _ := r.body["testers"].([]any)
	if len(list) != 2 {
		t.Fatalf("abertos = %v", list)
	}
	counts := r.body["counts"].(map[string]any)
	if counts["requested"] != float64(1) || counts["added"] != float64(1) {
		t.Fatalf("contadores = %v", counts)
	}
	first := list[0].(map[string]any)
	if first["profile_url"] != "https://www.facebook.com/profile.php?id=100012345" || first["org_name"] != "Pizzaria Boa" || first["status"] != "requested" {
		t.Fatalf("linha = %v", first)
	}
	// Link que não é do Facebook não vira link: monta do usuário.
	if second := list[1].(map[string]any); second["profile_url"] != "https://www.facebook.com/ana.pizza" {
		t.Fatalf("perfil montado = %v", second["profile_url"])
	}

	acc := e.do("GET", "/api/testers?status=accepted", staff, nil)
	if l, _ := acc.body["testers"].([]any); acc.code != 200 || len(l) != 1 || l[0].(map[string]any)["profile_url"] != "https://www.facebook.com/outro" {
		t.Fatalf("aceitos = %d %s", acc.code, acc.raw)
	}
	if c := acc.body["counts"].(map[string]any); c["requested"] != float64(1) {
		t.Fatalf("contadores com filtro = %v", c)
	}
	if r := e.do("GET", "/api/testers?status=xpto", staff, nil); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("status inválido = %d", r.code)
	}
	if r := e.do("GET", "/api/testers", nil, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("anônimo = %d", r.code)
	}
	if me := e.do("GET", "/api/me", staff, nil); me.body["meta_app_id"] != "987654" {
		t.Fatalf("me = %s", me.raw)
	}
}

func TestTesterAddedEnqueuesNoticeWithCSRFAndAudit(t *testing.T) {
	e, f := newTesterEnv(t)
	staff := e.login("marina@meuanuncio.io")

	noToken := e.do("POST", "/api/testers/1/added", staff, nil, func(r *http.Request) { r.Header.Del("X-CSRF-Token") })
	foreign := e.do("POST", "/api/testers/1/added", staff, nil, func(r *http.Request) { r.Header.Set("Origin", "https://malicioso.com") })
	if noToken.code != http.StatusForbidden || foreign.code != http.StatusForbidden || len(f.jobIDs(t)) != 0 {
		t.Fatalf("csrf = %d / %d, jobs %v", noToken.code, foreign.code, f.jobs)
	}

	r := e.do("POST", "/api/testers/1/added", staff, nil)
	if r.code != 200 || r.body["notice"] != "na_fila" {
		t.Fatalf("adicionei = %d %s", r.code, r.raw)
	}
	if tt := r.body["tester"].(map[string]any); tt["status"] != "added" || tt["added_by"] != "marina@meuanuncio.io" {
		t.Fatalf("pedido = %v", tt)
	}
	if ids := f.jobIDs(t); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("jobs = %v", ids)
	}
	// De novo: já não está "pedido".
	if r := e.do("POST", "/api/testers/1/added", staff, nil); r.code != http.StatusConflict || r.body["error"] != "pedido_fechado" {
		t.Fatalf("repetido = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/testers/abc/added", staff, nil); r.code != http.StatusNotFound {
		t.Fatalf("id ruim = %d", r.code)
	}
	if got := e.auditActions("marina@meuanuncio.io"); !contains(got, "tester_added") {
		t.Fatalf("registro = %v", got)
	}
}

func TestTesterAddedQueueFailureKeepsMark(t *testing.T) {
	e, f := newTesterEnv(t)
	adm := e.login("dono@meuanuncio.io")
	f.enqueueErr = errors.New("fila fora")
	r := e.do("POST", "/api/testers/1/added", adm, nil)
	if r.code != 200 || r.body["notice"] != "falhou" {
		t.Fatalf("fila fora = %d %s", r.code, r.raw)
	}
	// Reenviar com a fila de volta.
	f.enqueueErr = nil
	if r := e.do("POST", "/api/testers/1/resend", adm, nil); r.code != http.StatusAccepted {
		t.Fatalf("reenviar = %d %s", r.code, r.raw)
	}
	if ids := f.jobIDs(t); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("jobs = %v", ids)
	}
}

func TestTesterResend(t *testing.T) {
	e, f := newTesterEnv(t)
	staff := e.login("marina@meuanuncio.io")
	if r := e.do("POST", "/api/testers/1/resend", staff, nil); r.code != http.StatusConflict || r.body["error"] != "ainda_nao_adicionado" {
		t.Fatalf("pedido ainda não adicionado = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/testers/3/resend", staff, nil); r.code != http.StatusNotFound {
		t.Fatalf("aceito (fechado) = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/testers/2/resend", staff, nil, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }); r.code != http.StatusForbidden {
		t.Fatalf("sem csrf = %d", r.code)
	}
	if r := e.do("POST", "/api/testers/2/resend", staff, nil); r.code != http.StatusAccepted {
		t.Fatalf("reenviar = %d %s", r.code, r.raw)
	}
	if ids := f.jobIDs(t); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("jobs = %v", ids)
	}
	if got := e.auditActions("marina@meuanuncio.io"); !contains(got, "tester_resend") {
		t.Fatalf("registro = %v", got)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// M4: reenviar no máximo uma vez a cada 2 min por pedido, e achar o pedido mesmo fora da lista.
func TestTesterResendRateLimitAndLookup(t *testing.T) {
	e, f := newTesterEnv(t)
	now := time.Now()
	e.admin.Now = func() time.Time { return now }
	staff := e.login("marina@meuanuncio.io")
	if r := e.do("POST", "/api/testers/2/resend", staff, nil); r.code != http.StatusAccepted {
		t.Fatalf("reenviar = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/testers/2/resend", staff, nil); r.code != http.StatusTooManyRequests {
		t.Fatalf("reenviar de novo em seguida = %d %s", r.code, r.raw)
	}
	now = now.Add(2*time.Minute + time.Second)
	if r := e.do("POST", "/api/testers/2/resend", staff, nil); r.code != http.StatusAccepted {
		t.Fatalf("reenviar depois de 2 min = %d %s", r.code, r.raw)
	}
	// Pedido além do LIMIT da lista continua achável.
	f.mu.Lock()
	f.reqs = append(f.reqs, store.TesterRequestView{TesterRequest: store.TesterRequest{ID: 4, OrgID: e.orgID, RefKind: "username",
		RefValue: "longe", Status: store.TesterAdded}, MemberName: "Bia", OrgName: "Doceria", Phone: "5511900001111"})
	f.listLimit = 1
	f.mu.Unlock()
	if r := e.do("POST", "/api/testers/4/resend", staff, nil); r.code != http.StatusAccepted {
		t.Fatalf("pedido fora da lista = %d %s", r.code, r.raw)
	}
	if ids := f.jobIDs(t); len(ids) != 3 || ids[2] != 4 {
		t.Fatalf("jobs = %v", ids)
	}
}

// Botão "Testador adicionado" na conversa: marca, avisa o cliente com o link e não aceita clique repetido.
func TestConversationTesterAdded(t *testing.T) {
	e, f := newTesterEnv(t)
	staff := e.login("marina@meuanuncio.io")

	r := e.do("POST", "/api/conversations/7/tester-added", staff, nil)
	if r.code != 200 || r.body["notice"] != "na_fila" {
		t.Fatalf("botão = %d %s", r.code, r.raw)
	}
	tv := r.body["tester"].(map[string]any)
	if tv["status"] != "added" || tv["added_by"] != "marina@meuanuncio.io" || tv["profile_url"] != "" {
		t.Fatalf("pedido = %v", tv)
	}
	if ids := f.jobIDs(t); len(ids) != 1 || ids[0] != 107 {
		t.Fatalf("avisos = %v", ids)
	}
	if again := e.do("POST", "/api/conversations/7/tester-added", staff, nil); again.code != http.StatusTooManyRequests {
		t.Fatalf("clique repetido = %d %s", again.code, again.raw)
	}
	if nf := e.do("POST", "/api/conversations/404/tester-added", staff, nil); nf.code != http.StatusNotFound {
		t.Fatalf("conversa inexistente = %d %s", nf.code, nf.raw)
	}
}
