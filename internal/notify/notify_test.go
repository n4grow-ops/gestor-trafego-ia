package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/notify"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

func TestRenderKeepsUnknownVars(t *testing.T) {
	got := notify.Render("{nome} pagou {valor} {nada} {}", map[string]string{"nome": "Ana", "valor": "R$ 1,00"})
	if got != "Ana pagou R$ 1,00 {nada} {}" {
		t.Fatalf("render = %q", got)
	}
	ev, _ := notify.EventByKey(notify.EventSale)
	title, text := notify.Message(ev, "", "  ", notify.Vars(ev, ev.Sample, time.Date(2026, 11, 12, 15, 4, 0, 0, time.UTC)))
	if title != "💰 Venda aprovada: R$ 97,00" || !strings.Contains(text, "Ana Souza (Pizzaria Boa) comprou Plano Pro") {
		t.Fatalf("padrão = %q / %q", title, text)
	}
	_, text = notify.Message(ev, "x", "{quando} · {evento}", notify.Vars(ev, nil, time.Date(2026, 11, 12, 15, 4, 0, 0, time.UTC)))
	if text != "12/11 12:04 · Venda aprovada" {
		t.Fatalf("variáveis comuns = %q", text)
	}
	if long := notify.Clip(strings.Repeat("á", 400), 300); len([]rune(long)) != 300 || !strings.HasSuffix(long, "…") {
		t.Fatalf("clip = %d", len([]rune(long)))
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ips, ok := f[host]; ok {
		return ips, nil
	}
	return nil, &net404{}
}

type net404 struct{}

func (*net404) Error() string { return "no such host" }

func TestValidateURLBlocksSSRF(t *testing.T) {
	s := &notify.Sender{Resolver: fakeResolver{
		"api.pushcut.io":         {netip.MustParseAddr("34.120.1.1")},
		"trigger.macrodroid.com": {netip.MustParseAddr("2600:1f18::1")},
		"bom.example.com":        {netip.MustParseAddr("93.184.216.34")},
		"interno.example.com":    {netip.MustParseAddr("10.0.0.5")},
		"misto.example.com":      {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("169.254.169.254")},
		"v6.example.com":         {netip.MustParseAddr("::ffff:127.0.0.1")},
	}}
	ctx := context.Background()
	ok := []struct{ kind, url string }{
		{"pushcut", "https://api.pushcut.io/segredo123/notifications/Venda"},
		{"macrodroid", "https://trigger.macrodroid.com/abc-123/venda"},
		{"webhook", "https://bom.example.com/hook?x=1"},
	}
	for _, c := range ok {
		if err := s.ValidateURL(ctx, c.kind, c.url); err != nil {
			t.Errorf("%s %s = %v", c.kind, c.url, err)
		}
	}
	bad := []struct{ kind, url string }{
		{"webhook", "http://bom.example.com/hook"},                     // só https
		{"webhook", "https://user:senha@bom.example.com/"},             // credencial na URL
		{"webhook", "https://localhost/hook"},                          // localhost
		{"webhook", "https://127.0.0.1/hook"},                          // loopback
		{"webhook", "https://10.1.2.3/hook"},                           // privado
		{"webhook", "https://169.254.169.254/latest/meta-data/"},       // metadados da nuvem
		{"webhook", "https://[::1]/hook"},                              // loopback v6
		{"webhook", "https://[fe80::1]/hook"},                          // link-local v6
		{"webhook", "https://100.64.0.1/hook"},                         // CGNAT
		{"webhook", "https://interno.example.com/hook"},                // DNS para IP privado
		{"webhook", "https://misto.example.com/hook"},                  // um dos IPs é link-local
		{"webhook", "https://v6.example.com/hook"},                     // IPv4 mapeado
		{"webhook", "https://naoexiste.example.com/hook"},              // sem DNS
		{"webhook", "https://bom.example.com:8443/hook"},               // porta diferente
		{"pushcut", "https://bom.example.com/notifications/Venda"},     // host errado para o app
		{"macrodroid", "https://api.pushcut.io/x/notifications/Venda"}, // host errado para o app
		{"webhook", "javascript:alert(1)"},                             // lixo
	}
	for _, c := range bad {
		err := s.ValidateURL(ctx, c.kind, c.url)
		if err == nil {
			t.Errorf("%s %s passou", c.kind, c.url)
			continue
		}
		if strings.Contains(err.Error(), "example.com/hook") || strings.Contains(err.Error(), "segredo") {
			t.Errorf("erro repete a URL: %v", err)
		}
	}
}

// Depois do DNS: um nome que resolve para loopback é barrado na hora de conectar, mesmo sem passar
// pela validação (DNS que muda entre salvar e enviar).
func TestSendBlocksPrivateAfterDNS(t *testing.T) {
	hit := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":"):]
	s := &notify.Sender{}
	res := s.Send(context.Background(), notify.Request{Kind: "pushcut", URL: "https://localhost" + port + "/segredo/notifications/X", Title: "t", Text: "x"})
	if res.OK() || hit || res.Retry || !strings.Contains(res.Error, "bloqueado") {
		t.Fatalf("localhost = %+v hit=%v", res, hit)
	}
	if strings.Contains(res.Error, "segredo") {
		t.Fatalf("erro repete a URL: %q", res.Error)
	}
}

