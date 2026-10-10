package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

type metaEnv struct {
	st    *store.Store
	pool  *pgxpool.Pool
	ident store.Identity
}

func newMetaEnv(t *testing.T) metaEnv {
	t.Helper()
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	_, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	return metaEnv{st: st, pool: pool, ident: ident}
}

func (e metaEnv) link(t *testing.T, hash string, expires time.Duration) store.MetaLink {
	t.Helper()
	must(t, e.st.CreateMetaLink(ctx, e.ident.ID, hash, time.Now().Add(expires)))
	l, err := e.st.MetaLink(ctx, hash)
	must(t, err)
	return l
}

func (e metaEnv) count(t *testing.T, sql string) int {
	t.Helper()
	var n int
	must(t, e.pool.QueryRow(ctx, sql).Scan(&n))
	return n
}

var (
	metaAcc  = meta.AdAccount{ID: "act_1", Name: "Loja", Currency: "BRL", Timezone: "America/Sao_Paulo", BusinessID: "B1", Status: 1}
	metaPage = meta.Page{ID: "P1", Name: "Loja Page", InstagramID: "IG1", InstagramUsername: "loja", WhatsApp: true, WhatsAppNumber: "+55 49 3333-4444"}
	metaOne  = store.MetaCandidates{AdAccounts: []meta.AdAccount{metaAcc}, Pages: []meta.Page{metaPage}}
)

func metaMsg(convID int64, chatID string) store.JobSpec {
	return store.JobSpec{Kind: "send", Payload: map[string]any{"conversation_id": convID, "chat_id": chatID, "text": "ok"}}
}

func TestMetaLinkValidity(t *testing.T) {
	e := newMetaEnv(t)
	l := e.link(t, "h1", time.Hour)
	if l.IdentityID != e.ident.ID || l.OrgID != e.ident.OrgID {
		t.Fatalf("link = %+v", l)
	}
	must(t, e.st.CreateMetaLink(ctx, e.ident.ID, "h2", time.Now().Add(-time.Minute)))
	for _, h := range []string{"h2", "nao-existe"} {
		if _, err := e.st.MetaLink(ctx, h); !errors.Is(err, store.ErrMetaLinkInvalid) {
			t.Errorf("%s: %v", h, err)
		}
	}
	recent, err := e.st.MetaLinkSentWithin(ctx, e.ident.ID, 10*time.Minute)
	if err != nil || !recent {
		t.Fatalf("recente = %v, %v", recent, err)
	}
}

