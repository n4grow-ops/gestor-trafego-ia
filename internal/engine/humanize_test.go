package engine_test

import (
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
	h.eng.Rand = func() float64 { return 0.5 }
	return h, tts
}

// drain roda os jobs até a fila esvaziar, com relógio simulado: quando nada está pronto,
// adianta a fila até o próximo job, mantendo a ordem dos run_after.
func (h *harness) drain() {
	h.t.Helper()
	for range 300 {
		job, ok, err := h.st.ClaimJob(ctx)
		h.must(err)
		if !ok {
			tag, err := h.pool.Exec(ctx, `UPDATE outbox_jobs
				SET run_after = run_after - (SELECT min(run_after) FROM outbox_jobs WHERE status = 'pending') + now() - interval '1 millisecond'
				WHERE status = 'pending'`)
			h.must(err)
			if tag.RowsAffected() == 0 {
				return
			}
			continue
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

// O worker não dorme: "digitando…" aparece e o balão sai num job agendado para depois.
func TestTypingSchedulesBubbleInsteadOfSleeping(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"question","reply":"Oi! Tudo certo por aí?"}`}
	h.say("oi")
	h.runKind("conversation.reply") // resposta + presença; a entrega do balão fica agendada
	if got := joined(h.wa.log()); got != "seen | typing" {
		t.Fatalf("eventos = %s", got)
	}
	var wait float64
	h.must(h.pool.QueryRow(ctx, `SELECT extract(epoch FROM run_after - now()) FROM outbox_jobs
		WHERE status = 'pending' AND kind = 'whatsapp.send'`).Scan(&wait))
	if wait < 1.3 || wait > 7 {
		t.Fatalf("balão agendado para %.1f s", wait)
	}
}

// Resposta nova espera os balões da anterior: não intercala e enxerga tudo no histórico.
func TestReplyWaitsForPendingBubbles(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{
		`{"intent":"create_campaign","fields":{"offer":"Pizza"},"reply":"A1\n\nA2\n\nA3"}`,
		`{"intent":"provide_info","fields":{"location":"Campinas"},"reply":"B1"}`,
	}
	h.say("quero anunciar pizza")
	h.runKind("conversation.reply")
	h.say("em Campinas")
	h.drain()
	if got := strings.Join(h.wa.to(chatID), ","); got != "A1,A2,A3,B1" {
		t.Fatalf("ordem dos balões = %s", got)
	}
	if user := h.att.Requests[1].Messages[1].Content; !strings.Contains(user, "Atendente: A3") {
		t.Fatalf("segunda resposta não viu a primeira inteira:\n%s", user)
	}
}

// A proposta com código só chega depois dos balões de "vou pedir ao especialista".
func TestProposalArrivesAfterBubbles(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{turnOffer, `{"intent":"provide_info","fields":{"audience":"famílias","location":"Campinas","daily_budget_brl":50},"reply":"Perfeito!\n\nVou pedir a proposta ao especialista."}`}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	h.drain()
	h.say("famílias em Campinas, 50 por dia")
	h.drain()
	sent := h.wa.to(chatID)
	if len(sent) < 3 || sent[len(sent)-2] != "Vou pedir a proposta ao especialista." || codeRe.FindString(sent[len(sent)-1]) == "" {
		t.Fatalf("ordem = %q", sent)
	}
}

// Limite diário no meio do debounce: a última mensagem permitida ainda é respondida,
// e as que passaram do limite não entram no lote do dia seguinte.
func TestDailyLimitKeepsLastAllowedMessage(t *testing.T) {
	h, _ := newHumanHarness(t)
	_, err := h.pool.Exec(ctx, `UPDATE organizations SET daily_message_limit = 1`)
	h.must(err)
	h.att.Responses = []string{`{"intent":"question","reply":"Oi!"}`, `{"intent":"question","reply":"Bom dia!"}`}
	h.say("oi")
	h.say("spam")
	h.drain()
	if h.att.Calls() != 1 || !strings.Contains(h.att.Requests[0].Messages[1].Content, "<cliente>oi</cliente>") {
		t.Fatalf("mensagem permitida sem resposta: chamadas = %d", h.att.Calls())
	}
	if got := strings.Join(h.wa.to(chatID), ","); !strings.Contains(got, "Oi!") || !strings.Contains(got, "limite de mensagens") {
		t.Fatalf("enviados = %s", got)
	}
	// dia seguinte: as mensagens de ontem saem da contagem do limite
	_, err = h.pool.Exec(ctx, `UPDATE messages SET created_at = created_at - interval '2 days'`)
	h.must(err)
	h.say("bom dia")
	h.drain()
	if user := h.att.Requests[1].Messages[1].Content; !strings.Contains(user, "Mensagem atual do cliente:\n<cliente>bom dia</cliente>") {
		t.Fatalf("lote do dia seguinte:\n%s", user)
	}
}

// Ligar o HUMANIZE depois de um tempo desligado não reenvia mensagens já respondidas.
func TestHumanizeOffStillMarksReplied(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.eng.Humanize = false
	h.att.Responses = []string{turnOffer, `{"intent":"provide_info","fields":{"location":"Campinas"},"reply":"Anotado!"}`}
	h.say("quero anunciar pizza")
	h.eng.Humanize = true
	h.say("em Campinas")
	h.drain()
	if user := h.att.Requests[1].Messages[1].Content; !strings.Contains(user, "Mensagem atual do cliente:\n<cliente>em Campinas</cliente>") {
		t.Fatalf("lote depois de ligar o modo humano:\n%s", user)
	}
}

// Falha num balão de conversa não derruba a proposta que já foi entregue.
func TestFailedChitChatKeepsProposal(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.reachApprovalHuman(false)
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Qualquer dúvida é só falar!"}`)
	h.say("legal")
	h.wa.setFail(true)
	h.drain()
	h.wa.setFail(false)
	if h.conv().State != store.StateAwaitingApproval || len(h.wa.to(operatorChat)) != 0 {
		t.Fatalf("estado = %s operador = %q", h.conv().State, h.wa.to(operatorChat))
	}
}

// WAHA recusou a voz: vai em texto na hora, sem pagar TTS de novo.
func TestVoiceSendFailureFallsBackToText(t *testing.T) {
	h, tts := newHumanHarness(t)
	h.wa.voiceFail = true
	h.att.Transcript = "oi"
	h.att.Responses = []string{`{"intent":"question","reply":"Oi! Tudo certo?"}`}
	h.sayAudio()
	h.drain()
	want := []string{"seen", "recording", "paused", "typing", "text:Oi! Tudo certo?", "paused"}
	if got := h.wa.log(); joined(got) != joined(want) {
		t.Fatalf("eventos = %s; want %s", joined(got), joined(want))
	}
	if len(tts.Spoken) != 1 {
		t.Fatalf("TTS chamado %d vezes", len(tts.Spoken))
	}
}

// Balões pendentes param quando a conversa passa para um humano.
func TestBubblesStopAfterEscalation(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.att.Responses = []string{`{"intent":"question","reply":"A1\n\nA2"}`}
	h.say("oi")
	h.runKind("conversation.reply")
	_, err := h.pool.Exec(ctx, `UPDATE conversations SET state = 'needs_human'`)
	h.must(err)
	h.drain()
	if h.wa.count() != 0 {
		t.Fatalf("balões enviados depois da escalada: %q", h.wa.to(chatID))
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

// "SIM 123456" seguido de "obrigado" no mesmo lote ainda aprova.
func TestApprovalFoundEarlierInBatch(t *testing.T) {
	h, _ := newHumanHarness(t)
	code := h.reachApprovalHuman(false)
	h.say("SIM " + code)
	h.say("obrigado")
	h.drain()
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "Campanha aprovada") {
		t.Fatalf("estado = %s último = %q", h.conv().State, h.wa.last().Text)
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
