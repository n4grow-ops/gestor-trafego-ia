package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestStaffMessageIsSentAndRecordedAsStaff(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = append(h.att.Responses, `{"intent":"human","fields":{},"reply":"ok"}`)
	h.say("quero falar com uma pessoa")
	h.runJobs()
	conv := h.conv()
	if conv.State != store.StateNeedsHuman {
		t.Fatalf("estado = %q", conv.State)
	}
	if _, err := h.st.TakeOverConversation(ctx, conv.ID, "marina@meuanuncio.io"); err != nil {
		t.Fatal(err)
	}
	err := h.st.EnqueueStaffMessage(ctx, conv.ID, func(chat string) store.JobSpec {
		return engine.StaffSendJob(conv.ID, chat, "Oi Ana, aqui é a Marina da equipe!", "Marina")
	})
	h.must(err)
	h.runJobs()
	if got := h.wa.last(); got.ChatID != chatID || got.Text != "Oi Ana, aqui é a Marina da equipe!" {
		t.Fatalf("envio = %+v", got)
	}
	thread, err := h.st.Thread(ctx, conv.ID, 0)
	h.must(err)
	last := thread.Messages[len(thread.Messages)-1]
	if last.Kind != store.KindStaff || last.Author != "Marina" || last.Direction != "out" || len(thread.Pending) != 0 {
		t.Fatalf("mensagem gravada = %+v, fila = %+v", last, thread.Pending)
	}
	// Com a equipe atendendo, a IA continua calada.
	calls := h.att.Calls()
	h.say("obrigada!")
	h.runJobs()
	if h.att.Calls() != calls {
		t.Fatal("a IA respondeu durante o atendimento humano")
	}
	// Devolvida, a IA volta a responder.
	state, err := h.st.ReleaseConversation(ctx, conv.ID)
	if err != nil || state != store.StateIdle {
		t.Fatalf("devolver: %q %v", state, err)
	}
	h.att.Responses = append(h.att.Responses, `{"intent":"question","fields":{},"reply":"Voltei! Em que posso ajudar?"}`)
	h.say("e agora?")
	h.runJobs()
	if !strings.Contains(h.wa.last().Text, "Voltei!") {
		t.Fatalf("a IA não voltou: %q", h.wa.last().Text)
	}
}

func TestResendSignupLink(t *testing.T) {
	h := newSignupHarness(t)
	ident, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	// Cliente trazido à mão, que ainda não escreveu: o destino sai do telefone.
	err = engine.ResendSignupLink(ctx, h.st, signupBase, ident, phone+"@c.us", h.now)
	h.must(err)
	h.runJobs()
	last := h.wa.last()
	if last.ChatID != chatID || !strings.Contains(last.Text, signupBase+"/") {
		t.Fatalf("envio = %+v", last)
	}
	if n := h.count(`SELECT count(*) FROM signup_links`); n != 1 {
		t.Fatalf("links = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out' AND body LIKE '%/c/[link]%'`); n != 1 {
		t.Fatalf("link fora do histórico (ou sem conversa): %d", n)
	}
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action = 'signup_link_sent' AND actor = 'admin'`); n != 1 {
		t.Fatalf("auditoria = %d", n)
	}

	h.complete()
	ident, _ = h.st.FindIdentity(ctx, "openwa", phone)
	if err := engine.ResendSignupLink(ctx, h.st, signupBase, ident, chatID, h.now); !errors.Is(err, store.ErrAlreadyRegistered) {
		t.Fatalf("cadastro concluído: %v", err)
	}
	if err := engine.ResendSignupLink(ctx, h.st, "", ident, chatID, h.now); err == nil {
		t.Fatal("sem URL de cadastro deve falhar")
	}
}
