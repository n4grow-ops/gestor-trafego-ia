package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestCreateOrReopenProjectIsCaseInsensitive(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	p1, created, err := st.CreateOrReopenProject(ctx, ident.OrgID, "Pizzaria", contract.Fields{Location: contract.Str("Salvador")})
	must(t, err)
	if !created || p1.Stage != "collecting" || *p1.Fields.Location != "Salvador" {
		t.Fatalf("p1 = %+v created = %v", p1, created)
	}
	must(t, st.SetProjectStage(ctx, p1.ID, "approved"))
	p2, created, err := st.CreateOrReopenProject(ctx, ident.OrgID, "pizzaria", contract.Fields{})
	must(t, err)
	if created || p2.ID != p1.ID || p2.Stage != "collecting" || p2.Name != "Pizzaria" {
		t.Errorf("reabertura = %+v created = %v", p2, created)
	}
}

func TestFindProjectsBySimilarityAndOrganization(t *testing.T) {
	st, _ := newStore(t)
	a := seedIdentity(t, st, "owner", "5511988887777")
	b := seedIdentity(t, st, "owner", "5511977776666")
	for _, n := range []string{"Pizzaria Centro", "Delivery Açaí"} {
		_, _, err := st.CreateOrReopenProject(ctx, a.OrgID, n, contract.Fields{})
		must(t, err)
	}
	_, _, err := st.CreateOrReopenProject(ctx, b.OrgID, "Pizzaria Outra Empresa", contract.Fields{})
	must(t, err)

	cases := map[string][]string{
		"pizzaria":            {"Pizzaria Centro"},
		"acai":                {"Delivery Açaí"},
		"DELIVERY AÇAÍ":       {"Delivery Açaí"},
		"campanha que não há": nil,
	}
	for q, want := range cases {
		got, err := st.FindProjects(ctx, a.OrgID, q)
		must(t, err)
		if len(got) != len(want) {
			t.Errorf("%q: %d resultados, want %d (%+v)", q, len(got), len(want), got)
			continue
		}
		for i := range want {
			if got[i].Name != want[i] || got[i].OrgID != a.OrgID {
				t.Errorf("%q: got %+v", q, got[i])
			}
		}
	}
	list, err := st.ListProjects(ctx, a.OrgID)
	must(t, err)
	if len(list) != 2 {
		t.Errorf("lista = %+v", list)
	}
}

func TestActiveProjectSyncsWithConversation(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, err)
	p, _, err := st.CreateOrReopenProject(ctx, ident.OrgID, "Pizzaria", contract.Fields{})
	must(t, err)
	must(t, st.ActivateProject(ctx, conv.ID, p.ID, store.StateCollecting, contract.Fields{}))

	must(t, st.UpdateConversation(ctx, conv.ID, store.StateCollecting, contract.Fields{Offer: contract.Str("Pizza")}))
	got, err := st.GetProject(ctx, ident.OrgID, p.ID)
	must(t, err)
	if got.Stage != "collecting" || got.Fields.Offer == nil || *got.Fields.Offer != "Pizza" {
		t.Fatalf("projeto após coleta = %+v", got)
	}
	// Voltar a idle (ex.: após aprovar) não apaga os dados do projeto.
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateIdle, contract.Fields{}))
	got, _ = st.GetProject(ctx, ident.OrgID, p.ID)
	if got.Fields.Offer == nil {
		t.Error("idle apagou os campos do projeto")
	}
	c, err := st.GetConversation(ctx, conv.ID)
	must(t, err)
	if c.ActiveProjectID == nil || *c.ActiveProjectID != p.ID {
		t.Errorf("projeto ativo = %v", c.ActiveProjectID)
	}
}

