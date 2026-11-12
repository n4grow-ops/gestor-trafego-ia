package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/notify"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Aba Notificações: destinos (Pushcut, MacroDroid, webhook), eventos assinados, teste na hora,
// histórico das entregas e o token da venda de fora. Tudo só para administrador: a URL do destino é
// segredo (sai mascarada) e o token só aparece uma vez.
func (a *Admin) registerNotify(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/notifications", a.only(true, a.notifyList))
	mux.HandleFunc("POST /admin/api/notifications", a.only(true, a.notifyCreate))
	mux.HandleFunc("PUT /admin/api/notifications/{id}", a.only(true, a.notifyUpdate))
	mux.HandleFunc("DELETE /admin/api/notifications/{id}", a.only(true, a.notifyDelete))
	mux.HandleFunc("POST /admin/api/notifications/{id}/active", a.only(true, a.notifyActive))
	mux.HandleFunc("POST /admin/api/notifications/{id}/test", a.only(true, a.notifyTest))
	mux.HandleFunc("GET /admin/api/notifications/{id}/deliveries", a.only(true, a.notifyDeliveries))
	mux.HandleFunc("POST /admin/api/notifications/sale-token", a.only(true, a.notifySaleToken))
}

func (a *Admin) notifySender() *notify.Sender {
	if a.NotifySender != nil {
		return a.NotifySender
	}
	return &notify.Sender{}
}

// maskURL mostra só o host e o final da URL: https://api.pushcut.io/…a1b2
func maskURL(raw string) string {
	host := raw
	if i := strings.Index(raw, "://"); i >= 0 {
		host = raw[i+3:]
	}
	if i := strings.IndexAny(host, "/?"); i >= 0 {
		host = host[:i]
	}
	r := []rune(raw)
	tail := ""
	if len(r) > 4 {
		tail = string(r[len(r)-4:])
	}
	return "https://" + host + "/…" + tail
}

type notifyDestJSON struct {
	store.NotifyDestination
	URLMasked string `json:"url_masked"`
}

func destJSON(d store.NotifyDestination) notifyDestJSON {
	return notifyDestJSON{NotifyDestination: d, URLMasked: maskURL(d.URL)}
}

type notifyVarJSON struct {
	Key  string `json:"key"`
	Help string `json:"help"`
}

func (a *Admin) notifyList(w http.ResponseWriter, r *http.Request, _ store.TeamMember, _ adminSession) {
	dests, err := a.Store.NotifyDestinations(r.Context())
	if a.storeError(w, err) {
		return
	}
	token, err := a.Store.NotifySaleTokenStatus(r.Context())
	if a.storeError(w, err) {
		return
	}
	out := make([]notifyDestJSON, 0, len(dests))
	for _, d := range dests {
		out = append(out, destJSON(d))
	}
	vars := []notifyVarJSON{}
	for _, k := range []string{"nome", "empresa", "whatsapp", "valor", "produto", "origem", "mensagem", "evento", "quando"} {
		vars = append(vars, notifyVarJSON{k, notify.VarHelp[k]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"destinations": out, "events": notify.Events, "common_vars": notify.CommonVars, "vars": vars,
		"kinds": notify.Kinds, "sale_token": token, "keep": store.NotifyKeepDeliveries,
	})
}

type notifyDestBody struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	URL      string   `json:"url"`
	Events   []string `json:"events"`
	Active   bool     `json:"active"`
	TitleTpl string   `json:"title_tpl"`
	TextTpl  string   `json:"text_tpl"`
}

// input confere o corpo; creating exige URL. Responde 422 e devolve false se algo está errado.
func (a *Admin) notifyInput(w http.ResponseWriter, r *http.Request, b notifyDestBody, creating bool, by string) (store.NotifyDestinationInput, bool) {
	bad := func(detail string) (store.NotifyDestinationInput, bool) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": detail})
		return store.NotifyDestinationInput{}, false
	}
	name := strings.Join(strings.Fields(b.Name), " ")
	if n := utf8.RuneCountInString(name); n < 1 || n > 60 {
		return bad("nome: de 1 a 60 caracteres")
	}
	if !slices.Contains(notify.Kinds, b.Kind) {
		return bad("tipo")
	}
	events := []string{}
	for _, ev := range b.Events {
		if !notify.Subscribable(ev) {
			return bad("evento desconhecido")
		}
		if !slices.Contains(events, ev) {
			events = append(events, ev)
		}
	}
	if utf8.RuneCountInString(b.TitleTpl) > 200 || utf8.RuneCountInString(b.TextTpl) > 1000 {
		return bad("modelo longo demais")
	}
	url := strings.TrimSpace(b.URL)
	if creating && url == "" {
		return bad("url: cole a URL do webhook")
	}
	if url != "" {
		if err := a.notifySender().ValidateURL(r.Context(), b.Kind, url); err != nil {
			return bad("url: " + err.Error())
		}
	}
	return store.NotifyDestinationInput{Name: name, Kind: b.Kind, URL: url, Events: events, Active: b.Active,
		TitleTpl: strings.TrimSpace(b.TitleTpl), TextTpl: strings.TrimSpace(b.TextTpl), By: by}, true
}

