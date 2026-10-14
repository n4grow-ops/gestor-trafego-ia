package httpapi_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Campanhas no painel: lista com link do Gerenciador e pagamento; pausar e atualizar pagamento entram na fila.
func TestAdminCampaigns(t *testing.T) {
	e := newAdminEnv(t, "otp")
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var draft, conn, pub int64
	must(e.pool.QueryRow(ctx, `INSERT INTO campaign_drafts (organization_id, conversation_id, briefing, proposal, proposal_hash, status)
		VALUES ($1, $2, '{}', '{"daily_budget_brl": 40}', 'h', 'proposed') RETURNING id`, e.orgID, e.convID).Scan(&draft))
	ident, err := e.st.FindIdentity(ctx, "waha", "5511988887777")
	must(err)
	must(e.st.CreateMetaLink(ctx, ident.ID, "hc", time.Now().Add(time.Hour)))
	l, err := e.st.MetaLink(ctx, "hc")
	must(err)
	acc := meta.AdAccount{ID: "act_987", Name: "Conta", Currency: "BRL", Status: 1}
	pg := meta.Page{ID: "P1", Name: "Página"}
	conn, err = e.st.SavePendingMeta(ctx, l, "U1", []byte("x"), store.MetaCandidates{AdAccounts: []meta.AdAccount{acc}, Pages: []meta.Page{pg}})
	must(err)
	_, err = e.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: conn, LinkID: l.ID, Account: acc, Page: pg})
	must(err)
	_, err = e.pool.Exec(ctx, `UPDATE meta_connections SET funding = '{"Status":1,"Prepaid":true,"HasSource":true,"SourceType":20,"AvailableCents":1500}' WHERE id = $1`, conn)
	must(err)
	must(e.pool.QueryRow(ctx, `INSERT INTO meta_publications (organization_id, draft_id, meta_connection_id, status, campaign_id)
		VALUES ($1, $2, $3, 'active', 'C55') RETURNING id`, e.orgID, draft, conn).Scan(&pub))

	s := e.login("marina@meuanuncio.io")
	r := e.do("GET", "/api/campaigns?status=active", s, nil)
	if r.code != 200 || !strings.Contains(r.raw, `"funding_flag":"saldo_baixo"`) ||
		!strings.Contains(r.raw, "selected_campaign_ids=C55") || !strings.Contains(r.raw, "Pizzaria Boa") {
		t.Fatalf("lista: %d %s", r.code, r.raw)
	}
	if r = e.do("GET", "/api/campaigns?status=xpto", s, nil); r.code != 422 {
		t.Errorf("status inválido: %d", r.code)
	}
	if r = e.do("POST", "/api/campaigns/"+itoa(pub)+"/pause", s, nil); r.code != 202 {
		t.Fatalf("pausar: %d %s", r.code, r.raw)
	}
	if r = e.do("POST", "/api/clients/"+itoa(e.orgID)+"/funding/refresh", s, nil); r.code != 202 {
		t.Fatalf("atualizar pagamento: %d", r.code)
	}
	var n int
	must(e.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind IN ($1, $2)`, engine.JobTeamPause, engine.JobFundingUpdate).Scan(&n))
	if n != 2 {
		t.Fatalf("jobs = %d", n)
	}
	r = e.do("GET", "/api/clients/"+itoa(e.orgID), s, nil)
	if !strings.Contains(r.raw, `"funding_flag":"ok"`) {
		t.Fatalf("ficha sem pagamento: %s", r.raw)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
