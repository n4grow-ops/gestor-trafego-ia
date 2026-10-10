package httpapi

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const (
	adminSessionTTL  = 12 * time.Hour  // teto desde o login, mesmo com uso contínuo
	adminSessionIdle = 2 * time.Hour   // sem nenhuma requisição por esse tempo, a sessão cai
	adminSessionTick = 1 * time.Minute // de quanto em quanto o cookie é renovado com o último uso
	csrfHeader       = "X-CSRF-Token"
)

// adminSession vai assinada no cookie. O papel, o "ativo" e a versão da sessão são relidos do banco
// a cada requisição: tirar alguém da equipe ou sair (logout) derruba a sessão na hora.
type adminSession struct {
	MemberID int64  `json:"m"`
	Version  int    `json:"v"`
	Expires  int64  `json:"e"`
	Seen     int64  `json:"s"` // último uso: passa de adminSessionIdle, cai
	CSRF     string `json:"c"`
}

func (a *Admin) cookieName() string {
	if a.Secure {
		return "__Host-ma_admin" // __Host-: só HTTPS, sem Domain, Path=/
	}
	return "ma_admin_dev"
}

func (a *Admin) sign(s adminSession) string {
	raw, _ := json.Marshal(s)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, a.SessionKey)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Admin) verify(token string) (adminSession, bool) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok {
		return adminSession{}, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return adminSession{}, false
	}
	mac := hmac.New(sha256.New, a.SessionKey)
	mac.Write([]byte(payload))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return adminSession{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return adminSession{}, false
	}
	var s adminSession
	now := a.now().Unix()
	if json.Unmarshal(raw, &s) != nil || s.MemberID <= 0 || s.Version <= 0 || s.CSRF == "" || now >= s.Expires ||
		now-s.Seen >= int64(adminSessionIdle.Seconds()) {
		return adminSession{}, false
	}
	return s, true
}

func randomToken() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *Admin) startSession(w http.ResponseWriter, m store.TeamMember) adminSession {
	now := a.now()
	s := adminSession{MemberID: m.ID, Version: m.SessionVersion, Expires: now.Add(adminSessionTTL).Unix(), Seen: now.Unix(), CSRF: randomToken()}
	a.setSessionCookie(w, s)
	return s
}

func (a *Admin) setSessionCookie(w http.ResponseWriter, s adminSession) {
	maxAge := s.Expires - a.now().Unix()
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name: a.cookieName(), Value: a.sign(s), Path: "/", MaxAge: int(maxAge),
		HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteStrictMode,
	})
}

func (a *Admin) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteStrictMode})
}

// staffHandler recebe o membro autenticado e a sessão.
type staffHandler func(w http.ResponseWriter, r *http.Request, m store.TeamMember, s adminSession)

// only protege a rota: exige sessão válida de membro ativo, papel admin quando adminOnly e,
// fora de GET, o token CSRF da sessão e a mesma origem.
func (a *Admin) only(adminOnly bool, h staffHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(a.cookieName())
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sessao_invalida"})
			return
		}
		s, ok := a.verify(c.Value)
		if !ok {
			a.clearSession(w)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sessao_invalida"})
			return
		}
		m, err := a.Store.TeamMemberByID(r.Context(), s.MemberID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && (!m.Active || m.SessionVersion != s.Version)) {
			a.clearSession(w)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sessao_invalida"})
			return
		}
		if err != nil {
			a.fail(w, err)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get(csrfHeader)), []byte(s.CSRF)) != 1 {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf"})
				return
			}
		}
		if adminOnly && !m.IsAdmin() {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "somente_admin"})
			return
		}
		// Renova o último uso (expiração por inatividade); o teto de 12 h não muda.
		if now := a.now().Unix(); now-s.Seen >= int64(adminSessionTick.Seconds()) {
			s.Seen = now
			a.setSessionCookie(w, s)
		}
		h(w, r, m, s)
	}
}

// sameOrigin recusa POST vindo de outro site (o SameSite=Strict já barra; isto é a segunda trava).
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// loginLimit aplica o limite por IP e por e-mail no login.
func (a *Admin) loginLimit(r *http.Request, kind, email string, perIP, perEmail int) bool {
	if a.Limiter == nil {
		return true
	}
	ok := a.Limiter.Allow("admin-"+kind+"-ip:"+clientIP(r), perIP)
	if email != "" {
		ok = a.Limiter.Allow("admin-"+kind+"-email:"+email, perEmail) && ok
	}
	return ok
}

type loginBody struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

var otpCode = regexp.MustCompile(`^[0-9]{6,10}$`)

// session diz à tela de login qual modo está ligado.
func (a *Admin) session(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"mode": a.Mode})
}

