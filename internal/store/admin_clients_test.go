package store_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

type clientsFixture struct {
	pool                         *pgxpool.Pool
	pending, ready, stuck, other store.Identity
	readyConv                    int64
}

// seedClients: um pendente de cadastro, um pronto com Meta conectada, um travado em coleta
// (última mensagem há 3 dias) e um bloqueado.
func seedClients(t *testing.T) (*store.Store, clientsFixture) {
	t.Helper()
	st, pool := newStore(t)
	legacyPlans(t, pool)
	f := clientsFixture{pool: pool}
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		must(t, err)
	}
	f.pending = seedIdentity(t, st, "owner", "5511900000001")
	must(t, st.SetRegistrationStatus(ctx, f.pending.OrgID, "pending"))
	exec(`UPDATE organizations SET name = 'Contato 0001' WHERE id = $1`, f.pending.OrgID)

	f.ready = seedIdentity(t, st, "owner", "5511900000002")
	exec(`UPDATE organizations SET name = 'Doceria Mel', trade_name = 'Doceria Mel', segment = 'Doceria', city = 'Campinas', uf = 'SP',
		cnpj = '11222333000181', website = 'https://doceria.com' WHERE id = $1`, f.ready.OrgID)
	exec(`UPDATE members SET full_name = 'Bia Mel', email = 'bia@doceria.com' WHERE id = $1`, f.ready.MemberID)
	conv, err := st.GetOrCreateConversation(ctx, f.ready.ID, "5511900000002@c.us")
	must(t, err)
	f.readyConv = conv.ID
	must(t, st.AddMessage(ctx, conv.ID, "in", "text", "pronto"))
	p, _, err := st.CreateOrReopenProject(ctx, f.ready.OrgID, "Bolo de pote", contract.Fields{Offer: contract.Str("bolo")})
	must(t, err)
	must(t, st.SetProjectStage(ctx, p.ID, store.StageReady))
	exec(`INSERT INTO campaign_drafts (organization_id, conversation_id, project_id, briefing, proposal, proposal_hash, status, approved_by, approved_at)
		VALUES ($1, $2, $3, '{}', '{"objective":"OUTCOME_ENGAGEMENT","daily_budget_brl":30,"duration_days":7}', 'h', 'approved', $4, now())`,
		f.ready.OrgID, conv.ID, p.ID, f.ready.MemberID)
	exec(`INSERT INTO meta_connect_links (identity_id, token_hash, expires_at, used_at) VALUES ($1, 'tok', now(), now())`, f.ready.ID)
	exec(`INSERT INTO meta_connections (organization_id, connected_by, link_id, meta_user_id, ad_account_id, ad_account_name, page_name, status, connected_at, token_ciphertext)
		SELECT $1, $2, id, 'U1', 'act_9', 'Conta Mel', 'Doceria Mel', 'active', now(), '\x0102' FROM meta_connect_links WHERE token_hash = 'tok'`, f.ready.OrgID, f.ready.ID)
	exec(`INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'starter', 'active')`, f.ready.OrgID)
	must(t, st.Audit(ctx, f.ready.OrgID, "system", "proposal_created", map[string]any{"draft_id": 1}))
	must(t, st.LogAdminAction(ctx, store.AdminAction{ActorEmail: "dono@meuanuncio.io", Action: "client_unblock", TargetType: "organization", TargetID: itoa(f.ready.OrgID)}))
	must(t, st.LogAdminAction(ctx, store.AdminAction{ActorEmail: "dono@meuanuncio.io", Action: "conversation_takeover", TargetType: "conversation", TargetID: itoa(conv.ID)}))

	f.stuck = seedIdentity(t, st, "owner", "5511900000003")
	exec(`UPDATE organizations SET name = 'Barbearia Zé', created_at = now() - interval '5 days' WHERE id = $1`, f.stuck.OrgID)
	sc, _ := st.GetOrCreateConversation(ctx, f.stuck.ID, "5511900000003@c.us")
	must(t, st.AddMessage(ctx, sc.ID, "in", "text", "oi"))
	exec(`UPDATE messages SET created_at = now() - interval '3 days' WHERE conversation_id = $1`, sc.ID)
	_, _, err = st.CreateOrReopenProject(ctx, f.stuck.OrgID, "Corte", contract.Fields{})
	must(t, err)
	must(t, st.UpdateConversation(ctx, sc.ID, store.StateNeedsHuman, contract.Fields{}))

	f.other = seedIdentity(t, st, "owner", "5511900000004")
	must(t, st.SetOrgStatus(ctx, f.other.OrgID, "blocked"))
	exec(`UPDATE organizations SET name = 'Bloqueado' WHERE id = $1`, f.other.OrgID)
	return st, f
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestClientsListAndFilters(t *testing.T) {
	st, f := seedClients(t)
	res, err := st.Clients(ctx, store.ClientFilter{Stage: -1})
	must(t, err)
	if len(res.Clients) != 4 || res.Counts.All != 4 || res.Counts.Pending != 1 || res.Counts.Connected != 1 ||
		res.Counts.Blocked != 1 || res.Counts.Stuck != 1 || res.Counts.Human != 1 {
		t.Fatalf("lista = %d, contagens = %+v", len(res.Clients), res.Counts)
	}
	byOrg := map[int64]store.ClientRow{}
	for _, c := range res.Clients {
		byOrg[c.OrgID] = c
	}
	r := byOrg[f.ready.OrgID]
	if r.Name != "Doceria Mel" || r.Contact != "Bia Mel" || r.Phone != "5511900000002" || r.Stage != 5 || r.StageLabel != "Pronta" ||
		!r.MetaConnected || r.Plan != "starter" || r.City != "Campinas" || r.ConversationID != f.readyConv || r.Stuck {
		t.Errorf("pronto = %+v", r)
	}
	if p := byOrg[f.pending.OrgID]; p.Stage != 0 || p.Registration != "pending" {
		t.Errorf("pendente = %+v", p)
	}
	if s := byOrg[f.stuck.OrgID]; !s.Stuck || s.Stage != 2 || !s.NeedsHuman || s.LastActivity.After(time.Now().Add(-48*time.Hour)) {
		t.Errorf("travado = %+v", s)
	}

	for _, c := range []struct {
		f    store.ClientFilter
		want int64
	}{
		{store.ClientFilter{Filter: "travados", Stage: -1}, f.stuck.OrgID},
		{store.ClientFilter{Filter: "pendentes", Stage: -1}, f.pending.OrgID},
		{store.ClientFilter{Filter: "conectados", Stage: -1}, f.ready.OrgID},
		{store.ClientFilter{Filter: "bloqueados", Stage: -1}, f.other.OrgID},
		{store.ClientFilter{Filter: "humano", Stage: -1}, f.stuck.OrgID},
		{store.ClientFilter{Q: "campinas", Stage: -1}, f.ready.OrgID},
		{store.ClientFilter{Q: "900000002", Stage: -1}, f.ready.OrgID},
		{store.ClientFilter{Q: "bia@doceria", Stage: -1}, f.ready.OrgID},
		{store.ClientFilter{Stage: 5}, f.ready.OrgID},
	} {
		got, err := st.Clients(ctx, c.f)
		must(t, err)
		if len(got.Clients) != 1 || got.Clients[0].OrgID != c.want {
			t.Errorf("filtro %+v = %+v", c.f, got.Clients)
		}
	}
	// % digitado na busca não vira curinga.
	if got, _ := st.Clients(ctx, store.ClientFilter{Q: "%", Stage: -1}); len(got.Clients) != 0 {
		t.Errorf("busca por %% = %d", len(got.Clients))
	}
}

