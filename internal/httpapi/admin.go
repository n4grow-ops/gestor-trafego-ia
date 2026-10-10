package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// AdminAuth pede e confere o código de login por e-mail (Supabase Auth).
type AdminAuth interface {
	SendOTP(ctx context.Context, email string) error
	VerifyOTP(ctx context.Context, email, code string) (signup.User, error)
}

// Admin é o painel da equipe: página, login e API, tudo debaixo de /admin. No host do painel
// (AdminHost) as mesmas rotas respondem sem o prefixo.
type Admin struct {
	Store *store.Store
	DB    Pinger
	Auth  AdminAuth // obrigatório no modo "otp"
	// Mode: "otp" (código por e-mail) ou "dev" (só e-mail, desenvolvimento local sem Supabase).
	Mode       string
	SessionKey []byte
	Secure     bool // cookie Secure e __Host-; false só no desenvolvimento local em http

	SignupBaseURL  string              // …/c, para reenviar o link de cadastro; vazio = cadastro desligado
	ChatIDForPhone func(string) string // destino de quem ainda não escreveu
	BotNumber      string
	Provider       string // provedor do WhatsApp (estado da conexão)
	WindowLimited  bool   // Cloud API: texto livre só até 24 h depois da última mensagem do cliente
	USDBRL         float64

	// Mia: configuração em vigor (cache) e o que a aba Mia usa para testar e ouvir amostra.
	Mia      *mia.Cache
	MiaTest  MiaTester   // nil = "testar a Mia" desligado (sem OPENROUTER_API_KEY/ATTENDANT_MODEL na api)
	MiaVoice *MiaSpeaker // nil = "ouvir amostra" desligado (sem TTS_MODEL na api)

	Limiter *Limiter
	Log     *slog.Logger
	Now     func() time.Time
}

func (a *Admin) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Admin) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /admin/{$}", a.page)
	mux.HandleFunc("GET /admin/static/admin.css", a.asset("web/admin/admin.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /admin/static/admin.js", a.asset("web/admin/admin.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /admin/static/mia.js", a.asset("web/admin/mia.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /admin/static/marca.png", a.asset("web/admin/marca.png", "image/png"))
	mux.HandleFunc("GET /admin/static/icone.png", a.asset("web/admin/icone.png", "image/png"))
	mux.HandleFunc("GET /admin/static/figtree.woff2", a.asset("web/figtree.woff2", "font/woff2"))

	mux.HandleFunc("GET /admin/api/session", a.session)
	mux.HandleFunc("POST /admin/api/login/start", a.loginStart)
	mux.HandleFunc("POST /admin/api/login/verify", a.loginVerify)
	mux.HandleFunc("POST /admin/api/login/dev", a.loginDev)
	mux.HandleFunc("POST /admin/api/logout", a.only(false, a.logout))
	mux.HandleFunc("GET /admin/api/me", a.only(false, a.me))

	mux.HandleFunc("GET /admin/api/overview", a.only(false, a.overview))
	mux.HandleFunc("GET /admin/api/clients", a.only(false, a.clients))
	mux.HandleFunc("GET /admin/api/clients/{id}", a.only(false, a.client))
	mux.HandleFunc("POST /admin/api/clients/{id}/signup-link", a.only(false, a.resendSignup))
	mux.HandleFunc("POST /admin/api/clients/{id}/block", a.only(false, a.setBlocked(true)))
	mux.HandleFunc("POST /admin/api/clients/{id}/unblock", a.only(false, a.setBlocked(false)))
	mux.HandleFunc("GET /admin/api/clients/{id}/reset", a.only(true, a.resetPreview))
	mux.HandleFunc("POST /admin/api/clients/{id}/reset", a.only(true, a.reset))

	mux.HandleFunc("GET /admin/api/conversations", a.only(false, a.inbox))
	mux.HandleFunc("GET /admin/api/conversations/{id}", a.only(false, a.thread))
	mux.HandleFunc("POST /admin/api/conversations/{id}/takeover", a.only(false, a.takeover))
	mux.HandleFunc("POST /admin/api/conversations/{id}/send", a.only(false, a.send))
	mux.HandleFunc("POST /admin/api/conversations/{id}/release", a.only(false, a.release))

	mux.HandleFunc("GET /admin/api/billing", a.only(true, a.billing))
	mux.HandleFunc("POST /admin/api/payments", a.only(true, a.recordPayment))
	mux.HandleFunc("GET /admin/api/whatsapp", a.only(false, a.whatsapp))
	mux.HandleFunc("GET /admin/api/team", a.only(true, a.team))
	mux.HandleFunc("POST /admin/api/team", a.only(true, a.addMember))
	mux.HandleFunc("DELETE /admin/api/team/{id}", a.only(true, a.removeMember))
	mux.HandleFunc("GET /admin/api/audit", a.only(false, a.audit))
	a.registerMia(mux)
}

// adminCSP: nada de script ou estilo inline, nada de terceiros; a API é a mesma origem.
const adminCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; media-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'"

func adminHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", adminCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

func (a *Admin) page(w http.ResponseWriter, r *http.Request) {
	adminHeaders(w)
	w.Header().Set("Cache-Control", "no-cache")
	b, err := web.ReadFile("web/admin/index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func (a *Admin) asset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := web.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		adminHeaders(w)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", contentType)
		w.Write(b)
	}
}

// WithAdminHost faz o host do painel responder só o painel, sem o prefixo /admin
// (admin.meuanuncio.io/ = /admin/). O health check continua igual em qualquer host.
func WithAdminHost(adminHost string, next http.Handler) http.Handler {
	adminHost = strings.ToLower(strings.TrimSpace(adminHost))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if adminHost != "" && hostOnly(r.Host) == adminHost && r.URL.Path != "/healthz" &&
			r.URL.Path != "/admin" && !strings.HasPrefix(r.URL.Path, "/admin/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/admin" + r.URL.Path
			r2.URL.RawPath = ""
			next.ServeHTTP(w, r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(h, "."))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeJSONLimit(w, r, v, 32<<10)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "corpo_invalido"})
		return false
	}
	return true
}

