package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/asaas"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/billing/paylink"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Limites do checkout. O cartão é o alvo de quem testa cartão roubado: poucas tentativas por minuto e
// bloqueio do link depois de checkoutMaxFails recusas seguidas.
const (
	checkoutMaxFails = 5
	checkoutLock     = 30 * time.Minute
	checkoutPageCSP  = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
		"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
)

// CheckoutAsaas é o que o checkout usa do Asaas (asaas.Client; nos testes, um fake).
type CheckoutAsaas interface {
	GetPayment(ctx context.Context, paymentID string) (asaas.Payment, error)
	PixQRCode(ctx context.Context, paymentID string) (asaas.PixQRCode, error)
	SetPaymentBillingType(ctx context.Context, paymentID, billingType string) error
	PayWithCard(ctx context.Context, paymentID string, card asaas.CreditCard, holder asaas.CardHolder, remoteIP string) (asaas.CardResult, error)
	SetSubscriptionCard(ctx context.Context, subID, cardToken, remoteIP string) error
}

// Checkout é a página de pagamento própria (/pagar/{token}): Pix (QR e copia-e-cola da cobrança) ou cartão.
// A liberação do sistema continua sendo pelo webhook do Asaas. Dados do cartão só passam em memória.
type Checkout struct {
	Store   *store.Store
	Asaas   CheckoutAsaas
	Origin  string // PUBLIC_BASE_URL (https://cadastro.meuanuncio.io): o POST do cartão só vale vindo dela
	Sandbox bool   // aceita os cartões de teste do sandbox (o 4444 4444 4444 4444 não passa no Luhn)
	Limiter *Limiter
	Log     *slog.Logger
	Now     func() time.Time // nil = time.Now

	once    sync.Once
	csrfKey []byte
	tmpl    *template.Template
}

func (c *Checkout) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /pagar/{token}", c.limit("page", "ip", 60, c.page))
	mux.HandleFunc("GET /static/pagar.css", c.asset("web/pagar.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /static/pagar.js", c.asset("web/pagar.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /api/pagar/{token}/status", c.limit("status", "ip", 120, c.limit("status", "token", 40, c.status)))
	mux.HandleFunc("GET /api/pagar/{token}/pix", c.limit("pix", "ip", 20, c.limit("pix", "token", 8, c.pix)))
	mux.HandleFunc("POST /api/pagar/{token}/cartao", c.limit("card", "ip", 8, c.limit("card", "token", 5, c.card)))
}

func (c *Checkout) init() {
	c.once.Do(func() {
		// Chave do CSRF por processo: reiniciar a api só pede para recarregar a página aberta.
		c.csrfKey = make([]byte, 32)
		if _, err := rand.Read(c.csrfKey); err != nil {
			panic(err)
		}
		c.tmpl = template.Must(template.ParseFS(web, "web/pagar.html"))
	})
}

func (c *Checkout) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// securityHeaders vale para a página e para as respostas da API: sem cache, sem Referer, sem iframe.
func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Pragma", "no-cache")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", checkoutPageCSP)
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
}

func (c *Checkout) asset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := web.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(b)
	}
}

// limit aplica o limitador por IP ou pelo hash do token antes do handler. Uma conta por rota: a consulta
// de status (a cada 5 s) não pode gastar a cota do cartão.
func (c *Checkout) limit(route, by string, perMinute int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.Limiter != nil {
			key := "pagar-" + route + "-ip:" + clientIP(r)
			if by == "token" {
				key = "pagar-" + route + "-token:" + paylink.Hash(r.PathValue("token"))
			}
			if !c.Limiter.Allow(key, perMinute) {
				securityHeaders(w)
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
				return
			}
		}
		next(w, r)
	}
}

// link lê o link do token. ok=false = token inválido, desconhecido ou vencido (a resposta é a mesma nos
// três casos: a página não conta se o link existe).
func (c *Checkout) link(r *http.Request) (store.CheckoutLink, bool, error) {
	raw := r.PathValue("token")
	if !paylink.Valid(raw) {
		return store.CheckoutLink{}, false, nil
	}
	l, err := c.Store.CheckoutByHash(r.Context(), paylink.Hash(raw))
	if errors.Is(err, store.ErrNotFound) {
		return l, false, nil
	}
	if err != nil {
		return l, false, err
	}
	if !l.Paid && !c.now().Before(l.ExpiresAt) {
		return l, false, nil
	}
	return l, true, nil
}

