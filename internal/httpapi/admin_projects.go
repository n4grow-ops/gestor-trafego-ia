package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// project lê o projeto do caminho (de qualquer cliente) e responde 404 se não existe.
func (a *Admin) project(w http.ResponseWriter, r *http.Request) (store.Project, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.Project{}, false
	}
	p, err := a.Store.ProjectByID(r.Context(), id)
	if a.storeError(w, err) {
		return store.Project{}, false
	}
	return p, true
}

func projectTarget(p store.Project) string { return strconv.FormatInt(p.ID, 10) }

// archiveProject arquiva o projeto (a proposta aberta é cancelada; campanhas na Meta não mudam).
func (a *Admin) archiveProject(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	if err := a.Store.ArchiveProject(r.Context(), p.OrgID, p.ID); a.storeError(w, err) {
		return
	}
	a.log(r, m, "project_archive", "project", projectTarget(p), p.Name, map[string]any{"org_id": p.OrgID})
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID, "stage": store.StageArchived})
}

// renameProject troca o nome do projeto.
func (a *Admin) renameProject(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := a.Store.RenameProject(r.Context(), p.OrgID, p.ID, in.Name); a.storeError(w, err) {
		return
	}
	a.log(r, m, "project_rename", "project", projectTarget(p), strings.TrimSpace(in.Name), map[string]any{"org_id": p.OrgID, "from": p.Name})
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID, "name": strings.TrimSpace(in.Name)})
}

// projectFields edita os dados da campanha do projeto (o que vier preenche por cima; vazio não apaga).
func (a *Admin) projectFields(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	var in contract.Fields
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Media = nil // mídia só chega pelo WhatsApp
	if in.DailyBudgetBRL != nil {
		max, err := a.Store.OrgMaxDailyBudget(r.Context(), p.OrgID)
		if a.storeError(w, err) {
			return
		}
		if *in.DailyBudgetBRL <= 0 || *in.DailyBudgetBRL > max {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos",
				"detail": "orçamento por dia entre R$ 1 e o limite da conta (R$ " + strconv.FormatFloat(max, 'f', 2, 64) + ")"})
			return
		}
	}
	merged := p.Fields.Merge(in)
	if err := a.Store.SetProjectFields(r.Context(), p.OrgID, p.ID, merged); a.storeError(w, err) {
		return
	}
	changed, _ := json.Marshal(in)
	a.log(r, m, "project_fields", "project", projectTarget(p), p.Name, map[string]any{"org_id": p.OrgID, "changes": json.RawMessage(changed)})
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID, "fields": merged})
}

// proposeProject pede proposta nova ao especialista; o cliente recebe o resumo com o código pelo WhatsApp.
func (a *Admin) proposeProject(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	p, ok := a.project(w, r)
	if !ok {
		return
	}
	if p.Stage == store.StageArchived {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "projeto_arquivado"})
		return
	}
	conv, err := a.Store.OwnerConversation(r.Context(), p.OrgID)
	if a.storeError(w, err) {
		return
	}
	ident, err := a.Store.GetIdentity(r.Context(), conv.IdentityID)
	if a.storeError(w, err) {
		return
	}
	if missing := engine.WithProfileDefaults(ident.Profile, p.Fields).Missing(); len(missing) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_incompletos", "detail": "faltam: " + strings.Join(missing, ", ")})
		return
	}
	if a.WindowLimited {
		at, ok, err := a.Store.LastInboundAt(r.Context(), conv.ID)
		if a.storeError(w, err) {
			return
		}
		if !ok || a.now().Sub(at) > 23*time.Hour {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "janela_fechada"})
			return
		}
	}
	if err := a.Store.Enqueue(r.Context(), store.JobSpec{Kind: engine.JobProjectPropose, Payload: engine.ProjectProposeJob{ProjectID: p.ID, By: m.Email}}); a.storeError(w, err) {
		return
	}
	a.log(r, m, "project_propose", "project", projectTarget(p), p.Name, map[string]any{"org_id": p.OrgID})
	writeJSON(w, http.StatusAccepted, map[string]any{"id": p.ID, "status": "na_fila"})
}
