package httpapi

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

//go:embed web
var web embed.FS

// AuthVerifier confere a sessão do Supabase Auth (e-mail verificado).
type AuthVerifier interface {
	GetUser(ctx context.Context, accessToken string) (signup.User, error)
}

// CNPJLookup busca os dados públicos de um CNPJ.
type CNPJLookup interface {
	LookupCNPJ(ctx context.Context, cnpj string) (signup.Company, error)
}

// Signup serve a página de cadastro, os termos e a API que a página usa.
type Signup struct {
	Store        *store.Store
	Auth         AuthVerifier
	CNPJ         CNPJLookup
	SupabaseURL  string
	SupabaseKey  string // chave publishable: pública por natureza
	BotNumber    string // número do WhatsApp para o botão de voltar
	TermsVersion string
	Log          *slog.Logger
	Limiter      *Limiter   // nil = sem limite (só em testes)
	Mia          *mia.Cache // texto das boas-vindas; nil = padrão
}

func (s *Signup) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /cadastro/{token}", s.page("web/cadastro.html"))
	mux.HandleFunc("GET /c/{token}", s.page("web/cadastro.html")) // link curto enviado no WhatsApp
	mux.HandleFunc("GET /termos", s.page("web/termos.html"))
	mux.HandleFunc("GET /privacidade", s.page("web/privacidade.html"))
	mux.HandleFunc("GET /static/cadastro.css", s.asset("web/cadastro.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /static/logo.png", s.asset("web/logo.png", "image/png"))
	mux.HandleFunc("GET /static/icon.png", s.asset("web/icon.png", "image/png"))
	mux.HandleFunc("GET /static/favicon.png", s.asset("web/favicon.png", "image/png"))
	mux.HandleFunc("GET /favicon.ico", s.asset("web/favicon.png", "image/png"))
	mux.HandleFunc("GET /static/logo.svg", s.asset("web/logo.svg", "image/svg+xml"))
	mux.HandleFunc("GET /static/simbolo.svg", s.asset("web/simbolo.svg", "image/svg+xml"))
	mux.HandleFunc("GET /static/favicon.svg", s.asset("web/favicon.svg", "image/svg+xml"))
	mux.HandleFunc("GET /static/apple-touch-icon.png", s.asset("web/apple-touch-icon.png", "image/png"))
	mux.HandleFunc("GET /static/urbanist.woff2", s.asset("web/urbanist.woff2", "font/woff2"))
	mux.HandleFunc("GET /static/inicio.css", s.asset("web/inicio.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /static/inicio.js", s.asset("web/inicio.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /static/figtree.woff2", s.asset("web/figtree.woff2", "font/woff2"))
	mux.HandleFunc("GET /static/cadastro.js", s.asset("web/cadastro.js", "text/javascript; charset=utf-8"))
	// supabase-js 2.117.1 (UMD) servido daqui: sem CDN de terceiros rodando na página do cadastro.
	mux.HandleFunc("GET /static/supabase.js", s.asset("web/supabase.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /api/cadastro/{token}", s.limit("ip", 60, s.config))
	mux.HandleFunc("GET /api/cadastro/{token}/cnpj/{cnpj}", s.limit("ip", 30, s.limit("token", 10, s.cnpj)))
	mux.HandleFunc("POST /api/cadastro/{token}", s.limit("ip", 20, s.submit))
}

func (s *Signup) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// O token está na URL: nada de Referer para terceiros nem cache.
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
			"connect-src 'self' "+s.SupabaseURL+"; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		s.serve(w, name, "text/html; charset=utf-8")
	}
}

// home é a página inicial do meuanuncio.io, com o botão para começar no WhatsApp.
func (s *Signup) home(w http.ResponseWriter, r *http.Request) {
	b, err := web.ReadFile("web/inicio.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	link := s.whatsappLink() + "?text=" + url.QueryEscape("Oi! Quero anunciar meu negócio.")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
		"font-src 'self'; connect-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	b = bytes.ReplaceAll(b, []byte("{{WHATSAPP}}"), []byte(html.EscapeString(link)))
	b = bytes.ReplaceAll(b, []byte("{{WHATSAPP_NUMERO}}"), []byte(html.EscapeString(phoneDisplay(s.BotNumber))))
	w.Write(b)
}

func (s *Signup) asset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=300")
		s.serve(w, name, contentType)
	}
}

func (s *Signup) serve(w http.ResponseWriter, name, contentType string) {
	b, err := web.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Write(b)
}

// phoneDisplay escreve o número do bot do jeito que se lê no Brasil: +55 49 99191-1891.
// Fora do formato brasileiro, devolve só os dígitos com o +.
func phoneDisplay(raw string) string {
	d := signup.OnlyDigits(raw)
	if strings.HasPrefix(d, "55") && (len(d) == 12 || len(d) == 13) {
		local := d[4:]
		cut := len(local) - 4
		return "+55 " + d[2:4] + " " + local[:cut] + "-" + local[cut:]
	}
	if d == "" {
		return ""
	}
	return "+" + d
}

