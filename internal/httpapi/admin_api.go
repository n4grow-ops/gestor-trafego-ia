package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/shield"
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
	if d.Meta != nil {
		d.Meta.FundingFlag = fundingFlag(d.Meta.Funding, 0)
		if st := d.Meta.Shield; st != nil && shield.State(st.State).Bad() {
			// Guia de recuperação do estado da conta (o painel mostra o passo a passo).
			g := shield.GuideFor(shield.ScenarioFor(shield.State(st.State), st.DisableReason))
			d.Meta.ShieldGuide, _ = json.Marshal(g)
		}
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
	if err := engine.ResendSignupLink(r.Context(), a.Store, a.Mia.Get(r.Context()), a.SignupBaseURL, ident, chatID, a.now()); a.storeError(w, err) {
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

// grantTrial libera teste grátis por N dias (0 encerra o teste em andamento). Só admin: é acesso sem pagar.
func (a *Admin) grantTrial(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Days int `json:"days"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	row, err := a.Store.ClientRow(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	end, err := a.Store.GrantTrial(r.Context(), id, b.Days, a.now())
	if a.storeError(w, err) {
		return
	}
	action := "client_trial"
	if b.Days == 0 {
		action = "client_trial_end"
	}
	a.log(r, m, action, "organization", orgTarget(id), row.Name, map[string]any{"days": b.Days, "until": end})
	writeJSON(w, http.StatusOK, map[string]any{"status": "trialing", "until": end})
}

// resetFunc: ?escopo=campanhas apaga só campanhas e conversa (cadastro fica); sem escopo, o cliente inteiro.
func (a *Admin) resetFunc(r *http.Request) (func(context.Context, int64, bool, store.AdminAction) (store.ResetReport, error), string) {
	if r.URL.Query().Get("escopo") == "campanhas" {
		return a.Store.ResetCampaigns, "client_reset_campaigns"
	}
	return a.Store.ResetOrganization, "client_reset"
}

func (a *Admin) resetPreview(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, _ := a.resetFunc(r)
	rep, err := run(r.Context(), id, false, store.AdminAction{})
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
	// O registro é gravado na mesma transação: se não gravar, não zera.
	run, action := a.resetFunc(r)
	rep, err := run(r.Context(), id, true, store.AdminAction{ActorID: m.ID, ActorEmail: m.Email,
		Action: action, TargetType: "organization", TargetID: orgTarget(id), TargetLabel: row.Name, IP: clientIP(r)})
	if a.storeError(w, err) {
		return
	}
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

// messageMedia mostra a foto ou o vídeo que o cliente mandou, baixado do provedor do WhatsApp na hora.
func (a *Admin) messageMedia(w http.ResponseWriter, r *http.Request, _ store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind, ref, err := a.Store.MessageMedia(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	// Cópia guardada no Storage primeiro (não expira); sem ela, busca no provedor do WhatsApp.
	if links, err := a.Store.MediaLinksFor(r.Context(), "message", id); err == nil && len(links) > 0 {
		if data, mime, err := a.fetchLink(r.Context(), links[0]); err == nil {
			serveMedia(w, r, kind, data, mime)
			return
		} else {
			a.Log.Warn("cópia guardada da mídia indisponível", "message_id", id, "erro", err)
		}
	}
	if a.Media == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "midia_desligada"})
		return
	}
	data, mime, err := a.Media(r.Context(), ref)
	if err != nil {
		a.Log.Warn("mídia da conversa indisponível", "message_id", id, "erro", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "midia_indisponivel"})
		return
	}
	serveMedia(w, r, kind, data, mime)
}

// storedMedia serve um arquivo guardado pelo worker (anúncio ou prévia) pelo link dele.
func (a *Admin) storedMedia(w http.ResponseWriter, r *http.Request, _ store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	l, err := a.Store.GetMediaLink(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	data, mime, err := a.fetchLink(r.Context(), l)
	if err != nil {
		a.Log.Warn("arquivo guardado indisponível", "media_link_id", id, "erro", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "midia_indisponivel"})
		return
	}
	kind := "image"
	if strings.HasPrefix(l.Mime, "video/") {
		kind = "video"
	}
	serveMedia(w, r, kind, data, mime)
}

// maxStoredMedia limita o que a api traz do Storage (vídeo de anúncio fica bem abaixo).
const maxStoredMedia = 100 << 20

// fetchLink busca o arquivo pelo link assinado (só https; o link foi gravado pelo worker).
func (a *Admin) fetchLink(ctx context.Context, l store.MediaLink) ([]byte, string, error) {
	if !strings.HasPrefix(l.URL, "https://") && !a.AllowHTTPMedia {
		return nil, "", errors.New("link de mídia sem https")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL, nil)
	if err != nil {
		return nil, "", err
	}
	hc := a.MediaHTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("storage: status %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxStoredMedia))
	if err != nil {
		return nil, "", err
	}
	mime := l.Mime
	if mime == "" {
		mime = res.Header.Get("Content-Type")
	}
	return data, mime, nil
}

// serveMedia entrega imagem ou vídeo; qualquer outra coisa desce como arquivo, sem o navegador interpretar.
func serveMedia(w http.ResponseWriter, r *http.Request, kind string, data []byte, mime string) {
	mime = strings.TrimSpace(strings.Split(mime, ";")[0])
	if !strings.HasPrefix(mime, kind+"/") {
		mime = http.DetectContentType(data)
		if !strings.HasPrefix(mime, kind+"/") {
			mime = "application/octet-stream"
			w.Header().Set("Content-Disposition", "attachment")
		}
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data)) // Range: o vídeo avança no player
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
	err := a.Store.EnqueueStaffMessage(r.Context(), id, m.Email, func(chatID string) store.JobSpec {
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

// team: administrador vê a equipe toda; equipe vê só a própria linha (para cuidar do próprio
// WhatsApp e dos avisos).
func (a *Admin) team(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	if !m.IsAdmin() {
		writeJSON(w, http.StatusOK, map[string]any{"team": []store.TeamMember{m}, "scope": "meus"})
		return
	}
	team, err := a.Store.ListTeam(r.Context())
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team, "scope": "todos"})
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