type captured struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
}

func (c *captured) handler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.reqs, c.body = append(c.reqs, r), append(c.body, string(b))
		c.mu.Unlock()
		w.WriteHeader(status)
	}
}

func localSender(srv *httptest.Server) *notify.Sender {
	return &notify.Sender{AllowLocal: true, Transport: srv.Client().Transport}
}

func TestSendPayloadPerKind(t *testing.T) {
	c := &captured{}
	srv := httptest.NewTLSServer(c.handler(200))
	defer srv.Close()
	s := localSender(srv)
	at := time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)
	base := notify.Request{Event: "venda.nova", Title: "Venda & cia", Text: "Ana pagou R$ 97,00", Data: map[string]string{"nome": "Ana"}, At: at}

	for _, kind := range []string{"pushcut", "macrodroid", "webhook"} {
		rq := base
		rq.Kind, rq.URL = kind, srv.URL+"/x/"+kind+"?keep=1"
		if res := s.Send(context.Background(), rq); !res.OK() || res.StatusCode != 200 {
			t.Fatalf("%s = %+v", kind, res)
		}
	}
	if len(c.reqs) != 3 {
		t.Fatalf("requisições = %d", len(c.reqs))
	}
	var push map[string]string
	if err := json.Unmarshal([]byte(c.body[0]), &push); err != nil || push["title"] != "Venda & cia" || push["text"] != "Ana pagou R$ 97,00" || len(push) != 2 {
		t.Fatalf("pushcut = %s", c.body[0])
	}
	if c.reqs[0].Method != "POST" || c.reqs[0].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("pushcut método/tipo = %s %s", c.reqs[0].Method, c.reqs[0].Header.Get("Content-Type"))
	}
	q := c.reqs[1].URL.Query()
	if q.Get("title") != "Venda & cia" || q.Get("text") != "Ana pagou R$ 97,00" || q.Get("keep") != "1" {
		t.Fatalf("macrodroid query = %s", c.reqs[1].URL.RawQuery)
	}
	var md map[string]string
	if err := json.Unmarshal([]byte(c.body[1]), &md); err != nil || md["title"] != "Venda & cia" {
		t.Fatalf("macrodroid corpo = %s", c.body[1])
	}
	var gen struct {
		Evento    string            `json:"evento"`
		Dados     map[string]string `json:"dados"`
		EnviadoEm string            `json:"enviado_em"`
	}
	if err := json.Unmarshal([]byte(c.body[2]), &gen); err != nil || gen.Evento != "venda.nova" || gen.Dados["nome"] != "Ana" || gen.EnviadoEm != "2026-11-12T15:00:00Z" {
		t.Fatalf("webhook = %s", c.body[2])
	}
}

func TestSendClassifiesErrors(t *testing.T) {
	for _, c := range []struct {
		status int
		retry  bool
	}{{500, true}, {503, true}, {429, true}, {400, false}, {404, false}, {302, false}} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c.status == 302 {
				http.Redirect(w, r, "https://169.254.169.254/", http.StatusFound)
				return
			}
			w.WriteHeader(c.status)
		}))
		res := localSender(srv).Send(context.Background(), notify.Request{Kind: "pushcut", URL: srv.URL + "/segredo", Title: "t", Text: "x"})
		srv.Close()
		if res.OK() || res.Retry != c.retry || res.StatusCode != c.status || strings.Contains(res.Error, "segredo") {
			t.Errorf("status %d = %+v", c.status, res)
		}
	}
	// Servidor que não responde em 5 s: tempo esgotado, vale tentar de novo.
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(8 * time.Second):
		}
	}))
	defer slow.Close()
	start := time.Now()
	res := localSender(slow).Send(context.Background(), notify.Request{Kind: "webhook", URL: slow.URL + "/segredo", At: start})
	if res.OK() || !res.Retry || !strings.Contains(res.Error, "tempo esgotado") || strings.Contains(res.Error, "segredo") || time.Since(start) > 7*time.Second {
		t.Fatalf("lento = %+v em %s", res, time.Since(start))
	}
}

