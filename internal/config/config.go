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
	// Provedor do WhatsApp: "waha" (padrão) ou "wame". Wame oficial = instância da Cloud API.
	WhatsAppProvider string
	WameBaseURL      string
	WameKey          string
	WameToken        string
	WameOfficial     bool
	OperatorPhone    string

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
	// SignupFlowID: formulário de cadastro do WhatsApp publicado (Cloud API). Vazio = link da página.
	SignupFlowID    string
	SignupFlowDraft bool

	// Anúncios no Supabase Storage (só no worker) e relatório diário.
	SupabaseServiceRoleKey string
	ReportPhone            string

	// Conexão da conta de anúncios do cliente (Facebook Login for Business).
	MetaAppID         string
	MetaAppSecret     string
	MetaLoginConfigID string
	MetaTokenKey      string // 32 bytes em base64: cifra os tokens dos clientes

	// Painel da equipe (admin).
	AdminSessionKey string   // assina o cookie de sessão; 32 caracteres ou mais
	AdminEmails     []string // garantidos como administradores na partida
	AdminHost       string   // host que responde só o painel; com ele, /admin não existe em outro host
	AdminDevLogin   bool     // login só com e-mail, aceito apenas sem SUPABASE_URL (desenvolvimento local)
	USDBRL          float64  // cotação para o custo de IA no faturamento
}

// AdminMode diz como o painel faz login: "otp" (código por e-mail no Supabase Auth), "dev"
// (só e-mail, desenvolvimento local sem Supabase) ou "" (painel desligado).
func (c Config) AdminMode() string {
	switch {
	case c.SupabaseURL != "" && c.SupabasePublishableKey != "":
		if len(c.AdminSessionKey) >= 32 {
			return "otp"
		}
		return ""
	case c.SupabaseURL == "" && c.AdminDevLogin:
		return "dev"
	}
	return ""
}

// SignupEnabled diz se o cadastro está configurado: sem isso, ninguém é barrado pelo portão.
func (c Config) SignupEnabled() bool {
	return c.PublicBaseURL != "" && c.SupabaseURL != "" && c.SupabasePublishableKey != ""
}

// MetaConnectEnabled diz se a conexão da conta Meta está configurada.
func (c Config) MetaConnectEnabled() bool {
	return c.PublicBaseURL != "" && c.MetaAppID != "" && c.MetaAppSecret != "" && c.MetaLoginConfigID != "" && c.MetaTokenKey != ""
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
		WhatsAppProvider:  orDefault(strings.ToLower(strings.TrimSpace(getenv("WHATSAPP_PROVIDER"))), "waha"),
		WameBaseURL:       strings.TrimRight(orDefault(getenv("WAME_BASE_URL"), "https://server.wame.api.br"), "/"),
		WameKey:           getenv("WAME_KEY"),
		WameToken:         getenv("WAME_TOKEN"),
		WameOfficial:      getenv("WAME_OFFICIAL") == "true",
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
		SignupFlowID:           strings.TrimSpace(getenv("SIGNUP_FLOW_ID")),
		SignupFlowDraft:        getenv("SIGNUP_FLOW_DRAFT") == "true",
		SupabaseServiceRoleKey: getenv("SUPABASE_SERVICE_ROLE_KEY"),
		ReportPhone:            orDefault(getenv("REPORT_PHONE"), getenv("OPERATOR_PHONE")),
		MetaAppID:              getenv("META_APP_ID"),
		MetaAppSecret:          getenv("META_APP_SECRET"),
		MetaLoginConfigID:      getenv("META_LOGIN_CONFIG_ID"),
		MetaTokenKey:           getenv("META_TOKEN_KEY"),

		AdminSessionKey: getenv("ADMIN_SESSION_KEY"),
		AdminEmails:     splitList(getenv("ADMIN_EMAILS")),
		AdminHost:       adminHost(getenv),
		AdminDevLogin:   getenv("ADMIN_DEV_LOGIN") == "true",
		USDBRL:          positiveFloat(strings.Replace(getenv("USD_BRL"), ",", ".", 1), 5.5),
	}
}

// adminHost: ADMIN_HOST ou admin.meuanuncio.io. Com ADMIN_DEV_LOGIN=true e sem ADMIN_HOST fica vazio:
// no desenvolvimento local o painel responde em /admin de qualquer host.
func adminHost(getenv func(string) string) string {
	h := strings.ToLower(strings.TrimSpace(getenv("ADMIN_HOST")))
	if h == "" && getenv("ADMIN_DEV_LOGIN") != "true" {
		h = "admin.meuanuncio.io"
	}
	return h
}

// splitList separa uma lista por vírgulas, sem espaços nem itens vazios.
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
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
		"WAME_KEY":           c.WameKey,
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
