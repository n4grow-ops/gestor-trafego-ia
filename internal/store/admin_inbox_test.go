package store_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func staffJob(convID int64, chatID, text, author string) store.JobSpec {
	return store.JobSpec{Kind: "whatsapp.send", Payload: map[string]any{
		"conversation_id": convID, "chat_id": chatID, "text": text, "author": author}}
}

func TestTakeOverSendAndRelease(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	f := contract.Fields{Offer: contract.Str("pizza em dobro")}
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateCollecting, f))
	inID, err := st.AddInbound(ctx, conv.ID, "text", "quero falar com alguém")
	must(t, err)

	// Com a IA atendendo, ninguém da equipe escreve.
	if err := st.EnqueueStaffMessage(ctx, conv.ID, func(chatID string) store.JobSpec {
		return staffJob(conv.ID, chatID, "oi", "Marina")
	}); !errors.Is(err, store.ErrNotHuman) {
		t.Fatalf("enviar sem assumir: %v", err)
	}

	prev, err := st.TakeOverConversation(ctx, conv.ID, "marina@meuanuncio.io")
	if err != nil || prev != store.StateCollecting {
		t.Fatalf("assumir: prev=%q err=%v", prev, err)
	}
	c, err := st.GetConversation(ctx, conv.ID)
	if err != nil || c.State != store.StateNeedsHuman {
		t.Fatalf("estado depois de assumir: %+v %v", c, err)
	}
	var repliedThrough int64
	must(t, pool.QueryRow(ctx, `SELECT replied_through FROM conversations WHERE id = $1`, conv.ID).Scan(&repliedThrough))
	if repliedThrough != inID {
		t.Fatalf("assumir deve marcar as mensagens como respondidas: %d, quero %d", repliedThrough, inID)
	}
	// Assumir de novo (outra pessoa) mantém o estado de volta original.
	if prev, err = st.TakeOverConversation(ctx, conv.ID, "dono@meuanuncio.io"); err != nil || prev != store.StateNeedsHuman {
		t.Fatalf("reassumir: %q %v", prev, err)
	}

	must(t, st.EnqueueStaffMessage(ctx, conv.ID, func(chatID string) store.JobSpec {
		return staffJob(conv.ID, chatID, "Oi, aqui é a Marina!", "Marina")
	}))
	var payload []byte
	must(t, pool.QueryRow(ctx, `SELECT payload FROM outbox_jobs WHERE kind = 'whatsapp.send'`).Scan(&payload))
	var p map[string]any
	must(t, json.Unmarshal(payload, &p))
	if p["chat_id"] != "5511988887777@c.us" || p["author"] != "Marina" {
		t.Fatalf("job: %s", payload)
	}

	thread, err := st.Thread(ctx, conv.ID, 0)
	must(t, err)
	if thread.State != store.StateNeedsHuman || thread.HumanBy != "dono@meuanuncio.io" || thread.HumanSince == nil {
		t.Fatalf("cabeçalho: %+v", thread)
	}
	if len(thread.Messages) != 1 || thread.Messages[0].Body != "quero falar com alguém" {
		t.Fatalf("mensagens: %+v", thread.Messages)
	}
	if len(thread.Pending) != 1 || thread.Pending[0].Text != "Oi, aqui é a Marina!" || thread.Pending[0].Author != "Marina" || thread.Pending[0].Status != "pending" {
		t.Fatalf("envio na fila: %+v", thread.Pending)
	}
	if thread.LastInboundAt == nil || thread.ClientName != "Ana" || thread.Phone != "5511988887777" || thread.OrgID != ident.OrgID {
		t.Fatalf("cliente: %+v", thread)
	}
	// Envio entregue: o worker grava a mensagem da equipe e ela sai da fila.
	must(t, st.AddStaffMessage(ctx, conv.ID, "Oi, aqui é a Marina!", "Marina"))
	_, err = pool.Exec(ctx, `UPDATE outbox_jobs SET status = 'done'`)
	must(t, err)
	thread, _ = st.Thread(ctx, conv.ID, inID)
	if len(thread.Messages) != 1 || thread.Messages[0].Kind != "staff" || thread.Messages[0].Author != "Marina" || len(thread.Pending) != 0 {
		t.Fatalf("depois da entrega (só as novas): %+v %+v", thread.Messages, thread.Pending)
	}

	state, err := st.ReleaseConversation(ctx, conv.ID)
	if err != nil || state != store.StateCollecting {
		t.Fatalf("devolver: %q %v", state, err)
	}
	c, _ = st.GetConversation(ctx, conv.ID)
	if c.State != store.StateCollecting || c.Fields.Offer == nil || *c.Fields.Offer != "pizza em dobro" {
		t.Fatalf("devolver mantém os campos: %+v", c)
	}
	thread, _ = st.Thread(ctx, conv.ID, 0)
	if thread.HumanBy != "" || thread.HumanSince != nil {
		t.Fatalf("devolver limpa quem atendia: %+v", thread)
	}
	if _, err := st.ReleaseConversation(ctx, conv.ID); !errors.Is(err, store.ErrNotHuman) {
		t.Fatalf("devolver de novo: %v", err)
	}
	if _, err := st.TakeOverConversation(ctx, 9999, "x@y.z"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("conversa inexistente: %v", err)
	}
}

