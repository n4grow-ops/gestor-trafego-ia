package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

var ctx = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	pool := testdb.New(t)
	return store.New(pool), pool
}

// legacyPlans volta os planos ao que eram antes da 0048 (free grátis, starter R$ 97 e pro R$ 297 em
// breve): os testes de painel e cobrança foram escritos com esses valores.
func legacyPlans(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `UPDATE plans SET price_cents = CASE code WHEN 'free' THEN 0 WHEN 'starter' THEN 9700 ELSE 29700 END,
		available = code = 'free', max_campaigns_month = 0`)
	must(t, err)
}

// seedIdentity cria organização, membro e telefone, e devolve a identidade.
func seedIdentity(t *testing.T, st *store.Store, role, phone string) store.Identity {
	t.Helper()
	org, err := st.CreateOrganization(ctx, "Pizzaria Boa", "restaurante", 100, []string{"OUTCOME_ENGAGEMENT", "OUTCOME_LEADS"})
	must(t, err)
	mem, err := st.CreateMember(ctx, org, "Ana", role)
	must(t, err)
	must(t, st.LinkPhone(ctx, "openwa", phone, mem))
	ident, err := st.FindIdentity(ctx, "openwa", phone)
	must(t, err)
	return ident
}

func TestFindIdentity(t *testing.T) {
	st, _ := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	if ident.OrgName != "Pizzaria Boa" || ident.Niche != "restaurante" || ident.MaxDailyBudgetBRL != 100 ||
		!slices.Equal(ident.AllowedObjectives, []string{"OUTCOME_ENGAGEMENT", "OUTCOME_LEADS"}) ||
		ident.Role != "owner" || ident.MemberName != "Ana" || ident.Phone != "5511988887777" || ident.Provider != "openwa" {
		t.Errorf("ident = %+v", ident)
	}
	if !ident.CanApprove() {
		t.Error("owner deveria aprovar")
	}
	again, err := st.GetIdentity(ctx, ident.ID)
	must(t, err)
	if again.MemberID != ident.MemberID || again.OrgID != ident.OrgID {
		t.Errorf("GetIdentity = %+v", again)
	}
}

func TestCanApproveByRole(t *testing.T) {
	for role, want := range map[string]bool{"owner": true, "approver": true, "viewer": false} {
		if got := (store.Identity{Role: role}).CanApprove(); got != want {
			t.Errorf("%s: %v", role, got)
		}
	}
}

func TestFindIdentityNotFound(t *testing.T) {
	st, _ := newStore(t)
	if _, err := st.FindIdentity(ctx, "openwa", "5511900000000"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := st.GetIdentity(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPhoneUniqueAcrossOrganizations(t *testing.T) {
	st, _ := newStore(t)
	seedIdentity(t, st, "owner", "5511988887777")
	org, err := st.CreateOrganization(ctx, "Outra", "", 50, []string{"OUTCOME_LEADS"})
	must(t, err)
	mem, err := st.CreateMember(ctx, org, "Bia", "owner")
	must(t, err)
	if err := st.LinkPhone(ctx, "openwa", "5511988887777", mem); err == nil {
		t.Fatal("mesmo telefone em duas organizações deveria falhar")
	}
}
