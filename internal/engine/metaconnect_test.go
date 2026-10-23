package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const metaBase = "https://cadastro.teste/m"

func newMetaHarness(t *testing.T, role string) *harness {
	h := newHarness(t, role)
	h.eng.MetaConnectBaseURL = metaBase
	return h
}

func (h *harness) sentText() string {
	h.t.Helper()
	return strings.Join(h.wa.to(chatID), "\n")
}

func (h *harness) lastPrompt() string {
	h.t.Helper()
	return h.att.Requests[len(h.att.Requests)-1].Messages[1].Content
}

// connectMeta grava uma conexão ativa direto pelo store.
func (h *harness) connectMeta() {
	h.t.Helper()
	ident, err := h.st.FindIdentity(ctx, "openwa", phone)
	h.must(err)
	h.must(h.st.CreateMetaLink(ctx, ident.ID, "hash-teste", time.Now().Add(time.Hour)))
	l, err := h.st.MetaLink(ctx, "hash-teste")
	h.must(err)
	acc := meta.AdAccount{ID: "act_1", Name: "Loja", Currency: "BRL", Status: 1}
	pg := meta.Page{ID: "P1", Name: "Loja Page"}
	id, err := h.st.SavePendingMeta(ctx, l, "U1", []byte("x"), store.MetaCandidates{AdAccounts: []meta.AdAccount{acc}, Pages: []meta.Page{pg}})
	h.must(err)
	_, err = h.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id, LinkID: l.ID, Account: acc, Page: pg})
	h.must(err)
}

