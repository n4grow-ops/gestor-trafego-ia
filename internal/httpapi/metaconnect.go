package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// MetaAPI é a parte da Graph API que a conexão usa.
type MetaAPI interface {
	LoginURL(state string) string
	ExchangeCode(ctx context.Context, code string) (string, error)
	Assets(ctx context.Context, token string) (meta.Assets, error)
}

// MetaConnect serve o link de conexão da conta de anúncios, o retorno do Facebook e os avisos da Meta
// (desautorização e exclusão de dados).
type MetaConnect struct {
	Store     *store.Store
	Meta      MetaAPI
	Box       *secretbox.Box
	AppSecret string
	BaseURL   string // PUBLIC_BASE_URL, sem barra no fim
	BotNumber string
	Log       *slog.Logger
	Limiter   *Limiter // nil = sem limite (só em testes)
	// Timeout é o prazo para falar com a Meta no retorno do login (padrão 20 s, abaixo do WriteTimeout da api).
	Timeout time.Duration
}

// metaCookie guarda o nonce do state: o retorno do Facebook só vale no navegador que abriu o link.
const metaCookie = "meta_nonce"

var metaPage = template.Must(template.ParseFS(web, "web/meta.html"))

type metaView struct {
	State, Title, Message, Retry, LoginURL, Token, Nonce, WhatsApp string
	AdAccounts                                                     []meta.AdAccount
	Pages                                                          []meta.Page
}

const (
	msgLinkGone   = "Esse link de conexão não vale mais. Me pede outro no WhatsApp mandando \"conectar meta\"."
	msgTryLater   = "Tenta de novo em instantes."
	titleLinkGone = "Link expirado ou já usado"
)

func (m *MetaConnect) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /m/{token}", m.limit(30, m.start))
	mux.HandleFunc("GET /meta/callback", m.limit(30, m.callback))
	mux.HandleFunc("POST /meta/escolha", m.limit(20, m.choose))
	mux.HandleFunc("POST /meta/deauthorize", m.limit(60, m.deauthorize))
	mux.HandleFunc("POST /meta/exclusao-dados", m.limit(60, m.deletion))
	mux.HandleFunc("GET /meta/exclusao/{code}", m.limit(60, m.deletionStatus))
}

