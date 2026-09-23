package config_test

import (
	"testing"

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
