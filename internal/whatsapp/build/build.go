// Package build escolhe o adaptador de WhatsApp pela configuração.
package build

import (
	"fmt"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/waha"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
)

// Provider devolve o adaptador ativo; falha se faltar configuração.
func Provider(cfg config.Config) (whatsapp.Provider, error) {
	switch cfg.WhatsAppProvider {
	case "waha":
		if err := cfg.Require("WAHA_URL", "WAHA_API_KEY"); err != nil {
			return nil, err
		}
		return waha.New(cfg.WAHAURL, cfg.WAHAAPIKey, cfg.WAHASession), nil
	case "wame":
		if err := cfg.Require("WAME_KEY"); err != nil {
			return nil, err
		}
		return newWame(cfg), nil
	default:
		return nil, fmt.Errorf("config: WHATSAPP_PROVIDER inválido %q (use waha ou wame)", cfg.WhatsAppProvider)
	}
}

func newWame(cfg config.Config) *wame.Client {
	c := wame.New(cfg.WameBaseURL, cfg.WameKey, cfg.WameToken)
	c.Official = cfg.WameOfficial
	return c
}

// Parsers lê eventos de qualquer provedor já usado: na troca, o inbox ainda pode ter eventos do outro.
// O provedor ativo entra com a mesma instância que envia: a Wame oficial guarda nela a última
// mensagem de cada chat, usada no visto e no "digitando…".
func Parsers(cfg config.Config, active whatsapp.Provider) map[string]whatsapp.Provider {
	ps := map[string]whatsapp.Provider{
		"waha": waha.New(cfg.WAHAURL, cfg.WAHAAPIKey, cfg.WAHASession),
		"wame": newWame(cfg),
	}
	if active != nil {
		ps[active.Name()] = active
	}
	return ps
}
