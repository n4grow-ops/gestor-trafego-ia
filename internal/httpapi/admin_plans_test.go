package httpapi_test

import (
	"context"
	"net/http"
	"testing"
)

func planBody(code string, price int) map[string]any {
	return map[string]any{"code": code, "name": "Plano " + code, "price_cents": price, "interval": "month",
		"daily_message_limit": 100, "max_daily_budget_brl": 80, "max_projects": 4, "available": false, "visible": true, "sort_order": 5}
}

func TestPlansStaffSeesButCannotEdit(t *testing.T) {
	e := newAdminEnv(t, "otp")
	staff := e.login("marina@meuanuncio.io")

	r := e.do("GET", "/api/plans", staff, nil)
	if r.code != 200 || r.body["can_edit"] != false {
		t.Fatalf("staff lista = %d %s", r.code, r.raw)
	}
	if plans, _ := r.body["plans"].([]any); len(plans) != 3 {
		t.Fatalf("planos = %v", r.body["plans"])
	}
	for name, res := range map[string]resp{
		"criar":  e.do("POST", "/api/plans", staff, planBody("novo", 1000)),
		"editar": e.do("POST", "/api/plans/starter", staff, planBody("starter", 1)),
		"apagar": e.do("DELETE", "/api/plans/starter", staff, nil),
	} {
		if res.code != http.StatusForbidden || res.body["error"] != "somente_admin" {
			t.Errorf("staff %s = %d %s", name, res.code, res.raw)
		}
	}
	p, err := e.st.AdminPlan(context.Background(), "starter")
	if err != nil || p.PriceCents != 9700 {
		t.Fatalf("staff mudou o plano: %+v %v", p, err)
	}
	if r := e.do("GET", "/api/plans", nil, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("anônimo = %d", r.code)
	}
}

func TestPlansAdminCRUDWithCSRFAndAudit(t *testing.T) {
	e := newAdminEnv(t, "otp")
	adm := e.login("dono@meuanuncio.io")

	if r := e.do("GET", "/api/plans", adm, nil); r.body["can_edit"] != true {
		t.Fatalf("admin lista = %s", r.raw)
	}
	// Sem CSRF ou de outra origem: recusado.
	noToken := e.do("POST", "/api/plans", adm, planBody("novo", 1000), func(r *http.Request) { r.Header.Del("X-CSRF-Token") })
	foreign := e.do("POST", "/api/plans", adm, planBody("novo", 1000), func(r *http.Request) { r.Header.Set("Origin", "https://malicioso.com") })
	if noToken.code != http.StatusForbidden || foreign.code != http.StatusForbidden {
		t.Fatalf("csrf = %d / %d", noToken.code, foreign.code)
	}

	// Validação.
	bad := planBody("Plano Novo", 1000)
	if r := e.do("POST", "/api/plans", adm, bad); r.code != http.StatusUnprocessableEntity || r.body["error"] != "dados_invalidos" {
		t.Fatalf("código inválido = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/plans", adm, planBody("novo", -5)); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("preço negativo = %d %s", r.code, r.raw)
	}
	extra := planBody("novo", 1000)
	extra["subscribers"] = 99
	if r := e.do("POST", "/api/plans", adm, extra); r.code != http.StatusBadRequest {
		t.Fatalf("campo desconhecido = %d %s", r.code, r.raw)
	}

	// Cria, repete o código, edita e apaga.
	r := e.do("POST", "/api/plans", adm, planBody("novo", 1000))
	if r.code != http.StatusCreated || r.body["code"] != "novo" {
		t.Fatalf("criar = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/plans", adm, planBody("novo", 1000)); r.code != http.StatusConflict || r.body["error"] != "codigo_em_uso" {
		t.Fatalf("duplicado = %d %s", r.code, r.raw)
	}
	upd := planBody("novo", 2500)
	upd["base_updated_at"] = r.body["updated_at"]
	r2 := e.do("POST", "/api/plans/novo", adm, upd)
	if r2.code != 200 || r2.body["price_cents"] != float64(2500) {
		t.Fatalf("editar = %d %s", r2.code, r2.raw)
	}
	// A mesma base de novo: outra pessoa (aqui, a gente mesmo) salvou no meio.
	if r := e.do("POST", "/api/plans/novo", adm, upd); r.code != http.StatusConflict || r.body["error"] != "versao_desatualizada" {
		t.Fatalf("conflito = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/plans/sumiu", adm, planBody("sumiu", 1)); r.code != http.StatusNotFound {
		t.Fatalf("inexistente = %d %s", r.code, r.raw)
	}

	// Plano com assinante: não apaga nem troca código.
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'novo', 'active')`, e.orgID); err != nil {
		t.Fatal(err)
	}
	if r := e.do("DELETE", "/api/plans/novo", adm, nil); r.code != http.StatusConflict || r.body["error"] != "plano_com_assinantes" {
		t.Fatalf("apagar com assinante = %d %s", r.code, r.raw)
	}
	if r := e.do("POST", "/api/plans/novo", adm, planBody("renomeado", 2500)); r.code != http.StatusConflict || r.body["error"] != "plano_com_assinantes" {
		t.Fatalf("renomear com assinante = %d %s", r.code, r.raw)
	}
	if r := e.do("DELETE", "/api/plans/free", adm, nil); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("apagar free = %d %s", r.code, r.raw)
	}
	if r := e.do("DELETE", "/api/plans/pro", adm, nil); r.code != 200 {
		t.Fatalf("apagar pro = %d %s", r.code, r.raw)
	}

	got := e.auditActions("dono@meuanuncio.io")
	want := map[string]int{"plan_create": 1, "plan_update": 1, "plan_delete": 1}
	for _, a := range got {
		if _, ok := want[a]; ok {
			want[a]--
		}
	}
	for a, n := range want {
		if n != 0 {
			t.Errorf("registro %s sobrando/faltando: %d (%v)", a, n, got)
		}
	}
}