func TestConnectMetaCommandSendsLinkWithoutAI(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.say("quero conectar a conta de anúncios")
	if h.att.Calls() != 0 {
		t.Fatal("comando não deve chamar a IA")
	}
	if !strings.Contains(h.sentText(), metaBase+"/") {
		t.Fatalf("sem link: %q", h.sentText())
	}
	if n := h.count(`SELECT count(*) FROM meta_connect_links`); n != 1 {
		t.Fatalf("links = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM messages WHERE body LIKE '%/m/[link]%'`); n != 1 {
		t.Fatal("link não foi mascarado no histórico")
	}
	h.say("conectar meta")
	if n := h.count(`SELECT count(*) FROM meta_connect_links`); n != 1 {
		t.Fatal("reenviou link em menos de 10 minutos")
	}
	if !strings.Contains(h.sentText(), "há pouco") {
		t.Fatalf("sem aviso de link recente: %q", h.sentText())
	}
}

func TestConnectMetaOnlyForOwner(t *testing.T) {
	h := newMetaHarness(t, "approver")
	h.say("conectar facebook")
	if h.count(`SELECT count(*) FROM meta_connect_links`) != 0 || !strings.Contains(h.sentText(), "responsável") {
		t.Fatalf("membro recebeu link: %q", h.sentText())
	}
}

func TestConnectMetaCommandIgnoredWhenDisabled(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Posso ajudar?"}`)
	h.say("conectar meta")
	if h.att.Calls() != 1 || h.count(`SELECT count(*) FROM meta_connect_links`) != 0 {
		t.Fatal("desligado: vai para a IA e não gera link")
	}
	if strings.Contains(h.lastPrompt(), "Conta Meta") {
		t.Fatal("desligado: IA não deve saber de conexão")
	}
}

func TestAttendantConnectMetaIntentSendsReplyThenLink(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.att.Responses = append(h.att.Responses, `{"intent":"connect_meta","reply":"Claro! Te mando o link pra conectar."}`)
	h.say("pode mandar o negócio do face")
	txt := h.sentText()
	if !strings.Contains(txt, "Claro!") || !strings.Contains(txt, metaBase+"/") {
		t.Fatalf("enviado = %q", txt)
	}
	if strings.Index(txt, "Claro!") > strings.Index(txt, metaBase) {
		t.Fatal("link veio antes da resposta")
	}
	if !strings.Contains(h.lastPrompt(), "Conta Meta: não conectada") {
		t.Fatal("IA não soube que a conta não está conectada")
	}
}

func TestAttendantSeesConnectedAccount(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.connectMeta()
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Tá conectada sim."}`)
	h.say("minha conta tá ligada?")
	if !strings.Contains(h.lastPrompt(), "Conta Meta: conectada: Loja") {
		t.Fatalf("IA não recebeu a conta conectada: %s", h.lastPrompt())
	}
}

func jobText(j store.JobSpec) string {
	s := j.Payload.(engine.SendJob)
	return strings.Join(append([]string{s.Text}, s.Next...), "\n")
}

func TestMetaConnectedMessage(t *testing.T) {
	text := jobText(engine.MetaConnectedJob(1, chatID, engine.MetaSummary{AccountName: "Conta 1", Currency: "USD",
		Timezone: "America/Los_Angeles", AccountStatus: 2, PageName: "Clara Luz"}))
	for _, want := range []string{"Conectado", "Conta 1", "Clara Luz", "sem Instagram", "USD", "desativada", "WhatsApp Business"} {
		if !strings.Contains(text, want) {
			t.Errorf("falta %q em %q", want, text)
		}
	}
	ok := jobText(engine.MetaConnectedJob(1, chatID, engine.MetaSummary{AccountName: "Loja", Currency: "BRL", Timezone: "America/Sao_Paulo",
		AccountStatus: 1, PageName: "Loja", Instagram: "loja", PageWhatsApp: true}))
	if strings.Contains(ok, "WhatsApp Business") || strings.Contains(ok, "fuso") || strings.Contains(ok, "⚠️") || !strings.Contains(ok, "@loja") {
		t.Fatalf("sem avisos esperado: %q", ok)
	}
	// Caso do cliente: a Meta não respondeu se há WhatsApp; não afirma que a Página não tem.
	unk := jobText(engine.MetaConnectedJob(1, chatID, engine.MetaSummary{AccountName: "Loja", Currency: "BRL", Timezone: "America/Sao_Paulo",
		AccountStatus: 1, PageName: "Loja", Instagram: "loja", PageWhatsAppUnknown: true}))
	if strings.Contains(unk, "ainda não tem WhatsApp") || strings.Contains(unk, "WhatsApp Business") {
		t.Fatalf("aviso de sem WhatsApp com detecção incerta: %q", unk)
	}
}

func TestPreviewAsksForMetaConnection(t *testing.T) {
	h, _ := newCreativeHarness(t)
	h.eng.MetaConnectBaseURL = metaBase
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("pronto")
	h.say("pronto")
	h.runKind(engine.JobCreativesPreview)
	if len(h.wa.sentImages()) != 1 {
		t.Fatal("prévia não saiu")
	}
	if !strings.Contains(h.sentText(), metaBase+"/") || h.count(`SELECT count(*) FROM meta_connect_links`) != 1 {
		t.Fatalf("sem pedido de conexão depois da prévia: %q", h.sentText())
	}
}

func TestPreviewSkipsLinkWhenConnected(t *testing.T) {
	h, _ := newCreativeHarness(t)
	h.eng.MetaConnectBaseURL = metaBase
	h.connectMeta()
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("pronto")
	h.say("pronto")
	h.runKind(engine.JobCreativesPreview)
	if len(h.wa.sentImages()) != 1 {
		t.Fatal("prévia não saiu")
	}
	if strings.Contains(h.sentText(), metaBase+"/") || h.count(`SELECT count(*) FROM meta_connect_links`) != 1 {
		t.Fatal("pediu conexão de quem já está conectado")
	}
}

func TestConnectMetaShortcutDoesNotSwallowApproval(t *testing.T) {
	h := newMetaHarness(t, "owner")
	code := h.reachApproval()
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Primeiro aprova a proposta que eu já te mando o link."}`)
	h.say("conectar facebook")
	if h.count(`SELECT count(*) FROM meta_connect_links`) != 0 {
		t.Fatal("atalho passou na frente da aprovação")
	}
	h.say("SIM " + code)
	if h.conv().State == store.StateAwaitingApproval {
		t.Fatalf("aprovação não andou: estado = %s", h.conv().State)
	}
}

func TestConnectMetaShortcutIgnoresLongSentences(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Tudo bem!"}`)
	h.say("agora não quero conectar meta, só tirar uma dúvida")
	if h.count(`SELECT count(*) FROM meta_connect_links`) != 0 || h.att.Calls() != 1 {
		t.Fatal("frase longa virou comando")
	}
}

func TestPreviewNotResentWhenMetaLinkFails(t *testing.T) {
	h, _ := newCreativeHarness(t)
	h.eng.MetaConnectBaseURL = metaBase
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("pronto")
	h.say("pronto")
	_, err := h.pool.Exec(ctx, `ALTER TABLE meta_connect_links RENAME TO meta_connect_links_off`)
	h.must(err)
	h.runKind(engine.JobCreativesPreview)
	if n := h.count(`SELECT count(*) FROM outbox_jobs WHERE kind = $1 AND status <> 'done'`, engine.JobCreativesPreview); n != 0 {
		t.Fatalf("prévia vai ser reenviada: %d jobs não concluídos", n)
	}
}