func TestClientDetail(t *testing.T) {
	st, f := seedClients(t)
	d, err := st.ClientDetail(ctx, f.ready.OrgID)
	must(t, err)
	if d.Name != "Doceria Mel" || d.CNPJ != "11222333000181" || d.Website != "https://doceria.com" || d.Segment != "Doceria" {
		t.Errorf("cadastro = %+v", d.ClientRow)
	}
	if len(d.Members) != 1 || d.Members[0].Email != "bia@doceria.com" || len(d.Members[0].Phones) != 1 {
		t.Errorf("membros = %+v", d.Members)
	}
	if d.Subscription == nil || d.Subscription.Plan != "starter" || d.Subscription.PriceCents != 9700 {
		t.Errorf("assinatura = %+v", d.Subscription)
	}
	if d.Meta == nil || d.Meta.AdAccountID != "act_9" || d.Meta.PageName != "Doceria Mel" {
		t.Errorf("meta = %+v", d.Meta)
	}
	if len(d.Projects) != 1 || d.Projects[0].Stage != "ready" || d.Projects[0].Draft == nil ||
		d.Projects[0].Draft.DailyBudgetBRL != 30 || d.Projects[0].Draft.Status != "approved" {
		t.Errorf("projetos = %+v", d.Projects)
	}
	// Linha do tempo junta o que o sistema e a equipe fizeram (inclusive na conversa do cliente).
	sources := map[string]int{}
	for _, e := range d.Timeline {
		sources[e.Source]++
	}
	if sources["sistema"] != 1 || sources["equipe"] != 2 {
		t.Errorf("linha do tempo = %+v", d.Timeline)
	}
	if _, err := st.ClientDetail(ctx, 99999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("inexistente: %v", err)
	}
}

