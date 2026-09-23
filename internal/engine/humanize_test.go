package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// newHumanHarness liga o modo humano sem esperas reais; devolve também o TTS falso.
func newHumanHarness(t *testing.T) (*harness, *llm.Fake) {
	h := newHarness(t, "owner")
	tts := &llm.Fake{Speech: []byte("MP3")}
	h.eng.Humanize = true
	h.eng.Debounce = 0
	h.eng.Voice = tts
	h.eng.TTSModel = "tts"
	h.eng.TTSVoice = "nova"
	h.eng.Sleep = func(context.Context, time.Duration) error { return nil }
	h.eng.Rand = func() float64 { return 0.5 }
	return h, tts
}

// drain roda os jobs pendentes (inclusive os agendados para depois) até a fila esvaziar.
func (h *harness) drain() {
	h.t.Helper()
	for range 100 {
		_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE status = 'pending'`)
		h.must(err)
		job, ok, err := h.st.ClaimJob(ctx)
		h.must(err)
		if !ok {
			return
		}
		failed, err := h.st.FinishJob(ctx, job.ID, h.eng.HandleJob(ctx, job))
		h.must(err)
		if failed {
			h.must(h.eng.OnJobFailed(ctx, job))
		}
	}
	h.t.Fatal("fila não esvaziou")
}

// runKind roda só os jobs pendentes de um tipo; os outros ficam na fila.
func (h *harness) runKind(kind string) {
	h.t.Helper()
	_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = CASE WHEN kind = $1 THEN now() ELSE now() + interval '1 hour' END
		WHERE status = 'pending'`, kind)
	h.must(err)
	for {
		job, ok, err := h.st.ClaimJob(ctx)
		h.must(err)
		if !ok {
			return
		}
		_, err = h.st.FinishJob(ctx, job.ID, h.eng.HandleJob(ctx, job))
		h.must(err)
	}
}

func (h *harness) sayAudio() {
	h.t.Helper()
	h.seq++
	h.wa.media, h.wa.mime = []byte("OGG"), "audio/ogg"
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: fmt.Sprintf("a%d", h.seq), ChatID: chatID, Phone: phone, Kind: whatsapp.KindAudio}))
}

// reachApprovalHuman leva a conversa até a proposta no modo humano e devolve o código.
func (h *harness) reachApprovalHuman(viaAudio bool) string {
	h.t.Helper()
	h.att.Responses = append(h.att.Responses, turnOffer, turnRest)
	h.mgr.Responses = append(h.mgr.Responses, validProposal)
	if viaAudio {
		h.att.Transcript = "quero anunciar pizza"
		h.sayAudio()
	} else {
		h.say("quero anunciar pizza")
	}
	h.drain()
	h.say("famílias em Campinas, 50 por dia")
	h.drain()
	m := codeRe.FindStringSubmatch(h.wa.last().Text)
	if m == nil {
		h.t.Fatalf("resumo sem código: %q", h.wa.last().Text)
	}
	return m[1]
}

func joined(events []string) string { return strings.Join(events, " | ") }

func TestHumanReplyInBubblesWithTyping(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"Pizza grande"},"reply":"Show, Ana!\n\nPra quem é a pizza e em qual cidade?"}`}
	h.say("quero anunciar pizza")
	if h.wa.count() != 0 || h.att.Calls() != 0 {
		t.Fatal("nada sai antes do debounce")
	}
	h.drain()
	want := []string{"seen", "typing", "text:Show, Ana!", "paused", "typing", "text:Pra quem é a pizza e em qual cidade?", "paused"}
	if got := h.wa.log(); joined(got) != joined(want) {
		t.Fatalf("eventos = %s\nwant %s", joined(got), joined(want))
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out'`); n != 2 {
		t.Fatalf("mensagens de saída = %d; want 2", n)
	}
}

func TestTypingWaitsProportionalToText(t *testing.T) {
	h, _ := newHumanHarness(t)
	var waits []time.Duration
	h.eng.Sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	h.att.Responses = []string{`{"intent":"question","reply":"Oi!"}`}
	h.say("oi")
	h.drain()
	if len(waits) != 1 || waits[0] < 1500*time.Millisecond || waits[0] > 7*time.Second {
		t.Fatalf("esperas = %v", waits)
	}
}