func (s *Signup) whatsappLink() string { return "https://wa.me/" + signup.OnlyDigits(s.BotNumber) }

func (s *Signup) config(w http.ResponseWriter, r *http.Request) {
	prefill, err := s.Store.SignupPrefill(r.Context(), signup.HashToken(r.PathValue("token")))
	if s.linkError(w, err) {
		return
	}
	plans, err := s.Store.Plans(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"prefill": prefill,
		"plans":   plans,
		"options": map[string]any{
			"segments": signup.Segments, "objectives": signup.Objectives, "monthly_budgets": signup.MonthlyBudgets,
			"ads_experiences": signup.AdsExperiences, "meta_ad_accounts": signup.MetaAdAccounts,
			"approvers": signup.Approvers, "ufs": signup.UFs,
		},
		"supabase":      map[string]string{"url": s.SupabaseURL, "key": s.SupabaseKey},
		"terms_version": s.TermsVersion,
		"whatsapp":      s.whatsappLink(),
	})
}

func (s *Signup) cnpj(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Store.SignupPrefill(r.Context(), signup.HashToken(r.PathValue("token"))); s.linkError(w, err) {
		return
	}
	c, err := s.CNPJ.LookupCNPJ(r.Context(), r.PathValue("cnpj"))
	switch {
	case errors.Is(err, signup.ErrInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "cnpj_invalido"})
	case errors.Is(err, signup.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "cnpj_nao_encontrado"})
	case err != nil:
		s.Log.Warn("consulta de CNPJ falhou", "erro", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "consulta_indisponivel"})
	default:
		writeJSON(w, http.StatusOK, c)
	}
}

type submitBody struct {
	signup.Form
	Plan string `json:"plan"`
}

func (s *Signup) submit(w http.ResponseWriter, r *http.Request) {
	// Link primeiro (consulta barata no banco); só então pergunta ao Supabase quem é o usuário.
	if _, err := s.Store.SignupPrefill(r.Context(), signup.HashToken(r.PathValue("token"))); s.linkError(w, err) {
		return
	}
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	user, err := s.Auth.GetUser(r.Context(), strings.TrimSpace(bearer))
	if errors.Is(err, signup.ErrUnauthorized) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sessao_invalida"})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	var body submitBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "corpo_invalido"})
		return
	}
	form, err := body.Form.Normalize()
	var ve *signup.ValidationError
	if errors.As(err, &ve) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "campos_invalidos", "fields": ve.Fields})
		return
	}
	if body.Plan == "" {
		body.Plan = "free"
	}
	_, err = s.Store.CompleteRegistration(r.Context(), store.Registration{
		TokenHash: signup.HashToken(r.PathValue("token")), AuthUserID: user.ID, Email: user.Email,
		EmailVerifiedAt: user.VerifiedAt, Form: form, PlanCode: body.Plan, TermsVersion: s.TermsVersion,
		IP: clientIP(r), UserAgent: truncate(r.UserAgent(), 300), Welcome: engine.WelcomeJobFor(s.Mia.Get(r.Context())),
	})
	if errors.Is(err, store.ErrNotOwner) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "somente_responsavel"})
		return
	}
	if errors.Is(err, store.ErrPlanUnavailable) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "plano_indisponivel", "fields": map[string]string{"plan": "plano ainda não disponível"}})
		return
	}
	if s.linkError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "whatsapp": s.whatsappLink()})
}

// linkError responde link inválido (410) ou cadastro já concluído (409); devolve true se respondeu.
func (s *Signup) linkError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrLinkInvalid):
		writeJSON(w, http.StatusGone, map[string]string{"error": "link_invalido"})
	case errors.Is(err, store.ErrAlreadyRegistered):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cadastro_concluido", "whatsapp": s.whatsappLink()})
	default:
		s.fail(w, err)
	}
	return true
}

func (s *Signup) fail(w http.ResponseWriter, err error) {
	s.Log.Error("cadastro falhou", "erro", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// limit aplica o limitador por IP ou por token do link antes do handler.
func (s *Signup) limit(by string, perMinute int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Limiter != nil {
			key := by + ":" + clientIP(r)
			if by == "token" {
				key = by + ":" + signup.HashToken(r.PathValue("token"))
			}
			if !s.Limiter.Allow(key, perMinute) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
				return
			}
		}
		next(w, r)
	}
}

// clientIP usa o último endereço do X-Forwarded-For, que é o que o proxy do Easypanel (Traefik)
// acrescenta; os anteriores vêm do cliente e podem ser inventados.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return truncate(strings.TrimSpace(parts[len(parts)-1]), 64)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