func TestGetProjectRespectsOrganization(t *testing.T) {
	st, _ := newStore(t)
	a := seedIdentity(t, st, "owner", "5511988887777")
	b := seedIdentity(t, st, "owner", "5511977776666")
	p, _, err := st.CreateOrReopenProject(ctx, a.OrgID, "Pizzaria", contract.Fields{})
	must(t, err)
	if _, err := st.GetProject(ctx, b.OrgID, p.ID); err == nil {
		t.Fatal("projeto de outra organização não pode ser lido")
	}
}

func TestSaveProposalLinksProjectAndStage(t *testing.T) {
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, _ := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	p, _, _ := st.CreateOrReopenProject(ctx, ident.OrgID, "Pizzaria", contract.Fields{})
	must(t, st.ActivateProject(ctx, conv.ID, p.ID, store.StateAwaitingProposal, contract.Fields{Offer: contract.Str("Pizza")}))
	_, err := st.SaveProposal(ctx, store.ProposalRecord{
		OrgID: ident.OrgID, ConversationID: conv.ID, ProposalHash: "h", CodeHash: "c",
		ExpiresAt: time.Now().Add(time.Hour), Send: store.JobSpec{Kind: "whatsapp.send"},
	})
	must(t, err)
	var projectID int64
	must(t, pool.QueryRow(ctx, `SELECT project_id FROM campaign_drafts`).Scan(&projectID))
	got, _ := st.GetProject(ctx, ident.OrgID, p.ID)
	if projectID != p.ID || got.Stage != "awaiting_approval" {
		t.Errorf("project_id = %d stage = %s", projectID, got.Stage)
	}
	d, ok, err := st.LatestProjectDraft(ctx, p.ID)
	must(t, err)
	if !ok || d.Status != "proposed" {
		t.Errorf("último rascunho = %+v ok = %v", d, ok)
	}
}

func TestOrganizationProfileStatusAndLimits(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	if ident.Status != "active" || ident.DailyMessageLimit != 60 || ident.Profile.HasData() {
		t.Fatalf("padrões = %+v", ident)
	}
	must(t, st.UpdateOrgProfile(ctx, ident.OrgID, contract.Profile{BusinessName: contract.Str("Pizzaria Boa"), City: contract.Str("Salvador")}))
	must(t, st.SetOrgStatus(ctx, ident.OrgID, "blocked"))
	must(t, st.SetOrgLimits(ctx, ident.OrgID, 30, 10))
	got, err := st.FindIdentity(ctx, "openwa", "5511988887777")
	must(t, err)
	if *got.Profile.City != "Salvador" || got.Status != "blocked" || got.MaxDailyBudgetBRL != 30 || got.DailyMessageLimit != 10 {
		t.Errorf("identidade = %+v", got)
	}
}

func TestAutoSignupCreatesClient(t *testing.T) {
	st, _ := newStore(t)
	ident, err := st.AutoSignup(ctx, "waha", "5573999372664", 50, 60)
	must(t, err)
	if ident.Role != "owner" || ident.MaxDailyBudgetBRL != 50 || ident.DailyMessageLimit != 60 || ident.SignupSource != "auto" || ident.Phone != "5573999372664" {
		t.Errorf("cliente = %+v", ident)
	}
	if _, err := st.AutoSignup(ctx, "waha", "5573999372664", 50, 60); err == nil {
		t.Error("segundo cadastro do mesmo telefone deveria falhar")
	}
}

func TestCountInboundSince(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, _ := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, st.AddMessage(ctx, conv.ID, "in", "text", "1"))
	must(t, st.AddMessage(ctx, conv.ID, "out", "text", "r"))
	must(t, st.AddMessage(ctx, conv.ID, "in", "text", "2"))
	n, err := st.CountInboundSince(ctx, ident.OrgID, time.Now().Add(-time.Hour))
	must(t, err)
	if n != 2 {
		t.Errorf("recebidas = %d", n)
	}
	n, _ = st.CountInboundSince(ctx, ident.OrgID, time.Now().Add(time.Hour))
	if n != 0 {
		t.Errorf("futuro = %d", n)
	}
}
