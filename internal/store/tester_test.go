package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestTesterRequestLifecycle(t *testing.T) {
	st, pool := newStore(t)
	id := seedIdentity(t, st, "owner", "5511911110001")
	_, err := pool.Exec(ctx, `UPDATE organizations SET trade_name = 'Doceria Mel' WHERE id = $1`, id.OrgID)
	must(t, err)
	_, err = pool.Exec(ctx, `UPDATE members SET full_name = 'Bia Mel' WHERE id = $1`, id.MemberID)
	must(t, err)
	conv, err := st.GetOrCreateConversation(ctx, id.ID, "5511911110001@c.us")
	must(t, err)

	if _, err := st.OpenTesterRequest(ctx, id.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sem pedido: %v", err)
	}
	r, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id.ID, ConversationID: conv.ID,
		ProfileURL: "https://www.facebook.com/maria.silva", RefKind: "username", RefValue: "maria.silva"})
	must(t, err)
	if r.ID == 0 || r.OrgID != id.OrgID || r.Status != store.TesterRequested || r.ConversationID != conv.ID || r.RequestedAt.IsZero() || r.AddedAt != nil {
		t.Fatalf("pedido = %+v", r)
	}
	// Mandou outro link: o mesmo pedido aberto troca o perfil.
	r2, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id.ID,
		ProfileURL: "https://www.facebook.com/profile.php?id=100012345678901", RefKind: "id", RefValue: "100012345678901"})
	must(t, err)
	if r2.ID != r.ID || r2.RefKind != "id" || r2.RefValue != "100012345678901" || r2.ConversationID != conv.ID || r2.Status != store.TesterRequested {
		t.Fatalf("atualizado = %+v", r2)
	}
	open, err := st.OpenTesterRequest(ctx, id.ID)
	must(t, err)
	if open.ID != r.ID || open.ProfileURL != r2.ProfileURL {
		t.Fatalf("aberto = %+v", open)
	}

	list, err := st.ListTesterRequests(ctx, "")
	must(t, err)
	if len(list) != 1 || list[0].ID != r.ID || list[0].MemberName != "Bia Mel" || list[0].OrgName != "Doceria Mel" || list[0].Phone != "5511911110001" {
		t.Fatalf("fila = %+v", list)
	}

	v, err := st.MarkTesterAdded(ctx, r.ID, "dono@meuanuncio.io")
	must(t, err)
	if v.Status != store.TesterAdded || v.AddedAt == nil || v.AddedBy != "dono@meuanuncio.io" || v.Phone != "5511911110001" || v.OrgName != "Doceria Mel" {
		t.Fatalf("adicionado = %+v", v)
	}
	if _, err := st.MarkTesterAdded(ctx, r.ID, "outro@meuanuncio.io"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("adicionar de novo: %v", err)
	}
	if _, err := st.MarkTesterAdded(ctx, 999999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("adicionar inexistente: %v", err)
	}
	if l, _ := st.ListTesterRequests(ctx, store.TesterAdded); len(l) != 1 {
		t.Fatalf("fila added = %+v", l)
	}
	if l, _ := st.ListTesterRequests(ctx, store.TesterRequested); len(l) != 0 {
		t.Fatalf("fila requested = %+v", l)
	}

	// Aguardando aceite: added sem conexão Meta ativa.
	aw, err := st.TestersAwaitingAcceptance(ctx)
	must(t, err)
	if len(aw) != 1 || aw[0].ID != r.ID || aw[0].Phone != "5511911110001" {
		t.Fatalf("aguardando = %+v", aw)
	}
	must(t, st.MarkTesterReminded(ctx, r.ID))
	if err := st.MarkTesterReminded(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lembrar duas vezes: %v", err)
	}
	open, err = st.OpenTesterRequest(ctx, id.ID)
	must(t, err)
	if open.RemindedAt == nil {
		t.Fatalf("lembrete não gravado: %+v", open)
	}

	must(t, st.MarkTesterAcceptedByIdentity(ctx, id.ID))
	if _, err := st.OpenTesterRequest(ctx, id.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ainda aberto: %v", err)
	}
	acc, err := st.ListTesterRequests(ctx, store.TesterAccepted)
	must(t, err)
	if len(acc) != 1 || acc[0].AcceptedAt == nil {
		t.Fatalf("aceitos = %+v", acc)
	}
	if l, _ := st.ListTesterRequests(ctx, ""); len(l) != 0 {
		t.Fatalf("fila aberta = %+v", l)
	}
	// Sem pedido aberto: nada a fazer, sem erro.
	must(t, st.MarkTesterAcceptedByIdentity(ctx, id.ID))

	// Pedido novo depois de fechado abre outra linha.
	r3, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id.ID,
		ProfileURL: "https://www.facebook.com/maria.silva", RefKind: "username", RefValue: "maria.silva"})
	must(t, err)
	if r3.ID == r.ID || r3.Status != store.TesterRequested || r3.ConversationID != conv.ID {
		t.Fatalf("novo pedido = %+v", r3)
	}
}

