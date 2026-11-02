package store_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func planInput(code string) store.PlanInput {
	return store.PlanInput{Code: code, Name: "Plano " + code, PriceCents: 4900, Interval: "month", DailyMessageLimit: 120,
		MaxDailyBudgetBRL: 150.5, MaxProjects: 5, Available: true, Visible: true, SortOrder: 9}
}

func planAuditFor(t *testing.T, st *store.Store, action string) []map[string]any {
	t.Helper()
	log, err := st.AdminLog(ctx, store.AdminLogFilter{})
	must(t, err)
	var out []map[string]any
	for _, l := range log {
		if l.Action != action {
			continue
		}
		raw, _ := json.Marshal(l.Data)
		var d map[string]any
		json.Unmarshal(raw, &d)
		out = append(out, d)
	}
	return out
}

func by(action string) store.AdminAction {
	return store.AdminAction{ActorEmail: "dono@meuanuncio.io", Action: action}
}

func TestAdminPlansListWithSubscribers(t *testing.T) {
	st, pool := newStore(t)
	legacyPlans(t, pool)
	id := seedIdentity(t, st, "owner", "5511900000010")
	_, err := pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'starter', 'active')`, id.OrgID)
	must(t, err)
	id2 := seedIdentity(t, st, "owner", "5511900000011")
	_, err = pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'starter', 'canceled')`, id2.OrgID)
	must(t, err)

	plans, err := st.AdminPlans(ctx)
	must(t, err)
	// 3 da lista + o plano secreto de teste (0058).
	if len(plans) != 4 || plans[0].Code != "free" || plans[1].Code != "starter" {
		t.Fatalf("planos = %+v", plans)
	}
	sp := plans[1]
	if sp.Subscribers != 2 || sp.ActiveSubs != 1 || sp.PriceCents != 9700 || !sp.Visible || sp.UpdatedAt.IsZero() {
		t.Fatalf("starter = %+v", sp)
	}
}

