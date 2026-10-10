package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestTeamChannel(t *testing.T) {
	c := config.Load(env(map[string]string{"OPERATOR_PHONE": "+55 (11) 99999-8888"}))
	if len(c.TeamPhones) != 1 || c.TeamPhones[0] != "5511999998888" || c.TeamGroupEnabled() {
		t.Fatalf("padrão = OPERATOR_PHONE: %+v grupo=%v", c.TeamPhones, c.TeamGroupEnabled())
	}
	// Números como estão no WhatsApp (sem inserir o nono dígito), sem repetidos nem inválidos.
	c = config.Load(env(map[string]string{"OPERATOR_PHONE": "5511999998888", "TEAM_PHONES": " 551188887777, 5511977776666 ,123, 551188887777,"}))
	if strings.Join(c.TeamPhones, ",") != "551188887777,5511977776666" {
		t.Fatalf("TEAM_PHONES = %q", c.TeamPhones)
	}
	if c = config.Load(env(map[string]string{})); len(c.TeamPhones) != 0 {
		t.Fatalf("sem números = %q", c.TeamPhones)
	}
	c = config.Load(env(map[string]string{"TEAM_GROUP_JID": " 120363000000000001@g.us ", "TEAM_GROUP_WAME_KEY": "gk"}))
	if !c.TeamGroupEnabled() || c.TeamGroupJID != "120363000000000001@g.us" || c.TeamGroupWameKey != "gk" || c.TeamGroupWameToken != "" {
		t.Fatalf("grupo: %+v", c)
	}
	c = config.Load(env(map[string]string{"TEAM_GROUP_JID": "120363000000000001", "TEAM_GROUP_WAME_KEY": "gk", "TEAM_GROUP_WAME_TOKEN": "gt"}))
	if c.TeamGroupJID != "120363000000000001@g.us" || c.TeamGroupWameToken != "gt" {
		t.Fatalf("JID sem sufixo: %q", c.TeamGroupJID)
	}
	if config.Load(env(map[string]string{"TEAM_GROUP_JID": "120363000000000001@g.us"})).TeamGroupEnabled() {
		t.Fatal("sem a key da instância o grupo fica desligado")
	}
}

func TestLoadDefaults(t *testing.T) {
	c := config.Load(env(nil))
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.OpenRouterBaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("OpenRouterBaseURL = %q", c.OpenRouterBaseURL)
	}
}

func TestLoadTrimsWAHAURL(t *testing.T) {
	c := config.Load(env(map[string]string{"WAHA_URL": "http://waha:3000/"}))
	if c.WAHAURL != "http://waha:3000" {
		t.Errorf("WAHAURL = %q", c.WAHAURL)
	}
	if c.WAHASession != "default" {
		t.Errorf("WAHASession = %q", c.WAHASession)
	}
}

