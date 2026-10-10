package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Brasília não tem horário de verão desde 2019.
var brt = time.FixedZone("BRT", -3*3600)

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nao_encontrado"})
		return 0, false
	}
	return id, true
}

// Health é o que a visão geral mostra sobre banco, WhatsApp e worker.
type adminHealth struct {
	DBOK     bool                   `json:"db_ok"`
	DBMillis int64                  `json:"db_ms"`
	WhatsApp []store.WhatsAppStatus `json:"whatsapp"`
	Worker   store.WorkerHealth     `json:"worker"`
	Provider string                 `json:"provider"`
}

func (a *Admin) health(ctx context.Context) (adminHealth, error) {
	h := adminHealth{Provider: a.Provider}
	start := time.Now()
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := a.DB.Ping(pctx)
	cancel()
	h.DBMillis = time.Since(start).Milliseconds()
	h.DBOK = err == nil
	if !h.DBOK {
		return h, nil
	}
	w, err := a.Store.WhatsAppStats(ctx)
	if err != nil {
		return h, err
	}
	h.WhatsApp = w.Statuses
	h.Worker, err = a.Store.WorkerHealth(ctx)
	return h, err
}

func (a *Admin) overview(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	ov, err := a.Store.Overview(r.Context(), days)
	if a.storeError(w, err) {
		return
	}
	h, err := a.health(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"overview": ov, "health": h, "stages": store.ClientStages})
}

func (a *Admin) clients(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	q := r.URL.Query()
	stage := -1
	if s := q.Get("stage"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < len(store.ClientStages) {
			stage = n
		}
	}
	res, err := a.Store.Clients(r.Context(), store.ClientFilter{Q: truncate(q.Get("q"), 80), Filter: q.Get("filter"), Stage: stage})
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": res.Clients, "counts": res.Counts, "stages": store.ClientStages})
}

func (a *Admin) client(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := a.Store.ClientDetail(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func orgTarget(id int64) string { return strconv.FormatInt(id, 10) }

func (a *Admin) resendSignup(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if a.SignupBaseURL == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cadastro_desligado"})
		return
	}
	row, err := a.Store.ClientRow(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	if row.IdentityID == 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "sem_telefone"})
		return
	}
	ident, err := a.Store.GetIdentity(r.Context(), row.IdentityID)
	if a.storeError(w, err) {
		return
	}
	chatID := ""
	if a.ChatIDForPhone != nil {
		chatID = a.ChatIDForPhone(ident.Phone)
	}
	if err := engine.ResendSignupLink(r.Context(), a.Store, a.SignupBaseURL, ident, chatID, a.now()); a.storeError(w, err) {
		return
	}
	a.log(r, m, "client_signup_link", "organization", orgTarget(id), row.Name, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "enviado"})
}

