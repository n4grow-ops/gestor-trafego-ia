package build_test

import (
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/build"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

func TestProvider(t *testing.T) {
	cases := []struct {
		cfg     config.Config
		name    string
		wantErr bool
	}{
		{config.Config{WhatsAppProvider: "waha", WAHAURL: "http://waha:3000", WAHAAPIKey: "k"}, "waha", false},
		{config.Config{WhatsAppProvider: "waha"}, "", true},
		{config.Config{WhatsAppProvider: "wame", WameKey: "k", WameBaseURL: "https://server.wame.api.br"}, "wame", false},
		{config.Config{WhatsAppProvider: "wame"}, "", true},
		{config.Config{WhatsAppProvider: "zap"}, "", true},
	}
	for _, tc := range cases {
		p, err := build.Provider(tc.cfg)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v", tc.cfg.WhatsAppProvider, err)
			continue
		}
		if err == nil && p.Name() != tc.name {
			t.Errorf("%s: nome %q", tc.cfg.WhatsAppProvider, p.Name())
		}
	}
}

func TestProviderWameOfficial(t *testing.T) {
	p, err := build.Provider(config.Config{WhatsAppProvider: "wame", WameKey: "k", WameOfficial: true})
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := p.(*wame.Client); !ok || !c.Official || c.BaseURL != "https://server.wame.api.br" {
		t.Fatalf("cliente: %+v", p)
	}
}

func TestParsersAlwaysHasBoth(t *testing.T) {
	ps := build.Parsers(config.Config{})
	if ps["waha"] == nil || ps["wame"] == nil {
		t.Fatalf("parsers: %v", ps)
	}
}