func TestTesterRequestProfileChangeAfterAdded(t *testing.T) {
	st, _ := newStore(t)
	id := seedIdentity(t, st, "owner", "5511911110002")
	r, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id.ID,
		ProfileURL: "https://www.facebook.com/maria.silva", RefKind: "username", RefValue: "maria.silva"})
	must(t, err)
	_, err = st.MarkTesterAdded(ctx, r.ID, "dono@meuanuncio.io")
	must(t, err)
	// O mesmo perfil de novo não desfaz o "Adicionei".
	same, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id.ID,
		ProfileURL: "https://www.facebook.com/maria.silva", RefKind: "username", RefValue: "maria.silva"})
	must(t, err)
	if same.Status != store.TesterAdded || same.AddedAt == nil || same.ConversationID != 0 {
		t.Fatalf("mesmo perfil = %+v", same)
	}
	// Perfil diferente: a equipe precisa adicionar de novo.
	other, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id.ID,
		ProfileURL: "https://www.facebook.com/profile.php?id=42", RefKind: "id", RefValue: "42"})
	must(t, err)
	if other.ID != r.ID || other.Status != store.TesterRequested || other.AddedAt != nil || other.AddedBy != "" || other.RemindedAt != nil {
		t.Fatalf("outro perfil = %+v", other)
	}
}

func TestTesterRequestValidation(t *testing.T) {
	st, _ := newStore(t)
	id := seedIdentity(t, st, "owner", "5511911110003")
	for _, r := range []store.TesterRequest{
		{IdentityID: id.ID, ProfileURL: "https://fb.me/x", RefKind: "share", RefValue: "https://fb.me/x"},
		{IdentityID: id.ID, ProfileURL: "", RefKind: "id", RefValue: "1"},
		{IdentityID: id.ID, ProfileURL: "https://www.facebook.com/x", RefKind: "username", RefValue: ""},
	} {
		if _, err := st.UpsertTesterRequest(ctx, r); err == nil {
			t.Errorf("aceitou %+v", r)
		}
	}
	if _, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: 999999,
		ProfileURL: "https://www.facebook.com/x.y.z", RefKind: "username", RefValue: "x.y.z"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("identidade inexistente: %v", err)
	}
	// OrgID de outra organização não vale: a organização vem sempre da identidade.
	other := seedIdentity(t, st, "owner", "5511911110004")
	r, err := st.UpsertTesterRequest(ctx, store.TesterRequest{OrgID: other.OrgID, IdentityID: id.ID,
		ProfileURL: "https://www.facebook.com/x.y.z", RefKind: "username", RefValue: "x.y.z"})
	must(t, err)
	if r.OrgID != id.OrgID {
		t.Fatalf("organização = %d, quer %d", r.OrgID, id.OrgID)
	}
	// Conversa de outro telefone também não vale.
	conv, err := st.GetOrCreateConversation(ctx, other.ID, "5511911110004@c.us")
	must(t, err)
	if _, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id.ID, ConversationID: conv.ID,
		ProfileURL: "https://www.facebook.com/x.y.z", RefKind: "username", RefValue: "x.y.z"}); err == nil {
		t.Fatal("aceitou conversa de outro telefone")
	}
}

