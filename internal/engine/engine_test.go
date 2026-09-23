package engine_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/manager"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

var ctx = context.Background()

const (
	phone        = "5511988887777"
	chatID       = phone + "@c.us"
	operatorChat = "5511900000000@c.us"

	turnOffer = `{"intent":"create_campaign","fields":{"offer":"Pizza grande"},"reply":"Legal! Para quem e em qual cidade?"}`
	turnRest  = `{"intent":"provide_info","fields":{"audience":"famílias","location":"Campinas","daily_budget_brl":50},"reply":"Perfeito, vou pedir a proposta ao especialista."}`
)

type sent struct{ ChatID, Text string }

// fakeWA grava o que seria enviado ao WhatsApp.
type fakeWA struct {
	mu       sync.Mutex
	sent     []sent
	media    []byte
	mime     string
	mediaErr error
	fail     bool     // simula o WhatsApp fora do ar
	events   []string // visto, presenças, voz e textos, na ordem
	voices   []sent
}

func (f *fakeWA) Name() string { return "openwa" }
func (f *fakeWA) ParseWebhook([]byte) (whatsapp.Inbound, bool, error) {
	return whatsapp.Inbound{}, false, nil
}
func (f *fakeWA) SendText(_ context.Context, chatID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("whatsapp fora do ar")
	}
	f.sent = append(f.sent, sent{chatID, text})
	f.events = append(f.events, "text:"+text)
	return nil
}
func (f *fakeWA) MarkSeen(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "seen")
	return nil
}
func (f *fakeWA) SetPresence(_ context.Context, _ string, p whatsapp.Presence) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, string(p))
	return nil
}
func (f *fakeWA) SendVoice(_ context.Context, chatID string, audio []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("whatsapp fora do ar")
	}
	f.events = append(f.events, "voice")
	f.voices = append(f.voices, sent{chatID, string(audio)})
	return nil
}

// log devolve visto, presenças, voz e textos na ordem em que aconteceram.
func (f *fakeWA) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}
func (f *fakeWA) DownloadMedia(context.Context, whatsapp.Inbound) ([]byte, string, error) {
	return f.media, f.mime, f.mediaErr
}

func (f *fakeWA) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}
func (f *fakeWA) ChatIDForPhone(p string) string { return p + "@c.us" }

func (f *fakeWA) last() sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return sent{}
	}
	return f.sent[len(f.sent)-1]
}

func (f *fakeWA) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeWA) to(chat string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.sent {
		if s.ChatID == chat {
			out = append(out, s.Text)
		}
	}
	return out
}

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	st   *store.Store
	wa   *fakeWA
	att  *llm.Fake
	mgr  *llm.Fake
	eng  *engine.Engine
	now  time.Time
	seq  int
}

func newHarness(t *testing.T, role string) *harness {
	t.Helper()
	pool := testdb.New(t)
	h := &harness{t: t, pool: pool, st: store.New(pool), wa: &fakeWA{}, att: &llm.Fake{}, mgr: &llm.Fake{},
		now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	org, err := h.st.CreateOrganization(ctx, "Pizzaria Boa", "restaurante", 100, []string{"OUTCOME_ENGAGEMENT"})
	h.must(err)
	mem, err := h.st.CreateMember(ctx, org, "Ana", role)
	h.must(err)
	h.must(h.st.LinkPhone(ctx, "openwa", phone, mem))
	h.eng = &engine.Engine{
		Store:           h.st,
		WA:              h.wa,
		Attendant:       &attendant.Agent{LLM: h.att, Model: "atendente"},
		Manager:         &manager.Agent{LLM: h.mgr, Model: "gestor"},
		Transcriber:     h.att,
		TranscribeModel: "transcritor",
		OperatorPhone:   "5511900000000",
		Now:             func() time.Time { return h.now },
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return h
}

func (h *harness) must(err error) {
	h.t.Helper()
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) say(text string) {
	h.t.Helper()
	h.seq++
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: fmt.Sprintf("m%d", h.seq), ChatID: chatID, Phone: phone, Kind: whatsapp.KindText, Text: text}))
}

