package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fundingFlag resume a última leitura do pagamento ("" quando o worker ainda não leu).
func fundingFlag(raw json.RawMessage, dailyCents int64) string {
	if len(raw) == 0 {
		return ""
	}
	var f meta.Funding
	if err := json.Unmarshal(raw, &f); err != nil {
		return ""
	}
	return engine.FundingFlag(f, dailyCents)
}

// adsManagerURL abre a campanha no Gerenciador de Anúncios.
func adsManagerURL(adAccountID, campaignID string) string {
	if adAccountID == "" || campaignID == "" {
		return ""
	}
	return "https://adsmanager.facebook.com/adsmanager/manage/campaigns?act=" + strings.TrimPrefix(adAccountID, "act_") +
		"&selected_campaign_ids=" + campaignID
}

// campaigns lista as campanhas publicadas na Meta (filtro opcional por status).
func (a *Admin) campaigns(w http.ResponseWriter, r *http.Request, _ store.TeamMember, _ adminSession) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "publishing", "paused", "active", "failed", "rejected":
	default:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": "status"})
		return
	}
	list, err := a.Store.AdminCampaigns(r.Context(), status)
	if a.storeError(w, err) {
		return
	}
	for i := range list {
		c := &list[i]
		c.FundingFlag = fundingFlag(c.Funding, int64(c.DailyBudgetBRL*100))
		c.AdsManager = adsManagerURL(c.AdAccountID, c.CampaignID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": list})
}

// pauseCampaign pede ao worker para pausar a campanha (toda a equipe pode; ativar fica com o cliente).
func (a *Admin) pauseCampaign(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.Enqueue(r.Context(), store.JobSpec{Kind: engine.JobTeamPause, Payload: engine.TeamPauseJob{PublicationID: id, By: m.Email}}); a.storeError(w, err) {
		return
	}
	a.log(r, m, "campaign_pause_requested", "campaign", strconv.FormatInt(id, 10), "", nil)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "status": "na_fila"})
}

// refreshFunding pede ao worker uma nova leitura do pagamento da conta do cliente.
func (a *Admin) refreshFunding(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.Store.Enqueue(r.Context(), store.JobSpec{Kind: engine.JobFundingUpdate, Payload: engine.FundingUpdateJob{OrgID: id}}); a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"org_id": id, "status": "na_fila"})
}
