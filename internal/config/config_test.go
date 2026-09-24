package config_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
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