func (h *harness) conv() store.Conversation {
	h.t.Helper()
	ident, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	c, err := h.st.GetOrCreateConversation(ctx, ident.ID, "")
	h.must(err)
	return c
}

func (h *harness) pendingJobs(kind string) int {
	h.t.Helper()
	var n int
	h.must(h.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE status = 'pending' AND kind = $1`, kind).Scan(&n))
	return n
}

func (h *harness) count(sql string, args ...any) int {
	h.t.Helper()
	var n int
	h.must(h.pool.QueryRow(ctx, sql, args...).Scan(&n))
	return n
}

func TestUnknownPhoneGetsFixedMessage(t *testing.T) {
	h := newHarness(t, "owner")
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: "x", ChatID: "5511911112222@c.us", Phone: "5511911112222", Kind: whatsapp.KindText, Text: "oi"}))
	got := h.wa.last()
	if got.ChatID != "5511911112222@c.us" || !strings.Contains(got.Text, "não está liberado") {
		t.Fatalf("enviado = %+v", got)
	}
	if h.att.Calls() != 0 {
		t.Fatal("não deveria chamar a IA")
	}
}

func TestCollectsFieldsThenRequestsProposal(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer, turnRest}

	h.say("quero anunciar pizza")
	c := h.conv()
	if c.State != store.StateCollecting || *c.Fields.Offer != "Pizza grande" {
		t.Fatalf("após 1º turno = %+v", c)
	}
	if got := h.wa.last(); got.ChatID != chatID || got.Text != "Legal! Para quem e em qual cidade?" {
		t.Errorf("resposta = %+v", got)
	}
	if h.pendingJobs(engine.JobPropose) != 0 {
		t.Fatal("proposta pedida cedo demais")
	}

	h.say("famílias em Campinas, 50 por dia")
	c = h.conv()
	if c.State != store.StateAwaitingProposal || *c.Fields.DailyBudgetBRL != 50 || *c.Fields.Offer != "Pizza grande" {
		t.Fatalf("após 2º turno = %+v", c)
	}
	if h.pendingJobs(engine.JobPropose) != 1 {
		t.Fatal("esperava 1 job de proposta")
	}
	user := h.att.Requests[1].Messages[1].Content
	for _, want := range []string{"Cliente: <cliente>quero anunciar pizza</cliente>", "Atendente: Legal!", "<cliente>famílias em Campinas, 50 por dia</cliente>"} {
		if !strings.Contains(user, want) {
			t.Errorf("2º pedido sem %q:\n%s", want, user)
		}
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out'`); n != 2 {
		t.Errorf("mensagens de saída = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM llm_usage WHERE agent = 'attendant'`); n != 2 {
		t.Errorf("uso do atendente = %d", n)
	}
}

func TestBudgetAboveLimitIsRefused(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"Pizza","daily_budget_brl":500},"reply":"ok"}`}
	h.say("quero gastar 500 por dia")
	c := h.conv()
	if c.Fields.DailyBudgetBRL != nil || c.State != store.StateCollecting || *c.Fields.Offer != "Pizza" {
		t.Fatalf("conversa = %+v", c)
	}
	if !strings.Contains(h.wa.last().Text, "R$ 100,00") {
		t.Errorf("resposta = %q", h.wa.last().Text)
	}
}

func TestMessagesWhileAwaitingProposalDoNotCallAI(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer, turnRest}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	calls := h.att.Calls()
	h.say("e aí?")
	if h.att.Calls() != calls {
		t.Fatal("chamou a IA durante awaiting_proposal")
	}
	if !strings.Contains(h.wa.last().Text, "montando sua proposta") {
		t.Errorf("resposta = %q", h.wa.last().Text)
	}
}

func TestHumanRequestEscalatesAndSilencesBot(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"human","fields":{},"reply":"ok"}`}
	h.say("quero falar com uma pessoa")
	h.runJobs()
	if h.conv().State != store.StateNeedsHuman {
		t.Fatalf("estado = %s", h.conv().State)
	}
	if ops := h.wa.to(operatorChat); len(ops) != 1 || !strings.Contains(ops[0], "Atendimento humano") || !strings.Contains(ops[0], phone) {
		t.Errorf("operador = %v", ops)
	}
	if client := h.wa.to(chatID); len(client) != 1 || !strings.Contains(client[0], "pessoa da nossa equipe") {
		t.Errorf("cliente = %v", client)
	}
	n := h.wa.count()
	h.say("alô?")
	if h.wa.count() != n || h.att.Calls() != 1 {
		t.Fatal("bot deveria ficar em silêncio")
	}
	if c := h.count(`SELECT count(*) FROM audit_events WHERE action = 'escalated_human'`); c != 1 {
		t.Errorf("auditoria = %d", c)
	}
}