func TestCreatePlanValidatesAndAudits(t *testing.T) {
	st, _ := newStore(t)
	bad := map[string]func(*store.PlanInput){
		"código com espaço":      func(p *store.PlanInput) { p.Code = "plano novo" },
		"código com acento":      func(p *store.PlanInput) { p.Code = "básico" },
		"código curto":           func(p *store.PlanInput) { p.Code = "a" },
		"preço negativo":         func(p *store.PlanInput) { p.PriceCents = -1 },
		"preço absurdo":          func(p *store.PlanInput) { p.PriceCents = 100_000_01 },
		"intervalo":              func(p *store.PlanInput) { p.Interval = "week" },
		"mensagens zero":         func(p *store.PlanInput) { p.DailyMessageLimit = 0 },
		"orçamento zero":         func(p *store.PlanInput) { p.MaxDailyBudgetBRL = 0 },
		"projetos zero":          func(p *store.PlanInput) { p.MaxProjects = 0 },
		"nome vazio":             func(p *store.PlanInput) { p.Name = "  " },
		"disponível mas oculto":  func(p *store.PlanInput) { p.Visible = false },
		"ordem negativa":         func(p *store.PlanInput) { p.SortOrder = -1 },
		"projetos acima do teto": func(p *store.PlanInput) { p.MaxProjects = 1001 },
	}
	for name, mod := range bad {
		in := planInput("basico")
		mod(&in)
		if _, err := st.CreatePlan(ctx, in, by("plan_create")); !errors.Is(err, store.ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}

	in := planInput(" Basico ")
	in.Name = "  Básico   mensal "
	p, err := st.CreatePlan(ctx, in, by("plan_create"))
	must(t, err)
	if p.Code != "basico" || p.Name != "Básico mensal" || p.MaxDailyBudgetBRL != 150.5 || p.Subscribers != 0 {
		t.Fatalf("criado = %+v", p)
	}
	if _, err := st.CreatePlan(ctx, planInput("basico"), by("plan_create")); !errors.Is(err, store.ErrPlanExists) {
		t.Fatalf("duplicado: %v", err)
	}
	logs := planAuditFor(t, st, "plan_create")
	if len(logs) != 1 || logs[0]["after"].(map[string]any)["price_cents"] != float64(4900) {
		t.Fatalf("registro = %v", logs)
	}
}

func TestUpdatePlanBeforeAfterConflictAndCode(t *testing.T) {
	st, pool := newStore(t)
	legacyPlans(t, pool)
	cur, err := st.AdminPlan(ctx, "starter")
	must(t, err)

	in := planInput("starter")
	in.Name, in.PriceCents, in.Available = "Starter", 12900, false
	p, err := st.UpdatePlan(ctx, "starter", in, cur.UpdatedAt, by("plan_update"))
	must(t, err)
	if p.PriceCents != 12900 || p.MaxProjects != 5 || p.Available || !p.UpdatedAt.After(cur.UpdatedAt) {
		t.Fatalf("editado = %+v", p)
	}
	logs := planAuditFor(t, st, "plan_update")
	if len(logs) != 1 {
		t.Fatalf("registros = %v", logs)
	}
	before, after := logs[0]["before"].(map[string]any), logs[0]["after"].(map[string]any)
	if before["price_cents"] != float64(9700) || after["price_cents"] != float64(12900) {
		t.Fatalf("antes/depois = %v / %v", before, after)
	}

	// Base velha: outra pessoa salvou no meio.
	if _, err := st.UpdatePlan(ctx, "starter", in, cur.UpdatedAt, by("plan_update")); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("conflito: %v", err)
	}
	// Salvar sem mudar nada não gera registro.
	if _, err := st.UpdatePlan(ctx, "starter", in, p.UpdatedAt, by("plan_update")); err != nil {
		t.Fatal(err)
	}
	if n := len(planAuditFor(t, st, "plan_update")); n != 1 {
		t.Fatalf("registros depois de salvar igual = %d", n)
	}

	// Sem assinatura, o código muda; o nome antigo some.
	in.Code = "inicial"
	p, err = st.UpdatePlan(ctx, "starter", in, time.Time{}, by("plan_update"))
	must(t, err)
	if p.Code != "inicial" {
		t.Fatalf("código = %q", p.Code)
	}
	if _, err := st.AdminPlan(ctx, "starter"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("código antigo: %v", err)
	}
	// Código já usado por outro plano.
	in.Code = "pro"
	if _, err := st.UpdatePlan(ctx, "inicial", in, time.Time{}, by("plan_update")); !errors.Is(err, store.ErrPlanExists) {
		t.Fatalf("código em uso: %v", err)
	}

	// Com assinatura, o código não muda (mas o resto muda).
	id := seedIdentity(t, st, "owner", "5511900000012")
	_, err = pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'inicial', 'canceled')`, id.OrgID)
	must(t, err)
	in.Code = "outro"
	if _, err := st.UpdatePlan(ctx, "inicial", in, time.Time{}, by("plan_update")); !errors.Is(err, store.ErrPlanInUse) {
		t.Fatalf("código com assinante: %v", err)
	}
	in.Code, in.Visible = "inicial", false
	if _, err := st.UpdatePlan(ctx, "inicial", in, time.Time{}, by("plan_update")); err != nil {
		t.Fatalf("ocultar com assinante: %v", err)
	}
	if _, err := st.UpdatePlan(ctx, "nao-existe", in, time.Time{}, by("plan_update")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}
}

func TestFreePlanStaysDefault(t *testing.T) {
	st, _ := newStore(t)
	in := planInput("free")
	in.PriceCents = 0
	in.Available = false
	if _, err := st.UpdatePlan(ctx, "free", in, time.Time{}, by("plan_update")); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("desligar free: %v", err)
	}
	in.Available, in.Code = true, "gratis"
	if _, err := st.UpdatePlan(ctx, "free", in, time.Time{}, by("plan_update")); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("renomear free: %v", err)
	}
	if err := st.DeletePlan(ctx, "free", by("plan_delete")); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatalf("apagar free: %v", err)
	}
	// Limites do free mudam normalmente.
	in.Code, in.MaxProjects = "free", 4
	p, err := st.UpdatePlan(ctx, "free", in, time.Time{}, by("plan_update"))
	must(t, err)
	if p.MaxProjects != 4 {
		t.Fatalf("free = %+v", p)
	}
}

func TestDeletePlanOnlyWithoutSubscribers(t *testing.T) {
	st, pool := newStore(t)
	id := seedIdentity(t, st, "owner", "5511900000013")
	_, err := pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'pro', 'canceled')`, id.OrgID)
	must(t, err)
	if err := st.DeletePlan(ctx, "pro", by("plan_delete")); !errors.Is(err, store.ErrPlanInUse) {
		t.Fatalf("apagar com assinante: %v", err)
	}
	must(t, st.DeletePlan(ctx, "starter", by("plan_delete")))
	if _, err := st.AdminPlan(ctx, "starter"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("apagado: %v", err)
	}
	if err := st.DeletePlan(ctx, "starter", by("plan_delete")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("apagar de novo: %v", err)
	}
	if logs := planAuditFor(t, st, "plan_delete"); len(logs) != 1 {
		t.Fatalf("registro = %v", logs)
	}
}

func TestHiddenPlanLeavesSignup(t *testing.T) {
	st, _ := newStore(t)
	in := planInput("starter")
	in.Available, in.Visible = false, false
	_, err := st.UpdatePlan(ctx, "starter", in, time.Time{}, by("plan_update"))
	must(t, err)
	plans, err := st.Plans(ctx)
	must(t, err)
	for _, p := range plans {
		if p.Code == "starter" {
			t.Fatal("plano oculto aparece no cadastro")
		}
	}
	if len(plans) != 2 {
		t.Fatalf("planos do cadastro = %+v", plans)
	}
}