func (a *Admin) fail(w http.ResponseWriter, err error) {
	a.Log.Error("painel: erro", "erro", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
}

// storeError responde os erros conhecidos do store; devolve true se respondeu.
func (a *Admin) storeError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "nao_encontrado"})
	case errors.Is(err, store.ErrInvalidInput):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": strings.TrimPrefix(err.Error(), store.ErrInvalidInput.Error()+": ")})
	case errors.Is(err, store.ErrNotHuman):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "ia_atendendo"})
	case errors.Is(err, store.ErrLastAdmin):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "ultimo_admin"})
	case errors.Is(err, store.ErrNoSubscription):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "sem_assinatura"})
	case errors.Is(err, store.ErrAlreadyRegistered):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cadastro_concluido"})
	default:
		a.fail(w, err)
	}
	return true
}

// log grava a ação no registro do painel; falha no registro não desfaz a ação, mas fica no log.
func (a *Admin) log(r *http.Request, m store.TeamMember, action, targetType, targetID, label string, data any) {
	err := a.Store.LogAdminAction(r.Context(), store.AdminAction{ActorID: m.ID, ActorEmail: m.Email, Action: action,
		TargetType: targetType, TargetID: targetID, TargetLabel: label, Data: data, IP: clientIP(r)})
	if err != nil {
		a.Log.Error("painel: registro de ação falhou", "acao", action, "erro", err)
	}
}

// displayName é como a pessoa da equipe aparece para o cliente e no painel.
func displayName(m store.TeamMember) string {
	if m.Name != "" {
		return m.Name
	}
	return strings.Split(m.Email, "@")[0]
}

func (a *Admin) me(w http.ResponseWriter, r *http.Request, m store.TeamMember, s adminSession) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id": m.ID, "email": m.Email, "name": displayName(m), "role": m.Role, "csrf": s.CSRF, "mode": a.Mode,
		"bot_number": a.BotNumber, "provider": a.Provider, "window_limited": a.WindowLimited,
		"signup_enabled": a.SignupBaseURL != "",
	})
}
