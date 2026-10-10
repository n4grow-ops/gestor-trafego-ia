package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// jobTesterAdded é o job do worker que avisa o cliente que ele já é testador (payload {"request_id": N}).
// Fica como texto aqui para o httpapi não depender do engine só por uma constante.
const jobTesterAdded = "tester.added"

// TesterQueue é o que a fila de testadores usa do store. *store.Store cumpre; os testes trocam por um falso.
type TesterQueue interface {
	ListTesterRequests(ctx context.Context, status string) ([]store.TesterRequestView, error)
	MarkTesterAdded(ctx context.Context, id int64, by string) (store.TesterRequestView, error)
	Enqueue(ctx context.Context, j store.JobSpec) error
}

func (a *Admin) testers() TesterQueue {
	if a.Testers != nil {
		return a.Testers
	}
	return a.Store
}

// Aba Testadores: operação do dia a dia, então administrador e equipe veem e agem.
func (a *Admin) registerTesters(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/testers", a.only(false, a.testerList))
	mux.HandleFunc("POST /admin/api/testers/{id}/added", a.only(false, a.testerAdded))
	mux.HandleFunc("POST /admin/api/testers/{id}/resend", a.only(false, a.testerResend))
}

// testerJSON é a linha da fila como o painel lê.
type testerJSON struct {
	ID          int64      `json:"id"`
	OrgID       int64      `json:"org_id"`
	OrgName     string     `json:"org_name"`
	MemberName  string     `json:"member_name"`
	Phone       string     `json:"phone"`
	ProfileURL  string     `json:"profile_url"`
	RefKind     string     `json:"ref_kind"`
	RefValue    string     `json:"ref_value"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	AddedAt     *time.Time `json:"added_at"`
	AddedBy     string     `json:"added_by"`
	AcceptedAt  *time.Time `json:"accepted_at"`
	RemindedAt  *time.Time `json:"reminded_at"`
}

// profileURL devolve o link do perfil: o que o cliente mandou, ou montado do ID/usuário.
// Só https do Facebook vira link no painel (o cliente pode ter mandado qualquer coisa).
func profileURL(v store.TesterRequestView) string {
	if u, err := url.Parse(v.ProfileURL); err == nil && u.Scheme == "https" && u.User == nil && facebookHost(u.Hostname()) {
		return u.String()
	}
	switch v.RefKind {
	case "id":
		return "https://www.facebook.com/profile.php?id=" + url.QueryEscape(v.RefValue)
	case "username":
		return "https://www.facebook.com/" + url.PathEscape(v.RefValue)
	}
	return ""
}

func facebookHost(h string) bool {
	h = strings.ToLower(h)
	for _, d := range []string{"facebook.com", "fb.com", "fb.me"} {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

func toTesterJSON(v store.TesterRequestView) testerJSON {
	return testerJSON{ID: v.ID, OrgID: v.OrgID, OrgName: v.OrgName, MemberName: v.MemberName, Phone: v.Phone,
		ProfileURL: profileURL(v), RefKind: v.RefKind, RefValue: v.RefValue, Status: v.Status,
		RequestedAt: v.RequestedAt, AddedAt: v.AddedAt, AddedBy: v.AddedBy, AcceptedAt: v.AcceptedAt, RemindedAt: v.RemindedAt}
}

var testerStatuses = map[string]bool{"": true, store.TesterRequested: true, store.TesterAdded: true,
	store.TesterAccepted: true, store.TesterCanceled: true}

func (a *Admin) testerList(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	status := r.URL.Query().Get("status")
	if !testerStatuses[status] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": "status"})
		return
	}
	list, err := a.testers().ListTesterRequests(r.Context(), status)
	if a.storeError(w, err) {
		return
	}
	// Contadores dos abertos, para o selo do menu e os filtros.
	open := list
	if status != "" {
		if open, err = a.testers().ListTesterRequests(r.Context(), ""); a.storeError(w, err) {
			return
		}
	}
	counts := map[string]int{store.TesterRequested: 0, store.TesterAdded: 0}
	for _, t := range open {
		if _, ok := counts[t.Status]; ok {
			counts[t.Status]++
		}
	}
	out := make([]testerJSON, 0, len(list))
	for _, t := range list {
		out = append(out, toTesterJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"testers": out, "counts": counts, "meta_app_id": a.MetaAppID})
}

func testerLabel(v store.TesterRequestView) string {
	if v.OrgName != "" {
		return v.OrgName
	}
	return v.MemberName
}

func (a *Admin) enqueueTesterNotice(ctx context.Context, id int64) error {
	return a.testers().Enqueue(ctx, store.JobSpec{Kind: jobTesterAdded, Payload: map[string]int64{"request_id": id}})
}

// testerAdded: a pessoa da equipe já incluiu o cliente na Meta. Marca e põe na fila o aviso ao cliente.
// Marcar e enfileirar não dividem transação: se a fila falhar, o pedido fica "adicionado" e o painel
// oferece "Reenviar aviso".
func (a *Admin) testerAdded(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	v, err := a.testers().MarkTesterAdded(r.Context(), id, m.Email)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "pedido_fechado"})
		return
	}
	if a.storeError(w, err) {
		return
	}
	notice := "na_fila"
	if err := a.enqueueTesterNotice(r.Context(), id); err != nil {
		a.Log.Error("painel: aviso de testador não entrou na fila", "pedido", id, "erro", err)
		notice = "falhou"
	}
	a.log(r, m, "tester_added", "tester_request", orgTarget(id), testerLabel(v), map[string]any{"org_id": v.OrgID, "notice": notice})
	writeJSON(w, http.StatusOK, map[string]any{"tester": toTesterJSON(v), "notice": notice})
}

// testerResend põe de novo na fila o aviso ao cliente; só para pedido já adicionado.
func (a *Admin) testerResend(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	open, err := a.testers().ListTesterRequests(r.Context(), "")
	if a.storeError(w, err) {
		return
	}
	var v *store.TesterRequestView
	for i := range open {
		if open[i].ID == id {
			v = &open[i]
			break
		}
	}
	if v == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nao_encontrado"})
		return
	}
	if v.Status != store.TesterAdded {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "ainda_nao_adicionado"})
		return
	}
	if err := a.enqueueTesterNotice(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	a.log(r, m, "tester_resend", "tester_request", orgTarget(id), testerLabel(*v), map[string]any{"org_id": v.OrgID})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "na_fila"})
}