func (c *Checkout) csrf(l store.CheckoutLink) string {
	m := hmac.New(sha256.New, c.csrfKey)
	fmt.Fprintf(m, "pagar:%d", l.ID)
	return hex.EncodeToString(m.Sum(nil))
}

type checkoutView struct {
	State    string // open, paid, invalid
	Method   string // aba inicial: pix ou card
	Plan     string
	Amount   string
	Due      string
	Overdue  bool
	CSRF     string
	Sandbox  bool
	Name     string
	CpfCnpj  string
	Email    string
	Phone    string
	Disabled bool // cartão bloqueado por recusas seguidas
}

func (c *Checkout) page(w http.ResponseWriter, r *http.Request) {
	c.init()
	securityHeaders(w)
	l, ok, err := c.link(r)
	if err != nil {
		c.Log.Error("checkout: falha ao ler o link", "erro", err)
		c.render(w, http.StatusInternalServerError, checkoutView{State: "invalid"})
		return
	}
	if !ok {
		c.render(w, http.StatusNotFound, checkoutView{State: "invalid"})
		return
	}
	v := checkoutView{State: "open", Method: l.Method, Plan: l.PlanName, Amount: brl(l.AmountCents), Due: l.DueDate.Format("02/01/2006"),
		Overdue: l.DueDate.Format("2006-01-02") < c.now().In(brtZone).Format("2006-01-02"), Sandbox: c.Sandbox, CSRF: c.csrf(l), Disabled: l.Locked(c.now())}
	if l.Paid {
		v.State = "paid"
	} else if h, err := c.Store.CheckoutHolder(r.Context(), l.OrgID); err == nil {
		v.Name, v.CpfCnpj, v.Email, v.Phone = h.Name, h.CpfCnpj, h.Email, h.Phone
	} else {
		c.Log.Warn("checkout: titular indisponível para pré-preencher", "org_id", l.OrgID, "erro", err)
	}
	c.render(w, http.StatusOK, v)
}

func (c *Checkout) render(w http.ResponseWriter, status int, v checkoutView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.tmpl.Execute(w, v); err != nil {
		c.Log.Error("checkout: falha ao montar a página", "erro", err)
	}
}

// status é a consulta leve da página (só o banco): o webhook marca a cobrança paga.
func (c *Checkout) status(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	l, ok, err := c.link(r)
	switch {
	case err != nil:
		c.Log.Error("checkout: falha ao ler o link", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
	case !ok:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link_invalido"})
	case l.Paid:
		writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
	}
}

// pix devolve o QR Code e o copia-e-cola da cobrança. Nunca a chave Pix da conta.
func (c *Checkout) pix(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	l, ok, err := c.link(r)
	if err != nil {
		c.Log.Error("checkout: falha ao ler o link", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link_invalido"})
		return
	}
	if l.Paid {
		writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
		return
	}
	ctx := r.Context()
	p, err := c.Asaas.GetPayment(ctx, l.AsaasPayment)
	if err != nil {
		c.asaasFail(w, "pix", l, err)
		return
	}
	if !c.sameCharge(l, p.ID, p.Value, "pix") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cobranca_divergente"})
		return
	}
	if asaas.Paid(p.Status) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
		return
	}
	// Cobrança criada como cartão não aceita Pix: troca só esta cobrança (a assinatura fica como está).
	if p.BillingType != asaas.BillingPix {
		if err := c.Asaas.SetPaymentBillingType(ctx, l.AsaasPayment, asaas.BillingPix); err != nil {
			c.asaasFail(w, "pix", l, err)
			return
		}
		// Pago este Pix, o webhook passa a assinatura para Pix (a recorrência segue a forma usada).
		if err := c.Store.CheckoutPixSwitched(ctx, l.ID); err != nil {
			c.Log.Error("checkout: troca para Pix não registrada", "org_id", l.OrgID, "erro", err)
		}
	}
	q, err := c.Asaas.PixQRCode(ctx, l.AsaasPayment)
	if err != nil {
		c.asaasFail(w, "pix", l, err)
		return
	}
	if !base64Re.MatchString(q.EncodedImage) {
		c.Log.Error("checkout: QR do Pix com imagem ilegível", "org_id", l.OrgID)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "indisponivel"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pending", "image": "data:image/png;base64," + q.EncodedImage,
		"payload": q.Payload, "due": l.DueDate.Format("2006-01-02")})
}

