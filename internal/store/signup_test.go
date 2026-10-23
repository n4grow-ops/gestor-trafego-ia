package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func regForm() signup.Form {
	return signup.Form{FullName: "Ana Souza", BusinessName: "Pizzaria Boa", LegalName: "PIZZARIA BOA LTDA",
		CNPJ: "11222333000181", CPF: "52998224725", Segment: "restaurante", City: "Campinas", UF: "SP",
		Instagram: "@pizzariaboa", Objectives: []string{"mensagens", "vendas"}, MonthlyBudget: "500_1500", AdsExperience: "nunca",
		MetaAdAccount: "nao_sei", Approver: "self", AcceptTerms: true}
}

func welcome(convID int64, chatID, name, email string) store.JobSpec {
	return store.JobSpec{Kind: "whatsapp.send", Payload: map[string]any{"conversation_id": convID, "chat_id": chatID, "text": "oi " + name + " " + email}}
}

// pendingIdentity cria um cliente como o cadastro automático: organização pendente.
func pendingIdentity(t *testing.T, st *store.Store, role, phone string) store.Identity {
	t.Helper()
	ident := seedIdentity(t, st, role, phone)
	must(t, st.SetRegistrationStatus(ctx, ident.OrgID, "pending"))
	ident.RegistrationStatus = "pending"
	return ident
}

func TestSignupLinkLifecycle(t *testing.T) {
	st, pool := newStore(t)
	if admin := seedIdentity(t, st, "owner", "5511911112222"); admin.RegistrationStatus != "complete" {
		t.Fatalf("organização criada pelo admin já nasce cadastrada: %q", admin.RegistrationStatus)
	}
	ident := pendingIdentity(t, st, "owner", "5511988887777")
	if auto, err := st.AutoSignup(ctx, "openwa", "5511933334444", 50, 60); err != nil || auto.RegistrationStatus != "pending" {
		t.Fatalf("cadastro automático nasce pendente: %q %v", auto.RegistrationStatus, err)
	}
	if ok, err := st.SignupLinkSentWithin(ctx, ident.ID, 10*time.Minute); err != nil || ok {
		t.Fatalf("sem link ainda: ok=%v err=%v", ok, err)
	}
	must(t, st.CreateSignupLink(ctx, ident.ID, "hash-ok", time.Now().Add(time.Hour)))
	must(t, st.CreateSignupLink(ctx, ident.ID, "hash-old", time.Now().Add(-time.Minute)))
	if ok, err := st.SignupLinkSentWithin(ctx, ident.ID, 10*time.Minute); err != nil || !ok {
		t.Fatalf("link recente: %v %v", ok, err)
	}
	_, err := pool.Exec(ctx, `UPDATE signup_links SET created_at = now() - interval '11 minutes'`)
	must(t, err)
	if ok, _ := st.SignupLinkSentWithin(ctx, ident.ID, 10*time.Minute); ok {
		t.Fatal("link de 11 min atrás não é recente")
	}
	var p store.SignupPrefill
	must(t, st.UpdateOrgProfile(ctx, ident.OrgID, contract.Profile{BusinessName: ptr("Pizzaria Boa"), City: ptr("Campinas")}))
	p, err = st.SignupPrefill(ctx, "hash-ok")
	must(t, err)
	if p.Phone != "5511988887777" || p.BusinessName != "Pizzaria Boa" || p.City != "Campinas" {
		t.Fatalf("pré-preenchimento = %+v", p)
	}
	if _, err := st.SignupPrefill(ctx, "hash-old"); !errors.Is(err, store.ErrLinkInvalid) {
		t.Fatalf("expirado: %v", err)
	}
	if _, err := st.SignupPrefill(ctx, "nao-existe"); !errors.Is(err, store.ErrLinkInvalid) {
		t.Fatalf("inexistente: %v", err)
	}
}

func TestPlansSeeded(t *testing.T) {
	st, _ := newStore(t)
	plans, err := st.Plans(ctx)
	must(t, err)
	if len(plans) != 3 || plans[0].Code != "free" || plans[0].Name != "Básico" || plans[0].PriceCents != 14700 || plans[0].MaxCampaignsMonth != 4 ||
		!plans[1].Available || plans[1].PriceCents != 39700 || plans[1].MaxCampaignsMonth != 35 ||
		!plans[2].Available || plans[2].PriceCents != 99700 || plans[2].MaxCampaignsMonth != 0 {
		t.Fatalf("planos = %+v", plans)
	}
}