// loginStart pede o código por e-mail. Responde igual para quem é e quem não é da equipe.
func (a *Admin) loginStart(w http.ResponseWriter, r *http.Request) {
	if a.Mode != "otp" {
		http.NotFound(w, r)
		return
	}
	var b loginBody
	if !decodeJSON(w, r, &b) {
		return
	}
	email, ok := store.NormalizeEmail(b.Email)
	if !a.loginLimit(r, "start", email, 10, 3) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "email_invalido"})
		return
	}
	m, err := a.Store.TeamMemberByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.fail(w, err)
		return
	}
	member := err == nil && m.Active
	// Teto no banco: 5 códigos por e-mail e 20 no total por hora; e-mail bloqueado não pede código.
	if err := a.Store.AdminCodeRequest(r.Context(), email, member); err != nil {
		if errors.Is(err, store.ErrLoginLocked) || errors.Is(err, store.ErrTooManyCodes) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
			return
		}
		a.fail(w, err)
		return
	}
	if member {
		if err := a.Auth.SendOTP(r.Context(), email); err != nil {
			a.Log.Error("painel: envio do código falhou", "erro", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "enviado"})
}

// loginVerify confere o código. A trava fica no banco: com 5 falhas o código deixa de valer e o
// e-mail fica bloqueado por 15 min (e há um teto diário de falhas). Quem não é da equipe recebe as
// mesmas respostas e conta do mesmo jeito.
func (a *Admin) loginVerify(w http.ResponseWriter, r *http.Request) {
	if a.Mode != "otp" {
		http.NotFound(w, r)
		return
	}
	var b loginBody
	if !decodeJSON(w, r, &b) {
		return
	}
	email, ok := store.NormalizeEmail(b.Email)
	if !a.loginLimit(r, "verify", email, 20, 6) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "codigo_invalido"})
		return
	}
	valid, err := a.Store.AdminLoginGate(r.Context(), email)
	if errors.Is(err, store.ErrLoginLocked) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	m, err := a.Store.TeamMemberByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.fail(w, err)
		return
	}
	member := err == nil && m.Active
	code := strings.TrimSpace(b.Code)
	if !valid || !member || !otpCode.MatchString(code) {
		a.loginFailed(w, r, email, m, member)
		return
	}
	user, err := a.Auth.VerifyOTP(r.Context(), email, code)
	if errors.Is(err, signup.ErrUnauthorized) || (err == nil && user.Email != email) {
		a.loginFailed(w, r, email, m, member)
		return
	}
	if err != nil {
		a.Log.Error("painel: conferência do código falhou", "erro", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "login_indisponivel"})
		return
	}
	if err := a.Store.AdminLoginSucceeded(r.Context(), email); err != nil {
		a.fail(w, err)
		return
	}
	a.finishLogin(w, r, m, "otp")
}

// loginFailed conta a falha, grava login_failed (e login_locked quando bloqueou) e responde.
func (a *Admin) loginFailed(w http.ResponseWriter, r *http.Request, email string, m store.TeamMember, member bool) {
	locked, err := a.Store.AdminLoginFailed(r.Context(), email)
	if err != nil {
		a.fail(w, err)
		return
	}
	actor := store.TeamMember{Email: email}
	if member {
		actor = m
	}
	a.log(r, actor, "login_failed", "", "", "", nil)
	if locked {
		a.log(r, actor, "login_locked", "", "", "", map[string]int{"minutos": store.AdminLoginLockMinutes})
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "codigo_invalido"})
}

// loginDev entra só com o e-mail da equipe. Existe apenas no modo de desenvolvimento (sem Supabase).
func (a *Admin) loginDev(w http.ResponseWriter, r *http.Request) {
	if a.Mode != "dev" {
		http.NotFound(w, r)
		return
	}
	var b loginBody
	if !decodeJSON(w, r, &b) {
		return
	}
	email, _ := store.NormalizeEmail(b.Email)
	if !a.loginLimit(r, "dev", email, 20, 10) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	m, err := a.Store.TeamMemberByEmail(r.Context(), email)
	if err != nil || !m.Active {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "fora_da_equipe"})
		return
	}
	a.finishLogin(w, r, m, "dev")
}

func (a *Admin) finishLogin(w http.ResponseWriter, r *http.Request, m store.TeamMember, how string) {
	s := a.startSession(w, m)
	if err := a.Store.TouchTeamLogin(r.Context(), m.ID); err != nil {
		a.Log.Warn("painel: último login não gravado", "erro", err)
	}
	a.log(r, m, "login", "", "", "", map[string]string{"via": how})
	a.me(w, r, m, s)
}

// logout sobe a versão da sessão: o cookie atual e qualquer cópia dele deixam de valer.
func (a *Admin) logout(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	a.clearSession(w)
	if err := a.Store.BumpSessionVersion(r.Context(), m.ID); err != nil {
		a.fail(w, err)
		return
	}
	a.log(r, m, "logout", "", "", "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
