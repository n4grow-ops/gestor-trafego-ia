package httpapi

import (
	"net/http"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Avisos da equipe: o WhatsApp de cada pessoa e se ela recebe os avisos (atendimento humano,
// testador, publicação e alertas). Administrador edita qualquer um; equipe só o próprio.
func (a *Admin) registerTeamAlerts(mux *http.ServeMux) {
	mux.HandleFunc("PUT /admin/api/team/{id}/alerts", a.only(false, a.setTeamAlerts))
	mux.HandleFunc("POST /admin/api/team/{id}/alerts/test", a.only(false, a.testTeamAlert))
}

// teamTarget lê o {id} e confere a permissão: administrador mexe em qualquer um, equipe só em si.
func (a *Admin) teamTarget(w http.ResponseWriter, r *http.Request, m store.TeamMember) (store.TeamMember, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.TeamMember{}, false
	}
	if !m.IsAdmin() && id != m.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "somente_admin"})
		return store.TeamMember{}, false
	}
	target, err := a.Store.TeamMemberByID(r.Context(), id)
	if a.storeError(w, err) {
		return store.TeamMember{}, false
	}
	return target, true
}

type teamAlertsJSON struct {
	WhatsApp       string `json:"whatsapp"`
	ReceivesAlerts bool   `json:"receives_alerts"`
}

func (a *Admin) setTeamAlerts(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	target, ok := a.teamTarget(w, r, m)
	if !ok {
		return
	}
	var b teamAlertsJSON
	if !decodeJSON(w, r, &b) {
		return
	}
	nm, err := a.Store.SetTeamMemberAlerts(r.Context(), target.ID, b.WhatsApp, b.ReceivesAlerts)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "team_alerts", "team_member", orgTarget(nm.ID), nm.Email, map[string]any{
		"antes":  teamAlertsJSON{WhatsApp: target.WhatsApp, ReceivesAlerts: target.ReceivesAlerts},
		"depois": teamAlertsJSON{WhatsApp: nm.WhatsApp, ReceivesAlerts: nm.ReceivesAlerts},
	})
	writeJSON(w, http.StatusOK, nm)
}

// testTeamAlert manda o aviso de teste para o número salvo da pessoa, pela fila do worker (mesmo
// caminho dos avisos de verdade: fora da janela de 24 h vai o modelo aprovado aviso_equipe).
func (a *Admin) testTeamAlert(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	target, ok := a.teamTarget(w, r, m)
	if !ok {
		return
	}
	if target.WhatsApp == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "sem_whatsapp"})
		return
	}
	if a.ChatIDForPhone == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "whatsapp_desligado"})
		return
	}
	if a.Limiter != nil && !a.Limiter.Allow("team-test:"+m.Email, 5) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	job := engine.TeamTestJob(a.ChatIDForPhone(target.WhatsApp), target.WhatsApp)
	if err := a.Store.Enqueue(r.Context(), job); a.storeError(w, err) {
		return
	}
	a.log(r, m, "team_alerts_test", "team_member", orgTarget(target.ID), target.Email, map[string]string{"whatsapp": target.WhatsApp})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "enviado"})
}
