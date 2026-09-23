// Package config lê a configuração das variáveis de ambiente.
package config

import (
	"fmt"
	"strings"
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
	OpenWAURL         string
	OpenWAAPIKey      string
	OperatorPhone     string
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
		OpenWAURL:         strings.TrimRight(getenv("OPENWA_URL"), "/"),
		OpenWAAPIKey:      getenv("OPENWA_API_KEY"),
		OperatorPhone:     getenv("OPERATOR_PHONE"),
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
		"OPENWA_URL":         c.OpenWAURL,
		"OPENWA_API_KEY":     c.OpenWAAPIKey,
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