func TestRequireListsMissing(t *testing.T) {
	c := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	err := c.Require("DATABASE_URL", "WEBHOOK_SECRET", "OPENROUTER_API_KEY")
	want := "config: variáveis obrigatórias ausentes: WEBHOOK_SECRET, OPENROUTER_API_KEY"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestRequireUnknownName(t *testing.T) {
	if err := config.Load(env(nil)).Require("NOPE"); err == nil {
		t.Fatal("esperava erro para variável desconhecida")
	}
}

func TestRequireOK(t *testing.T) {
	c := config.Load(env(map[string]string{"DATABASE_URL": "x", "WEBHOOK_SECRET": "y"}))
	if err := c.Require("DATABASE_URL", "WEBHOOK_SECRET"); err != nil {
		t.Fatal(err)
	}
}

func TestAutoSignupConfig(t *testing.T) {
	c := config.Load(env(nil))
	if c.AutoSignup || c.SignupMaxBudget != 50 || c.SignupDailyLimit != 60 {
		t.Errorf("padrões = %+v", c)
	}
	c = config.Load(env(map[string]string{"AUTO_SIGNUP": "true", "AUTO_SIGNUP_MAX_BUDGET": "30", "AUTO_SIGNUP_DAILY_LIMIT": "20"}))
	if !c.AutoSignup || c.SignupMaxBudget != 30 || c.SignupDailyLimit != 20 {
		t.Errorf("configurado = %+v", c)
	}
	c = config.Load(env(map[string]string{"AUTO_SIGNUP_MAX_BUDGET": "abc", "AUTO_SIGNUP_DAILY_LIMIT": "-1"}))
	if c.SignupMaxBudget != 50 || c.SignupDailyLimit != 60 {
		t.Errorf("valores inválidos deveriam cair no padrão: %+v", c)
	}
}

func TestHumanizeConfig(t *testing.T) {
	c := config.Load(env(nil))
	if !c.Humanize || c.ReplyDebounce != 4*time.Second || !c.VoiceReplies || c.TTSModel != "" || c.TTSVoice != "pt-BR-FranciscaNeural" {
		t.Fatalf("padrões = %+v", c)
	}
	c = config.Load(env(map[string]string{"HUMANIZE": "false", "REPLY_DEBOUNCE_SECONDS": "7", "VOICE_REPLIES": "false", "TTS_MODEL": "m", "TTS_VOICE": "alloy"}))
	if c.Humanize || c.ReplyDebounce != 7*time.Second || c.VoiceReplies || c.TTSModel != "m" || c.TTSVoice != "alloy" {
		t.Fatalf("lidos = %+v", c)
	}
}

func TestSignupConfig(t *testing.T) {
	c := config.Load(env(map[string]string{"PUBLIC_BASE_URL": "https://cadastro.x.com/", "SUPABASE_URL": "https://ref.supabase.co/",
		"SUPABASE_PUBLISHABLE_KEY": "pub", "BOT_WHATSAPP_NUMBER": "+55 11 90000-0000"}))
	if c.PublicBaseURL != "https://cadastro.x.com" || c.SupabaseURL != "https://ref.supabase.co" || c.SupabasePublishableKey != "pub" || c.BotWhatsAppNumber != "+55 11 90000-0000" {
		t.Fatalf("cadastro = %+v", c)
	}
	if c.SignupEnabled() != true || config.Load(env(nil)).SignupEnabled() {
		t.Fatal("cadastro ligado só com PUBLIC_BASE_URL e Supabase")
	}
}

func TestCreativesAndReportConfig(t *testing.T) {
	c := config.Load(env(map[string]string{"SUPABASE_SERVICE_ROLE_KEY": "srv", "OPERATOR_PHONE": "5511900000000"}))
	if c.SupabaseServiceRoleKey != "srv" || c.ReportPhone != "5511900000000" {
		t.Fatalf("config = %+v", c)
	}
	c = config.Load(env(map[string]string{"OPERATOR_PHONE": "5511900000000", "REPORT_PHONE": "5511911111111"}))
	if c.ReportPhone != "5511911111111" {
		t.Fatalf("REPORT_PHONE tem prioridade: %q", c.ReportPhone)
	}
}

func TestMetaConnectEnabled(t *testing.T) {
	vars := map[string]string{"PUBLIC_BASE_URL": "https://cadastro.x/", "META_APP_ID": "1", "META_APP_SECRET": "s",
		"META_LOGIN_CONFIG_ID": "c", "META_TOKEN_KEY": "k"}
	c := config.Load(env(vars))
	if !c.MetaConnectEnabled() || c.MetaAppID != "1" || c.MetaAppSecret != "s" || c.MetaLoginConfigID != "c" || c.MetaTokenKey != "k" {
		t.Fatalf("config = %+v", c)
	}
	for _, k := range []string{"PUBLIC_BASE_URL", "META_APP_ID", "META_APP_SECRET", "META_LOGIN_CONFIG_ID", "META_TOKEN_KEY"} {
		old := vars[k]
		vars[k] = ""
		if config.Load(env(vars)).MetaConnectEnabled() {
			t.Errorf("ligado sem %s", k)
		}
		vars[k] = old
	}
}

func TestLoadWhatsAppProvider(t *testing.T) {
	c := config.Load(env(map[string]string{}))
	if c.WhatsAppProvider != "waha" || c.WameBaseURL != "https://server.wame.api.br" || c.WameOfficial {
		t.Fatalf("padrões: %q %q %v", c.WhatsAppProvider, c.WameBaseURL, c.WameOfficial)
	}
	c = config.Load(env(map[string]string{"WHATSAPP_PROVIDER": " Wame ", "WAME_KEY": "k", "WAME_TOKEN": "t", "WAME_BASE_URL": "https://x/", "WAME_OFFICIAL": "true"}))
	if c.WhatsAppProvider != "wame" || c.WameKey != "k" || c.WameToken != "t" || c.WameBaseURL != "https://x" || !c.WameOfficial {
		t.Fatalf("%+v", c)
	}
	if err := c.Require("WAME_KEY"); err != nil {
		t.Fatal(err)
	}
}

func TestAdminConfig(t *testing.T) {
	c := config.Load(env(nil))
	if c.AdminHost != "admin.meuanuncio.io" || c.USDBRL != 5.5 || c.AdminMode() != "" {
		t.Fatalf("padrões: host=%q cotação=%v modo=%q", c.AdminHost, c.USDBRL, c.AdminMode())
	}
	key := "0123456789abcdef0123456789abcdef"
	c = config.Load(env(map[string]string{
		"ADMIN_SESSION_KEY": key, "ADMIN_EMAILS": " dono@x.io, ,Outro@X.io ", "ADMIN_HOST": "Painel.Exemplo.com",
		"SUPABASE_URL": "https://p.supabase.co", "SUPABASE_PUBLISHABLE_KEY": "pub", "USD_BRL": "5,1",
	}))
	if c.AdminMode() != "otp" || len(c.AdminEmails) != 2 || c.AdminEmails[1] != "Outro@X.io" || c.AdminHost != "painel.exemplo.com" || c.USDBRL != 5.1 {
		t.Fatalf("produção: %+v modo=%q", c, c.AdminMode())
	}
	// Chave curta desliga o painel em produção.
	c = config.Load(env(map[string]string{"ADMIN_SESSION_KEY": "curta", "SUPABASE_URL": "https://p.supabase.co", "SUPABASE_PUBLISHABLE_KEY": "pub"}))
	if c.AdminMode() != "" {
		t.Fatalf("chave curta: %q", c.AdminMode())
	}
	// Login de desenvolvimento só sem Supabase; com Supabase configurado ele é ignorado.
	c = config.Load(env(map[string]string{"ADMIN_DEV_LOGIN": "true"}))
	if c.AdminMode() != "dev" {
		t.Fatalf("dev: %q", c.AdminMode())
	}
	// No desenvolvimento local sem ADMIN_HOST, o painel fica em /admin (o host padrão o esconderia).
	if c.AdminHost != "" {
		t.Fatalf("dev sem ADMIN_HOST: host=%q", c.AdminHost)
	}
	c = config.Load(env(map[string]string{"ADMIN_DEV_LOGIN": "true", "ADMIN_SESSION_KEY": key, "SUPABASE_URL": "https://p.supabase.co", "SUPABASE_PUBLISHABLE_KEY": "pub"}))
	if c.AdminMode() != "otp" {
		t.Fatalf("dev com Supabase: %q", c.AdminMode())
	}
}

func TestAdminDevLoginRefusedInProduction(t *testing.T) {
	for name, m := range map[string]map[string]string{
		"https":      {"ADMIN_DEV_LOGIN": "true", "PUBLIC_BASE_URL": "https://meuanuncio.io"},
		"HTTPS":      {"ADMIN_DEV_LOGIN": "true", "PUBLIC_BASE_URL": "HTTPS://meuanuncio.io"},
		"admin host": {"ADMIN_DEV_LOGIN": "true", "ADMIN_HOST": "admin.meuanuncio.io"},
		"com supabase": {"ADMIN_DEV_LOGIN": "true", "ADMIN_HOST": "admin.meuanuncio.io", "SUPABASE_URL": "https://p.supabase.co",
			"SUPABASE_PUBLISHABLE_KEY": "pub", "ADMIN_SESSION_KEY": "0123456789abcdef0123456789abcdef"},
	} {
		if err := config.Load(env(m)).CheckAdminDevLogin(); err == nil {
			t.Errorf("%s: modo dev aceito em produção", name)
		}
	}
	for name, m := range map[string]map[string]string{
		"local":     {"ADMIN_DEV_LOGIN": "true", "PUBLIC_BASE_URL": "http://localhost:8080"},
		"sem dev":   {"PUBLIC_BASE_URL": "https://meuanuncio.io"},
		"dev vazio": {"ADMIN_DEV_LOGIN": "true"},
		"produção":  {"ADMIN_HOST": "admin.meuanuncio.io", "PUBLIC_BASE_URL": "https://meuanuncio.io"},
	} {
		if err := config.Load(env(m)).CheckAdminDevLogin(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