// Disparo por evento: só destino ligado e que assina o evento ganha job; o job entrega, registra e,
// em erro 5xx, devolve erro para a fila tentar de novo.
func TestEmitAndDeliverWithRetry(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()

	var mu sync.Mutex
	statuses := []int{500, 200}
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		got = append(got, string(b))
		s := statuses[0]
		if len(statuses) > 1 {
			statuses = statuses[1:]
		}
		w.WriteHeader(s)
	}))
	defer srv.Close()

	mk := func(name string, events []string, active bool) store.NotifyDestination {
		d, err := st.CreateNotifyDestination(ctx, store.NotifyDestinationInput{Name: name, Kind: "pushcut", URL: srv.URL + "/" + name,
			Events: events, Active: active, TextTpl: "{nome} pagou {valor}"})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	sale := mk("vendas", []string{"venda.nova"}, true)
	mk("mensagens", []string{"cliente.mensagem"}, true)
	mk("desligado", []string{"venda.nova"}, false)

	if err := st.EmitNotify(ctx, "venda.nova", map[string]string{"nome": "Ana", "valor": "R$ 97,00"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = 'notify.deliver'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs = %d %v", n, err)
	}
	var maxAtt int
	if err := pool.QueryRow(ctx, `SELECT max_attempts FROM outbox_jobs WHERE kind = 'notify.deliver'`).Scan(&maxAtt); err != nil || maxAtt != 3 {
		t.Fatalf("tentativas = %d %v", maxAtt, err)
	}

	d := &notify.Deliverer{Store: st, Sender: localSender(srv)}
	job, ok, err := st.ClaimJobOf(ctx, []string{store.NotifyJobKind})
	if err != nil || !ok {
		t.Fatalf("claim = %v %v", ok, err)
	}
	err = d.Deliver(ctx, job)
	if err == nil {
		t.Fatal("500 devia pedir nova tentativa")
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("erro com URL: %v", err)
	}
	if _, err := st.FinishJob(ctx, job.ID, err); err != nil {
		t.Fatal(err)
	}
	var runAfter time.Duration
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM run_after - now())::int * interval '1 second' FROM outbox_jobs WHERE id = $1`, job.ID).Scan(&runAfter); err != nil || runAfter < 20*time.Second {
		t.Fatalf("espera da nova tentativa = %s %v", runAfter, err)
	}
	job.Attempts = 2
	if err := d.Deliver(ctx, job); err != nil {
		t.Fatalf("segunda tentativa = %v", err)
	}
	list, err := st.NotifyDeliveries(ctx, sale.ID, 10)
	if err != nil || len(list) != 2 || !list[0].OK || list[0].Attempt != 2 || list[1].OK || list[1].StatusCode != 500 {
		t.Fatalf("entregas = %+v %v", list, err)
	}
	if len(got) != 2 || !strings.Contains(got[1], "Ana pagou R$ 97,00") {
		t.Fatalf("corpo = %q", got)
	}
	// Destino desligado depois do evento: o job não entrega.
	if _, err := st.SetNotifyDestinationActive(ctx, sale.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := d.Deliver(ctx, job); err != nil || len(got) != 2 {
		t.Fatalf("desligado entregou: %v %d", err, len(got))
	}
}

func TestDeliveriesKeepLast100(t *testing.T) {
	pool := testdb.New(t)
	st := store.New(pool)
	ctx := context.Background()
	d, err := st.CreateNotifyDestination(ctx, store.NotifyDestinationInput{Name: "x", Kind: "webhook", URL: "https://x.example.com/", Events: []string{}, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 105 {
		if err := st.RecordNotifyDelivery(ctx, store.NotifyDelivery{DestinationID: d.ID, Event: "teste", Attempt: 1, OK: true, StatusCode: 200}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notify_deliveries WHERE destination_id = $1`, d.ID).Scan(&n); err != nil || n != 100 {
		t.Fatalf("guardadas = %d %v", n, err)
	}
}
