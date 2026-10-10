package httpapi

import (
	"net/http"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Aba Planos: a equipe toda vê; só administrador cria, edita e apaga.
func (a *Admin) registerPlans(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/plans", a.only(false, a.plans))
	mux.HandleFunc("POST /admin/api/plans", a.only(true, a.createPlan))
	mux.HandleFunc("POST /admin/api/plans/{code}", a.only(true, a.updatePlan))
	mux.HandleFunc("DELETE /admin/api/plans/{code}", a.only(true, a.deletePlan))
}

func (a *Admin) plans(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	list, err := a.Store.AdminPlans(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": list, "can_edit": m.IsAdmin(), "default_plan": store.DefaultPlan})
}

func (a *Admin) planAction(r *http.Request, m store.TeamMember, action string) store.AdminAction {
	return store.AdminAction{ActorID: m.ID, ActorEmail: m.Email, Action: action, IP: clientIP(r)}
}

func (a *Admin) createPlan(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var in store.PlanInput
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := a.Store.CreatePlan(r.Context(), in, a.planAction(r, m, "plan_create"))
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *Admin) updatePlan(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var b struct {
		store.PlanInput
		BaseUpdatedAt time.Time `json:"base_updated_at"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	p, err := a.Store.UpdatePlan(r.Context(), r.PathValue("code"), b.PlanInput, b.BaseUpdatedAt, a.planAction(r, m, "plan_update"))
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *Admin) deletePlan(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	err := a.Store.DeletePlan(r.Context(), r.PathValue("code"), a.planAction(r, m, "plan_delete"))
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
