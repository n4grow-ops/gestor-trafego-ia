// Package config lê a configuração das variáveis de ambiente.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL       string
	HTTPAddr          string
	WebhookSecret     string
	OpenRouterAPIKey  string
	OpenRouterBaseURL string
	AttendantModel    string
	ManagerModel      string
	TranscribeModel   string
	WAHAURL           string
	WAHAAPIKey        string
	WAHASession       string
	OperatorPhone     string

	// Cadastro automático de número novo.
	AutoSignup       bool
	SignupMaxBudget  float64
	SignupDailyLimit int

	// Atendente humana.
	Humanize      bool
	ReplyDebounce time.Duration
	VoiceReplies  bool
	TTSModel      string
	TTSVoice      string

	// Cadastro do cliente (página + Supabase Auth).
	PublicBaseURL          string
	SupabaseURL            string
	SupabasePublishableKey string
	BotWhatsAppNumber      string
}

// SignupEnabled diz se o cadastro está configurado: sem isso, ninguém é barrado pelo portão.
func (c Config) SignupEnabled() bool {
	return c.PublicBaseURL != "" && c.SupabaseURL != "" && c.SupabasePublishableKey != ""
}

func Load(getenv func(string) string) Config {
	return Config{
		DatabaseURL:       getenv("DATABASE_URL"),
		HTTPAddr:          orDefault(getenv("HTTP_ADDR"), ":8080"),
		WebhookSecret:     getenv("WEBHOOK_SECRET"),
		OpenRouterAPIKey:  getenv("OPENROUTER_API_KEY"),
		OpenRouterBaseURL: orDefault(getenv("OPENROUTER_BASE_URL"), "https://openrouter.ai/api/v1"),
		AttendantModel:    getenv("ATTENDANT_MODEL"),
		ManagerModel:      getenv("MANAGER_MODEL"),
		TranscribeModel:   getenv("TRANSCRIBE_MODEL"),
		WAHAURL:           strings.TrimRight(getenv("WAHA_URL"), "/"),
		WAHAAPIKey:        getenv("WAHA_API_KEY"),
		WAHASession:       orDefault(getenv("WAHA_SESSION"), "default"),
		OperatorPhone:     getenv("OPERATOR_PHONE"),
		AutoSignup:        getenv("AUTO_SIGNUP") == "true",
		SignupMaxBudget:   positiveFloat(getenv("AUTO_SIGNUP_MAX_BUDGET"), 50),
		SignupDailyLimit:  positiveInt(getenv("AUTO_SIGNUP_DAILY_LIMIT"), 60),
		Humanize:          getenv("HUMANIZE") != "false",
		ReplyDebounce:     time.Duration(positiveInt(getenv("REPLY_DEBOUNCE_SECONDS"), 4)) * time.Second,
		VoiceReplies:      getenv("VOICE_REPLIES") != "false",
		TTSModel:          getenv("TTS_MODEL"),
		TTSVoice:          orDefault(getenv("TTS_VOICE"), "pt-BR-FranciscaNeural"),

		PublicBaseURL:          strings.TrimRight(getenv("PUBLIC_BASE_URL"), "/"),
		SupabaseURL:            strings.TrimRight(getenv("SUPABASE_URL"), "/"),
		SupabasePublishableKey: getenv("SUPABASE_PUBLISHABLE_KEY"),
		BotWhatsAppNumber:      getenv("BOT_WHATSAPP_NUMBER"),
	}
}

// Require falha listando as variáveis obrigatórias que estão vazias.
func (c Config) Require(names ...string) error {
	values := map[string]string{
		"DATABASE_URL":       c.DatabaseURL,
		"WEBHOOK_SECRET":     c.WebhookSecret,
		"OPENROUTER_API_KEY": c.OpenRouterAPIKey,
		"ATTENDANT_MODEL":    c.AttendantModel,
		"MANAGER_MODEL":      c.ManagerModel,
		"TRANSCRIBE_MODEL":   c.TranscribeModel,
		"WAHA_URL":           c.WAHAURL,
		"WAHA_API_KEY":       c.WAHAAPIKey,
	}
	var missing []string
	for _, n := range names {
		v, known := values[n]
		if !known {
			return fmt.Errorf("config: variável desconhecida %q", n)
		}
		if v == "" {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("config: variáveis obrigatórias ausentes: %s", strings.Join(missing, ", "))
	}
	return nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func positiveFloat(v string, def float64) float64 {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return f
}

func positiveInt(v string, def int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
