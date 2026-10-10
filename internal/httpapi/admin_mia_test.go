package httpapi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

type fakeTester struct{ in attendant.Input }

func (f *fakeTester) Reply(_ context.Context, in attendant.Input) (contract.AttendantTurn, llm.Usage, error) {
	f.in = in
	return contract.AttendantTurn{Intent: contract.IntentQuestion, Reply: "Oi! Sou a Mia de teste."}, llm.Usage{Model: "flash", CostUSD: 0.001}, nil
}

type fakeSpeaker struct{ voice string }

func (f *fakeSpeaker) Speak(_ context.Context, model, voice, text string) (llm.Speech, error) {
	f.voice = voice
	return llm.Speech{Audio: []byte("mp3"), Mime: "audio/mpeg", Usage: llm.Usage{Model: model}}, nil
}

// cfgFrom lê a configuração devolvida pela API.
func cfgFrom(t *testing.T, r resp) (mia.Config, int) {
	t.Helper()
	raw, _ := json.Marshal(r.body["config"])
	var c mia.Config
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c, int(r.body["version"].(float64))
}

func TestMiaOnlyAdmins(t *testing.T) {
	e := newAdminEnv(t, "otp")
	staff := e.login("marina@meuanuncio.io")
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/mia"}, {"POST", "/api/mia"}, {"POST", "/api/mia/restore"}, {"POST", "/api/mia/revert"},
		{"POST", "/api/mia/test"}, {"POST", "/api/mia/voice-sample"},
	} {
		if r := e.do(c.method, c.path, staff, map[string]any{}); r.code != http.StatusForbidden {
			t.Errorf("staff %s %s = %d", c.method, c.path, r.code)
		}
		if r := e.do(c.method, c.path, nil, map[string]any{}); r.code != http.StatusUnauthorized {
			t.Errorf("anônimo %s %s = %d", c.method, c.path, r.code)
		}
	}
}

func TestMiaSaveVersionsRestoreAndRevert(t *testing.T) {
	e := newAdminEnv(t, "otp")
	admin := e.login("dono@meuanuncio.io")
	e.admin.Mia = &mia.Cache{Load: func(ctx context.Context) (mia.Config, error) { c, _, err := e.st.MiaCurrent(ctx); return c, err }}

	r := e.do("GET", "/api/mia", admin, nil)
	if r.code != 200 {
		t.Fatalf("abrir = %d %s", r.code, r.raw)
	}
	cfg, v := cfgFrom(t, r)
	if v != 0 || cfg.Texts[mia.TextFirstMessage] != mia.Defaults().Texts[mia.TextFirstMessage] {
		t.Fatalf("sem configuração deve mostrar o padrão: v%d", v)
	}
	// Etapas com clientes: a conversa do ambiente está em coleta.
	if counts := r.body["stage_counts"].(map[string]any); counts["coleta"] != float64(1) {
		t.Fatalf("contagem por etapa = %v", counts)
	}

	cfg.Texts[mia.TextFirstMessage] = "Oi! Sou a Mia, com texto novo."
	cfg.Segments = []mia.Segment{{Key: "solar", Name: "Energia solar", Keywords: []string{"fotovoltaica"}, Strategy: "Fale de economia na conta de luz.", Active: true}}
	r = e.do("POST", "/api/mia", admin, map[string]any{"config": cfg, "base_version": 0, "note": "saudação nova"})
	if r.code != 200 {
		t.Fatalf("salvar = %d %s", r.code, r.raw)
	}
	if _, v = cfgFrom(t, r); v != 1 {
		t.Fatalf("versão = %d", v)
	}
	// A configuração em vigor (cache) já é a nova.
	if got := e.admin.Mia.Get(context.Background()).Texts[mia.TextFirstMessage]; got != "Oi! Sou a Mia, com texto novo." {
		t.Fatalf("cache = %q", got)
	}
	// Quem estava com a versão 0 aberta não sobrescreve.
	if r := e.do("POST", "/api/mia", admin, map[string]any{"config": cfg, "base_version": 0}); r.code != http.StatusConflict {
		t.Fatalf("conflito = %d", r.code)
	}
	bad := cfg
	bad.Texts = map[string]string{}
	for k, val := range cfg.Texts {
		bad.Texts[k] = val
	}
	bad.Texts[mia.TextReminder2] = "sem o link"
	if r := e.do("POST", "/api/mia", admin, map[string]any{"config": bad, "base_version": 1}); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.raw, "{link}") {
		t.Fatalf("inválida = %d %s", r.code, r.raw)
	}

	r = e.do("POST", "/api/mia/restore", admin, map[string]any{"base_version": 1})
	if c, v := cfgFrom(t, r); r.code != 200 || v != 2 || c.Texts[mia.TextFirstMessage] != mia.Defaults().Texts[mia.TextFirstMessage] || len(c.Segments) != 0 {
		t.Fatalf("restaurar = %d v%d", r.code, v)
	}
	r = e.do("POST", "/api/mia/revert", admin, map[string]any{"version": 1, "base_version": 2})
	if c, v := cfgFrom(t, r); r.code != 200 || v != 3 || c.Texts[mia.TextFirstMessage] != "Oi! Sou a Mia, com texto novo." || len(c.Segments) != 1 {
		t.Fatalf("desfazer = %d v%d", r.code, v)
	}
	if hist := r.body["versions"].([]any); len(hist) != 3 {
		t.Fatalf("histórico = %v", hist)
	}
	log, _ := e.st.AdminLog(context.Background(), store.AdminLogFilter{TargetType: "mia"})
	var actions []string
	for _, l := range log {
		actions = append(actions, l.Action)
	}
	if strings.Join(actions, ",") != "mia_revert,mia_restore,mia_save" {
		t.Fatalf("registro = %v", actions)
	}
}

