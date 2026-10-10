package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// openTester abre um pedido de testador para a identidade.
func openTester(t *testing.T, st *store.Store, ident store.Identity, ref string) store.TesterRequest {
	t.Helper()
	r, err := st.UpsertTesterRequest(ctx, store.TesterRequest{IdentityID: ident.ID,
		ProfileURL: "https://www.facebook.com/" + ref, RefKind: "username", RefValue: ref})
	must(t, err)
	return r
}

// activateMeta grava uma conexão Meta ativa na organização da identidade.
func activateMeta(t *testing.T, pool *pgxpool.Pool, ident store.Identity, token string) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO meta_connect_links (identity_id, token_hash, expires_at, used_at) VALUES ($1, $2, now(), now())`, ident.ID, token)
	must(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO meta_connections (organization_id, connected_by, link_id, meta_user_id, status, connected_at)
		SELECT $1, $2, id, 'U1', 'active', now() FROM meta_connect_links WHERE token_hash = $3`, ident.OrgID, ident.ID, token)
	must(t, err)
}

func TestMarkTesterNotifiedRateLimit(t *testing.T) {
	st, pool := newStore(t)
	a := seedIdentity(t, st, "owner", "5511911120001")
	r := openTester(t, st, a, "ana.lima")
	ok, err := st.MarkTesterNotified(ctx, r.ID, 30*time.Minute)
	must(t, err)
	if !ok {
		t.Fatal("primeiro aviso deve sair")
	}
	if ok, err = st.MarkTesterNotified(ctx, r.ID, 30*time.Minute); err != nil || ok {
		t.Fatalf("segundo aviso em 30 min: %v %v", ok, err)
	}
	_, err = pool.Exec(ctx, `UPDATE tester_requests SET notified_at = now() - interval '31 minutes' WHERE id = $1`, r.ID)
	must(t, err)
	if ok, err = st.MarkTesterNotified(ctx, r.ID, 30*time.Minute); err != nil || !ok {
		t.Fatalf("depois de 30 min: %v %v", ok, err)
	}
	if ok, err = st.MarkTesterNotified(ctx, 999999, 30*time.Minute); err != nil || ok {
		t.Fatalf("inexistente: %v %v", ok, err)
	}
}

func TestMarkTesterAcceptNotifiedOnce(t *testing.T) {
	st, _ := newStore(t)
	a := seedIdentity(t, st, "owner", "5511911120002")
	r := openTester(t, st, a, "ana.lima")
	must(t, st.MarkTesterAcceptNotified(ctx, r.ID))
	if err := st.MarkTesterAcceptNotified(ctx, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("segunda marca: %v", err)
	}
	if err := st.MarkTesterAcceptNotified(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}
}

func TestOpenTestersWithoutMeta(t *testing.T) {
	st, pool := newStore(t)
	req := seedIdentity(t, st, "owner", "5511911120003")
	add := seedIdentity(t, st, "owner", "5511911120004")
	conn := seedIdentity(t, st, "owner", "5511911120005")
	done := seedIdentity(t, st, "owner", "5511911120006")
	openTester(t, st, req, "a.a.a")
	ra := openTester(t, st, add, "b.b.b")
	_, err := st.MarkTesterAdded(ctx, ra.ID, "x")
	must(t, err)
	openTester(t, st, conn, "c.c.c")
	activateMeta(t, pool, conn, "tk-conn")
	openTester(t, st, done, "d.d.d")
	must(t, st.MarkTesterAcceptedByIdentity(ctx, done.ID))

	list, err := st.OpenTestersWithoutMeta(ctx)
	must(t, err)
	var got []int64
	for _, v := range list {
		got = append(got, v.IdentityID)
		if v.Phone == "" || v.OrgName == "" {
			t.Errorf("linha sem dados: %+v", v)
		}
	}
	if len(got) != 2 || got[0] != req.ID || got[1] != add.ID {
		t.Fatalf("candidatos = %v (quer %d,%d)", got, req.ID, add.ID)
	}
}