func TestDebounceSchedulesReplyLater(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.eng.Debounce = 4 * time.Second
	h.att.Responses = []string{turnOffer}
	h.say("oi")
	if _, ok, err := h.st.ClaimJob(ctx); err != nil || ok {
		t.Fatalf("resposta não pode rodar antes do debounce: ok=%v err=%v", ok, err)
	}
	if h.pendingJobs("conversation.reply") != 1 {
		t.Fatal("resposta deveria estar agendada")
	}
}

func TestAudioGetsVoiceReply(t *testing.T) {
	h, tts := newHumanHarness(t)
	h.att.Transcript = "quero anunciar pizza"
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"Pizza"},"reply":"Show! Em qual cidade?"}`}
	h.sayAudio()
	h.drain()
	want := []string{"seen", "recording", "voice", "paused"}
	if got := h.wa.log(); joined(got) != joined(want) {
		t.Fatalf("eventos = %s; want %s", joined(got), joined(want))
	}
	if len(tts.Spoken) != 1 || tts.Spoken[0] != "Show! Em qual cidade?" {
		t.Fatalf("TTS recebeu %q", tts.Spoken)
	}
	if !strings.Contains(h.att.Requests[0].Messages[1].Content, "Formato da resposta: áudio") {
		t.Error("Atendente deveria saber que a resposta vira áudio")
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out' AND kind = 'voice'`); n != 1 {
		t.Fatalf("voz registrada = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM llm_usage WHERE agent = 'tts'`); n != 1 {
		t.Fatalf("uso do TTS = %d", n)
	}
}

func TestTextGetsTextEvenWithVoiceOn(t *testing.T) {
	h, tts := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"question","reply":"Oi!"}`}
	h.say("oi")
	h.drain()
	if len(tts.Spoken) != 0 || len(h.wa.voices) != 0 {
		t.Fatal("texto não vira voz")
	}
}

func TestVoiceFallsBackToText(t *testing.T) {
	h, tts := newHumanHarness(t)
	tts.SpeechErr = errors.New("tts fora")
	h.att.Transcript = "oi"
	h.att.Responses = []string{`{"intent":"question","reply":"Oi! Tudo certo?"}`}
	h.sayAudio()
	h.drain()
	want := []string{"seen", "recording", "paused", "typing", "text:Oi! Tudo certo?", "paused"}
	if got := h.wa.log(); joined(got) != joined(want) {
		t.Fatalf("eventos = %s; want %s", joined(got), joined(want))
	}
}

func TestFixedMessageNeverVoice(t *testing.T) {
	h, tts := newHumanHarness(t)
	h.att.Transcript = "oi"
	h.att.Errors = []error{errors.New("modelo fora")}
	h.sayAudio()
	h.drain()
	if len(tts.Spoken) != 0 || !strings.Contains(h.wa.last().Text, "problema técnico") {
		t.Fatalf("mensagem fixa foi para voz: spoken=%q last=%q", tts.Spoken, h.wa.last().Text)
	}
}

func TestOperatorNoteGoesDirect(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"human","reply":"ok"}`}
	h.say("quero falar com uma pessoa")
	h.drain()
	if len(h.wa.to(operatorChat)) != 1 || h.conv().State != store.StateNeedsHuman {
		t.Fatalf("operador = %q estado = %s", h.wa.to(operatorChat), h.conv().State)
	}
}