func TestCompleteRegistration(t *testing.T) {
	st, pool := newStore(t)
	ident := pendingIdentity(t, st, "owner", "5511988887777")
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateOnboarding, conv.Fields))
	must(t, st.CreateSignupLink(ctx, ident.ID, "hash-ok", time.Now().Add(time.Hour)))

	reg := store.Registration{TokenHash: "hash-ok", AuthUserID: "7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Email: "ana@mail.com", EmailVerifiedAt: time.Now(), Form: regForm(), PlanCode: "free",
		TermsVersion: "2026-09-23", IP: "1.2.3.4", UserAgent: "teste", Welcome: welcome}
	res, err := st.CompleteRegistration(ctx, reg)
	must(t, err)
	if res.OrgID != ident.OrgID || res.FullName != "Ana Souza" {
		t.Fatalf("resultado = %+v", res)
	}

	got, err := st.GetIdentity(ctx, ident.ID)
	must(t, err)
	// Limites: o maior entre o que a organização já tinha (100 no seed) e o do plano Free (50 / 60).
	if got.RegistrationStatus != "complete" || got.OrgName != "Pizzaria Boa" || got.DailyMessageLimit != 60 || got.MaxDailyBudgetBRL != 100 {
		t.Fatalf("identidade = %+v", got)
	}
	if got.Profile.BusinessName == nil || *got.Profile.BusinessName != "Pizzaria Boa" || *got.Profile.City != "Campinas" {
		t.Fatalf("memória do negócio = %+v", got.Profile)
	}
	var email, fullName, cpf, cnpj, segment, plan string
	must(t, pool.QueryRow(ctx, `SELECT m.email, m.full_name, m.cpf, o.cnpj, o.segment, s.plan_code
		FROM members m JOIN organizations o ON o.id = m.organization_id JOIN subscriptions s ON s.organization_id = o.id
		WHERE m.id = $1`, ident.MemberID).Scan(&email, &fullName, &cpf, &cnpj, &segment, &plan))
	if email != "ana@mail.com" || fullName != "Ana Souza" || cpf != "52998224725" || cnpj != "11222333000181" ||
		segment != "Restaurante e alimentação" || plan != "free" || got.Niche != "restaurante" {
		t.Fatalf("gravado: %s %s %s %s %s %s", email, fullName, cpf, cnpj, segment, plan)
	}
	var objectives []string
	must(t, pool.QueryRow(ctx, `SELECT ads_objective FROM organizations WHERE id = $1`, ident.OrgID).Scan(&objectives))
	if len(objectives) != 2 || objectives[0] != "mensagens" || objectives[1] != "vendas" {
		t.Fatalf("objetivos gravados = %v", objectives)
	}
	var consents, jobs int
	must(t, pool.QueryRow(ctx, `SELECT count(*) FROM consents WHERE member_id = $1 AND version = '2026-09-23'`, ident.MemberID).Scan(&consents))
	must(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE payload->>'text' = 'oi Ana Souza ana@mail.com'`).Scan(&jobs))
	if consents != 2 || jobs != 1 {
		t.Fatalf("consentimentos = %d, boas-vindas = %d", consents, jobs)
	}
	c, err := st.GetConversation(ctx, conv.ID)
	must(t, err)
	if c.State != store.StateOnboarding {
		t.Fatalf("depois do cadastro a conversa conhece o negócio (onboarding); estado = %s", c.State)
	}

	if _, err := st.CompleteRegistration(ctx, reg); !errors.Is(err, store.ErrAlreadyRegistered) {
		t.Fatalf("repetido: %v", err)
	}
	if _, err := st.SignupPrefill(ctx, "hash-ok"); !errors.Is(err, store.ErrAlreadyRegistered) {
		t.Fatalf("pré-preenchimento depois de concluir: %v", err)
	}
}

func TestCompleteRegistrationKeepsConversationState(t *testing.T) {
	st, _ := newStore(t)
	ident := pendingIdentity(t, st, "owner", "5511988887777")
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateCollecting, contract.Fields{Offer: ptr("pizza")}))
	must(t, st.CreateSignupLink(ctx, ident.ID, "h", time.Now().Add(time.Hour)))
	_, err = st.CompleteRegistration(ctx, store.Registration{TokenHash: "h", AuthUserID: "7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Email: "a@b.com", EmailVerifiedAt: time.Now(), Form: regForm(), PlanCode: "free", TermsVersion: "v1", Welcome: welcome})
	must(t, err)
	c, _ := st.GetConversation(ctx, conv.ID)
	if c.State != store.StateCollecting || c.Fields.Offer == nil {
		t.Fatalf("conversa em andamento foi mexida: %+v", c)
	}
}

func TestCompleteRegistrationRejectsBadInput(t *testing.T) {
	st, pool := newStore(t)
	legacyPlans(t, pool)
	ident := pendingIdentity(t, st, "owner", "5511988887777")
	_, err := st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	must(t, st.CreateSignupLink(ctx, ident.ID, "old", time.Now().Add(-time.Minute)))
	must(t, st.CreateSignupLink(ctx, ident.ID, "ok", time.Now().Add(time.Hour)))
	base := store.Registration{AuthUserID: "7c9e6679-7425-40de-944b-e07fc1f90ae7", Email: "a@b.com",
		EmailVerifiedAt: time.Now(), Form: regForm(), PlanCode: "free", TermsVersion: "v1", Welcome: welcome}
	expired := base
	expired.TokenHash = "old"
	if _, err := st.CompleteRegistration(ctx, expired); !errors.Is(err, store.ErrLinkInvalid) {
		t.Fatalf("expirado: %v", err)
	}
	paid := base
	paid.TokenHash, paid.PlanCode = "ok", "pro"
	if _, err := st.CompleteRegistration(ctx, paid); !errors.Is(err, store.ErrPlanUnavailable) {
		t.Fatalf("plano indisponível: %v", err)
	}
}

func TestOnlyOwnerCompletesRegistration(t *testing.T) {
	st, _ := newStore(t)
	ident := pendingIdentity(t, st, "approver", "5511988887777")
	must(t, st.CreateSignupLink(ctx, ident.ID, "h", time.Now().Add(time.Hour)))
	_, err := st.CompleteRegistration(ctx, store.Registration{TokenHash: "h", AuthUserID: "7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Email: "a@b.com", EmailVerifiedAt: time.Now(), Form: regForm(), PlanCode: "free", TermsVersion: "v1", Welcome: welcome})
	if !errors.Is(err, store.ErrNotOwner) {
		t.Fatalf("aprovador concluindo cadastro: %v", err)
	}
}

func TestAutoSignupGetsFreeLimits(t *testing.T) {
	st, _ := newStore(t)
	ident, err := st.AutoSignup(ctx, "openwa", "5511988887777", 20, 10)
	must(t, err)
	_, err = st.GetOrCreateConversation(ctx, ident.ID, "5511988887777@c.us")
	must(t, err)
	must(t, st.CreateSignupLink(ctx, ident.ID, "h", time.Now().Add(time.Hour)))
	_, err = st.CompleteRegistration(ctx, store.Registration{TokenHash: "h", AuthUserID: "7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Email: "a@b.com", EmailVerifiedAt: time.Now(), Form: regForm(), PlanCode: "free", TermsVersion: "v1", Welcome: welcome})
	must(t, err)
	got, _ := st.GetIdentity(ctx, ident.ID)
	if got.MaxDailyBudgetBRL != 50 || got.DailyMessageLimit != 60 {
		t.Fatalf("limites do Free não aplicados: %v %d", got.MaxDailyBudgetBRL, got.DailyMessageLimit)
	}
}

func ptr(s string) *string { return &s }

// Cadastro pelo formulário do WhatsApp: sem login do Supabase, e-mail não confirmado.
func TestCompleteRegistrationWithoutAuth(t *testing.T) {
	st, pool := newStore(t)
	ident := pendingIdentity(t, st, "owner", "5511988887777")
	must(t, st.CreateSignupLink(ctx, ident.ID, "h", time.Now().Add(time.Hour)))
	_, err := st.CompleteRegistration(ctx, store.Registration{TokenHash: "h", Email: "a@b.com", Form: regForm(),
		PlanCode: "free", TermsVersion: "v1", Welcome: welcome})
	must(t, err)
	var email string
	var verified, authID *string
	must(t, pool.QueryRow(ctx, `SELECT email, email_verified_at::text, auth_user_id::text FROM members WHERE id = $1`, ident.MemberID).Scan(&email, &verified, &authID))
	if email != "a@b.com" || verified != nil || authID != nil {
		t.Fatalf("email=%q verificado=%v auth=%v", email, verified, authID)
	}
}