var base64Re = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// cardForm é o que a página manda. Nunca vai para log, banco, erro ou auditoria.
type cardForm struct {
	Number string `json:"number"`
	Name   string `json:"name"`
	Expiry string `json:"expiry"` // MM/AA
	CVV    string `json:"cvv"`
	Holder struct {
		Name          string `json:"name"`
		CpfCnpj       string `json:"cpfCnpj"`
		Email         string `json:"email"`
		PostalCode    string `json:"postalCode"`
		AddressNumber string `json:"addressNumber"`
		Phone         string `json:"phone"`
	} `json:"holder"`
}

// card cobra a cobrança existente com o cartão e, aprovado, põe o cartão na assinatura para os próximos meses.
func (c *Checkout) card(w http.ResponseWriter, r *http.Request) {
	c.init()
	securityHeaders(w)
	l, ok, err := c.link(r)
	if err != nil {
		c.Log.Error("checkout: falha ao ler o link", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "link_invalido"})
		return
	}
	// CSRF: o token da página (HMAC por link) e a origem do pedido.
	if !c.sameOrigin(r) || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(c.csrf(l))) != 1 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "pedido_invalido"})
		return
	}
	if l.Paid {
		writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
		return
	}
	now := c.now()
	if l.Locked(now) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "bloqueado"})
		return
	}
	ctx := r.Context()
	// O front só manda cartão e titular. Valor, plano, cobrança, cliente e assinatura vêm do token (banco e
	// Asaas): qualquer campo a mais (value, plan, payment_id, billingType…) recusa o pedido inteiro.
	var f cardForm
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || dec.Decode(&f) != nil || dec.More() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dados_invalidos"})
		return
	}
	// Uma cobrança por link de cada vez, travada no banco (vale entre réplicas): duplo clique ou duas abas
	// passariam ambos pela conferência abaixo.
	got, err := c.Store.CheckoutBeginCharge(ctx, l.ID, 90*time.Second)
	if err != nil {
		c.Log.Error("checkout: falha na trava da cobrança", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
		return
	}
	if !got {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "em_andamento"})
		return
	}
	defer func() {
		if err := c.Store.CheckoutEndCharge(context.WithoutCancel(ctx), l.ID); err != nil {
			c.Log.Error("checkout: falha ao soltar a trava da cobrança", "erro", err)
		}
	}()
	if l.LockedUntil != nil {
		if err := c.Store.CheckoutUnlock(ctx, l.ID); err != nil {
			c.Log.Error("checkout: falha ao liberar o cartão", "erro", err)
		}
	}
	card, holder, field := c.validCard(f, now)
	if field != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dados_invalidos", "field": field})
		return
	}
	// Cobrança já paga (Pix em outra aba, webhook atrasado): não cobra de novo.
	p, err := c.Asaas.GetPayment(ctx, l.AsaasPayment)
	if err != nil {
		c.asaasFail(w, "cartao", l, err)
		return
	}
	// A cobrança do Asaas tem que ser a do token, com o valor do token: se alguém mexeu nela, não cobra.
	if !c.sameCharge(l, p.ID, p.Value, "cartao") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cobranca_divergente"})
		return
	}
	if asaas.Paid(p.Status) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
		return
	}
	ip := clientIP(r)
	res, err := c.Asaas.PayWithCard(ctx, l.AsaasPayment, card, holder, ip)
	if err != nil {
		var ae *asaas.APIError
		if errors.As(err, &ae) && ae.Status == http.StatusBadRequest {
			locked, ferr := c.Store.CheckoutCardFailed(ctx, l.ID, checkoutMaxFails, checkoutLock)
			if ferr != nil {
				c.Log.Error("checkout: falha ao contar a recusa", "erro", ferr)
			}
			c.Log.Warn("checkout: cartão recusado", "org_id", l.OrgID, "pagamento", l.AsaasPayment, "final", last4(card.Number),
				"asaas", safeAsaasErr(err), "bloqueado", locked)
			writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "recusado", "locked": locked})
			return
		}
		// Timeout ou falha do Asaas: o pagamento pode ter passado. Confere antes de dizer que falhou.
		if p2, gerr := c.Asaas.GetPayment(ctx, l.AsaasPayment); gerr == nil && asaas.Paid(p2.Status) && c.sameCharge(l, p2.ID, p2.Value, "cartao") {
			c.cardPaid(ctx, l, "", ip)
			writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
			return
		}
		c.asaasFail(w, "cartao", l, err)
		return
	}
	switch {
	case res.ChallengeURL != "":
		u, perr := url.Parse(res.ChallengeURL)
		if perr != nil || u.Scheme != "https" || !(u.Hostname() == "asaas.com" || strings.HasSuffix(u.Hostname(), ".asaas.com")) {
			c.Log.Error("checkout: desafio 3DS com endereço inesperado", "org_id", l.OrgID)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "indisponivel"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "challenge", "url": u.String()})
	case asaas.Paid(res.Status) && !c.sameCharge(l, res.ID, res.Value, "cartao"):
		// Aprovado, mas não na cobrança ou no valor esperados: não marca nada; a liberação fica com o webhook
		// e a equipe vê o alerta no log.
		writeJSON(w, http.StatusOK, map[string]string{"status": "analysis"})
	case asaas.Paid(res.Status):
		c.cardPaid(ctx, l, res.CardToken, ip)
		c.Log.Info("checkout: cartão aprovado", "org_id", l.OrgID, "pagamento", l.AsaasPayment, "final", last4(card.Number), "bandeira", res.Brand)
		writeJSON(w, http.StatusOK, map[string]string{"status": "paid"})
	default:
		// Em análise de risco: o webhook confirma depois.
		c.Log.Info("checkout: cartão em análise", "org_id", l.OrgID, "pagamento", l.AsaasPayment, "status", res.Status)
		writeJSON(w, http.StatusOK, map[string]string{"status": "analysis"})
	}
}