func TestCompleteMetaActivatesAndReplaces(t *testing.T) {
	e := newMetaEnv(t)
	l1 := e.link(t, "h1", time.Hour)
	id1, err := e.st.SavePendingMeta(ctx, l1, "U1", []byte("cifrado1"), metaOne)
	must(t, err)
	gotID, cands, err := e.st.PendingMeta(ctx, l1.ID)
	if err != nil || gotID != id1 || len(cands.AdAccounts) != 1 || cands.Pages[0].InstagramUsername != "loja" {
		t.Fatalf("pendente = %d %+v %v", gotID, cands, err)
	}
	org, err := e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id1, LinkID: l1.ID, Account: metaAcc, Page: metaPage, Message: metaMsg})
	if err != nil || org != e.ident.OrgID {
		t.Fatalf("conclusão = %d, %v", org, err)
	}
	if _, err := e.st.MetaLink(ctx, "h1"); !errors.Is(err, store.ErrMetaLinkInvalid) {
		t.Fatal("link continua valendo depois de usado")
	}
	if _, err := e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id1, LinkID: l1.ID, Account: metaAcc, Page: metaPage, Message: metaMsg}); !errors.Is(err, store.ErrMetaLinkInvalid) {
		t.Fatalf("segunda conclusão: %v", err)
	}
	c, ok, err := e.st.ActiveMeta(ctx, e.ident.OrgID)
	if err != nil || !ok || c.AdAccountID != "act_1" || c.PageName != "Loja Page" || c.InstagramUsername != "loja" || !c.PageWhatsApp || c.Currency != "BRL" ||
		c.PageWhatsAppNumber != "+55 49 3333-4444" {
		t.Fatalf("ativa = %+v %v %v", c, ok, err)
	}

	l2 := e.link(t, "h3", time.Hour)
	id2, err := e.st.SavePendingMeta(ctx, l2, "U1", []byte("cifrado2"), metaOne)
	must(t, err)
	acc2 := metaAcc
	acc2.ID, acc2.Name = "act_2", "Loja 2"
	_, err = e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id2, LinkID: l2.ID, Account: acc2, Page: metaPage, Message: metaMsg})
	must(t, err)
	c, _, _ = e.st.ActiveMeta(ctx, e.ident.OrgID)
	if c.AdAccountID != "act_2" {
		t.Fatalf("ativa = %+v", c)
	}
	if n := e.count(t, `SELECT count(*) FROM meta_connections WHERE status = 'replaced' AND token_ciphertext IS NULL`); n != 1 {
		t.Fatalf("substituídas = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM meta_connections WHERE status = 'active' AND candidates = '{}'::jsonb AND token_ciphertext IS NOT NULL`); n != 1 {
		t.Fatalf("ativa sem token ou com opções = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM outbox_jobs WHERE kind = 'send'`); n != 2 {
		t.Fatalf("mensagens na fila = %d", n)
	}
}

func TestRevokeMeta(t *testing.T) {
	e := newMetaEnv(t)
	l := e.link(t, "h1", time.Hour)
	id, err := e.st.SavePendingMeta(ctx, l, "U9", []byte("cifrado"), metaOne)
	must(t, err)
	_, err = e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id, LinkID: l.ID, Account: metaAcc, Page: metaPage, Message: metaMsg})
	must(t, err)
	n, err := e.st.RevokeMeta(ctx, "U9", "COD123", metaMsg)
	if err != nil || n != 1 {
		t.Fatalf("revogadas = %d, %v", n, err)
	}
	if _, ok, _ := e.st.ActiveMeta(ctx, e.ident.OrgID); ok {
		t.Fatal("continua ativa")
	}
	if n := e.count(t, `SELECT count(*) FROM meta_connections WHERE token_ciphertext IS NOT NULL`); n != 0 {
		t.Fatal("token não foi apagado")
	}
	if n := e.count(t, `SELECT count(*) FROM outbox_jobs WHERE kind = 'send'`); n != 2 {
		t.Fatalf("aviso de desconexão não foi para a fila: %d", n)
	}
	done, err := e.st.MetaDeletionDone(ctx, "COD123")
	if err != nil || !done {
		t.Fatalf("exclusão = %v, %v", done, err)
	}
	if done, _ := e.st.MetaDeletionDone(ctx, "OUTRO"); done {
		t.Fatal("código desconhecido dado como feito")
	}
	if n, _ := e.st.RevokeMeta(ctx, "U9", "", metaMsg); n != 0 {
		t.Fatalf("revogou de novo: %d", n)
	}
}

func TestDeletionAfterDeauthorizeIsRecorded(t *testing.T) {
	e := newMetaEnv(t)
	l := e.link(t, "h1", time.Hour)
	id, err := e.st.SavePendingMeta(ctx, l, "U9", []byte("cifrado"), metaOne)
	must(t, err)
	_, err = e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id, LinkID: l.ID, Account: metaAcc, Page: metaPage, Message: metaMsg})
	must(t, err)
	_, err = e.st.RevokeMeta(ctx, "U9", "", metaMsg)
	must(t, err)
	_, err = e.st.RevokeMeta(ctx, "U9", "COD9", metaMsg)
	must(t, err)
	done, err := e.st.MetaDeletionDone(ctx, "COD9")
	if err != nil || !done {
		t.Fatalf("exclusão depois da desautorização não registrada: %v %v", done, err)
	}
	if n := e.count(t, `SELECT count(*) FROM meta_connections WHERE ad_account_name IS NOT NULL OR page_name IS NOT NULL OR instagram_username IS NOT NULL`); n != 0 {
		t.Fatal("exclusão manteve dados da conta")
	}
}

func TestPendingTokensAreWiped(t *testing.T) {
	e := newMetaEnv(t)
	l := e.link(t, "h1", time.Hour)
	_, err := e.st.SavePendingMeta(ctx, l, "U1", []byte("tentativa1"), metaOne)
	must(t, err)
	id2, err := e.st.SavePendingMeta(ctx, l, "U1", []byte("tentativa2"), metaOne)
	must(t, err)
	old := e.link(t, "h-velho", time.Hour)
	_, err = e.st.SavePendingMeta(ctx, old, "U1", []byte("abandonada"), metaOne)
	must(t, err)
	_, err = e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id2, LinkID: l.ID, Account: metaAcc, Page: metaPage, Message: metaMsg})
	must(t, err)
	if n := e.count(t, `SELECT count(*) FROM meta_connections WHERE status <> 'active' AND token_ciphertext IS NOT NULL`); n != 0 {
		t.Fatalf("%d tokens guardados fora da conexão ativa", n)
	}
}
