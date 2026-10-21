// Comando api: recebe o webhook do WhatsApp e responde o health check. Roda as migrations.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/httpapi"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
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
	// Login de desenvolvimento em produção: nem sobe.
	if err := cfg.CheckAdminDevLogin(); err != nil {
		return err
	}
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
	// Mia: a versão 1 é o padrão do código; o painel grava as seguintes.
	if seeded, err := st.EnsureMiaSeed(ctx, mia.Defaults()); err != nil {
		return err
	} else if seeded {
		log.Info("configuração da Mia semeada com o padrão do código")
	}
	miaCache := &mia.Cache{TTL: 30 * time.Second, Load: func(ctx context.Context) (mia.Config, error) {
		c, _, err := st.MiaCurrent(ctx)
		return c, err
	}, OnError: func(err error) { log.Error("configuração da Mia indisponível; segue a última boa", "erro", err) }}

	var routes []func(*http.ServeMux)
	if cfg.SignupEnabled() {
		reg := &httpapi.Signup{
			Store: st, Auth: signup.NewSupabaseAuth(cfg.SupabaseURL, cfg.SupabasePublishableKey), CNPJ: signup.NewBrasilAPI(),
			SupabaseURL: cfg.SupabaseURL, SupabaseKey: cfg.SupabasePublishableKey, BotNumber: cfg.BotWhatsAppNumber,
			TermsVersion: termsVersion, Log: log, Limiter: httpapi.NewLimiter(), Mia: miaCache,
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
	adminMode := cfg.AdminMode()
	if adminMode != "" {
		admin, err := newAdmin(ctx, cfg, st, pool, wa, log, miaCache)
		if err != nil {
			return err
		}
		routes = append(routes, admin.Register)
	} else {
		log.Warn("painel desligado: falta ADMIN_SESSION_KEY (32+ caracteres) com SUPABASE_URL e SUPABASE_PUBLISHABLE_KEY")
	}
	log.Info("api configurada", "cadastro", cfg.SignupEnabled(), "conexao_meta", cfg.MetaConnectEnabled(),
		"painel", adminMode, "painel_host", cfg.AdminHost)
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.WithAdminHost(cfg.AdminHost, httpapi.NewMux(pool, webhook, routes...)),
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

// newAdmin monta o painel da equipe e garante os administradores de ADMIN_EMAILS.
func newAdmin(ctx context.Context, cfg config.Config, st *store.Store, pool httpapi.Pinger, wa whatsapp.Provider, log *slog.Logger, miaCache *mia.Cache) (*httpapi.Admin, error) {
	if err := st.EnsureAdmins(ctx, cfg.AdminEmails); err != nil {
		return nil, err
	}
	a := &httpapi.Admin{
		Store: st, DB: pool, Mode: cfg.AdminMode(), SessionKey: []byte(cfg.AdminSessionKey), Secure: true,
		ChatIDForPhone: wa.ChatIDForPhone, BotNumber: cfg.BotWhatsAppNumber, Provider: wa.Name(),
		WindowLimited: cfg.WhatsAppProvider == "wame" && cfg.WameOfficial, USDBRL: cfg.USDBRL,
		Limiter: httpapi.NewLimiter(), Log: log, Mia: miaCache, MetaAppID: cfg.MetaAppID,
	}
	// Fotos e vídeos dos clientes no painel: a Wame baixa pelo id da mensagem (o WAHA precisaria do payload).
	if wa.Name() == "wame" {
		a.Media = func(ctx context.Context, ref string) ([]byte, string, error) {
			return wa.DownloadMedia(ctx, whatsapp.Inbound{ExternalID: ref})
		}
	}
	// "Testar a Mia" e "ouvir amostra" usam a mesma IA e o mesmo TTS do worker, se a api tiver as chaves.
	if cfg.OpenRouterAPIKey != "" {
		router := llm.NewBudget(llm.NewOpenRouter(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey), st, cfg.OpenRouterAPIKey, cfg.LLMBudgetUSD, log)
		if cfg.AttendantModel != "" {
			a.MiaTest = &attendant.Agent{LLM: router, Model: cfg.AttendantModel}
		}
		if cfg.TTSModel != "" {
			a.MiaVoice = &httpapi.MiaSpeaker{Speaker: router, Model: cfg.TTSModel, DefaultVoice: cfg.TTSVoice}
		}
	}
	if cfg.PublicBaseURL != "" {
		a.SignupBaseURL = cfg.PublicBaseURL + "/c"
	}
	switch a.Mode {
	case "otp":
		a.Auth = signup.NewSupabaseAuth(cfg.SupabaseURL, cfg.SupabasePublishableKey)
	case "dev":
		// Só sem Supabase (config.AdminMode garante): http local, sem Secure, chave nova a cada partida se faltar.
		log.Warn("painel em MODO DE DESENVOLVIMENTO: login só com e-mail; nunca use em produção")
		a.Secure = false
		if len(a.SessionKey) < 32 {
			a.SessionKey = make([]byte, 32)
			rand.Read(a.SessionKey)
		}
	}
	return a, nil
}