func TestOverview(t *testing.T) {
	st, f := seedClients(t)
	ov, err := st.Overview(ctx, 7)
	must(t, err)
	if ov.Signups != 3 || ov.MetaActive != 1 || ov.Approved != 1 || ov.Ready != 1 || ov.Messages != 2 {
		t.Errorf("números = %+v", ov)
	}
	if len(ov.Funnel) != 7 || ov.Funnel[0].Count != 4 || ov.Funnel[1].Count != 3 || ov.Funnel[2].Count != 2 ||
		ov.Funnel[3].Count != 1 || ov.Funnel[4].Count != 1 || ov.Funnel[5].Count != 1 || ov.Funnel[6].Count != 1 {
		t.Errorf("funil = %+v", ov.Funnel)
	}
	if ov.StuckCount != 1 || len(ov.Stuck) != 1 || ov.Stuck[0].OrgID != f.stuck.OrgID {
		t.Errorf("travados = %d %+v", ov.StuckCount, ov.Stuck)
	}
	if len(ov.Series.Messages) != 7 || ov.Series.Messages[6] != 1 || ov.Series.Messages[3] != 1 {
		t.Errorf("série = %+v", ov.Series)
	}
	if ov.Inbox.NeedsHuman != 1 {
		t.Errorf("caixa = %+v", ov.Inbox)
	}
	if len(ov.Feed) == 0 {
		t.Error("feed vazio")
	}
}

func TestWorkerHealth(t *testing.T) {
	st, pool := newStore(t)
	h, err := st.WorkerHealth(ctx)
	must(t, err)
	if h.OutboxPending != 0 || h.LastError != nil {
		t.Fatalf("vazio = %+v", h)
	}
	must(t, st.EnqueueJob(ctx, "whatsapp.send", map[string]any{"text": "x"}))
	_, err = pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() - interval '10 minutes', created_at = now() - interval '10 minutes'`)
	must(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload, status, last_error, updated_at) VALUES ('whatsapp.send', '{}', 'failed', 'wame: 500 token=abcdef1234567890abcdef', now())`)
	must(t, err)
	h, err = st.WorkerHealth(ctx)
	must(t, err)
	if h.OutboxPending != 1 || h.OldestPendingAt == nil || h.FailedLast24h != 1 || h.LastError == nil || h.LastError.Kind != "whatsapp.send" {
		t.Fatalf("saúde = %+v", h)
	}
	if h.LastError.Error != "wame: 500 token=[oculto]" {
		t.Fatalf("segredo no erro: %q", h.LastError.Error)
	}
}

func resetAudit(email string) store.AdminAction {
	return store.AdminAction{ActorEmail: email, Action: "client_reset", TargetType: "organization", TargetID: "x", IP: "1.2.3.4"}
}

