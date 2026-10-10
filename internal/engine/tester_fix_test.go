package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Testes da revisão do testador: parser e store REAIS (sem fakeParse nem fakeTesters), porque o que
// se prova aqui depende deles (falso positivo do link, travas e marcas no banco).

const reply = `{"intent":"question","reply":"Legal!"}`

func newRealTesterHarness(t *testing.T, role string) *harness {
	h := newMetaHarness(t, role)
	h.eng.MetaAppID = "999"
	h.eng.ResolveShare = func(context.Context, string) (string, error) { return "", errors.New("sem rede nos testes") }
	return h
}

func (h *harness) testerRows() int { return h.count(`SELECT count(*) FROM tester_requests`) }

// seedIdentity cria outro telefone (na organização orgID, ou numa nova se orgID = 0) com conversa.
func (h *harness) seedIdentity(orgID int64, name, phone string) store.Identity {
	h.t.Helper()
	if orgID == 0 {
		var err error
		orgID, err = h.st.CreateOrganization(ctx, "Outra "+name, "restaurante", 100, []string{"OUTCOME_ENGAGEMENT"})
		h.must(err)
	}
	mem, err := h.st.CreateMember(ctx, orgID, name, "owner")
	h.must(err)
	h.must(h.st.LinkPhone(ctx, "openwa", phone, mem))
	ident, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	_, err = h.st.GetOrCreateConversation(ctx, ident.ID, phone+"@c.us")
	h.must(err)
	return ident
}

// operatorChattedAgo faz o número da equipe ter escrito ao bot há `ago` (abre a janela de 24 h do chat dele).
func (h *harness) operatorChattedAgo(ago time.Duration) {
	h.t.Helper()
	op := h.seedIdentity(0, "Equipe", "5511900000000")
	conv, err := h.st.GetOrCreateConversation(ctx, op.ID, operatorChat)
	h.must(err)
	_, err = h.st.AddInbound(ctx, conv.ID, "text", "oi")
	h.must(err)
	_, err = h.pool.Exec(ctx, `UPDATE messages SET created_at = $1 WHERE conversation_id = $2`, h.now.Add(-ago), conv.ID)
	h.must(err)
}

// clientQuietFor joga as mensagens do cliente para `ago` atrás (fecha a janela de 24 h do cliente).
func (h *harness) clientQuietFor(ago time.Duration) {
	h.t.Helper()
	_, err := h.pool.Exec(ctx, `UPDATE messages SET created_at = $1 WHERE conversation_id = $2`, h.now.Add(-ago), h.conv().ID)
	h.must(err)
}

// C1: link de página/post/rota da Meta no meio da conversa vai para a Atendente, não vira pedido.
func TestProfileLinkFalsePositivesGoToAttendant(t *testing.T) {
	for _, text := range []string{
		"olha nossa página facebook.com/pizzariaboa, ficou boa com as fotos novas?",
		"https://www.facebook.com/pizzariaboa/posts/123 o que achou desse post?",
		"vi isso https://www.facebook.com/v18.0/dialog/oauth?client_id=1 é normal?",
		"https://www.facebook.com/commerce/products/1",
		"https://www.facebook.com/pizzariaboa/",
	} {
		t.Run(text, func(t *testing.T) {
			h := newRealTesterHarness(t, "owner")
			if strings.HasSuffix(text, "pizzariaboa/") {
				text = "dá uma olhada na nossa página " + text + " e me diz o que acha das fotos"
			}
			h.att.Responses = append(h.att.Responses, reply)
			h.say(text)
			h.runJobs()
			if h.att.Calls() != 1 || h.testerRows() != 0 || len(h.wa.to(operatorChat)) != 0 {
				t.Fatalf("ia=%d pedidos=%d equipe=%v", h.att.Calls(), h.testerRows(), h.wa.to(operatorChat))
			}
		})
	}
}

func TestProfileLinkIgnoredWhenMetaConnected(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	h.connectMeta()
	h.att.Responses = append(h.att.Responses, reply)
	h.say("https://www.facebook.com/maria.silva.5")
	h.runJobs()
	if h.att.Calls() != 1 || h.testerRows() != 0 {
		t.Fatalf("conta conectada: ia=%d pedidos=%d", h.att.Calls(), h.testerRows())
	}
}

func TestProfileLinkFromNonOwnerGoesToAttendant(t *testing.T) {
	h := newRealTesterHarness(t, "approver")
	h.att.Responses = append(h.att.Responses, reply)
	h.say("https://www.facebook.com/maria.silva.5")
	h.runJobs()
	if h.att.Calls() != 1 || h.testerRows() != 0 {
		t.Fatalf("quem não é responsável: ia=%d pedidos=%d", h.att.Calls(), h.testerRows())
	}
}