func TestReleaseAfterEscalationResetsLikeCLI(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, _ := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	// O engine escalou (cliente pediu humano): não há estado anterior guardado.
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateNeedsHuman, contract.Fields{Offer: contract.Str("x")}))
	state, err := st.ReleaseConversation(ctx, conv.ID)
	if err != nil || state != store.StateIdle {
		t.Fatalf("devolver depois de escalar: %q %v", state, err)
	}
	c, _ := st.GetConversation(ctx, conv.ID)
	if c.Fields.Offer != nil {
		t.Fatalf("igual ao resolve-human, os campos voltam vazios: %+v", c.Fields)
	}
}

func TestReleaseFromAwaitingProposalGoesBackToCollecting(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, _ := st.GetOrCreateConversation(ctx, ident.ID, "c")
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateAwaitingProposal, contract.Fields{}))
	_, err := st.TakeOverConversation(ctx, conv.ID, "a@b.co")
	must(t, err)
	// O job da proposta viu a conversa com humano e desistiu: esperar proposta travaria.
	if state, err := st.ReleaseConversation(ctx, conv.ID); err != nil || state != store.StateCollecting {
		t.Fatalf("%q %v", state, err)
	}
}

func TestInbox(t *testing.T) {
	st, _ := newStore(t)
	a := seedIdentity(t, st, "owner", "5511911111111")
	b := seedIdentity(t, st, "owner", "5511922222222")
	ca, _ := st.GetOrCreateConversation(ctx, a.ID, "a@c.us")
	cb, _ := st.GetOrCreateConversation(ctx, b.ID, "b@c.us")
	must(t, st.AddMessage(ctx, ca.ID, "in", "text", "oi"))
	must(t, st.AddMessage(ctx, ca.ID, "out", "text", "olá!"))
	must(t, st.AddMessage(ctx, cb.ID, "in", "text", "preciso de ajuda"))
	must(t, st.UpdateConversation(ctx, cb.ID, store.StateNeedsHuman, contract.Fields{}))

	all, err := st.Inbox(ctx, store.InboxFilter{})
	must(t, err)
	if len(all) != 2 || all[0].ConversationID != cb.ID || !all[0].NeedsHuman || all[0].LastBody != "preciso de ajuda" || all[0].Waiting != 1 {
		t.Fatalf("pedido de humano primeiro: %+v", all)
	}
	if all[1].Waiting != 0 || all[1].LastDirection != "out" {
		t.Fatalf("respondida: %+v", all[1])
	}
	human, _ := st.Inbox(ctx, store.InboxFilter{Filter: "humano"})
	if len(human) != 1 || human[0].ConversationID != cb.ID {
		t.Fatalf("filtro humano: %+v", human)
	}
	waiting, _ := st.Inbox(ctx, store.InboxFilter{Filter: "sem_resposta"})
	if len(waiting) != 1 || waiting[0].ConversationID != cb.ID {
		t.Fatalf("filtro sem resposta: %+v", waiting)
	}
	q, _ := st.Inbox(ctx, store.InboxFilter{Q: "1111"})
	if len(q) != 1 || q[0].ConversationID != ca.ID {
		t.Fatalf("busca por telefone: %+v", q)
	}
	counts, err := st.InboxCounts(ctx)
	if err != nil || counts.NeedsHuman != 1 || counts.Unassigned != 1 || counts.Waiting != 1 {
		t.Fatalf("contagens: %+v %v", counts, err)
	}
}