// cardPaid marca o link pago e põe o cartão na assinatura (recorrência no cartão). Falha aqui não desfaz o
// pagamento: só fica no log (o Asaas segue mandando a cobrança do mês seguinte pela forma antiga).
func (c *Checkout) cardPaid(ctx context.Context, l store.CheckoutLink, cardToken, ip string) {
	if err := c.Store.CheckoutCardPaid(ctx, l.ID); err != nil {
		c.Log.Error("checkout: falha ao marcar o link pago", "erro", err)
	}
	if cardToken == "" {
		return
	}
	if err := c.Asaas.SetSubscriptionCard(ctx, l.AsaasSub, cardToken, ip); err != nil {
		c.Log.Error("checkout: cartão não ficou na assinatura", "org_id", l.OrgID, "assinatura", l.AsaasSub, "asaas", safeAsaasErr(err))
	}
}

// sameCharge confere a cobrança que o Asaas devolveu com a do token: mesmo id e mesmo valor em centavos.
// Divergência = alerta no log (sem dado de cartão) e nada é marcado.
func (c *Checkout) sameCharge(l store.CheckoutLink, id string, value float64, step string) bool {
	cents := int64(math.Round(value * 100))
	if id == l.AsaasPayment && cents == l.AmountCents {
		return true
	}
	c.Log.Error("checkout: ALERTA cobrança divergente do link", "etapa", step, "org_id", l.OrgID, "pagamento", l.AsaasPayment,
		"pagamento_asaas", id, "valor_esperado", l.AmountCents, "valor_asaas", cents)
	return false
}

// asaasFail: erro do Asaas nunca vai cru para o cliente; o detalhe (sem dado de cartão) fica no log.
func (c *Checkout) asaasFail(w http.ResponseWriter, what string, l store.CheckoutLink, err error) {
	c.Log.Error("checkout: falha no Asaas", "etapa", what, "org_id", l.OrgID, "pagamento", l.AsaasPayment, "asaas", safeAsaasErr(err))
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "indisponivel"})
}