func (a *Admin) setBlocked(blocked bool) staffHandler {
	status, action := "active", "client_unblock"
	if blocked {
		status, action = "blocked", "client_block"
	}
	return func(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		row, err := a.Store.ClientRow(r.Context(), id)
		if a.storeError(w, err) {
			return
		}
		if err := a.Store.SetOrgStatus(r.Context(), id, status); a.storeError(w, err) {
			return
		}
		a.log(r, m, action, "organization", orgTarget(id), row.Name, nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": status})
	}
}

func (a *Admin) resetPreview(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rep, err := a.Store.ResetOrganization(r.Context(), id, false)
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (a *Admin) reset(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Confirm string `json:"confirm"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	if b.Confirm != "ZERAR" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "confirmacao"})
		return
	}
	row, err := a.Store.ClientRow(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	rep, err := a.Store.ResetOrganization(r.Context(), id, true)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "client_reset", "organization", orgTarget(id), row.Name, map[string]any{"phones": rep.Phones, "steps": rep.Steps})
	writeJSON(w, http.StatusOK, rep)
}

func (a *Admin) inbox(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	q := r.URL.Query()
	items, err := a.Store.Inbox(r.Context(), store.InboxFilter{Filter: q.Get("filter"), Q: truncate(q.Get("q"), 80)})
	if a.storeError(w, err) {
		return
	}
	counts, err := a.Store.InboxCounts(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts})
}

func (a *Admin) thread(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	t, err := a.Store.Thread(r.Context(), id, after)
	if a.storeError(w, err) {
		return
	}
	// Cloud API: passada a janela de 24 h, texto livre é recusado pelo WhatsApp.
	windowOpen := !a.WindowLimited || (t.LastInboundAt != nil && a.now().Sub(*t.LastInboundAt) < 23*time.Hour)
	writeJSON(w, http.StatusOK, map[string]any{"thread": t, "window_open": windowOpen, "me": displayName(m)})
}

func (a *Admin) convLabel(ctx context.Context, id int64) string {
	t, err := a.Store.Thread(ctx, id, 1<<62)
	if err != nil {
		return ""
	}
	return t.OrgName
}

func (a *Admin) takeover(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	prev, err := a.Store.TakeOverConversation(r.Context(), id, m.Email)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "conversation_takeover", "conversation", orgTarget(id), a.convLabel(r.Context(), id), map[string]string{"from": prev})
	writeJSON(w, http.StatusOK, map[string]string{"status": store.StateNeedsHuman})
}

func (a *Admin) release(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	state, err := a.Store.ReleaseConversation(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "conversation_release", "conversation", orgTarget(id), a.convLabel(r.Context(), id), map[string]string{"to": state})
	writeJSON(w, http.StatusOK, map[string]string{"status": state})
}

const maxStaffMessage = 4000

func (a *Admin) send(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	text := strings.TrimSpace(b.Text)
	if text == "" || utf8.RuneCountInString(text) > maxStaffMessage {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "texto_invalido"})
		return
	}
	author := displayName(m)
	err := a.Store.EnqueueStaffMessage(r.Context(), id, func(chatID string) store.JobSpec {
		return engine.StaffSendJob(id, chatID, text, author)
	})
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "conversation_send", "conversation", orgTarget(id), a.convLabel(r.Context(), id),
		map[string]any{"chars": utf8.RuneCountInString(text)})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "na_fila"})
}

func (a *Admin) billing(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	month := time.Date(a.now().In(brt).Year(), a.now().In(brt).Month(), 1, 0, 0, 0, 0, brt)
	if s := r.URL.Query().Get("month"); s != "" {
		t, err := time.ParseInLocation("2006-01", s, brt)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "mes_invalido"})
			return
		}
		month = t
	}
	rep, err := a.Store.Billing(r.Context(), month, a.USDBRL)
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (a *Admin) recordPayment(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var b struct {
		OrgID       int64  `json:"org_id"`
		AmountCents int64  `json:"amount_cents"`
		Method      string `json:"method"`
		Status      string `json:"status"`
		Date        string `json:"date"`
		Note        string `json:"note"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	day, err := time.ParseInLocation("2006-01-02", b.Date, brt)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": "data"})
		return
	}
	row, err := a.Store.ClientRow(r.Context(), b.OrgID)
	if a.storeError(w, err) {
		return
	}
	// Meio-dia de Brasília: o pagamento cai no dia certo em qualquer fuso.
	id, err := a.Store.RecordManualPayment(r.Context(), store.ManualPayment{OrgID: b.OrgID, AmountCents: b.AmountCents,
		Method: b.Method, Status: b.Status, Date: day.Add(12 * time.Hour), Note: b.Note, RecordedBy: m.Email})
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "payment_record", "organization", orgTarget(b.OrgID), row.Name,
		map[string]any{"payment_id": id, "amount_cents": b.AmountCents, "method": b.Method, "status": b.Status, "date": b.Date})
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (a *Admin) whatsapp(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	st, err := a.Store.WhatsAppStats(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": st, "provider": a.Provider, "bot_number": a.BotNumber, "window_limited": a.WindowLimited})
}

func (a *Admin) team(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	team, err := a.Store.ListTeam(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team})
}

func (a *Admin) addMember(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var b struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	nm, err := a.Store.AddTeamMember(r.Context(), b.Email, b.Name, b.Role, m.Email)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "team_add", "team_member", orgTarget(nm.ID), nm.Email, map[string]string{"role": nm.Role})
	writeJSON(w, http.StatusCreated, nm)
}

func (a *Admin) removeMember(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == m.ID {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "voce_mesmo"})
		return
	}
	target, err := a.Store.TeamMemberByID(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	if err := a.Store.RemoveTeamMember(r.Context(), id); a.storeError(w, err) {
		return
	}
	a.log(r, m, "team_remove", "team_member", orgTarget(id), target.Email, map[string]string{"role": target.Role})
	writeJSON(w, http.StatusOK, map[string]string{"status": "removido"})
}

// audit: administrador vê tudo (ou filtra por pessoa); equipe vê só as próprias ações.
func (a *Admin) audit(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	f := store.AdminLogFilter{Limit: 200}
	if m.IsAdmin() {
		if actor := r.URL.Query().Get("actor"); actor != "" {
			f.ActorEmail, _ = store.NormalizeEmail(actor)
		}
	} else {
		f.ActorEmail = m.Email
	}
	entries, err := a.Store.AdminLog(r.Context(), f)
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "scope": map[bool]string{true: "todos", false: "meus"}[m.IsAdmin()]})
}