func (m *MetaConnect) limit(perMinute int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.Limiter != nil && !m.Limiter.Allow("meta:"+clientIP(r), perMinute) {
			http.Error(w, "muitas tentativas", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

func (m *MetaConnect) start(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("token")
	if _, err := m.Store.MetaLink(r.Context(), signup.HashToken(raw)); err != nil {
		m.fail(w, r, err, http.StatusGone, titleLinkGone, msgLinkGone, "")
		return
	}
	nonce, err := randomHex(16)
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", msgTryLater, "")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: metaCookie, Value: nonce, Path: "/meta", MaxAge: 900, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	m.render(w, http.StatusOK, metaView{State: "start", LoginURL: m.Meta.LoginURL(raw + "." + nonce)})
}

func (m *MetaConnect) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raw, nonce, _ := strings.Cut(q.Get("state"), ".")
	retry := m.BaseURL + "/m/" + raw
	if q.Get("error") != "" {
		m.render(w, http.StatusOK, metaView{State: "error", Title: "Conexão cancelada",
			Message: "Tudo bem, nada foi conectado. Quando quiser, é só tentar de novo.", Retry: retry})
		return
	}
	if !m.nonceOK(r, nonce) {
		m.fail(w, r, errors.New("nonce ausente ou diferente"), http.StatusBadRequest, "Não deu pra confirmar",
			"Parece que o login voltou em outro navegador. Volta no WhatsApp e abre o link de novo neste mesmo navegador.", "")
		return
	}
	link, err := m.Store.MetaLink(r.Context(), signup.HashToken(raw))
	if err != nil {
		m.fail(w, r, err, http.StatusGone, titleLinkGone, msgLinkGone, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), m.timeout())
	defer cancel()
	token, err := m.Meta.ExchangeCode(ctx, q.Get("code"))
	if err != nil {
		m.fail(w, r, err, http.StatusBadGateway, "A Meta não respondeu", "Não consegui confirmar com o Facebook agora. "+msgTryLater, retry)
		return
	}
	assets, err := m.Meta.Assets(ctx, token)
	if err != nil {
		m.fail(w, r, err, http.StatusBadGateway, "A Meta não respondeu", "Não consegui ler suas contas no Facebook agora. "+msgTryLater, retry)
		return
	}
	if len(assets.AdAccounts) == 0 || len(assets.Pages) == 0 {
		// Nada para conectar: o token nem é guardado.
		m.render(w, http.StatusOK, metaView{State: "error", Title: "Faltou escolher no Facebook",
			Message: "No login, marque pelo menos uma conta de anúncios e uma Página para o meuanuncio. Clique em tentar de novo e confira as opções.",
			Retry:   retry})
		return
	}
	sealed, err := m.Box.Seal([]byte(token))
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", msgTryLater, retry)
		return
	}
	cands := store.MetaCandidates{AdAccounts: assets.AdAccounts, Pages: assets.Pages}
	connID, err := m.Store.SavePendingMeta(r.Context(), link, assets.UserID, sealed, cands)
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", msgTryLater, retry)
		return
	}
	if len(assets.AdAccounts) == 1 && len(assets.Pages) == 1 {
		m.complete(w, r, link, connID, assets.AdAccounts[0], assets.Pages[0])
		return
	}
	m.render(w, http.StatusOK, metaView{State: "choose", Token: raw, Nonce: nonce, AdAccounts: assets.AdAccounts, Pages: assets.Pages})
}

func (m *MetaConnect) choose(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulário inválido", http.StatusBadRequest)
		return
	}
	raw := r.PostFormValue("token")
	if !m.nonceOK(r, r.PostFormValue("nonce")) {
		m.fail(w, r, errors.New("nonce ausente ou diferente"), http.StatusBadRequest, "Não deu pra confirmar",
			"Volta no WhatsApp e abre o link de novo neste mesmo navegador.", "")
		return
	}
	link, err := m.Store.MetaLink(r.Context(), signup.HashToken(raw))
	if err != nil {
		m.fail(w, r, err, http.StatusGone, titleLinkGone, msgLinkGone, "")
		return
	}
	connID, cands, err := m.Store.PendingMeta(r.Context(), link.ID)
	if err != nil {
		m.fail(w, r, err, http.StatusGone, titleLinkGone, msgLinkGone, "")
		return
	}
	var acc *meta.AdAccount
	for i := range cands.AdAccounts {
		if cands.AdAccounts[i].ID == r.PostFormValue("ad_account") {
			acc = &cands.AdAccounts[i]
		}
	}
	var page *meta.Page
	for i := range cands.Pages {
		if cands.Pages[i].ID == r.PostFormValue("page") {
			page = &cands.Pages[i]
		}
	}
	if acc == nil || page == nil {
		m.fail(w, r, errors.New("escolha fora da lista"), http.StatusBadRequest, "Escolha inválida",
			"Volta e escolhe uma conta e uma Página da lista.", m.BaseURL+"/m/"+raw)
		return
	}
	m.complete(w, r, link, connID, *acc, *page)
}