func TestTestersAwaitingAcceptanceSkipsConnected(t *testing.T) {
	st, pool := newStore(t)
	a := seedIdentity(t, st, "owner", "5511911110005")
	b := seedIdentity(t, st, "owner", "5511911110006")
	for _, x := range []store.Identity{a, b} {
		r, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: x.ID,
			ProfileURL: "https://www.facebook.com/x.y.z", RefKind: "username", RefValue: "x.y.z"})
		must(t, err)
		_, err = st.MarkTesterAdded(ctx, r.ID, "dono@meuanuncio.io")
		must(t, err)
	}
	_, err := pool.Exec(ctx, `INSERT INTO meta_connect_links (identity_id, token_hash, expires_at, used_at) VALUES ($1, 'tk', now(), now())`, b.ID)
	must(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO meta_connections (organization_id, connected_by, link_id, meta_user_id, status, connected_at)
		SELECT $1, $2, id, 'U1', 'active', now() FROM meta_connect_links WHERE token_hash = 'tk'`, b.OrgID, b.ID)
	must(t, err)
	aw, err := st.TestersAwaitingAcceptance(ctx)
	must(t, err)
	if len(aw) != 1 || aw[0].IdentityID != a.ID {
		t.Fatalf("aguardando = %+v", aw)
	}
}

func TestSyncAppTesters(t *testing.T) {
	st, _ := newStore(t)
	// Primeira leitura: grava todos e não devolve nenhum como novo.
	fresh, err := st.SyncAppTesters(ctx, []string{"901", "902", "901", ""})
	must(t, err)
	if len(fresh) != 0 {
		t.Fatalf("primeira = %v", fresh)
	}
	fresh, err = st.SyncAppTesters(ctx, []string{"902", "903", "901", "904", "903"})
	must(t, err)
	if strings.Join(fresh, ",") != "903,904" {
		t.Fatalf("novos = %v", fresh)
	}
	fresh, err = st.SyncAppTesters(ctx, nil)
	must(t, err)
	if len(fresh) != 0 {
		t.Fatalf("vazio = %v", fresh)
	}
	fresh, err = st.SyncAppTesters(ctx, []string{"904", "905"})
	must(t, err)
	if strings.Join(fresh, ",") != "905" {
		t.Fatalf("novos = %v", fresh)
	}
}

func TestResetRemovesTesterRequests(t *testing.T) {
	st, f := seedClients(t)
	_, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: f.pending.ID,
		ProfileURL: "https://www.facebook.com/x.y.z", RefKind: "username", RefValue: "x.y.z"})
	must(t, err)
	rep, err := st.ResetOrganization(ctx, f.pending.OrgID, true, resetAudit("dono@meuanuncio.io"))
	must(t, err)
	if !rep.Applied || rep.Rows("tester_requests") != 1 || rep.Rows("organizations") != 1 {
		t.Fatalf("zerar = %+v", rep)
	}
	if _, err := st.OpenTesterRequest(ctx, f.pending.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pedido continua: %v", err)
	}
}

// Botão "Testador adicionado" da conversa: sem pedido aberto cria um já adicionado; com pedido
// requested passa para added; com added fica como está (só reavisa).
func TestMarkTesterAddedForConversation(t *testing.T) {
	st, _ := newStore(t)
	id := seedIdentity(t, st, "owner", "5511911110009")
	conv, err := st.GetOrCreateConversation(ctx, id.ID, "5511911110009@c.us")
	must(t, err)

	v, err := st.MarkTesterAddedForConversation(ctx, conv.ID, "ana@x.com")
	must(t, err)
	if v.ID == 0 || v.Status != store.TesterAdded || v.AddedAt == nil || v.AddedBy != "ana@x.com" ||
		v.ConversationID != conv.ID || v.OrgID != id.OrgID || v.ProfileURL != store.TesterFromPanel || v.Phone != "5511911110009" {
		t.Fatalf("criado = %+v", v)
	}
	again, err := st.MarkTesterAddedForConversation(ctx, conv.ID, "bia@x.com")
	must(t, err)
	if again.ID != v.ID || again.Status != store.TesterAdded || again.AddedBy != "ana@x.com" {
		t.Fatalf("segundo clique = %+v", again)
	}

	// Pedido do cliente aberto (requested): o botão fecha ele como adicionado, sem criar outro.
	id2 := seedIdentity(t, st, "owner", "5511911110010")
	conv2, err := st.GetOrCreateConversation(ctx, id2.ID, "5511911110010@c.us")
	must(t, err)
	r, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: id2.ID, ConversationID: conv2.ID,
		ProfileURL: "https://www.facebook.com/jv", RefKind: "username", RefValue: "jv"})
	must(t, err)
	v2, err := st.MarkTesterAddedForConversation(ctx, conv2.ID, "ana@x.com")
	must(t, err)
	if v2.ID != r.ID || v2.Status != store.TesterAdded || v2.RefValue != "jv" {
		t.Fatalf("pedido aberto = %+v", v2)
	}

	if _, err := st.MarkTesterAddedForConversation(ctx, 999999, "ana@x.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("conversa inexistente: %v", err)
	}
}
