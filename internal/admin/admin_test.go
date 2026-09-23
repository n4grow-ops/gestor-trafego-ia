package admin_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/admin"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/testdb"
)

var ctx = context.Background()

func run(t *testing.T, st *store.Store, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := admin.Run(ctx, st, args, &out)
	return out.String(), err
}

func createOrg(t *testing.T, st *store.Store) string {
	t.Helper()
	out, err := run(t, st, "org-create", "--name", "Pizzaria Boa", "--niche", "restaurante", "--max-budget", "100")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if _, err := fmt.Sscanf(out, "organização criada: id=%d", &id); err != nil {
		t.Fatalf("saída = %q", out)
	}
	return fmt.Sprint(id)
}

func TestOrgCreateAndMemberAddNormalizesPhone(t *testing.T) {
	st := store.New(testdb.New(t))
	org := createOrg(t, st)
	out, err := run(t, st, "member-add", "--org", org, "--name", "Ana", "--role", "owner", "--phone", "+55 11 8888-7777")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "5511988887777") {
		t.Errorf("saída = %q", out)
	}
	ident, err := st.FindIdentity(ctx, "openwa", "5511988887777")
	if err != nil {
		t.Fatal(err)
	}
	if ident.Role != "owner" || ident.MaxDailyBudgetBRL != 100 || ident.AllowedObjectives[0] != "OUTCOME_ENGAGEMENT" || ident.Niche != "restaurante" {
		t.Errorf("ident = %+v", ident)
	}
}

func TestOrgCreateValidates(t *testing.T) {
	st := store.New(testdb.New(t))
	if _, err := run(t, st, "org-create", "--name", "X"); err == nil {
		t.Error("sem --max-budget deveria falhar")
	}
}

func TestMemberAddRejectsInvalidRole(t *testing.T) {
	st := store.New(testdb.New(t))
	org := createOrg(t, st)
	if _, err := run(t, st, "member-add", "--org", org, "--name", "Ana", "--role", "admin", "--phone", "5511988887777"); err == nil {
		t.Fatal("papel inválido deveria falhar")
	}
}

func TestMemberAddRejectsDuplicatePhone(t *testing.T) {
	st := store.New(testdb.New(t))
	org := createOrg(t, st)
	args := []string{"member-add", "--org", org, "--name", "Ana", "--role", "owner", "--phone", "5511988887777"}
	if _, err := run(t, st, args...); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, st, args...); err == nil || !strings.Contains(err.Error(), "já cadastrado") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveHuman(t *testing.T) {
	st := store.New(testdb.New(t))
	org := createOrg(t, st)
	if _, err := run(t, st, "member-add", "--org", org, "--name", "Ana", "--role", "owner", "--phone", "5511988887777"); err != nil {
		t.Fatal(err)
	}
	ident, _ := st.FindIdentity(ctx, "openwa", "5511988887777")
	c, _ := st.GetOrCreateConversation(ctx, ident.ID, "x@c.us")
	st.UpdateConversation(ctx, c.ID, store.StateNeedsHuman, contract.Fields{Offer: contract.Str("Pizza")})

	if _, err := run(t, st, "resolve-human", "--phone", "551188887777"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetConversation(ctx, c.ID)
	if got.State != store.StateIdle || got.Fields.HasData() {
		t.Errorf("conversa = %+v", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	st := store.New(testdb.New(t))
	if _, err := run(t, st, "apagar-tudo"); err == nil {
		t.Fatal("comando desconhecido deveria falhar")
	}
	if _, err := run(t, st); err == nil {
		t.Fatal("sem comando deveria falhar")
	}
}
