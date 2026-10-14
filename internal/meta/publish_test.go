package meta_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

const tok = "TOKEN1"

type rec struct {
	Method, Path string
	Form         url.Values
	File         bool
}

// fakeGraph responde por "MÉTODO caminho" e grava cada chamada. Sem resposta cadastrada, devolve {"id":"<n>"}.
type fakeGraph struct {
	t    *testing.T
	mu   sync.Mutex
	reqs []rec
	resp map[string]func(rec) (int, string)
	n    int
}

func newFake(t *testing.T) (*fakeGraph, *meta.Client) {
	f := &fakeGraph{t: t, resp: map[string]func(rec) (int, string){}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := client(srv)
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return f, c
}

func (f *fakeGraph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	x := rec{Method: r.Method, Path: strings.TrimPrefix(r.URL.Path, "/v23.0")}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			f.t.Errorf("multipart: %v", err)
		}
		x.Form = url.Values(r.MultipartForm.Value)
		x.File = len(r.MultipartForm.File["source"]) == 1
	} else {
		r.ParseForm()
		x.Form = r.Form
	}
	if x.Form.Get("access_token") != tok || x.Form.Get("appsecret_proof") != proof(tok) {
		f.t.Errorf("%s %s sem token/proof", x.Method, x.Path)
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, x)
	f.n++
	n := f.n
	h := f.resp[x.Method+" "+x.Path]
	f.mu.Unlock()
	if h == nil {
		fmt.Fprintf(w, `{"id":"%d"}`, n)
		return
	}
	code, body := h(x)
	w.WriteHeader(code)
	w.Write([]byte(body))
}

func (f *fakeGraph) on(key, body string) { f.onCode(key, 200, body) }

func (f *fakeGraph) onCode(key string, code int, body string) {
	f.resp[key] = func(rec) (int, string) { return code, body }
}

