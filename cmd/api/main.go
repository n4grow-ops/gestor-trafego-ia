// Comando api: recebe o webhook do WhatsApp e responde o health check. Roda as migrations.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/build"
)

// termsVersion é a versão dos Termos e da Política de Privacidade gravada em cada aceite.
const termsVersion = signup.TermsVersion

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("api parou", "erro", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load(os.Getenv)
	if err := cfg.Require("DATABASE_URL", "WEBHOOK_SECRET"); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	st := store.New(pool)
	wa, err := build.Provider(cfg)
	if err != nil {
		return err
	}
	webhook := &httpapi.Webhook{
		Store:  st,
		WA:     wa, // só o parse é usado aqui
		Secret: cfg.WebhookSecret,
		Log:    log,
	}
	var routes []func(*http.ServeMux)
	if cfg.SignupEnabled() {
		reg := &httpapi.Signup{
			Store: st, Auth: signup.NewSupabaseAuth(cfg.SupabaseURL, cfg.SupabasePublishableKey), CNPJ: signup.NewBrasilAPI(),
			SupabaseURL: cfg.SupabaseURL, SupabaseKey: cfg.SupabasePublishableKey, BotNumber: cfg.BotWhatsAppNumber,
			TermsVersion: termsVersion, Log: log, Limiter: httpapi.NewLimiter(),
		}
		if cfg.BotWhatsAppNumber == "" {
			log.Warn("BOT_WHATSAPP_NUMBER vazio: o botão de voltar ao WhatsApp fica sem número")
		}
		routes = append(routes, reg.Register)
	}
	if cfg.MetaConnectEnabled() {
		box, err := secretbox.New(cfg.MetaTokenKey)
		if err != nil {
			return err
		}
		mc := &httpapi.MetaConnect{
			Store: st, Meta: meta.New(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaLoginConfigID, cfg.PublicBaseURL+"/meta/callback"),
			Box: box, AppSecret: cfg.MetaAppSecret, BaseURL: cfg.PublicBaseURL, BotNumber: cfg.BotWhatsAppNumber,
			Log: log, Limiter: httpapi.NewLimiter(),
		}
		routes = append(routes, mc.Register)
	}
	log.Info("api configurada", "cadastro", cfg.SignupEnabled(), "conexao_meta", cfg.MetaConnectEnabled())
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewMux(pool, webhook, routes...),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("api ouvindo", "addr", cfg.HTTPAddr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