// sameOrigin: o POST do cartão tem que vir da nossa página (Origin, senão Sec-Fetch-Site).
func (c *Checkout) sameOrigin(r *http.Request) bool {
	want := strings.TrimRight(c.Origin, "/")
	if want == "" {
		want = "https://" + r.Host
	}
	if o := r.Header.Get("Origin"); o != "" {
		return strings.EqualFold(o, want)
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

var (
	digitsRe = regexp.MustCompile(`\d{6,}`)
	emailRe  = regexp.MustCompile(`^[^@\s]{1,64}@[^@\s]+\.[^@\s]{2,}$`)
)

// safeAsaasErr resume o erro para o log: status e códigos do Asaas, sem descrição e sem sequência longa de
// dígitos (nada de número de cartão, mesmo que algum dia ele volte no corpo do erro).
func safeAsaasErr(err error) string {
	var ae *asaas.APIError
	if errors.As(err, &ae) {
		codes := []string{}
		for _, part := range strings.Split(ae.Message, ";") {
			if f := strings.Fields(part); len(f) > 0 {
				codes = append(codes, f[0])
			}
		}
		return digitsRe.ReplaceAllString(fmt.Sprintf("%s %s: status %d: %s", ae.Method, ae.Path, ae.Status, strings.Join(codes, ",")), "[...]")
	}
	return digitsRe.ReplaceAllString(err.Error(), "[...]")
}

func last4(number string) string {
	if len(number) < 4 {
		return ""
	}
	return number[len(number)-4:]
}

// validCard confere e normaliza o formulário. field = primeiro campo inválido ("" = tudo certo).
func (c *Checkout) validCard(f cardForm, now time.Time) (asaas.CreditCard, asaas.CardHolder, string) {
	number := signup.OnlyDigits(f.Number)
	if len(number) < 13 || len(number) > 19 || !(luhn(number) || c.Sandbox && number == "4444444444444444") {
		return asaas.CreditCard{}, asaas.CardHolder{}, "number"
	}
	name := strings.Join(strings.Fields(f.Name), " ")
	if len(name) < 3 || len(name) > 60 {
		return asaas.CreditCard{}, asaas.CardHolder{}, "name"
	}
	exp := signup.OnlyDigits(f.Expiry)
	if len(exp) != 4 {
		return asaas.CreditCard{}, asaas.CardHolder{}, "expiry"
	}
	month, _ := strconv.Atoi(exp[:2])
	year, _ := strconv.Atoi(exp[2:])
	year += 2000
	brNow := now.In(brtZone)
	if month < 1 || month > 12 || year < brNow.Year() || year == brNow.Year() && month < int(brNow.Month()) || year > brNow.Year()+20 {
		return asaas.CreditCard{}, asaas.CardHolder{}, "expiry"
	}
	cvv := signup.OnlyDigits(f.CVV)
	wantCVV := 3
	if strings.HasPrefix(number, "34") || strings.HasPrefix(number, "37") {
		wantCVV = 4
	}
	if len(cvv) != wantCVV || len(f.CVV) != wantCVV {
		return asaas.CreditCard{}, asaas.CardHolder{}, "cvv"
	}
	h := f.Holder
	hName := strings.Join(strings.Fields(h.Name), " ")
	if len(hName) < 3 || len(hName) > 100 {
		return asaas.CreditCard{}, asaas.CardHolder{}, "holderName"
	}
	doc := signup.OnlyDigits(h.CpfCnpj)
	if !(len(doc) == 11 && signup.ValidCPF(doc) || len(doc) == 14 && signup.ValidCNPJ(doc)) {
		return asaas.CreditCard{}, asaas.CardHolder{}, "cpfCnpj"
	}
	email := strings.TrimSpace(h.Email)
	if len(email) > 120 || !emailRe.MatchString(email) {
		return asaas.CreditCard{}, asaas.CardHolder{}, "email"
	}
	cep := signup.OnlyDigits(h.PostalCode)
	if len(cep) != 8 {
		return asaas.CreditCard{}, asaas.CardHolder{}, "postalCode"
	}
	num := strings.TrimSpace(h.AddressNumber)
	if num == "" || len(num) > 10 {
		return asaas.CreditCard{}, asaas.CardHolder{}, "addressNumber"
	}
	phone := signup.OnlyDigits(h.Phone)
	if len(phone) < 10 || len(phone) > 11 {
		return asaas.CreditCard{}, asaas.CardHolder{}, "phone"
	}
	return asaas.CreditCard{HolderName: strings.ToUpper(name), Number: number, ExpiryMonth: exp[:2], ExpiryYear: strconv.Itoa(year), CCV: cvv},
		asaas.CardHolder{Name: hName, Email: email, CpfCnpj: doc, PostalCode: cep, AddressNumber: num, Phone: phone}, ""
}

func luhn(number string) bool {
	sum, double := 0, false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if double {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// brl: R$ 97,00 / R$ 1.297,50.
func brl(cents int64) string {
	reais := strconv.FormatInt(cents/100, 10)
	for i := len(reais) - 3; i > 0; i -= 3 {
		reais = reais[:i] + "." + reais[i:]
	}
	return fmt.Sprintf("R$ %s,%02d", reais, cents%100)
}

// brtZone: datas do Asaas são dias de São Paulo.
var brtZone = time.FixedZone("BRT", -3*3600)