func TestProfileLinkWithContextOpensRequest(t *testing.T) {
	// Palavra-chave no texto.
	h := newRealTesterHarness(t, "owner")
	h.say("oi, segue o link do meu perfil facebook.com/maria.silva.5 pra vocês me liberarem lá")
	h.runJobs()
	if h.att.Calls() != 0 || h.testerRows() != 1 {
		t.Fatalf("com palavra-chave: ia=%d pedidos=%d", h.att.Calls(), h.testerRows())
	}
	// Link de conexão mandado há pouco: texto comprido sem palavra-chave também vale.
	h2 := newRealTesterHarness(t, "owner")
	h2.say("conectar meta")
	h2.say("deu um erro aqui, olha o que eu tenho https://www.facebook.com/maria.silva.5 e agora o que faço")
	h2.runJobs()
	if h2.testerRows() != 1 {
		t.Fatalf("depois do link de conexão: pedidos=%d", h2.testerRows())
	}
}

// I4: no máximo um aviso de pedido novo à equipe a cada 30 min.
func TestTesterTeamNoticeRateLimited(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	h.say("https://www.facebook.com/maria.silva.5")
	h.say("https://www.facebook.com/joao.souza")
	h.say("https://www.facebook.com/ana.lima")
	h.runJobs()
	if ops := h.wa.to(operatorChat); len(ops) != 1 {
		t.Fatalf("avisos em 30 min = %d: %v", len(ops), ops)
	}
	_, err := h.pool.Exec(ctx, `UPDATE tester_requests SET notified_at = now() - interval '31 minutes'`)
	h.must(err)
	h.say("https://www.facebook.com/bia.mel")
	h.runJobs()
	if ops := h.wa.to(operatorChat); len(ops) != 2 || !strings.Contains(ops[1], "bia.mel") {
		t.Fatalf("depois de 30 min = %v", ops)
	}
}

// I1: no número oficial, o aviso vai em texto completo quando o chat da equipe está na janela.
func TestTeamNoticeInsideTeamWindowSendsFullText(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.eng.AdminURL = "https://admin.teste/"
	h.operatorChattedAgo(time.Hour)
	h.say(profileLink)
	h.runJobs()
	ops := h.wa.to(operatorChat)
	if len(ops) != 1 || len(wa.tpls) != 0 {
		t.Fatalf("texto=%v modelos=%+v", ops, wa.tpls)
	}
	for _, want := range []string{"🧪", "1000123", profileLink, "https://developers.facebook.com/apps/999/roles/roles/"} {
		if !strings.Contains(ops[0], want) {
			t.Errorf("aviso sem %q: %q", want, ops[0])
		}
	}
}

// I1: fora da janela vai o modelo, com o essencial do testador no motivo.
func TestTeamNoticeOutsideTeamWindowUsesTemplateWithRef(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.operatorChattedAgo(30 * time.Hour)
	h.say(profileLink)
	h.runJobs()
	if len(h.wa.to(operatorChat)) != 0 || len(wa.tpls) != 1 || wa.tpls[0].Name != engine.TplTeamAlert {
		t.Fatalf("texto=%v modelos=%+v", h.wa.to(operatorChat), wa.tpls)
	}
	reason := wa.tpls[0].Params[3]
	if !strings.Contains(reason, "testador: ID/usuário 1000123") || !strings.Contains(reason, "Testadores") || strings.ContainsAny(reason, "\n\t") {
		t.Fatalf("motivo = %q", reason)
	}
}

// M5: aviso de testador fora da janela do cliente não sai com o rótulo de atendimento humano.
func TestTesterOutsideClientWindowNoticeLabel(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.operatorChattedAgo(time.Hour)
	h.say(profileLink)
	h.runJobs()
	r, err := h.st.OpenTesterRequest(ctx, h.ident().ID)
	h.must(err)
	_, err = h.st.MarkTesterAdded(ctx, r.ID, "x")
	h.must(err)
	h.clientQuietFor(30 * time.Hour)
	enqueueAdded(h, r.ID)
	ops := h.wa.to(operatorChat)
	last := ops[len(ops)-1]
	if strings.Contains(last, "Atendimento humano") || !strings.Contains(last, "🧪") || !strings.Contains(last, "janela") {
		t.Fatalf("aviso = %q", last)
	}
}

// I2: lembrete de 24 h com o cliente fora da janela: nada ao cliente e UM aviso à equipe.
func TestTesterReminderOutsideClientWindowSingleTeamNotice(t *testing.T) {
	h, ft := newTesterHarness(t)
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.eng.AppTesters = func(context.Context) ([]string, error) { return nil, nil }
	id := openRequest(h, ft)
	ft.markAdded(id, h.now.Add(-25*time.Hour))
	h.clientQuietFor(30 * time.Hour)
	tplsBefore, clientBefore := len(wa.tpls), len(h.wa.to(chatID))
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.runPoll()
	if len(h.wa.to(chatID)) != clientBefore || h.count(`SELECT count(*) FROM meta_connect_links`) != 0 {
		t.Fatal("fora da janela não manda lembrete ao cliente")
	}
	got := wa.tpls[tplsBefore:]
	if len(got) != 1 || !strings.Contains(got[0].Params[3], "janela") {
		t.Fatalf("avisos à equipe = %+v", got)
	}
	h.runPoll()
	if len(wa.tpls) != tplsBefore+1 {
		t.Fatal("lembrete só uma vez")
	}
}

