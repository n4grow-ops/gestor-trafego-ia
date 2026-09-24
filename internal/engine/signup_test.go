package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

const signupBase = "https://cadastro.teste/cadastro"

func newSignupHarness(t *testing.T) *harness {
	h := newHarness(t, "owner")
	h.eng.SignupBaseURL = signupBase
	return h
}

func (h *harness) complete() {
	h.t.Helper()
	_, err := h.pool.Exec(ctx, `UPDATE organizations SET registration_status = 'complete'`)
	h.must(err)
}

func TestPendingClientGetsSignupLink(t *testing.T) {
	h := newSignupHarness(t)
	h.say("oi, quero anunciar")
	if h.att.Calls() != 0 {
		t.Fatal("cliente sem cadastro não conversa com a IA")
	}
	last := h.wa.last().Text
	if !strings.Contains(last, signupBase+"/") || !strings.Contains(last, "cadastro") {
		t.Fatalf("mensagem = %q", last)
	}
	token := last[strings.Index(last, signupBase+"/")+len(signupBase)+1:]
	token = strings.Fields(token)[0]
	if len(token) != 43 {
		t.Fatalf("token = %q", token)
	}
	if n := h.count(`SELECT count(*) FROM signup_links`); n != 1 {
		t.Fatalf("links = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'in' AND kind = 'skipped'`); n != 1 {
		t.Fatalf("mensagem do cliente não gravada como ignorada: %d", n)
	}
}

func TestSignupLinkNotResentWithinTenMinutes(t *testing.T) {
	h := newSignupHarness(t)
	h.say("oi")
	h.say("oi??")
	if h.wa.count() != 1 || h.count(`SELECT count(*) FROM signup_links`) != 1 {
		t.Fatalf("reenviou: %d mensagens, %d links", h.wa.count(), h.count(`SELECT count(*) FROM signup_links`))
	}
	_, err := h.pool.Exec(ctx, `UPDATE signup_links SET created_at = now() - interval '11 minutes'`)
	h.must(err)
	h.say("e aí?")
	if h.wa.count() != 2 {
		t.Fatalf("depois de 10 min devia reenviar: %d", h.wa.count())
	}
}

func TestCompleteClientTalksToAI(t *testing.T) {
	h := newSignupHarness(t)
	h.complete()
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	if h.att.Calls() != 1 || h.count(`SELECT count(*) FROM signup_links`) != 0 {
		t.Fatal("cliente cadastrado conversa normal")
	}
}

func TestPendingMessagesDoNotReachLaterBatch(t *testing.T) {
	h, _ := newHumanHarness(t)
	h.eng.SignupBaseURL = signupBase
	h.say("oi")
	h.drain()
	h.complete()
	h.att.Responses = []string{`{"intent":"question","reply":"Oi!"}`}
	h.say("pronto, fiz o cadastro")
	h.drain()
	if user := h.att.Requests[0].Messages[1].Content; !strings.Contains(user, "Mensagem atual do cliente:\n<cliente>pronto, fiz o cadastro</cliente>") {
		t.Fatalf("lote:\n%s", user)
	}
}

func TestWelcomeJobIsHumanText(t *testing.T) {
	spec := engine.WelcomeJob(7, "5511@c.us", "Ana Souza")
	s, ok := spec.Payload.(engine.SendJob)
	if spec.Kind != engine.JobSend || !ok || !s.Human || s.Voice || s.ConversationID != 7 || !strings.Contains(s.Text+strings.Join(s.Next, " "), "Ana") {
		t.Fatalf("boas-vindas = %+v", spec)
	}
}
