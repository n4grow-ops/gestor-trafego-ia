// Comando worker: processa webhooks e jobs e roda as duas IAs.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/config"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/copylab"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/db"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/instagram"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/storage"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/build"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp/wame"
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
	router := llm.NewBudget(llm.NewOpenRouter(cfg.OpenRouterBaseURL, cfg.OpenRouterAPIKey), st, cfg.OpenRouterAPIKey, cfg.LLMBudgetUSD, log)
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
	if cfg.PublicBaseURL != "" {
		eng.LeadPrivacyURL = cfg.PublicBaseURL + "/privacidade"
	}
	if cfg.MetaConnectEnabled() {
		eng.MetaConnectBaseURL = cfg.PublicBaseURL + "/m"
		// Testador automático: lê os testadores do app (token APP_ID|SECRET) a cada 5 min.
		eng.MetaAppID = cfg.MetaAppID
		eng.AppTesters = meta.New(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaLoginConfigID, cfg.PublicBaseURL+"/meta/callback").AppTesters
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
			// prévias e mídia da conversa: guardadas com link para o painel
			for _, b := range []string{engine.PreviewsBucket, engine.ChatMediaBucket} {
				if err := files.EnsureBucket(ctx, b); err != nil {
					log.Error("bucket indisponível", "bucket", b, "erro", err)
				}
			}
		}
		// Conta oficial: imagem (prévia do anúncio) só vai por link. Sobe num bucket privado e manda o
		// link temporário.
		if c, ok := wa.(*wame.Client); ok && c.Official {
			if err := files.EnsureBucket(ctx, engine.PreviewsBucket); err != nil {
				log.Error("bucket das prévias indisponível; prévia do anúncio não vai no WhatsApp oficial", "erro", err)
			} else {
				c.MediaURL = files.TempLinks(engine.PreviewsBucket, 24*60*60)
			}
		}
	}
	if cfg.MetaPublishEnabled() {
		box, err := secretbox.New(cfg.MetaTokenKey)
		if err != nil {
			return err
		}
		eng.MetaTokenKey = box
		client := meta.New(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaLoginConfigID, cfg.PublicBaseURL+"/meta/callback")
		// O cliente da Meta passa a ser Publisher quando o publish.go do pacote meta existir;
		// até lá a publicação fica desligada e o cliente ouve que ela ainda não está ligada.
		if p, ok := any(client).(meta.Publisher); ok {
			eng.Publisher = p
		} else {
			log.Warn("cliente da Meta ainda sem publicação; publicação automática desligada")
		}
	}
	if eng.Storage != nil {
		// Link de post do Instagram na etapa dos anúncios: da conta conectada pela Graph API (com a
		// publicação ligada, que abre o token do cliente) ou da página pública do post.
		var graph instagram.Graph
		if eng.MetaTokenKey != nil {
			graph = meta.New(cfg.MetaAppID, cfg.MetaAppSecret, cfg.MetaLoginConfigID, cfg.PublicBaseURL+"/meta/callback")
		}
		eng.Instagram = instagram.New(graph)
	}
	eng.ReportPhone = whatsapp.Digits(cfg.ReportPhone)
	// Proteções do cliente: gasto e janela mínimos antes de julgar o custo por conversa (padrão R$ 30 e 24 h).
	if v, err := strconv.ParseFloat(strings.Replace(os.Getenv("GUARDRAIL_MIN_SPEND_BRL"), ",", ".", 1), 64); err == nil && v > 0 {
		eng.GuardrailMinSpendCents = int64(v * 100)
	}
	if v, err := strconv.ParseFloat(strings.Replace(os.Getenv("GUARDRAIL_MIN_WINDOW_HOURS"), ",", ".", 1), 64); err == nil && v > 0 {
		eng.GuardrailMinWindow = time.Duration(v * float64(time.Hour))
	}
	// Canal da equipe: privado de cada número e, opcional, o grupo por uma instância Wame não
	// oficial (QR) — o número oficial não manda para grupo. Ela só envia: não tem webhook aqui.
	eng.TeamPhones = cfg.TeamPhones
	// Números marcados no painel (Equipe → "Recebe avisos") valem antes do TEAM_PHONES.
	eng.TeamDirectory = st.TeamAlertPhones
	if cfg.TeamGroupEnabled() {
		eng.TeamGroup = wame.New(cfg.WameBaseURL, cfg.TeamGroupWameKey, cfg.TeamGroupWameToken)
		eng.TeamGroupJID = cfg.TeamGroupJID
	}
	if cfg.AdminHost != "" {
		eng.AdminURL = "https://" + cfg.AdminHost + "/"
	}
	if err := eng.EnsureCleanup(ctx); err != nil {
		log.Error("falha ao agendar limpeza diária", "erro", err)
	}
	if err := eng.EnsureTokenCheck(ctx); err != nil {
		return err
	}
	// Escudo anti-bloqueio: situação da conta de anúncios dos clientes conectados, de tempos em tempos.
	if err := eng.EnsureShieldHealth(ctx); err != nil {
		log.Error("falha ao agendar a saúde da conta de anúncios", "erro", err)
	}
	// Reprovação de anúncio: o texto sugerido vem do laboratório de copy.
	eng.CopyFix = copylab.Fix
	if err := eng.EnsureDailyResults(ctx); err != nil {
		return err
	}
	if err := eng.EnsureGuardrailCheck(ctx); err != nil {
		log.Error("falha ao agendar a conferência das proteções", "erro", err)
	}
	if err := eng.EnsureDailyReport(ctx); err != nil {
		log.Error("falha ao agendar relatório diário", "erro", err)
	}
	if err := eng.EnsureTesterPoll(ctx); err != nil {
		log.Error("falha ao agendar leitura dos testadores", "erro", err)
	}
	w := &worker.Worker{Store: st, WA: wa, Parsers: build.Parsers(cfg, wa), Engine: eng, Log: log}
	log.Info("worker iniciado", "whatsapp", wa.Name(), "atendente", cfg.AttendantModel, "gestor", cfg.ManagerModel,
		"humano", cfg.Humanize, "voz", eng.Voice != nil, "cadastro", eng.SignupBaseURL != "", "formulario_cadastro", eng.SignupFlowID != "",
		"anuncios", eng.Storage != nil, "relatorio", eng.ReportPhone != "", "conexao_meta", eng.MetaConnectBaseURL != "",
		"publicacao_meta", eng.Publisher != nil && eng.MetaTokenKey != nil,
		"equipe_numeros", len(eng.TeamPhones), "equipe_grupo", eng.TeamGroup != nil,
		"testador_auto", eng.AppTesters != nil, "post_instagram", eng.Instagram != nil)
	return w.Run(ctx)
}

// miaLoader lê a versão em vigor da configuração da Mia.
func miaLoader(st *store.Store) func(ctx context.Context) (mia.Config, error) {
	return func(ctx context.Context) (mia.Config, error) {
		c, _, err := st.MiaCurrent(ctx)
		return c, err
	}
}
