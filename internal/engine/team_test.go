package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const groupJID = "120363000000000001@g.us"

// fakeGroup é a instância não oficial que só manda texto para o grupo da equipe.
type fakeGroup struct {
	mu   sync.Mutex
	fail bool
	sent []sent
}

func (g *fakeGroup) SendText(_ context.Context, chatID, text string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail {
		return errors.New("grupo fora")
	}
	g.sent = append(g.sent, sent{chatID, text})
	return nil
}

func (g *fakeGroup) all() []sent {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]sent(nil), g.sent...)
}

func (g *fakeGroup) setFail(v bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fail = v
}

const humanTurn = `{"intent":"human","fields":{},"reply":"ok"}`

func TestHumanNoticeGoesToEveryTeamPhoneWithMotive(t *testing.T) {
	h := newHarness(t, "owner")
	h.eng.TeamPhones = []string{"5511900000001", "5511900000002"}
	h.eng.AdminURL = "https://admin.meuanuncio.io/"
	h.att.Responses = []string{humanTurn}
	h.say("quero falar com alguém sobre a fatura\n meu token=abcdefgh12345678 e o link https://x.io/c/AbC123xYz789qq")
	h.runJobs()
	want := "🙋 Cliente quer falar com humano — Ana (Pizzaria Boa), WhatsApp " + phone +
		". Motivo: «quero falar com alguém sobre a fatura meu token=[oculto] e o link https://x.io/c/[link]». " +
		"Atender: https://admin.meuanuncio.io/"
	for _, p := range h.eng.TeamPhones {
		if got := h.wa.to(p + "@c.us"); len(got) != 1 || got[0] != want {
			t.Fatalf("equipe %s = %q\nwant %q", p, got, want)
		}
	}
	if ops := h.wa.to(operatorChat); len(ops) != 0 {
		t.Fatalf("com TEAM_PHONES o OPERATOR_PHONE não recebe à parte: %q", ops)
	}
	// A auditoria guarda o motivo genérico, não o texto do cliente.
	if c := h.count(`SELECT count(*) FROM audit_events WHERE action = 'escalated_human' AND data->>'reason' = 'cliente pediu atendimento humano'`); c != 1 {
		t.Fatalf("auditoria = %d", c)
	}
}

func TestHumanNoticeWithoutAdminURLOmitsLink(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{humanTurn}
	h.say("me liga")
	h.runJobs()
	ops := h.wa.to(operatorChat)
	if len(ops) != 1 || !strings.HasSuffix(ops[0], "Motivo: «me liga».") || strings.Contains(ops[0], "Atender") {
		t.Fatalf("operador = %q", ops)
	}
}

func TestHumanNoticeMotiveIsClipped(t *testing.T) {
	h := newHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.att.Responses = []string{humanTurn}
	h.say(strings.Repeat("é", 500))
	h.runJobs()
	// Aviso da equipe sai fora da janela: vai o modelo, com o motivo curto.
	if len(wa.tpls) != 1 || wa.tpls[0].Name != engine.TplTeamAlert {
		t.Fatalf("modelos = %+v", wa.tpls)
	}
	motive := wa.tpls[0].Params[3]
	if n := utf8.RuneCountInString(motive); n != 200 || !strings.HasSuffix(motive, "…") || strings.Contains(motive, "\n") {
		t.Fatalf("motivo com %d runas: %q", n, motive)
	}
}

func TestTeamGroupGetsNoticeAndRetries(t *testing.T) {
	h := newHarness(t, "owner")
	g := &fakeGroup{fail: true}
	h.eng.TeamGroup = g
	h.eng.TeamGroupJID = groupJID
	h.att.Responses = []string{humanTurn}
	h.say("quero uma pessoa")
	if n := h.pendingJobs(engine.JobTeamGroup); n != 1 {
		t.Fatalf("jobs do grupo = %d", n)
	}
	h.runJobs() // grupo fora: cliente e privado da equipe seguem
	if client := h.wa.to(chatID); len(client) != 1 {
		t.Fatalf("cliente = %v", client)
	}
	if ops := h.wa.to(operatorChat); len(ops) != 1 {
		t.Fatalf("operador = %v", ops)
	}
	if n := h.pendingJobs(engine.JobTeamGroup); n != 1 {
		t.Fatalf("falha no grupo deve ficar para nova tentativa: %d", n)
	}
	g.setFail(false)
	h.retryAll(1)
	got := g.all()
	if len(got) != 1 || got[0].ChatID != groupJID || !strings.HasPrefix(got[0].Text, "🙋 Cliente quer falar com humano") {
		t.Fatalf("grupo = %+v", got)
	}
	if len(h.wa.to(groupJID)) != 0 {
		t.Fatal("o número oficial não pode mandar para o grupo")
	}
}

func TestTeamGroupFailureExhaustedOnlyLogs(t *testing.T) {
	h := newHarness(t, "owner")
	h.eng.TeamGroup = &fakeGroup{fail: true}
	h.eng.TeamGroupJID = groupJID
	h.att.Responses = []string{humanTurn}
	h.say("quero uma pessoa")
	h.retryAll(10)
	if h.conv().State != store.StateNeedsHuman || h.pendingJobs(engine.JobTeamGroup) != 0 {
		t.Fatalf("estado = %s pendentes = %d", h.conv().State, h.pendingJobs(engine.JobTeamGroup))
	}
}

func TestNoTeamGroupWithoutConfig(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{humanTurn}
	h.say("quero uma pessoa")
	if n := h.pendingJobs(engine.JobTeamGroup); n != 0 {
		t.Fatalf("jobs do grupo = %d", n)
	}
}

func TestProposalFailureAlertGoesToTeamPhones(t *testing.T) {
	h := newHarness(t, "owner")
	h.eng.TeamPhones = []string{"5511900000001"}
	h.att.Responses = []string{turnOffer, turnRest}
	bad := strings.Replace(validProposal, `"daily_budget_brl":40`, `"daily_budget_brl":10000`, 1)
	h.mgr.Responses = []string{bad, bad, bad}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	h.runJobs()
	if h.conv().State != store.StateNeedsHuman {
		t.Fatalf("estado = %s", h.conv().State)
	}
	got := h.wa.to("5511900000001@c.us")
	if len(got) != 1 || !strings.Contains(got[0], "Atendimento humano") {
		t.Fatalf("equipe = %q", got)
	}
}
