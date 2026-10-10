package worker_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/worker"
)

var ctx = context.Background()

type fakeWA struct {
	mu    sync.Mutex
	parse func([]byte) (whatsapp.Inbound, bool, error)
	sent  []string
}

func (f *fakeWA) Name() string { return "openwa" }
func (f *fakeWA) ParseWebhook(b []byte) (whatsapp.Inbound, bool, error) {
	return f.parse(b)
}
func (f *fakeWA) SendText(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}
func (f *fakeWA) DownloadMedia(context.Context, whatsapp.Inbound) ([]byte, string, error) {
	return nil, "", nil
}
func (f *fakeWA) ChatIDForPhone(p string) string                                  { return p + "@c.us" }
func (f *fakeWA) MarkSeen(context.Context, string) error                          { return nil }
func (f *fakeWA) SetPresence(context.Context, string, whatsapp.Presence) error    { return nil }
func (f *fakeWA) SendVoice(context.Context, string, []byte, string) error         { return nil }
func (f *fakeWA) SendImage(context.Context, string, []byte, string, string) error { return nil }

func setup(t *testing.T, wa *fakeWA) (*worker.Worker, *store.Store, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	st := store.New(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := &engine.Engine{Store: st, WA: wa, Log: log}
	return &worker.Worker{Store: st, WA: wa, Engine: eng, Log: log}, st, pool
}

func eventStatus(t *testing.T, pool *pgxpool.Pool) (string, int, string) {
	t.Helper()
	var status string
	var attempts int
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM inbox_events`).Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatal(err)
	}
	if lastErr == nil {
		return status, attempts, ""
	}
	return status, attempts, *lastErr
}

func TestTickProcessesInboxEvent(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) {
		return whatsapp.Inbound{ExternalID: "e1", ChatID: "5511911112222@c.us", Phone: "5511911112222", Kind: whatsapp.KindText, Text: "oi"}, true, nil
	}}
	w, st, pool := setup(t, wa)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{"event":"onMessage"}`))

	did, err := w.Tick(ctx)
	if err != nil || !did {
		t.Fatalf("did = %v err = %v", did, err)
	}
	if status, _, _ := eventStatus(t, pool); status != "done" {
		t.Errorf("status = %s", status)
	}
	if len(wa.sent) != 1 || !strings.Contains(wa.sent[0], "não está liberado") {
		t.Errorf("enviado = %v", wa.sent)
	}
}

func TestTickIgnoredEventIsDone(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { return whatsapp.Inbound{}, false, nil }}
	w, st, pool := setup(t, wa)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{}`))
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := eventStatus(t, pool); status != "done" || len(wa.sent) != 0 {
		t.Errorf("status = %s enviado = %v", status, wa.sent)
	}
}

func TestTickRecoversPanic(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { panic("quebrou") }}
	w, st, pool := setup(t, wa)
	st.InsertInbox(ctx, "openwa", "e1", []byte(`{}`))
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	status, attempts, lastErr := eventStatus(t, pool)
	if status != "pending" || attempts != 1 || !strings.Contains(lastErr, "panic") {
		t.Errorf("status = %s attempts = %d last_error = %q", status, attempts, lastErr)
	}
}

func TestTickRunsJobsAndFlagsFailure(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { return whatsapp.Inbound{}, false, nil }}
	w, st, pool := setup(t, wa)
	st.EnqueueJob(ctx, engine.JobSend, engine.SendJob{ChatID: "x@c.us", Text: "olá"})
	pool.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload) VALUES ('desconhecido', '{}')`)

	for i := 0; i < 2; i++ {
		if _, err := w.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(wa.sent) != 1 || wa.sent[0] != "olá" {
		t.Errorf("enviado = %v", wa.sent)
	}
	var lastErr string
	pool.QueryRow(ctx, `SELECT last_error FROM outbox_jobs WHERE kind = 'desconhecido'`).Scan(&lastErr)
	if !strings.Contains(lastErr, "desconhecido") {
		t.Errorf("last_error = %q", lastErr)
	}
}

func TestTickWithoutWork(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { return whatsapp.Inbound{}, false, nil }}
	w, _, _ := setup(t, wa)
	did, err := w.Tick(ctx)
	if err != nil || did {
		t.Fatalf("did = %v err = %v", did, err)
	}
}

