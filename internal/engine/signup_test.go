package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const signupBase = "https://cadastro.teste/c"

func newSignupHarness(t *testing.T) *harness {
	return newSignupHarnessRole(t, "owner")
}

func newSignupHarnessRole(t *testing.T, role string) *harness {
	h := newHarness(t, role)
	h.eng.SignupBaseURL = signupBase
	h.pending()
	return h
}

func (h *harness) pending() {
	h.t.Helper()
	_, err := h.pool.Exec(ctx, `UPDATE organizations SET registration_status = 'pending'`)
	h.must(err)
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
	if len(token) != 12 {
		t.Fatalf("token = %q", token)
	}
	if n := h.count(`SELECT count(*) FROM signup_links`); n != 1 {
		t.Fatalf("links = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'in' AND kind = 'skipped'`); n != 1 {
		t.Fatalf("mensagem do cliente não gravada como ignorada: %d", n)
	}
	// O link não fica guardado no histórico (que vai para a IA e para quem lê o banco).
	if n := h.count(`SELECT count(*) FROM messages WHERE body LIKE '%' || $1 || '%'`, token); n != 0 {
		t.Fatal("token do link gravado no histórico")
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE direction = 'out' AND body LIKE '%/c/[link]%'`); n != 1 {
		t.Fatal("mensagem do link não registrada de forma segura")
	}
}

func TestNonOwnerIsAskedForOwner(t *testing.T) {
	h := newSignupHarnessRole(t, "approver")
	h.say("oi")
	if h.count(`SELECT count(*) FROM signup_links`) != 0 || !strings.Contains(h.wa.last().Text, "responsável") {
		t.Fatalf("aprovador recebeu link ou mensagem errada: %q", h.wa.last().Text)
	}
}

func TestGateSkipsHumanAttendance(t *testing.T) {
	h := newSignupHarness(t)
	h.conv() // cria a conversa
	_, err := h.pool.Exec(ctx, `UPDATE conversations SET state = 'needs_human'`)
	h.must(err)
	h.say("oi, é com a atendente")
	if h.wa.count() != 0 || h.count(`SELECT count(*) FROM signup_links`) != 0 {
		t.Fatal("atendimento humano não recebe link de cadastro")
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
	h.pending()
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
	spec := engine.WelcomeJob(7, "5511@c.us", "Ana Souza", "ana@mail.com")
	s, ok := spec.Payload.(engine.SendJob)
	all := s.Text + " " + strings.Join(s.Next, " ")
	if spec.Kind != engine.JobSend || !ok || !s.Human || s.Voice || s.ConversationID != 7 || !strings.Contains(all, "Ana") ||
		!strings.Contains(all, "ana@mail.com") || !strings.Contains(all, "o que a sua empresa faz") || strings.Contains(all, "anunciar primeiro") {
		t.Fatalf("boas-vindas = %+v", spec)
	}
}

// Zerar campanhas no painel: o cadastro fica, então a conversa nova segue com a Mia sem pedir cadastro.
func TestResetCampaignsKeepsClientRegistered(t *testing.T) {
	h := newSignupHarness(t)
	h.complete()
	h.reachApproval()
	org := h.count(`SELECT organization_id FROM members LIMIT 1`)
	rep, err := h.st.ResetCampaigns(ctx, int64(org), true, store.AdminAction{ActorEmail: "dono@meuanuncio.io", Action: "client_reset_campaigns",
		TargetType: "organization", TargetID: "1"})
	h.must(err)
	if rep.Rows("conversations") != 1 || rep.Rows("campaign_drafts") == 0 {
		t.Fatalf("zerar = %+v", rep)
	}
	calls := h.att.Calls()
	h.att.Responses = append(h.att.Responses, turnOffer)
	h.say("oi, quero anunciar de novo")
	if h.att.Calls() == calls {
		t.Fatal("cliente cadastrado não voltou a conversar com a IA")
	}
	if strings.Contains(h.wa.last().Text, signupBase) || h.count(`SELECT count(*) FROM signup_links`) != 0 {
		t.Fatalf("pediu cadastro de novo: %q", h.wa.last().Text)
	}
	if n := h.count(`SELECT count(*) FROM conversations`); n != 1 {
		t.Fatalf("conversas = %d", n)
	}
}