func TestResetOrganization(t *testing.T) {
	st, f := seedClients(t)
	audit := resetAudit("dono@meuanuncio.io")

	// Cliente real (cadastro completo com assinatura paga): não zera, nem na simulação.
	if _, err := st.ResetOrganization(ctx, f.ready.OrgID, false, audit); !errors.Is(err, store.ErrRealClient) {
		t.Fatalf("simular cliente real: %v", err)
	}
	if _, err := st.ResetOrganization(ctx, f.ready.OrgID, true, audit); !errors.Is(err, store.ErrRealClient) {
		t.Fatalf("zerar cliente real: %v", err)
	}
	// Qualquer pagamento registrado também recusa (o pagamento nunca é apagado).
	_, err := f.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'free', 'active')`, f.stuck.OrgID)
	must(t, err)
	_, err = st.RecordManualPayment(ctx, store.ManualPayment{OrgID: f.stuck.OrgID, AmountCents: 100, Method: "pix", Status: "pending", Date: time.Now()})
	must(t, err)
	if _, err := st.ResetOrganization(ctx, f.stuck.OrgID, true, audit); !errors.Is(err, store.ErrRealClient) {
		t.Fatalf("zerar com pagamento: %v", err)
	}
	if _, err := st.ClientDetail(ctx, f.ready.OrgID); err != nil {
		t.Fatalf("cliente real apagado: %v", err)
	}

	// Conta de teste (plano grátis, sem pagamento): zera.
	_, err = f.pool.Exec(ctx, `UPDATE subscriptions SET plan_code = 'free' WHERE organization_id = $1`, f.ready.OrgID)
	must(t, err)
	_, err = f.pool.Exec(ctx, `INSERT INTO creatives (organization_id, draft_id, kind, storage_path, mime, size_bytes)
		SELECT organization_id, id, 'image', 'x.jpg', 'image/jpeg', 10 FROM campaign_drafts WHERE organization_id = $1`, f.ready.OrgID)
	must(t, err)
	_, err = f.pool.Exec(ctx, `INSERT INTO inbox_events (provider, external_id, payload) VALUES ('waha', 'e1', '{"from":"5511900000002@c.us"}')`)
	must(t, err)

	// Simulação não apaga nada nem grava registro.
	rep, err := st.ResetOrganization(ctx, f.ready.OrgID, false, audit)
	must(t, err)
	if rep.Applied || rep.Rows("organizations") != 1 || rep.Rows("creatives") != 1 || rep.Rows("inbox_events") != 1 {
		t.Fatalf("simulação = %+v", rep)
	}
	if _, err := st.ClientDetail(ctx, f.ready.OrgID); err != nil {
		t.Fatalf("simulação apagou: %v", err)
	}
	// Sem registro no admin_audit_log, não zera.
	if _, err := st.ResetOrganization(ctx, f.ready.OrgID, true, resetAudit("")); err == nil {
		t.Fatal("zerou sem registro")
	}
	if _, err := st.ClientDetail(ctx, f.ready.OrgID); err != nil {
		t.Fatalf("zerou sem registro: %v", err)
	}
	rep, err = st.ResetOrganization(ctx, f.ready.OrgID, true, audit)
	must(t, err)
	if !rep.Applied || rep.Rows("meta_connections") != 1 || rep.Rows("subscriptions") != 1 {
		t.Fatalf("zerar = %+v", rep)
	}
	for _, s := range rep.Steps {
		if s.Table == "payments" {
			t.Fatalf("zerar mexeu em pagamentos: %+v", rep)
		}
	}
	log, err := st.AdminLog(ctx, store.AdminLogFilter{ActorEmail: "dono@meuanuncio.io"})
	must(t, err)
	if len(log) == 0 || log[0].Action != "client_reset" || !strings.Contains(string(log[0].Data), "5511900000002") {
		t.Fatalf("registro do zerar = %+v", log)
	}
	if _, err := st.ClientDetail(ctx, f.ready.OrgID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cliente continua: %v", err)
	}
	if _, err := st.FindIdentity(ctx, "openwa", "5511900000002"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("telefone continua: %v", err)
	}
	// Os outros clientes ficam.
	if res, _ := st.Clients(ctx, store.ClientFilter{Stage: -1}); len(res.Clients) != 3 {
		t.Fatalf("sobraram %d", len(res.Clients))
	}
	if _, err := st.ResetOrganization(ctx, f.ready.OrgID, true, audit); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("de novo: %v", err)
	}
}

func TestResetContactsRefusesRealClient(t *testing.T) {
	st, f := seedClients(t)
	rep, _, err := st.ResetContacts(ctx, []string{"5511900000002"}, true)
	if !errors.Is(err, store.ErrRealClient) || rep.Applied {
		t.Fatalf("script com cliente real: %+v %v", rep, err)
	}
	if _, err := st.ClientDetail(ctx, f.ready.OrgID); err != nil {
		t.Fatalf("cliente real apagado: %v", err)
	}
	// O número de teste (sem pagamento, sem plano pago) continua zerando.
	rep, others, err := st.ResetContacts(ctx, []string{"5511900000001"}, true)
	if err != nil || others != 0 || !rep.Applied {
		t.Fatalf("número de teste: %+v %d %v", rep, others, err)
	}
}