func (a *Admin) notifyCreate(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	var b notifyDestBody
	if !decodeJSON(w, r, &b) {
		return
	}
	in, ok := a.notifyInput(w, r, b, true, m.Email)
	if !ok {
		return
	}
	d, err := a.Store.CreateNotifyDestination(r.Context(), in)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "notify_create", "notify_destination", orgTarget(d.ID), d.Name, map[string]any{"tipo": d.Kind, "eventos": d.Events, "url": maskURL(d.URL)})
	writeJSON(w, http.StatusCreated, destJSON(d))
}

func (a *Admin) notifyUpdate(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b notifyDestBody
	if !decodeJSON(w, r, &b) {
		return
	}
	before, err := a.Store.NotifyDestination(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	in, ok := a.notifyInput(w, r, b, false, m.Email)
	if !ok {
		return
	}
	// Trocar o tipo sem trocar a URL: a URL antiga precisa valer para o tipo novo.
	if in.URL == "" && in.Kind != before.Kind {
		if err := a.notifySender().ValidateURL(r.Context(), in.Kind, before.URL); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": "url: cole a URL do novo tipo"})
			return
		}
	}
	d, err := a.Store.UpdateNotifyDestination(r.Context(), id, in)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "notify_update", "notify_destination", orgTarget(d.ID), d.Name, map[string]any{"tipo": d.Kind, "eventos": d.Events,
		"ativo": d.Active, "url_trocada": in.URL != ""})
	writeJSON(w, http.StatusOK, destJSON(d))
}

func (a *Admin) notifyDelete(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := a.Store.NotifyDestination(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	if a.storeError(w, a.Store.DeleteNotifyDestination(r.Context(), id)) {
		return
	}
	a.log(r, m, "notify_delete", "notify_destination", orgTarget(id), d.Name, map[string]any{"tipo": d.Kind})
	writeJSON(w, http.StatusOK, map[string]string{"status": "apagado"})
}

func (a *Admin) notifyActive(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Active bool `json:"active"`
	}
	if !decodeJSON(w, r, &b) {
		return
	}
	d, err := a.Store.SetNotifyDestinationActive(r.Context(), id, b.Active)
	if a.storeError(w, err) {
		return
	}
	a.log(r, m, "notify_active", "notify_destination", orgTarget(id), d.Name, map[string]bool{"ativo": d.Active})
	writeJSON(w, http.StatusOK, destJSON(d))
}

// notifyTest manda o evento "teste" na hora (sem fila e sem repetir) e devolve o resultado.
func (a *Admin) notifyTest(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if a.Limiter != nil && !a.Limiter.Allow("notify-test:"+m.Email, 10) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	d, err := a.Store.NotifyDestination(r.Context(), id)
	if a.storeError(w, err) {
		return
	}
	ev, _ := notify.EventByKey(notify.EventTest)
	deliverer := notify.Deliverer{Store: a.Store, Sender: a.notifySender(), Log: a.Log}
	ctx, cancel := context.WithTimeout(r.Context(), notify.Timeout+time.Second)
	defer cancel()
	res := deliverer.Send(ctx, d, ev, ev.Sample, store.NotifyDeliverJob{DestinationID: d.ID, Event: ev.Key, Data: ev.Sample, At: a.now()})
	// O registro não usa o contexto da requisição: o teste fica no histórico mesmo se a tela fechar.
	if err := a.Store.RecordNotifyDelivery(context.WithoutCancel(r.Context()), store.NotifyDelivery{DestinationID: d.ID, Event: ev.Key,
		Attempt: 1, OK: res.OK(), StatusCode: res.StatusCode, Error: res.Error}); err != nil {
		a.Log.Error("painel: registro do teste de notificação falhou", "destino", d.ID, "erro", err)
	}
	a.log(r, m, "notify_test", "notify_destination", orgTarget(d.ID), d.Name, map[string]any{"ok": res.OK(), "status": res.StatusCode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.OK(), "status_code": res.StatusCode, "error": res.Error})
}

