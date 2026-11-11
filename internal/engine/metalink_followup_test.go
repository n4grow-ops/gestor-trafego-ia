package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// openLink simula a api: cliente abriu o link de conexão e o login não voltou do Facebook.
func openLink(h *harness) int64 {
	h.t.Helper()
	var id int64
	h.must(h.pool.QueryRow(ctx, `SELECT id FROM meta_connect_links WHERE used_at IS NULL ORDER BY id DESC LIMIT 1`).Scan(&id))
	// O relógio da Mia nos testes é de 2026-09: o vencimento do link segue o relógio do banco.
	_, err := h.pool.Exec(ctx, `UPDATE meta_connect_links SET expires_at = now() + interval '1 hour' WHERE id = $1`, id)
	h.must(err)
	first, err := h.st.MarkMetaLinkOpened(ctx, id)
	h.must(err)
	if !first {
		h.t.Fatal("primeira abertura não marcada")
	}
	h.must(h.st.Enqueue(ctx, store.JobSpec{Kind: engine.JobMetaLinkFollowup, Payload: engine.MetaLinkFollowupJob{LinkID: id}}))
	return id
}

// Caso real (11/11): cliente abriu o link, o Facebook disse "app indisponível" (não era testador) e
// ninguém pediu o perfil. Agora a Mia pede, uma vez, e o link do perfil abre o pedido na fila.
func TestOpenedLinkWithoutReturnAsksProfile(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.say("conectar meta")
	h.runJobs()
	id := openLink(h)
	h.runJobs()
	if !strings.Contains(h.sentText(), "Vi que você abriu o link pra conectar a conta") ||
		!strings.Contains(h.sentText(), "Copiar link do perfil") {
		t.Fatalf("cliente = %q", h.sentText())
	}
	n := len(h.wa.to(chatID))
	// O mesmo link aberto de novo (ou job repetido) não pede outra vez.
	h.must(h.st.Enqueue(ctx, store.JobSpec{Kind: engine.JobMetaLinkFollowup, Payload: engine.MetaLinkFollowupJob{LinkID: id}}))
	h.runJobs()
	if again, _ := h.st.MarkMetaLinkOpened(ctx, id); again || len(h.wa.to(chatID)) != n {
		t.Fatalf("pediu de novo: %v", h.wa.to(chatID)[n:])
	}
	h.say(profileLink)
	h.runJobs()
	if reqs := ft.all(); len(reqs) != 1 || reqs[0].RefValue != "1000123" {
		t.Fatalf("pedidos = %+v", reqs)
	}
}

func TestOpenedLinkSkipsWhenProfileAlreadySent(t *testing.T) {
	h, ft := newTesterHarness(t)
	h.say("conectar meta")
	h.runJobs()
	openRequest(h, ft)
	n := len(h.wa.to(chatID))
	openLink(h)
	h.runJobs()
	if got := h.wa.to(chatID)[n:]; len(got) != 0 {
		t.Fatalf("não devia pedir o perfil de novo: %v", got)
	}
}

func TestOpenedLinkSkipsWhenConnected(t *testing.T) {
	h, _ := newTesterHarness(t)
	h.say("conectar meta")
	h.runJobs()
	h.connectMeta()
	n := len(h.wa.to(chatID))
	openLink(h)
	h.runJobs()
	if got := h.wa.to(chatID)[n:]; len(got) != 0 {
		t.Fatalf("conectado não precisa de testador: %v", got)
	}
}