// Depois da troca de provedor, eventos ainda pendentes do provedor antigo são lidos pelo parser dele.
func TestTickUsesParserOfEventProvider(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) {
		t.Fatal("parser do provedor ativo usado para evento do antigo")
		return whatsapp.Inbound{}, false, nil
	}}
	legacy := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) {
		return whatsapp.Inbound{ExternalID: "e1", ChatID: "5511911112222@c.us", Phone: "5511911112222", Kind: whatsapp.KindText, Text: "oi"}, true, nil
	}}
	w, st, pool := setup(t, wa)
	w.Parsers = map[string]whatsapp.Provider{"legado": legacy}
	st.InsertInbox(ctx, "legado", "e1", []byte(`{}`))
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _, lastErr := eventStatus(t, pool); status != "done" {
		t.Fatalf("status = %s erro = %s", status, lastErr)
	}
	if len(wa.sent) != 1 {
		t.Fatalf("resposta deve sair pelo provedor ativo: %v", wa.sent)
	}
}

// A resposta do formulário de cadastro (CPF, e-mail) não fica guardada depois de processada.
func TestTickRedactsFlowAnswers(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) {
		return whatsapp.Inbound{ExternalID: "f1", ChatID: "5511911112222@c.us", Phone: "5511911112222", Kind: whatsapp.KindFlow, Text: `{"flow_token":"x"}`}, true, nil
	}}
	w, st, pool := setup(t, wa)
	st.InsertInbox(ctx, "openwa", "f1", []byte(`{"cpf":"529.982.247-25"}`))
	if _, err := w.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM inbox_events`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if payload != `{"redacted": true}` {
		t.Fatalf("payload = %s", payload)
	}
}

// I1: a publicação na Meta tem prazo próprio, maior que o padrão de 2 minutos e menor que a trava do job.
func TestPublishJobHasLongerTimeout(t *testing.T) {
	w := &worker.Worker{}
	if got := w.TimeoutFor(engine.JobMetaPublish); got != 15*time.Minute {
		t.Fatalf("prazo da publicação = %v", got)
	}
	if got := w.TimeoutFor(engine.JobInstagramFetch); got != 5*time.Minute {
		t.Fatalf("prazo do post do instagram = %v", got)
	}
	if got := w.TimeoutFor(engine.JobSend); got != 2*time.Minute {
		t.Fatalf("prazo padrão = %v", got)
	}
	w.Timeout = time.Minute
	if got := w.TimeoutFor(engine.JobMetaPublish); got != 15*time.Minute {
		t.Fatalf("prazo da publicação com Timeout curto = %v", got)
	}
}

// slowHandler segura os jobs lentos até release; o resto passa direto.
type slowHandler struct {
	mu       sync.Mutex
	inbound  []string
	running  int
	peak     int
	started  chan int64
	release  chan struct{}
	finished chan int64
}

func newSlowHandler() *slowHandler {
	return &slowHandler{started: make(chan int64, 10), release: make(chan struct{}), finished: make(chan int64, 10)}
}

func (h *slowHandler) HandleInbound(_ context.Context, in whatsapp.Inbound) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.inbound = append(h.inbound, in.Text)
	return nil
}

func (h *slowHandler) HandleJob(ctx context.Context, job store.Job) error {
	if job.Kind != engine.JobInstagramFetch && job.Kind != engine.JobMetaPublish {
		return nil
	}
	h.mu.Lock()
	h.running++
	h.peak = max(h.peak, h.running)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.running--
		h.mu.Unlock()
		h.finished <- job.ID
	}()
	h.started <- job.ID
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		time.Sleep(100 * time.Millisecond) // termina o que fazia antes de sair
		return ctx.Err()
	}
}

func (h *slowHandler) OnJobFailed(context.Context, store.Job) error { return nil }

func (h *slowHandler) inboundCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.inbound)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("esperando: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func jobStatus(t *testing.T, pool *pgxpool.Pool, id int64) (string, bool) {
	t.Helper()
	var status string
	var locked bool
	if err := pool.QueryRow(ctx, `SELECT status, locked_until IS NOT NULL FROM outbox_jobs WHERE id = $1`, id).Scan(&status, &locked); err != nil {
		t.Fatal(err)
	}
	return status, locked
}

// I2: job lento (post do Instagram, publicação) roda fora do laço: o inbox e os outros jobs seguem,
// no máximo 2 lentos por vez, e o desligamento espera o lento terminar e gravar o resultado.
func TestSlowJobsRunOffTheLoop(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) {
		return whatsapp.Inbound{ExternalID: "e1", ChatID: "x@c.us", Kind: whatsapp.KindText, Text: "oi"}, true, nil
	}}
	_, st, pool := setup(t, wa)
	h := newSlowHandler()
	w := &worker.Worker{Store: st, WA: wa, Engine: h, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Idle: 10 * time.Millisecond}
	var slow []int64
	for range 3 {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO outbox_jobs (kind, payload) VALUES ($1, '{}') RETURNING id`, engine.JobInstagramFetch).Scan(&id); err != nil {
			t.Fatal(err)
		}
		slow = append(slow, id)
	}
	rctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { w.Run(rctx); close(done) }()

	<-h.started
	<-h.started
	// com os dois lentos rodando, mensagem e job rápido seguem
	if _, err := st.InsertInbox(ctx, "openwa", "e1", []byte(`{"text":"oi"}`)); err != nil {
		t.Fatal(err)
	}
	st.EnqueueJob(ctx, engine.JobSend, engine.SendJob{ChatID: "x@c.us", Text: "olá"})
	waitFor(t, "mensagem processada com job lento rodando", func() bool {
		s, _, _ := eventStatus(t, pool)
		return s == "done"
	})
	waitFor(t, "job rápido processado", func() bool {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = $1 AND status = 'done'`, engine.JobSend).Scan(&n)
		return n == 1
	})
	if h.inboundCount() != 1 {
		t.Fatalf("mensagens tratadas = %d", h.inboundCount())
	}
	select {
	case id := <-h.started:
		t.Fatalf("terceiro lento (%d) começou com 2 rodando", id)
	case <-time.After(200 * time.Millisecond):
	}
	h.release <- struct{}{} // libera um: o terceiro entra
	<-h.finished
	<-h.started
	h.mu.Lock()
	peak := h.peak
	h.mu.Unlock()
	if peak != 2 {
		t.Fatalf("lentos ao mesmo tempo = %d", peak)
	}

	// desligamento com dois lentos rodando: Run espera e o resultado fica gravado (sem trava pendurada)
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run não voltou")
	}
	if len(h.finished) != 2 {
		t.Fatalf("Run voltou antes dos lentos terminarem: %d terminados", len(h.finished))
	}
	var finished, retry int
	for _, id := range slow {
		switch s, locked := jobStatus(t, pool, id); {
		case s == "done" && !locked:
			finished++
		case s == "pending" && !locked:
			retry++
		default:
			t.Fatalf("job %d: status = %s travado = %v", id, s, locked)
		}
	}
	if finished != 1 || retry != 2 {
		t.Fatalf("concluídos = %d para nova tentativa = %d", finished, retry)
	}
}

// A trava do job (ClaimJob) cobre o prazo do tipo com folga: outro worker não pega o mesmo job no meio.
func TestJobLockCoversTimeout(t *testing.T) {
	wa := &fakeWA{parse: func([]byte) (whatsapp.Inbound, bool, error) { return whatsapp.Inbound{}, false, nil }}
	w, st, pool := setup(t, wa)
	for _, kind := range []string{engine.JobInstagramFetch, engine.JobMetaPublish, engine.JobSend} {
		if _, err := pool.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload) VALUES ($1, '{}')`, kind); err != nil {
			t.Fatal(err)
		}
		job, ok, err := st.ClaimJob(ctx)
		if err != nil || !ok {
			t.Fatalf("claim %s: %v %v", kind, ok, err)
		}
		var lock time.Duration
		if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM locked_until - now()) * interval '1 second' FROM outbox_jobs WHERE id = $1`, job.ID).Scan(&lock); err != nil {
			t.Fatal(err)
		}
		if lock < w.TimeoutFor(kind)+time.Minute {
			t.Fatalf("%s: trava %v não cobre o prazo %v com folga", kind, lock, w.TimeoutFor(kind))
		}
		if _, err := st.FinishJob(ctx, job.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
}