func (a *Admin) notifyDeliveries(w http.ResponseWriter, r *http.Request, _ store.TeamMember, _ adminSession) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.Store.NotifyDestination(r.Context(), id); a.storeError(w, err) {
		return
	}
	list, err := a.Store.NotifyDeliveries(r.Context(), id, store.NotifyKeepDeliveries)
	if a.storeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": list})
}

// Prefixo do token da venda de fora (ajuda a reconhecer o token num arquivo de configuração).
const saleTokenPrefix = "mav_"

func hashSaleToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// notifySaleToken gera um token novo para o POST /api/hooks/venda. O anterior para de valer na hora.
// O token só aparece nesta resposta; o banco guarda o hash.
func (a *Admin) notifySaleToken(w http.ResponseWriter, r *http.Request, m store.TeamMember, _ adminSession) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		a.fail(w, err)
		return
	}
	raw := saleTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	last4 := raw[len(raw)-4:]
	if err := a.Store.SetNotifySaleToken(r.Context(), hashSaleToken(raw), last4, m.Email); a.storeError(w, err) {
		return
	}
	a.log(r, m, "notify_sale_token", "notify_sale_token", "1", "Token da venda", map[string]string{"final": last4})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{"token": raw, "last4": last4})
}

// SaleHook é o POST /api/hooks/venda: uma venda feita fora do sistema (outra plataforma, n8n...) vira
// o evento venda.nova. Autenticado pelo token gerado no painel (Authorization: Bearer <token>).
type SaleHook struct {
	Store   *store.Store
	Limiter *Limiter
	Log     interface {
		Warn(msg string, args ...any)
		Error(msg string, args ...any)
	}
}

func (h *SaleHook) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/hooks/venda", h.sale)
}

type saleBody struct {
	Nome     string `json:"nome"`
	WhatsApp string `json:"whatsapp"`
	Empresa  string `json:"empresa"`
	Valor    any    `json:"valor"` // número (97.5) ou texto ("R$ 97,50")
	Produto  string `json:"produto"`
	Origem   string `json:"origem"`
}

func (h *SaleHook) sale(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Limiter != nil && !h.Limiter.Allow("sale-hook:"+clientIP(r), 60) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "muitas_tentativas"})
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-Token"))
	}
	if !strings.HasPrefix(token, saleTokenPrefix) || len(token) > 100 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token_invalido"})
		return
	}
	ok, err := h.Store.UseNotifySaleToken(r.Context(), hashSaleToken(token))
	if err != nil {
		h.Log.Error("venda de fora: token", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token_invalido"})
		return
	}
	var b saleBody
	if !decodeJSONLimit(w, r, &b, 8<<10) {
		return
	}
	valor, okV := saleValue(b.Valor)
	if !okV {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "dados_invalidos", "detail": "valor"})
		return
	}
	origem := notify.Clip(b.Origem, 60)
	if origem == "" {
		origem = "externo"
	}
	data := map[string]string{"nome": notify.Clip(b.Nome, 80), "whatsapp": notify.Clip(onlyDigits(b.WhatsApp), 20),
		"empresa": notify.Clip(b.Empresa, 80), "valor": valor, "produto": notify.Clip(b.Produto, 80), "origem": origem}
	if err := h.Store.EmitNotify(r.Context(), notify.EventSale, data); err != nil {
		h.Log.Error("venda de fora: fila", "erro", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "erro_interno"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "recebido"})
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// saleValue aceita número (em reais) ou texto já formatado; vazio vale (venda sem valor informado).
func saleValue(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case float64:
		if x < 0 || x > 100_000_000 {
			return "", false
		}
		return store.FormatBRL(int64(x*100 + 0.5)), true
	case string:
		x = strings.TrimSpace(x)
		if utf8.RuneCountInString(x) > 30 {
			return "", false
		}
		return x, true
	}
	return "", false
}

// emitManualSale avisa a venda lançada à mão no Faturamento (só pagamento pago).
func (a *Admin) emitManualSale(ctx context.Context, orgID, cents int64, method string) error {
	data, err := a.Store.OrgNotifyData(ctx, orgID)
	if err != nil {
		return err
	}
	data["valor"], data["produto"], data["origem"] = store.FormatBRL(cents), "lançamento manual", "manual · "+method
	return a.Store.EmitNotify(ctx, notify.EventSale, data)
}