func TestBurstGetsOneReply(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{turnOffer}
	h.say("oi")
	h.say("quero anunciar")
	h.say("pizza grande")
	h.drain()
	if h.att.Calls() != 1 {
		t.Fatalf("Atendente chamada %d vezes; want 1", h.att.Calls())
	}
	user := h.att.Requests[0].Messages[1].Content
	if !strings.Contains(user, "<cliente>oi\nquero anunciar\npizza grande</cliente>") {
		t.Fatalf("lote não chegou junto:\n%s", user)
	}
	if strings.Contains(user, "Histórico recente") {
		t.Errorf("mensagens do lote não podem aparecer também no histórico:\n%s", user)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'in'`); n != 3 {
		t.Fatalf("entradas gravadas = %d", n)
	}
}

func TestBatchIgnoresAlreadyAnswered(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{turnOffer, `{"intent":"provide_info","fields":{"location":"Campinas"},"reply":"Anotado!"}`}
	h.say("quero anunciar pizza")
	h.runKind("conversation.reply") // resposta decidida; balões ainda na fila, saída não registrada
	h.say("em Campinas")
	h.drain()
	if h.att.Calls() != 2 {
		t.Fatalf("chamadas = %d", h.att.Calls())
	}
	user := h.att.Requests[1].Messages[1].Content
	if !strings.Contains(user, "Mensagem atual do cliente:\n<cliente>em Campinas</cliente>") {
		t.Fatalf("segundo lote errado:\n%s", user)
	}
	if !strings.Contains(user, "Cliente: <cliente>quero anunciar pizza</cliente>") {
		t.Fatalf("histórico perdeu a primeira mensagem:\n%s", user)
	}
}

func TestNeedsHumanMarksReplied(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"human","reply":"ok"}`, `{"intent":"question","reply":"Oi de novo!"}`}
	h.say("quero uma pessoa")
	h.drain()
	h.say("alô?") // durante o atendimento humano: sem resposta automática
	h.drain()
	if h.att.Calls() != 1 {
		t.Fatalf("bot respondeu durante atendimento humano: %d chamadas", h.att.Calls())
	}
	h.must(h.st.ResetConversation(ctx, h.ident().ID))
	h.say("voltei")
	h.drain()
	if user := h.att.Requests[1].Messages[1].Content; !strings.Contains(user, "Mensagem atual do cliente:\n<cliente>voltei</cliente>") {
		t.Fatalf("lote depois do humano:\n%s", user)
	}
}

func TestApprovalUsesLastMessageOfBatch(t *testing.T) {
	h, _ := newHumanHarness(t)
	code := h.reachApprovalHuman(false)
	calls := h.att.Calls()
	h.say("gostei")
	h.say("SIM " + code)
	h.drain()
	if h.att.Calls() != calls {
		t.Fatal("aprovação não deveria chamar a IA")
	}
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "Campanha aprovada") {
		t.Fatalf("estado = %s último = %q", h.conv().State, h.wa.last().Text)
	}
}

func TestProposalSummaryStaysTextAfterAudio(t *testing.T) {
	h, _ := newHumanHarness(t)
	if code := h.reachApprovalHuman(true); code == "" {
		t.Fatal("sem código")
	}
	for _, v := range h.wa.voices {
		if strings.Contains(v.Text, "SIM") {
			t.Fatal("resumo com código não pode ir em voz")
		}
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out' AND kind = 'voice' AND body LIKE '%SIM%'`); n != 0 {
		t.Fatalf("resumo registrado como voz: %d", n)
	}
}

func TestBlockedClientBatchIsDropped(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.say("oi")
	_, err := h.pool.Exec(ctx, `UPDATE organizations SET status = 'blocked'`)
	h.must(err)
	h.drain()
	if h.att.Calls() != 0 || h.wa.count() != 0 {
		t.Fatal("cliente bloqueado não recebe resposta")
	}
}

// Envio humano com WhatsApp fora: tenta de novo e, esgotado, vira failed sem travar a fila.
func TestHumanSendRetriesThenFails(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.say("oi")
	h.wa.setFail(true)
	h.att.Responses = []string{`{"intent":"question","reply":"Oi!"}`}
	h.drain() // envio humano falha até esgotar
	h.wa.setFail(false)
	h.drain()
	if n := h.count(`SELECT count(*) FROM outbox_jobs WHERE status = 'failed' AND kind = 'whatsapp.send'`); n != 1 {
		t.Fatalf("envio esgotado = %d", n)
	}
}

func TestHumanizeOffKeepsDirectSend(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	if h.wa.count() != 1 || h.pendingJobs("conversation.reply") != 0 || len(h.wa.log()) != 1 {
		t.Fatalf("sem HUMANIZE a resposta sai na hora, sem presença: %s", joined(h.wa.log()))
	}
}