func TestAudioIsTranscribedBeforeAttendant(t *testing.T) {
	h := newHarness(t, "owner")
	h.wa.media, h.wa.mime = []byte("ogg"), "audio/ogg"
	h.att.Transcript = "quero anunciar pizza"
	h.att.Responses = []string{turnOffer}
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: "a1", ChatID: chatID, Phone: phone, Kind: whatsapp.KindAudio}))
	if !strings.Contains(h.att.Requests[0].Messages[1].Content, "<cliente>quero anunciar pizza</cliente>") {
		t.Errorf("transcrição não chegou ao atendente")
	}
	if n := h.count(`SELECT count(*) FROM llm_usage WHERE agent = 'transcriber' AND model = 'transcritor'`); n != 1 {
		t.Errorf("uso da transcrição = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE kind = 'audio' AND body = 'quero anunciar pizza'`); n != 1 {
		t.Errorf("mensagem de áudio gravada = %d", n)
	}
}

func TestImageIsStoredAsMedia(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"provide_info","fields":{},"reply":"Recebi a imagem!"}`}
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: "img1", ChatID: chatID, Phone: phone, Kind: whatsapp.KindImage, Text: "cardápio"}))
	c := h.conv()
	if len(c.Fields.Media) != 1 || c.Fields.Media[0] != "img1" {
		t.Errorf("media = %v", c.Fields.Media)
	}
	if !strings.Contains(h.att.Requests[0].Messages[1].Content, "[imagem enviada] cardápio") {
		t.Error("atendente não soube da imagem")
	}
}

func TestAttendantFailureSendsRetryMessage(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Errors = []error{llm.ErrTransient}
	h.say("oi")
	if !strings.Contains(h.wa.last().Text, "problema técnico") || h.conv().State != store.StateIdle {
		t.Errorf("resposta = %q estado = %s", h.wa.last().Text, h.conv().State)
	}
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action = 'llm_failed'`); n != 1 {
		t.Errorf("auditoria = %d", n)
	}
}

func TestQuestionKeepsIdle(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{`{"intent":"question","fields":{},"reply":"Funciona assim..."}`}
	h.say("como funciona?")
	if h.conv().State != store.StateIdle || h.wa.last().Text != "Funciona assim..." {
		t.Errorf("estado = %s resposta = %q", h.conv().State, h.wa.last().Text)
	}
}

func TestGiveUpDuringCollectionResets(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer, `{"intent":"reject","fields":{},"reply":"Sem problema!"}`}
	h.say("quero anunciar pizza")
	h.say("deixa pra lá")
	c := h.conv()
	if c.State != store.StateIdle || c.Fields.HasData() || h.wa.last().Text != "Sem problema!" {
		t.Errorf("conversa = %+v resposta = %q", c, h.wa.last().Text)
	}
}