func (f *fakeGraph) calls(key string) []rec {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rec
	for _, r := range f.reqs {
		if r.Method+" "+r.Path == key {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeGraph) one(key string) rec {
	f.t.Helper()
	c := f.calls(key)
	if len(c) != 1 {
		f.t.Fatalf("%s chamado %d vezes", key, len(c))
	}
	return c[0]
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("json %q: %v", s, err)
	}
	return m
}

// básico: respostas padrão de campanha, conjunto, imagens e buscas.
func (f *fakeGraph) basics() {
	f.on("POST /act_9/campaigns", `{"id":"CAMP"}`)
	f.on("POST /act_9/adsets", `{"id":"SET"}`)
	f.on("POST /act_9/adimages", `{"images":{"bytes":{"hash":"HASH1","url":"https://x"}}}`)
	f.resp["GET /search"] = func(r rec) (int, string) {
		q := r.Form
		switch {
		case q.Get("type") == "adgeolocation" && q.Get("location_types") == `["city"]` && q.Get("q") == "Chapecó":
			return 200, `{"data":[{"key":"X9","name":"Chapecó","type":"city","country_code":"BR","region":"Paraná","region_id":448},
				{"key":"C1","name":"Chapecó","type":"city","country_code":"BR","region":"Santa Catarina","region_id":449}]}`
		case q.Get("type") == "adgeolocation" && q.Get("location_types") == `["city"]` && q.Get("q") == "Campinas":
			// formato real da Meta: o estado homônimo da capital vem com " (state)"
			return 200, `{"data":[{"key":"CPS","name":"Campinas","type":"city","country_code":"BR","region":"São Paulo (state)","region_id":460}]}`
		case q.Get("type") == "adgeolocation" && q.Get("location_types") == `["region"]` && q.Get("q") == "Santa Catarina":
			return 200, `{"data":[{"key":"R42","name":"Santa Catarina","type":"region","country_code":"BR"}]}`
		case q.Get("type") == "adgeolocation":
			return 200, `{"data":[]}`
		case q.Get("type") == "adinterest" && q.Get("q") == "Pizza":
			return 200, `{"data":[{"id":"I1","name":"Pizza (food)"}]}`
		}
		return 200, `{"data":[]}`
	}
}

func baseInput() meta.PublishInput {
	return meta.PublishInput{
		AdAccountID: "act_9", PageID: "P1", Name: "Pizzaria", Objective: "OUTCOME_ENGAGEMENT", DailyBudgetCents: 4000,
		LocationQuery: "Chapecó, SC", AgeMin: 25, AgeMax: 55,
		Creatives: []meta.PublishCreative{{Kind: "image", Mime: "image/jpeg", Data: []byte("jpg"), Placement: "feed"}},
		Copies:    []meta.PublishCopy{{Headline: "Pizza em dobro", Text: "Só hoje", CTA: "WHATSAPP_MESSAGE"}},
	}
}

func TestPublishWhatsApp(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.on("POST /act_9/advideos", `{"id":"VID"}`)
	polls := 0
	f.resp["GET /VID"] = func(r rec) (int, string) {
		if r.Form.Get("fields") != "status" {
			t.Errorf("status do vídeo sem fields: %v", r.Form)
		}
		polls++
		if polls < 3 {
			return 200, `{"status":{"video_status":"processing"}}`
		}
		return 200, `{"status":{"video_status":"ready"}}`
	}
	f.on("GET /VID/thumbnails", `{"data":[{"uri":"https://t/1"},{"uri":"https://t/2","is_preferred":true}]}`)
	in := baseInput()
	in.WhatsAppNumber, in.InstagramActorID, in.RadiusKM = "+55 (49) 99999-0000", "IG1", 10
	in.Genders, in.Interests = []string{"female"}, []string{"Pizza", "Inexistente"}
	in.StartTime, in.EndTime = time.Now().Add(48*time.Hour), time.Now().Add(96*time.Hour)
	in.Creatives = append(in.Creatives, meta.PublishCreative{Kind: "video", Mime: "video/mp4", Data: []byte("mp4"), Placement: "stories"})
	in.Copies = append(in.Copies, meta.PublishCopy{Headline: "Peça já", Text: "Entrega grátis", CTA: "WHATSAPP_MESSAGE"})

	res, err := c.Publish(context.Background(), tok, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.CampaignID != "CAMP" || res.AdSetID != "SET" || len(res.AdIDs) != 4 {
		t.Fatalf("resultado = %+v", res)
	}
	joined := strings.Join(res.Warnings, " | ")
	if !strings.Contains(joined, "raio ajustado de 10 km para 17 km") || !strings.Contains(joined, "interesse não encontrado: Inexistente") {
		t.Fatalf("avisos = %v", res.Warnings)
	}

	camp := f.one("POST /act_9/campaigns").Form
	if camp.Get("objective") != "OUTCOME_ENGAGEMENT" || camp.Get("status") != "PAUSED" || camp.Get("special_ad_categories") != "[]" ||
		camp.Get("buying_type") != "AUCTION" || camp.Get("is_adset_budget_sharing_enabled") != "false" || camp.Get("name") != "Pizzaria" {
		t.Fatalf("campanha = %v", camp)
	}
	set := f.one("POST /act_9/adsets").Form
	if set.Get("campaign_id") != "CAMP" || set.Get("optimization_goal") != "CONVERSATIONS" || set.Get("destination_type") != "WHATSAPP" ||
		set.Get("daily_budget") != "4000" || set.Get("billing_event") != "IMPRESSIONS" || set.Get("status") != "PAUSED" ||
		set.Get("bid_strategy") != "LOWEST_COST_WITHOUT_CAP" || set.Get("start_time") == "" || set.Get("end_time") == "" {
		t.Fatalf("conjunto = %v", set)
	}
	po := decode(t, set.Get("promoted_object"))
	if po["page_id"] != "P1" || po["whatsapp_phone_number"] != "5549999990000" {
		t.Fatalf("promoted_object = %v", po)
	}
	tg := decode(t, set.Get("targeting"))
	city := tg["geo_locations"].(map[string]any)["cities"].([]any)[0].(map[string]any)
	if city["key"] != "C1" || city["radius"] != float64(17) || city["distance_unit"] != "kilometer" {
		t.Fatalf("cidade = %v", city)
	}
	if tg["age_min"] != float64(25) || tg["age_max"] != float64(55) || fmt.Sprint(tg["genders"]) != "[2]" ||
		fmt.Sprint(tg["targeting_automation"]) != "map[advantage_audience:0]" || tg["publisher_platforms"] != nil {
		t.Fatalf("público = %v", tg)
	}
	ints := tg["flexible_spec"].([]any)[0].(map[string]any)["interests"].([]any)
	if len(ints) != 1 || ints[0].(map[string]any)["id"] != "I1" {
		t.Fatalf("interesses = %v", ints)
	}

	vid := f.one("POST /act_9/advideos")
	if !vid.File {
		t.Fatal("vídeo sem arquivo source")
	}
	if polls != 3 {
		t.Fatalf("consultas do vídeo = %d", polls)
	}
	crs := f.calls("POST /act_9/adcreatives")
	if len(crs) != 4 {
		t.Fatalf("criativos = %d", len(crs))
	}
	img := decode(t, crs[0].Form.Get("object_story_spec"))
	ld := img["link_data"].(map[string]any)
	cta := ld["call_to_action"].(map[string]any)
	if img["page_id"] != "P1" || img["instagram_user_id"] != "IG1" || ld["image_hash"] != "HASH1" || ld["name"] != "Pizza em dobro" ||
		ld["message"] != "Só hoje" || ld["link"] != "https://api.whatsapp.com/send" || cta["type"] != "WHATSAPP_MESSAGE" ||
		cta["value"].(map[string]any)["app_destination"] != "WHATSAPP" {
		t.Fatalf("criativo imagem = %v", img)
	}
	v := decode(t, crs[1].Form.Get("object_story_spec"))["video_data"].(map[string]any)
	if v["video_id"] != "VID" || v["image_url"] != "https://t/2" || v["title"] != "Peça já" || v["message"] != "Entrega grátis" ||
		v["call_to_action"].(map[string]any)["type"] != "WHATSAPP_MESSAGE" {
		t.Fatalf("criativo vídeo = %v", v)
	}
	ads := f.calls("POST /act_9/ads")
	if len(ads) != 4 {
		t.Fatalf("anúncios = %d", len(ads))
	}
	for _, a := range ads {
		if a.Form.Get("adset_id") != "SET" || a.Form.Get("status") != "PAUSED" || !strings.Contains(a.Form.Get("creative"), `"creative_id"`) {
			t.Fatalf("anúncio = %v", a.Form)
		}
	}
	if len(f.calls("DELETE /CAMP")) != 0 {
		t.Fatal("apagou campanha sem falha")
	}
}

func TestPublishCapsAdsAtSix(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	in := baseInput()
	for range 3 {
		in.Creatives = append(in.Creatives, in.Creatives[0])
		in.Copies = append(in.Copies, in.Copies[0])
	}
	res, err := c.Publish(context.Background(), tok, in)
	if err != nil || len(res.AdIDs) != 6 || len(f.calls("POST /act_9/ads")) != 6 {
		t.Fatalf("anúncios = %+v, %v", res, err)
	}
}

func TestPublishObjectiveMapping(t *testing.T) {
	cases := []struct {
		name, objective, cta, wa, link     string
		wantObj, wantOpt, wantDest, wantCT string
		wantLink, wantWarn                 string
	}{
		{"conversas", "OUTCOME_ENGAGEMENT", "WHATSAPP_MESSAGE", "", "", "OUTCOME_ENGAGEMENT", "CONVERSATIONS", "WHATSAPP", "WHATSAPP_MESSAGE", "https://api.whatsapp.com/send", ""},
		{"leads whatsapp", "OUTCOME_LEADS", "CONTACT_US", "5549999990000", "", "OUTCOME_LEADS", "CONVERSATIONS", "WHATSAPP", "WHATSAPP_MESSAGE", "https://api.whatsapp.com/send", ""},
		{"leads sem whatsapp", "OUTCOME_LEADS", "LEARN_MORE", "", "", "OUTCOME_TRAFFIC", "LINK_CLICKS", "WEBSITE", "LEARN_MORE", "https://www.facebook.com/P1", "publicado como tráfego"},
		{"tráfego site", "OUTCOME_TRAFFIC", "WHATSAPP_MESSAGE", "", "https://loja.test", "OUTCOME_TRAFFIC", "LINK_CLICKS", "WEBSITE", "LEARN_MORE", "https://loja.test", ""},
		{"tráfego contato", "OUTCOME_TRAFFIC", "CONTACT_US", "", "", "OUTCOME_TRAFFIC", "LINK_CLICKS", "WEBSITE", "CONTACT_US", "https://www.facebook.com/P1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFake(t)
			f.basics()
			in := baseInput()
			in.Objective, in.WhatsAppNumber, in.LinkURL = tc.objective, tc.wa, tc.link
			in.Copies[0].CTA = tc.cta
			res, err := c.Publish(context.Background(), tok, in)
			if err != nil {
				t.Fatal(err)
			}
			if f.one("POST /act_9/campaigns").Form.Get("objective") != tc.wantObj {
				t.Fatal("objetivo da campanha")
			}
			set := f.one("POST /act_9/adsets").Form
			if set.Get("optimization_goal") != tc.wantOpt || set.Get("destination_type") != tc.wantDest {
				t.Fatalf("conjunto = %v", set)
			}
			if po := decode(t, set.Get("promoted_object")); (tc.wa != "") != (po["whatsapp_phone_number"] != nil) {
				t.Fatalf("promoted_object = %v", po)
			}
			ld := decode(t, f.one("POST /act_9/adcreatives").Form.Get("object_story_spec"))["link_data"].(map[string]any)
			cta := ld["call_to_action"].(map[string]any)
			if ld["link"] != tc.wantLink || cta["type"] != tc.wantCT {
				t.Fatalf("criativo = %v", ld)
			}
			if tc.wantDest == "WEBSITE" && cta["value"].(map[string]any)["link"] != tc.wantLink {
				t.Fatalf("cta = %v", cta)
			}
			if tc.wantWarn != "" && !strings.Contains(strings.Join(res.Warnings, "|"), tc.wantWarn) {
				t.Fatalf("avisos = %v", res.Warnings)
			}
		})
	}
}

func TestPublishLocationFallbacks(t *testing.T) {
	cases := []struct{ query, want, warn string }{
		{"SC", `"regions":[{"key":"R42"}]`, ""},
		{"Santa Catarina", `"regions":[{"key":"R42"}]`, ""},
		{"Chapecó", `"cities":[{"key":"X9"}]`, ""},
		{"Campinas, SP", `"cities":[{"key":"CPS"}]`, ""},
		{"Campinas - são paulo", `"cities":[{"key":"CPS"}]`, ""},
		{"", `"countries":["BR"]`, "Brasil inteiro"},
		{"Brasil", `"countries":["BR"]`, ""},
	}
	for _, tc := range cases {
		f, c := newFake(t)
		f.basics()
		in := baseInput()
		in.LocationQuery = tc.query
		res, err := c.Publish(context.Background(), tok, in)
		if err != nil {
			t.Fatal(err)
		}
		tg := f.one("POST /act_9/adsets").Form.Get("targeting")
		if !strings.Contains(tg, tc.want) {
			t.Errorf("%q: público = %s", tc.query, tg)
		}
		if tc.warn != "" && !strings.Contains(strings.Join(res.Warnings, "|"), tc.warn) {
			t.Errorf("%q: avisos = %v", tc.query, res.Warnings)
		}
	}
}

func TestPublishRollbackOnAdFailure(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.onCode("POST /act_9/ads", 400, `{"error":{"message":"Ad rejected: violates advertising policy","code":100,"error_subcode":1487,"error_user_title":"Política","error_user_msg":"Texto não permitido"}}`)
	res, err := c.Publish(context.Background(), tok, baseInput())
	var pe *meta.PublishError
	if !errors.As(err, &pe) {
		t.Fatalf("erro = %v", err)
	}
	if pe.Step != "ad" || pe.Code != 100 || pe.Subcode != 1487 || !strings.Contains(pe.Message, "Texto não permitido") ||
		!strings.Contains(pe.UserMessage, "políticas de publicidade") || strings.Contains(pe.Error(), tok) {
		t.Fatalf("PublishError = %+v", pe)
	}
	if res.CampaignID != "" || len(res.AdIDs) != 0 {
		t.Fatalf("resultado depois da falha = %+v", res)
	}
	f.one("DELETE /CAMP")
}

func TestPublishNoRollbackWithoutCampaign(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.onCode("POST /act_9/campaigns", 400, `{"error":{"message":"No payment method","code":100,"error_subcode":1359188}}`)
	_, err := c.Publish(context.Background(), tok, baseInput())
	var pe *meta.PublishError
	if !errors.As(err, &pe) || pe.Step != "campaign" || !strings.Contains(pe.UserMessage, "forma de pagamento") {
		t.Fatalf("erro = %v", err)
	}
	for _, r := range f.reqs {
		if r.Method == http.MethodDelete {
			t.Fatalf("apagou sem campanha: %s", r.Path)
		}
	}
}

func TestPublishErrorMapping(t *testing.T) {
	cases := []struct {
		key, body, step, want string
	}{
		{"POST /act_9/campaigns", `{"error":{"message":"Error validating access token","code":190}}`, "campaign", "expirou"},
		{"POST /act_9/campaigns", `{"error":{"message":"(#200) Requires ads_management permission","code":200}}`, "campaign", "permissão"},
		{"POST /act_9/campaigns", `{"error":{"message":"User request limit reached","code":17}}`, "campaign", "esperar"},
		{"POST /act_9/campaigns", `{"error":{"message":"Ad account is disabled","code":100}}`, "campaign", "desativada"},
		{"POST /act_9/adsets", `{"error":{"message":"Invalid parameter","code":100,"error_user_msg":"Page is not connected to a WhatsApp Business account"}}`, "adset", "WhatsApp Business"},
		{"POST /act_9/adimages", `{"error":{"message":"Image is too small","code":100}}`, "upload", "pequena demais"},
		{"POST /act_9/adcreatives", `{"error":{"message":"Unknown error","code":1}}`, "creative", "equipe já foi avisada"},
	}
	for _, tc := range cases {
		f, c := newFake(t)
		f.basics()
		f.onCode(tc.key, 400, tc.body)
		_, err := c.Publish(context.Background(), tok, baseInput())
		var pe *meta.PublishError
		if !errors.As(err, &pe) || pe.Step != tc.step || !strings.Contains(pe.UserMessage, tc.want) {
			t.Errorf("%s: erro = %+v", tc.key, pe)
			continue
		}
		if created := len(f.calls("DELETE /CAMP")) == 1; created != (tc.step == "adset" || tc.step == "creative") {
			t.Errorf("%s: rollback = %v", tc.key, created)
		}
	}
}

func TestPublishVideoFailure(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.on("POST /act_9/advideos", `{"id":"VID"}`)
	f.on("GET /VID", `{"status":{"video_status":"error"}}`)
	in := baseInput()
	in.Creatives = []meta.PublishCreative{{Kind: "video", Mime: "video/mp4", Data: []byte("mp4")}}
	_, err := c.Publish(context.Background(), tok, in)
	var pe *meta.PublishError
	if !errors.As(err, &pe) || pe.Step != "upload" || !strings.Contains(pe.UserMessage, "vídeo") {
		t.Fatalf("erro = %+v", err)
	}
	if len(f.calls("POST /act_9/campaigns")) != 0 {
		t.Fatal("criou campanha com vídeo quebrado")
	}
}

func TestPublishVideoTimeoutRespectsContext(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.on("POST /act_9/advideos", `{"id":"VID"}`)
	f.on("GET /VID", `{"status":{"video_status":"processing"}}`)
	ctx, cancel := context.WithCancel(context.Background())
	polls := 0
	c.Sleep = func(ctx context.Context, _ time.Duration) error {
		polls++
		if polls == 2 {
			cancel()
		}
		return ctx.Err()
	}
	in := baseInput()
	in.Creatives = []meta.PublishCreative{{Kind: "video", Data: []byte("mp4")}}
	_, err := c.Publish(ctx, tok, in)
	if !errors.Is(err, context.Canceled) && !strings.Contains(fmt.Sprint(err), "canceled") {
		t.Fatalf("erro = %v", err)
	}
}

func TestPublishRejectsIncompleteInput(t *testing.T) {
	_, c := newFake(t)
	in := baseInput()
	in.Copies = nil
	var pe *meta.PublishError
	if _, err := c.Publish(context.Background(), tok, in); !errors.As(err, &pe) || !strings.Contains(pe.Message, "textos") {
		t.Fatalf("erro = %v", err)
	}
	in = baseInput()
	in.Objective = "OUTCOME_SALES"
	if _, err := c.Publish(context.Background(), tok, in); !errors.As(err, &pe) {
		t.Fatalf("objetivo inválido: %v", err)
	}
}

func TestSetStatus(t *testing.T) {
	// I5: ligar de novo mexe só na campanha; conjunto e anúncios ficam como estão.
	f, c := newFake(t)
	f.on("POST /CAMP", `{"success":true}`)
	if err := c.SetStatus(context.Background(), tok, "CAMP", "ACTIVE"); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 1 || f.reqs[0].Method != http.MethodPost || f.reqs[0].Path != "/CAMP" || f.reqs[0].Form.Get("status") != "ACTIVE" {
		t.Fatalf("ativar = %+v", f.reqs)
	}

	f2, c2 := newFake(t)
	if err := c2.SetStatus(context.Background(), tok, "CAMP", "PAUSED"); err != nil {
		t.Fatal(err)
	}
	if len(f2.reqs) != 1 || f2.reqs[0].Path != "/CAMP" || f2.reqs[0].Form.Get("status") != "PAUSED" {
		t.Fatalf("pausar = %+v", f2.reqs)
	}

	f3, c3 := newFake(t)
	f3.onCode("POST /CAMP", 400, `{"error":{"message":"No payment method","code":100}}`)
	var pe *meta.PublishError
	if err := c3.SetStatus(context.Background(), tok, "CAMP", "PAUSED"); !errors.As(err, &pe) || pe.Step != "status" ||
		!strings.Contains(pe.UserMessage, "pagamento") {
		t.Fatalf("erro = %v", err)
	}
	if err := c3.SetStatus(context.Background(), tok, "CAMP", "DELETED"); !errors.As(err, &pe) {
		t.Fatalf("status inválido: %v", err)
	}
}

// C3: cidade pedida que não aparece na Meta não vira o estado nem o Brasil inteiro em silêncio.
func TestPublishUnknownCityAsksClient(t *testing.T) {
	for _, q := range []string{"Lugar Nenhum, SC", "Atlântida", "Campinas, RS"} {
		f, c := newFake(t)
		f.basics()
		in := baseInput()
		in.LocationQuery = q
		_, err := c.Publish(context.Background(), tok, in)
		var pe *meta.PublishError
		if !errors.As(err, &pe) || pe.Step != "targeting" || !strings.Contains(pe.UserMessage, "Não achei a cidade") ||
			!strings.Contains(pe.UserMessage, "Chapecó, SC") {
			t.Errorf("%q: erro = %+v", q, err)
		}
		if len(f.calls("POST /act_9/campaigns")) != 0 || len(f.calls("POST /act_9/adimages")) != 0 {
			t.Errorf("%q: criou coisas na conta com local errado", q)
		}
	}
}

// I5/I6: a primeira ativação liga o conjunto e os anúncios gravados (não todos os da campanha), com o fim
// contado a partir de agora, e a campanha por último.
func TestActivateFirst(t *testing.T) {
	f, c := newFake(t)
	var a meta.Activator = c
	end := time.Date(2026, 10, 17, 15, 0, 0, 0, time.UTC)
	err := a.ActivateFirst(context.Background(), tok, meta.FirstActivation{CampaignID: "CAMP", AdSetID: "SET", AdIDs: []string{"A1", "A2"}, EndTime: end})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range f.reqs {
		if r.Method != http.MethodPost || r.Form.Get("status") != "ACTIVE" {
			t.Fatalf("%s %s = %v", r.Method, r.Path, r.Form)
		}
		order = append(order, r.Path)
	}
	if strings.Join(order, ",") != "/SET,/A1,/A2,/CAMP" {
		t.Fatalf("ordem = %v", order)
	}
	if got := f.reqs[0].Form.Get("end_time"); got != "2026-10-17T15:00:00Z" {
		t.Fatalf("end_time = %q", got)
	}
	if f.reqs[1].Form.Get("end_time") != "" || f.reqs[3].Form.Get("end_time") != "" {
		t.Fatal("end_time fora do conjunto")
	}

	// sem duração: sem end_time; falha no conjunto não liga a campanha
	f2, c2 := newFake(t)
	f2.onCode("POST /SET", 400, `{"error":{"message":"No payment method","code":100}}`)
	var pe *meta.PublishError
	if err := c2.ActivateFirst(context.Background(), tok, meta.FirstActivation{CampaignID: "CAMP", AdSetID: "SET"}); !errors.As(err, &pe) ||
		pe.Step != "status" || strings.Contains(pe.UserMessage, "criar") {
		t.Fatalf("erro = %+v", err)
	}
	if len(f2.calls("POST /CAMP")) != 0 || f2.reqs[0].Form.Get("end_time") != "" {
		t.Fatalf("chamadas = %+v", f2.reqs)
	}
}

// M5: falha ao mudar o status não fala em "criar" a campanha.
func TestSetStatusFailureMessage(t *testing.T) {
	f, c := newFake(t)
	f.onCode("POST /CAMP", 500, `{"error":{"message":"Unknown error","code":1}}`)
	var pe *meta.PublishError
	if err := c.SetStatus(context.Background(), tok, "CAMP", "PAUSED"); !errors.As(err, &pe) ||
		strings.Contains(pe.UserMessage, "criar") || !strings.Contains(pe.UserMessage, "status") {
		t.Fatalf("erro = %+v", err)
	}
}

// I2/I7: nome com a marca da publicação (#p<id>): antes de criar, a campanha de uma tentativa anterior que
// ficou na conta (worker caiu, prazo estourou, rollback falhou) é procurada pelo nome e apagada.
func TestPublishRemovesLeftoverCampaignByMarker(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.on("GET /act_9/campaigns", `{"data":[{"id":"OLD","name":"Pizzaria · #p12","effective_status":"PAUSED"}]}`)
	in := baseInput()
	in.Name = "Pizzaria · #p12"
	res, err := c.Publish(context.Background(), tok, in)
	if err != nil || res.CampaignID != "CAMP" {
		t.Fatalf("resultado = %+v %v", res, err)
	}
	q := f.one("GET /act_9/campaigns").Form
	if !strings.Contains(q.Get("filtering"), `"value":"Pizzaria · #p12"`) || !strings.Contains(q.Get("filtering"), `"operator":"EQUAL"`) {
		t.Fatalf("busca = %v", q)
	}
	f.one("DELETE /OLD")
	var order []string
	for _, r := range f.reqs {
		order = append(order, r.Method+" "+r.Path)
	}
	if j := strings.Join(order, ","); strings.Index(j, "DELETE /OLD") > strings.Index(j, "POST /act_9/campaigns") {
		t.Fatalf("ordem = %v", order)
	}

	// sem a marca não procura (não apaga campanha do cliente com nome parecido)
	f2, c2 := newFake(t)
	f2.basics()
	if _, err := c2.Publish(context.Background(), tok, baseInput()); err != nil {
		t.Fatal(err)
	}
	if len(f2.calls("GET /act_9/campaigns")) != 0 {
		t.Fatal("procurou campanha sem marca")
	}
}

func TestPublishLeftoverCampaignActiveOrUndeletable(t *testing.T) {
	for _, tc := range []struct{ name, list string }{
		{"ativa", `{"data":[{"id":"OLD","name":"Pizzaria · #p12","effective_status":"ACTIVE"}]}`},
		{"não apaga", `{"data":[{"id":"OLD","name":"Pizzaria · #p12","effective_status":"PAUSED"}]}`},
	} {
		f, c := newFake(t)
		f.basics()
		f.on("GET /act_9/campaigns", tc.list)
		f.onCode("DELETE /OLD", 500, `{"error":{"message":"Unknown","code":1}}`)
		in := baseInput()
		in.Name = "Pizzaria · #p12"
		_, err := c.Publish(context.Background(), tok, in)
		var pe *meta.PublishError
		if !errors.As(err, &pe) || pe.Step != "campaign" {
			t.Errorf("%s: erro = %v", tc.name, err)
		}
		if len(f.calls("POST /act_9/campaigns")) != 0 {
			t.Errorf("%s: criou outra campanha", tc.name)
		}
	}
}

// I7: rollback que falha deixa a campanha órfã na conta e isso aparece no erro.
func TestPublishRollbackFailureIsReported(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	f.onCode("POST /act_9/ads", 400, `{"error":{"message":"Ad rejected","code":100}}`)
	f.onCode("DELETE /CAMP", 500, `{"error":{"message":"Unknown","code":1}}`)
	_, err := c.Publish(context.Background(), tok, baseInput())
	var pe *meta.PublishError
	if !errors.As(err, &pe) || pe.Orphan != "CAMP" || !strings.Contains(pe.Message, "rollback") {
		t.Fatalf("erro = %+v", err)
	}
}

// I3: Página com WhatsApp sem o número conhecido: leads vão para conversa no WhatsApp, sem número no promoted_object.
func TestPublishLeadsWithPageWhatsAppWithoutNumber(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	in := baseInput()
	in.Objective, in.PageWhatsApp = "OUTCOME_LEADS", true
	in.Copies[0].CTA = "CONTACT_US"
	if _, err := c.Publish(context.Background(), tok, in); err != nil {
		t.Fatal(err)
	}
	set := f.one("POST /act_9/adsets").Form
	po := decode(t, set.Get("promoted_object"))
	if set.Get("destination_type") != "WHATSAPP" || po["whatsapp_phone_number"] != nil || po["page_id"] != "P1" {
		t.Fatalf("conjunto = %v", set)
	}
}

// M4: nomes de interesse vazios não contam no limite de 10.
func TestInterestLimitIgnoresEmptyNames(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	in := baseInput()
	in.Interests = []string{"", "  "}
	for i := range 10 {
		in.Interests = append(in.Interests, fmt.Sprintf("Interesse %d", i))
	}
	res, err := c.Publish(context.Background(), tok, in)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range f.calls("GET /search") {
		if r.Form.Get("type") == "adinterest" {
			n++
		}
	}
	if n != 10 || strings.Contains(strings.Join(res.Warnings, "|"), "primeiros interesses") {
		t.Fatalf("buscas = %d avisos = %v", n, res.Warnings)
	}
}

// Cliente pediu só feed: o conjunto vai com publisher_platforms e só as posições de feed.
func TestPublishPlacements(t *testing.T) {
	cases := []struct {
		pl     []string
		fb, ig string
	}{
		{[]string{"feed"}, "[feed]", "[stream]"},
		{[]string{"stories"}, "[story]", "[story]"},
		{[]string{"feed", "reels"}, "[feed facebook_reels]", "[stream reels]"},
	}
	for _, tc := range cases {
		f, c := newFake(t)
		f.basics()
		in := baseInput()
		in.Placements = tc.pl
		if _, err := c.Publish(context.Background(), tok, in); err != nil {
			t.Fatal(err)
		}
		tg := decode(t, f.one("POST /act_9/adsets").Form.Get("targeting"))
		if fmt.Sprint(tg["publisher_platforms"]) != "[facebook instagram]" || fmt.Sprint(tg["facebook_positions"]) != tc.fb ||
			fmt.Sprint(tg["instagram_positions"]) != tc.ig {
			t.Errorf("%v: platforms=%v fb=%v ig=%v", tc.pl, tg["publisher_platforms"], tg["facebook_positions"], tg["instagram_positions"])
		}
	}
}