func TestMarkTesterAcceptedByOrg(t *testing.T) {
	st, _ := newStore(t)
	a := seedIdentity(t, st, "owner", "5511911120007")
	mem, err := st.CreateMember(ctx, a.OrgID, "Beto", "viewer")
	must(t, err)
	must(t, st.LinkPhone(ctx, "openwa", "5511911120008", mem))
	b, err := st.FindIdentity(ctx, "openwa", "5511911120008")
	must(t, err)
	other := seedIdentity(t, st, "owner", "5511911120009")
	openTester(t, st, a, "a.a.a")
	rb := openTester(t, st, b, "b.b.b")
	_, err = st.MarkTesterAdded(ctx, rb.ID, "x")
	must(t, err)
	openTester(t, st, other, "c.c.c")

	must(t, st.MarkTesterAcceptedByOrg(ctx, a.OrgID))
	for _, id := range []int64{a.ID, b.ID} {
		if _, err := st.OpenTesterRequest(ctx, id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("pedido de %d continua aberto: %v", id, err)
		}
	}
	if _, err := st.OpenTesterRequest(ctx, other.ID); err != nil {
		t.Fatalf("outra organização fechou: %v", err)
	}
	acc, err := st.ListTesterRequests(ctx, store.TesterAccepted)
	must(t, err)
	if len(acc) != 2 || acc[0].AcceptedAt == nil {
		t.Fatalf("aceitos = %+v", acc)
	}
}

func TestTesterRequestByID(t *testing.T) {
	st, _ := newStore(t)
	a := seedIdentity(t, st, "owner", "5511911120010")
	r := openTester(t, st, a, "ana.lima")
	v, err := st.TesterRequestByID(ctx, r.ID)
	must(t, err)
	if v.ID != r.ID || v.Phone != "5511911120010" || v.OrgName != "Pizzaria Boa" || v.MemberName != "Ana" || v.RefValue != "ana.lima" {
		t.Fatalf("pedido = %+v", v)
	}
	if _, err := st.TesterRequestByID(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}
}

func TestLastInboundByChat(t *testing.T) {
	st, pool := newStore(t)
	a := seedIdentity(t, st, "owner", "5511911120011")
	conv, err := st.GetOrCreateConversation(ctx, a.ID, "5511911120011")
	must(t, err)
	if _, ok, err := st.LastInboundByChat(ctx, "5511911120011"); err != nil || ok {
		t.Fatalf("sem mensagem: %v %v", ok, err)
	}
	must(t, st.AddMessage(ctx, conv.ID, "out", "text", "oi"))
	if _, ok, err := st.LastInboundByChat(ctx, "5511911120011"); err != nil || ok {
		t.Fatalf("só saída conta como entrada: %v %v", ok, err)
	}
	_, err = st.AddInbound(ctx, conv.ID, "text", "oi")
	must(t, err)
	_, err = pool.Exec(ctx, `UPDATE messages SET created_at = now() - interval '2 hours' WHERE conversation_id = $1 AND direction = 'in'`, conv.ID)
	must(t, err)
	at, ok, err := st.LastInboundByChat(ctx, "5511911120011")
	must(t, err)
	if !ok || time.Since(at) < 119*time.Minute || time.Since(at) > 121*time.Minute {
		t.Fatalf("última entrada = %v %v", at, ok)
	}
	if _, ok, err := st.LastInboundByChat(ctx, "5511900000000"); err != nil || ok {
		t.Fatalf("chat desconhecido: %v %v", ok, err)
	}
	if _, ok, err := st.LastInboundByChat(ctx, ""); err != nil || ok {
		t.Fatalf("chat vazio: %v %v", ok, err)
	}
}

// Primeira leitura vazia não pode virar a "linha de base" que esconde o primeiro testador real.
func TestSyncAppTestersEmptyFirstRead(t *testing.T) {
	st, _ := newStore(t)
	fresh, err := st.SyncAppTesters(ctx, nil)
	must(t, err)
	if len(fresh) != 0 {
		t.Fatalf("primeira = %v", fresh)
	}
	fresh, err = st.SyncAppTesters(ctx, []string{"901"})
	must(t, err)
	if strings.Join(fresh, ",") != "901" {
		t.Fatalf("primeiro testador real = %v", fresh)
	}
}