// I2: dentro da janela: lembrete ao cliente e um aviso à equipe.
func TestTesterReminderInsideClientWindow(t *testing.T) {
	h, ft := newTesterHarness(t)
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.eng.AppTesters = func(context.Context) ([]string, error) { return nil, nil }
	id := openRequest(h, ft)
	ft.markAdded(id, h.now.Add(-25*time.Hour))
	h.clientQuietFor(time.Hour)
	tplsBefore := len(wa.tpls)
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.runPoll()
	if !strings.Contains(h.sentText(), "aceitar o convite") {
		t.Fatalf("cliente = %q", h.sentText())
	}
	if got := wa.tpls[tplsBefore:]; len(got) != 1 {
		t.Fatalf("avisos à equipe = %+v", got)
	}
}

// I3: "Vi que você aceitou" só com um candidato aberto e uma vez só por pedido.
func TestTesterAcceptNoticeOnceAndOnlyWithSingleCandidate(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	ids := []string{"a"}
	h.eng.AppTesters = func(context.Context) ([]string, error) { return ids, nil }
	h.say(profileLink)
	h.runJobs()
	r, err := h.st.OpenTesterRequest(ctx, h.ident().ID)
	h.must(err)
	_, err = h.st.MarkTesterAdded(ctx, r.ID, "x")
	h.must(err)
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.runPoll() // linha de base
	ids = []string{"a", "b"}
	h.runPoll()
	if n := strings.Count(h.sentText(), "Vi que você aceitou"); n != 1 {
		t.Fatalf("avisos de aceite = %d", n)
	}
	ids = []string{"a", "b", "c"}
	h.runPoll()
	if n := strings.Count(h.sentText(), "Vi que você aceitou"); n != 1 {
		t.Fatalf("aceite repetido: %d", n)
	}
}

func TestTesterAcceptNoticeSkipsWhenAnotherRequestIsOpen(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	ids := []string{"a"}
	h.eng.AppTesters = func(context.Context) ([]string, error) { return ids, nil }
	h.say(profileLink)
	h.runJobs()
	r, err := h.st.OpenTesterRequest(ctx, h.ident().ID)
	h.must(err)
	_, err = h.st.MarkTesterAdded(ctx, r.ID, "x")
	h.must(err)
	// Outro cliente pediu (ainda requested): o testador novo pode ser qualquer um dos dois.
	other := h.seedIdentity(0, "Beto", "5511933334444")
	_, err = h.st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: other.ID, ProfileURL: "https://www.facebook.com/beto.b",
		RefKind: "username", RefValue: "beto.b"})
	h.must(err)
	h.must(h.eng.EnsureTesterPoll(ctx))
	h.runPoll()
	ids = []string{"a", "b"}
	h.runPoll()
	if strings.Contains(h.sentText(), "Vi que você aceitou") || len(h.wa.to("5511933334444@c.us")) != 0 {
		t.Fatal("dois candidatos: não dá pra saber quem aceitou")
	}
}

// I5: conexão Meta concluída fecha todos os pedidos abertos da organização.
func TestMetaConnectedClosesAllOrgTesterRequests(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	h.say(profileLink)
	h.runJobs()
	colleague := h.seedIdentity(h.ident().OrgID, "Beto", "5511933334444")
	_, err := h.st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: colleague.ID, ProfileURL: "https://www.facebook.com/beto.b",
		RefKind: "username", RefValue: "beto.b"})
	h.must(err)
	conv := h.conv()
	h.must(h.st.Enqueue(ctx, engine.MetaConnectedJob(conv.ID, chatID, engine.MetaSummary{AccountName: "Loja", Currency: "BRL", AccountStatus: 1, PageName: "Loja", PageWhatsApp: true})))
	for range 4 {
		h.runJobs()
		_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE status = 'pending'`)
		h.must(err)
	}
	if n := h.count(`SELECT count(*) FROM tester_requests WHERE status IN ('requested', 'added')`); n != 0 {
		t.Fatalf("pedidos abertos = %d", n)
	}
}

// M1: o link de compartilhamento é resolvido num job próprio; o turno da mensagem volta logo.
func TestShareLinkResolvedInOwnJob(t *testing.T) {
	h := newRealTesterHarness(t, "owner")
	called := 0
	h.eng.ResolveShare = func(context.Context, string) (string, error) {
		called++
		return "https://www.facebook.com/joao.silva", nil
	}
	h.say("https://www.facebook.com/share/1AbCd")
	if called != 0 || h.pendingJobs(engine.JobTesterResolve) != 1 {
		t.Fatalf("resolveu no turno: chamadas=%d jobs=%d", called, h.pendingJobs(engine.JobTesterResolve))
	}
	h.runJobs()
	if called != 1 || h.testerRows() != 1 {
		t.Fatalf("chamadas=%d pedidos=%d", called, h.testerRows())
	}
}