func (m *MetaConnect) complete(w http.ResponseWriter, r *http.Request, link store.MetaLink, connID int64, acc meta.AdAccount, page meta.Page) {
	summary := engine.MetaSummary{AccountID: acc.ID, AccountName: acc.Name, Currency: acc.Currency, Timezone: acc.Timezone, AccountStatus: acc.Status,
		DisableReason: acc.DisableReason, PageName: page.Name, Instagram: page.InstagramUsername, PageWhatsApp: page.WhatsApp}
	orgID, err := m.Store.CompleteMeta(r.Context(), store.MetaChoice{ConnectionID: connID, LinkID: link.ID, Account: acc, Page: page,
		Message: func(convID int64, chatID string) store.JobSpec {
			return engine.MetaConnectedJob(convID, chatID, summary)
		}})
	if errors.Is(err, store.ErrMetaLinkInvalid) {
		m.fail(w, r, err, http.StatusGone, titleLinkGone, msgLinkGone, "")
		return
	}
	if err != nil {
		m.fail(w, r, err, http.StatusInternalServerError, "Algo deu errado", msgTryLater, "")
		return
	}
	if err := m.Store.Audit(r.Context(), orgID, "identity:"+strconv.FormatInt(link.IdentityID, 10), "meta_connected",
		map[string]any{"ad_account_id": acc.ID, "page_id": page.ID}); err != nil {
		m.Log.Error("falha ao auditar conexão meta", "erro", err)
	}
	http.SetCookie(w, &http.Cookie{Name: metaCookie, Value: "", Path: "/meta", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	m.render(w, http.StatusOK, metaView{State: "done"})
}

func (m *MetaConnect) deauthorize(w http.ResponseWriter, r *http.Request) {
	userID, ok := m.signedUser(w, r)
	if !ok {
		return
	}
	if _, err := m.Store.RevokeMeta(r.Context(), userID, "", engine.MetaRevokedJob); err != nil {
		m.Log.Error("falha ao revogar conexão meta", "erro", err)
		http.Error(w, "erro", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (m *MetaConnect) deletion(w http.ResponseWriter, r *http.Request) {
	userID, ok := m.signedUser(w, r)
	if !ok {
		return
	}
	code, _, err := signup.NewToken()
	if err == nil {
		_, err = m.Store.RevokeMeta(r.Context(), userID, code, engine.MetaRevokedJob)
	}
	if err != nil {
		m.Log.Error("falha na exclusão de dados meta", "erro", err)
		http.Error(w, "erro", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": m.BaseURL + "/meta/exclusao/" + code, "confirmation_code": code})
}

func (m *MetaConnect) deletionStatus(w http.ResponseWriter, r *http.Request) {
	done, err := m.Store.MetaDeletionDone(r.Context(), r.PathValue("code"))
	if err != nil || !done {
		m.render(w, http.StatusNotFound, metaView{State: "error", Title: "Pedido não encontrado",
			Message: "Não achamos um pedido de exclusão com esse código."})
		return
	}
	m.render(w, http.StatusOK, metaView{State: "error", Title: "Dados excluídos",
		Message: "O acesso do meuanuncio à sua conta foi removido e o token de acesso foi apagado."})
}

func (m *MetaConnect) signedUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulário inválido", http.StatusBadRequest)
		return "", false
	}
	userID, err := meta.ParseSignedRequest(r.PostFormValue("signed_request"), m.AppSecret)
	if err != nil {
		http.Error(w, "assinatura inválida", http.StatusBadRequest)
		return "", false
	}
	return userID, true
}

func (m *MetaConnect) nonceOK(r *http.Request, nonce string) bool {
	c, err := r.Cookie(metaCookie)
	return err == nil && nonce != "" && c.Value == nonce
}

func (m *MetaConnect) fail(w http.ResponseWriter, r *http.Request, err error, status int, title, msg, retry string) {
	m.Log.Warn("conexão meta", "caminho", r.URL.Path, "status", status, "erro", err)
	m.render(w, status, metaView{State: "error", Title: title, Message: msg, Retry: retry})
}

func (m *MetaConnect) render(w http.ResponseWriter, status int, v metaView) {
	v.WhatsApp = "https://wa.me/" + signup.OnlyDigits(m.BotNumber)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'none'; style-src 'self'; img-src 'self' data:; "+
		"frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.WriteHeader(status)
	if err := metaPage.Execute(w, v); err != nil {
		m.Log.Error("falha ao montar página meta", "erro", err)
	}
}

func (m *MetaConnect) timeout() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return 20 * time.Second
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
