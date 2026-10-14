package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Resultado do dia, campanha ativa e pagamento guardado; reprovação refaz a publicação.
func TestResultsFundingAndRejection(t *testing.T) {
	f, connID := seedPublishable(t)
	org := f.ident.OrgID
	p, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	must(t, err)
	must(t, f.st.FinishPublication(ctx, p.ID, store.PublicationResult{CampaignID: "C1", AdSetID: "S1", AdIDs: []string{"A1"}}))
	must(t, f.st.SetPublicationStatus(ctx, p.ID, "active"))
	act, err := f.st.ActivePublications(ctx)
	if err != nil || len(act) != 1 || act[0].ID != p.ID {
		t.Fatalf("ativas = %+v %v", act, err)
	}
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	must(t, f.st.SaveCampaignResult(ctx, store.CampaignResult{PublicationID: p.ID, Day: day, SpendCents: 100, Conversations: 1}))
	must(t, f.st.SaveCampaignResult(ctx, store.CampaignResult{PublicationID: p.ID, Day: day, SpendCents: 3500, Conversations: 5}))
	var spend, n int64
	must(t, f.pool.QueryRow(ctx, `SELECT spend_cents, (SELECT count(*) FROM campaign_results) FROM campaign_results`).Scan(&spend, &n))
	if spend != 3500 || n != 1 {
		t.Fatalf("resultado = %d (%d linhas)", spend, n)
	}
	must(t, f.st.SaveFunding(ctx, org, map[string]any{"Status": 1}, time.Now()))
	var funding string
	must(t, f.pool.QueryRow(ctx, `SELECT funding::text FROM meta_connections WHERE id = $1`, connID).Scan(&funding))
	if funding != `{"Status": 1}` {
		t.Fatalf("pagamento = %s", funding)
	}

	must(t, f.st.RejectPublication(ctx, p.ID, "texto demais"))
	got, err := f.st.GetPublication(ctx, p.ID)
	if err != nil || got.Status != "rejected" || got.Error != "texto demais" {
		t.Fatalf("reprovada = %+v %v", got, err)
	}
	again, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	if err != nil || again.ID != p.ID || again.Status != "publishing" {
		t.Fatalf("refazer = %+v %v", again, err)
	}
}
