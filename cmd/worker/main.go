// Comando worker: processa webhooks e jobs e roda as duas IAs.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/storage"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/waha"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("worker parou", "erro", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load(os.Getenv)
	if err := cfg.Require("DATABASE_URL", "OPENROUTER_API_KEY", "ATTENDANT_MODEL", "MANAGER_MODEL",
		"TRANSCRIBE_MODEL", "WAHA_URL", "WAHA_API_KEY"); err != nil {
		return err
	}
	// Sem normalizar: o aviso precisa ir ao número exatamente como está no WhatsApp do operador.
	operator := whatsapp.Digits(cfg.OperatorPhone)

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
	wa := waha.New(cfg.WAHAURL, cfg.WAHAAPIKey, cfg.WAHASession)
	router := llm.NewOpenRouter(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey)
	eng := &engine.Engine{
		Store:           st,
		WA:              wa,
		Attendant:       &attendant.Agent{LLM: router, Model: cfg.AttendantModel},
		Manager:         &manager.Agent{LLM: router, Model: cfg.ManagerModel},
		Transcriber:     router,
		TranscribeModel: cfg.TranscribeModel,
		OperatorPhone:   operator,
		Log:             log,

		AutoSignup:       cfg.AutoSignup,
		SignupMaxBudget:  cfg.SignupMaxBudget,
		SignupDailyLimit: cfg.SignupDailyLimit,

		Humanize: cfg.Humanize,
		Debounce: cfg.ReplyDebounce,
		TTSModel: cfg.TTSModel,
		TTSVoice: cfg.TTSVoice,
	}
	if cfg.SignupEnabled() {
		eng.SignupBaseURL = cfg.PublicBaseURL + "/c"
	}
	if cfg.VoiceReplies && cfg.TTSModel != "" {
		eng.Voice = router // voz só com modelo de TTS configurado
	}
	if cfg.SupabaseURL != "" && cfg.SupabaseServiceRoleKey != "" {
		files := storage.New(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey)
		if err := files.EnsureBucket(ctx, engine.CreativesBucket); err != nil {
			log.Error("bucket dos anúncios indisponível; etapa de anúncios desligada", "erro", err)
		} else {
			eng.Storage = files
		}
	}
	eng.ReportPhone = whatsapp.Digits(cfg.ReportPhone)
	if err := eng.EnsureDailyReport(ctx); err != nil {
		log.Error("falha ao agendar relatório diário", "erro", err)
	}
	w := &worker.Worker{Store: st, WA: wa, Engine: eng, Log: log}
	log.Info("worker iniciado", "atendente", cfg.AttendantModel, "gestor", cfg.ManagerModel,
		"humano", cfg.Humanize, "voz", eng.Voice != nil, "cadastro", eng.SignupBaseURL != "",
		"anuncios", eng.Storage != nil, "relatorio", eng.ReportPhone != "")
	return w.Run(ctx)
}
