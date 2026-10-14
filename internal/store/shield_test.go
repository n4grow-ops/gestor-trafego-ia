package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func activeConn(t *testing.T, e metaEnv) int64 {
	t.Helper()
	l := e.link(t, "h1", time.Hour)
	id, err := e.st.SavePendingMeta(ctx, l, "U1", []byte("cifrado"), metaOne)
	must(t, err)
	_, err = e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: id, LinkID: l.ID, Account: metaAcc, Page: metaPage})
	must(t, err)
	return id
}

func TestShieldStateUpsertKeepsNotifiedAndReport(t *testing.T) {
	e := newMetaEnv(t)
	conn := activeConn(t, e)
	t0 := time.Now().UTC().Truncate(time.Second)

	if _, ok, err := e.st.ShieldStateFor(ctx, conn); err != nil || ok {
		t.Fatalf("estado antes de checar: ok=%v err=%v", ok, err)
	}
	// Relatório completo (aprovação) e depois só o estado (job periódico): o relatório sobrevive.
	must(t, e.st.SaveShieldState(ctx, conn, e.ident.OrgID, "ok", 1, 0, map[string]any{"items": []string{"a"}}, t0))
	must(t, e.st.SaveShieldState(ctx, conn, e.ident.OrgID, "restrita", 7, 0, nil, t0.Add(time.Hour)))
	st, ok, err := e.st.ShieldStateFor(ctx, conn)
	if err != nil || !ok || st.State != "restrita" || st.AccountStatus != 7 || st.NotifiedState != "ok" || len(st.Report) == 0 {
		t.Fatalf("estado = %+v ok=%v err=%v", st, ok, err)
	}
	if !st.ChangedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("changed_at = %v", st.ChangedAt)
	}
	// Mesmo estado de novo: changed_at não anda, checked_at anda.
	must(t, e.st.SaveShieldState(ctx, conn, e.ident.OrgID, "restrita", 7, 0, nil, t0.Add(5*time.Hour)))
	st, _, _ = e.st.ShieldStateFor(ctx, conn)
	if !st.ChangedAt.Equal(t0.Add(time.Hour)) || !st.CheckedAt.Equal(t0.Add(5*time.Hour)) {
		t.Fatalf("changed=%v checked=%v", st.ChangedAt, st.CheckedAt)
	}
	must(t, e.st.MarkShieldNotified(ctx, conn, "restrita", t0.Add(6*time.Hour)))
	must(t, e.st.SaveShieldState(ctx, conn, e.ident.OrgID, "restrita", 7, 0, nil, t0.Add(7*time.Hour)))
	st, _, _ = e.st.ShieldStateFor(ctx, conn)
	if st.NotifiedState != "restrita" || st.NotifiedAt == nil {
		t.Fatalf("salvar não pode mexer no avisado: %+v", st)
	}
	byOrg, ok, err := e.st.ShieldStateByOrg(ctx, e.ident.OrgID)
	if err != nil || !ok || byOrg.ConnectionID != conn {
		t.Fatalf("por organização = %+v ok=%v err=%v", byOrg, ok, err)
	}
}

func TestShieldTargetsAndRecentRejected(t *testing.T) {
	e := newMetaEnv(t)
	conn := activeConn(t, e)
	list, err := e.st.ShieldTargets(ctx)
	if err != nil || len(list) != 1 || list[0].ConnectionID != conn || list[0].AdAccountID != "act_1" || list[0].PageID != "P1" || list[0].InstagramID != "IG1" {
		t.Fatalf("alvos = %+v %v", list, err)
	}
	// Conexão sem token (revogada) sai da lista.
	_, err = e.pool.Exec(ctx, `UPDATE meta_connections SET token_ciphertext = NULL`)
	must(t, err)
	if list, _ := e.st.ShieldTargets(ctx); len(list) != 0 {
		t.Fatalf("conexão sem token continua na lista: %+v", list)
	}
	rej, err := e.st.RecentRejected(ctx, e.ident.OrgID, time.Now().Add(-time.Hour))
	if err != nil || len(rej) != 0 {
		t.Fatalf("reprovadas = %+v %v", rej, err)
	}
}

// A aba do cliente no admin mostra a situação da conta guardada pelo escudo.
func TestClientDetailCarriesShieldState(t *testing.T) {
	st, f := seedClients(t)
	d, err := st.ClientDetail(ctx, f.ready.OrgID)
	must(t, err)
	if d.Meta == nil || d.Meta.Shield != nil {
		t.Fatalf("antes de checar: %+v", d.Meta)
	}
	var connID int64
	must(t, f.pool.QueryRow(ctx, `SELECT id FROM meta_connections WHERE organization_id = $1 AND status = 'active'`, f.ready.OrgID).Scan(&connID))
	must(t, st.SaveShieldState(ctx, connID, f.ready.OrgID, "pagamento", 3, 0, map[string]any{"items": []any{}}, time.Now()))
	d, err = st.ClientDetail(ctx, f.ready.OrgID)
	must(t, err)
	if d.Meta.Shield == nil || d.Meta.Shield.State != "pagamento" || d.Meta.Shield.AccountStatus != 3 {
		t.Fatalf("shield = %+v", d.Meta.Shield)
	}
}
