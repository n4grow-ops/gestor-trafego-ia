package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// failedJobs lista as tarefas que falharam (padrão: últimos 7 dias, até 30).
func (a *Admin) failedJobs(w http.ResponseWriter, r *http.Request, _ store.TeamMember, _ adminSession) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 30 {
		days = 7
	}
	list, err := a.Store.FailedJobs(r.Context(), a.now().Add(-time.Duration(days)*24*time.Hour))
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": list, "days": days})
}

// retryJob põe a tarefa que falhou de volta na fila (só admin).
func (a *Admin) retryJob(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind, err := a.Store.RetryJob(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "job_retried", "job", strconv.FormatInt(id, 10), kind, nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "pending"})
}

// discardJob encerra a tarefa que falhou sem rodar de novo (só admin).
func (a *Admin) discardJob(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind, err := a.Store.DiscardJob(r.Context(), id, m.Email)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "job_discarded", "job", strconv.FormatInt(id, 10), kind, nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "discarded"})
}
