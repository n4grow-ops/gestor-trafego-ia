package httpapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

// Ações no projeto pelo painel: renomear, editar dados (com teto de orçamento), pedir proposta e arquivar.
func TestAdminProjectActions(t *testing.T) {
	e := newAdminEnv(t, "otp")
	ctx := context.Background()
	p, _, err := e.st.CreateOrReopenProject(ctx, e.orgID, "Chalé", contract.Fields{})
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := e.st.CreateOrReopenProject(ctx, e.orgID, "Imóvel", contract.Fields{})
	if err != nil {
		t.Fatal(err)
	}
	s := e.login("marina@meuanuncio.io")
	base := "/api/projects/" + itoa(p.ID)

	if r := e.do("POST", base+"/rename", s, map[string]string{"name": "Imóvel"}); r.code != 422 || !strings.Contains(r.raw, "já existe") {
		t.Fatalf("nome repetido: %d %s", r.code, r.raw)
	}
	if r := e.do("POST", base+"/rename", s, map[string]string{"name": "Venda Chalé"}); r.code != 200 {
		t.Fatalf("renomear: %d %s", r.code, r.raw)
	}
	// incompleto: proposta recusada com o que falta
	if r := e.do("POST", base+"/propose", s, nil); r.code != 422 || !strings.Contains(r.raw, "faltam") {
		t.Fatalf("proposta incompleta: %d %s", r.code, r.raw)
	}
	if r := e.do("POST", base+"/fields", s, map[string]any{"daily_budget_brl": 999999}); r.code != 422 {
		t.Fatalf("acima do teto: %d", r.code)
	}
	r := e.do("POST", base+"/fields", s, map[string]any{"offer": "chalé", "audience": "homens de 40 a 56 anos",
		"location": "Maravilha", "daily_budget_brl": 20, "placements": []string{"Feed", "stories"}})
	if r.code != 200 || !strings.Contains(r.raw, `"placements":["feed","stories"]`) {
		t.Fatalf("editar: %d %s", r.code, r.raw)
	}
	if r := e.do("POST", base+"/propose", s, nil); r.code != 202 {
		t.Fatalf("proposta: %d %s", r.code, r.raw)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = $1`, engine.JobProjectPropose).Scan(&n); err != nil || n != 1 {
		t.Fatalf("job = %d %v", n, err)
	}
	if r := e.do("POST", "/api/projects/"+itoa(other.ID)+"/archive", s, nil); r.code != 200 {
		t.Fatalf("arquivar: %d", r.code)
	}
	if r := e.do("POST", "/api/projects/"+itoa(other.ID)+"/propose", s, nil); r.code != 409 {
		t.Fatalf("proposta de arquivado: %d", r.code)
	}
	if r := e.do("POST", "/api/projects/999999/archive", s, nil); r.code != 404 {
		t.Fatalf("inexistente: %d", r.code)
	}
}
