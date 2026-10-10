// Comando worker: processa webhooks e jobs e roda as duas IAs.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/storage"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/build"
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
		"TRANSCRIBE_MODEL"); err != nil {
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
	wa, err := build.Provider(cfg)
	if err != nil {
		return err
	}
	router := llm.NewOpenRouter(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey)
	eng := &engine.Engine{
		Store: st,
		WA:    wa,
		// Namespace histórico das identidades: não muda com a troca de provedor.
		IdentityProvider: "waha",
		Attendant:        &attendant.Agent{LLM: router, Model: cfg.AttendantModel},
		Manager:          &manager.Agent{LLM: router, Model: cfg.ManagerModel},
		Transcriber:      router,
		TranscribeModel:  cfg.TranscribeModel,
		OperatorPhone:    operator,
		Log:              log,

		AutoSignup:       cfg.AutoSignup,
		SignupMaxBudget:  cfg.SignupMaxBudget,
		SignupDailyLimit: cfg.SignupDailyLimit,

		Humanize: cfg.Humanize,
		Debounce: cfg.ReplyDebounce,
		TTSModel: cfg.TTSModel,
		TTSVoice: cfg.TTSVoice,
	}
	// Mia configurada no painel: relida do banco a cada 30 s; sem configuração, o padrão do código.
	eng.Mia = &mia.Cache{TTL: 30 * time.Second, Load: miaLoader(st),
		OnError: func(err error) { log.Error("configuração da Mia indisponível; segue a última boa", "erro", err) }}
	if cfg.SignupEnabled() {
		eng.SignupBaseURL = cfg.PublicBaseURL + "/c"
		eng.SignupFlowID = cfg.SignupFlowID
		eng.SignupFlowDraft = cfg.SignupFlowDraft
		eng.TermsVersion = signup.TermsVersion
	}
	if cfg.MetaConnectEnabled() {
		eng.MetaConnectBaseURL = cfg.PublicBaseURL + "/m"
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
	w := &worker.Worker{Store: st, WA: wa, Parsers: build.Parsers(cfg, wa), Engine: eng, Log: log}
	log.Info("worker iniciado", "whatsapp", wa.Name(), "atendente", cfg.AttendantModel, "gestor", cfg.ManagerModel,
		"humano", cfg.Humanize, "voz", eng.Voice != nil, "cadastro", eng.SignupBaseURL != "", "formulario_cadastro", eng.SignupFlowID != "",
		"anuncios", eng.Storage != nil, "relatorio", eng.ReportPhone != "", "conexao_meta", eng.MetaConnectBaseURL != "")
	return w.Run(ctx)
}

// miaLoader lê a versão em vigor da configuração da Mia.
func miaLoader(st *store.Store) func(ctx context.Context) (mia.Config, error) {
	return func(ctx context.Context) (mia.Config, error) {
		c, _, err := st.MiaCurrent(ctx)
		return c, err
	}
}