func TestMiaTestAndVoiceSample(t *testing.T) {
	e := newAdminEnv(t, "otp")
	admin := e.login("dono@meuanuncio.io")
	if r := e.do("POST", "/api/mia/test", admin, map[string]any{"message": "oi"}); r.code != http.StatusServiceUnavailable {
		t.Fatalf("sem IA na api = %d", r.code)
	}
	tester, speaker := &fakeTester{}, &fakeSpeaker{}
	e.admin.MiaTest = tester
	e.admin.MiaVoice = &httpapi.MiaSpeaker{Speaker: speaker, Model: "tts", DefaultVoice: "pt-BR-FranciscaNeural"}

	cfg := mia.Defaults()
	cfg.Persona.Personality = "- Fala com sotaque mineiro, uai."
	cfg.Segments = []mia.Segment{{Key: "imob", Name: "Imobiliária", Strategy: "Leads por faixa de preço.", Active: true}}
	r := e.do("POST", "/api/mia/test", admin, map[string]any{"config": cfg, "state": "onboarding", "segment": "Imobiliária Central",
		"message": "oi, quero vender apartamentos", "history": []map[string]any{{"from_client": true, "text": "olá"}}})
	if r.code != 200 || r.body["turn"].(map[string]any)["reply"] != "Oi! Sou a Mia de teste." || r.body["segment"] != "Imobiliária" || r.body["stage"] != "Conhecer o negócio" {
		t.Fatalf("teste = %d %s", r.code, r.raw)
	}
	// A Atendente recebeu a configuração EM EDIÇÃO (não a salva) e o segmento do teste.
	if tester.in.Config == nil || tester.in.Config.Persona.Personality != "- Fala com sotaque mineiro, uai." || tester.in.Segment == nil || len(tester.in.History) != 1 {
		t.Fatalf("entrada = %+v", tester.in)
	}
	if r := e.do("POST", "/api/mia/test", admin, map[string]any{"config": cfg, "message": " "}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("mensagem vazia = %d", r.code)
	}

	r = e.do("POST", "/api/mia/voice-sample", admin, map[string]any{"voice": "pt-BR-ThalitaNeural"})
	if r.code != 200 || r.body["mime"] != "audio/mpeg" || r.body["audio"] != base64.StdEncoding.EncodeToString([]byte("mp3")) || speaker.voice != "pt-BR-ThalitaNeural" {
		t.Fatalf("amostra = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/mia/voice-sample", admin, map[string]any{"voice": "en-US-JennyNeural"}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("voz fora da lista = %d", r.code)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM llm_usage WHERE organization_id IS NULL`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("uso de IA do painel = %d %v", n, err)
	}
}
