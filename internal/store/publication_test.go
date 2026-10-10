package store_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// seedPublishable: rascunho aprovado e conexão Meta ativa na mesma organização.
func seedPublishable(t *testing.T) (draftFixture, int64) {
	t.Helper()
	f := seedDraft(t)
	must(t, f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0))
	must(t, f.st.CreateMetaLink(ctx, f.ident.ID, "hp", time.Now().Add(time.Hour)))
	l, err := f.st.MetaLink(ctx, "hp")
	must(t, err)
	connID, err := f.st.SavePendingMeta(ctx, l, "U1", []byte("cifrado"), metaOne)
	must(t, err)
	_, err = f.st.CompleteMeta(ctx, store.MetaChoice{ConnectionID: connID, LinkID: l.ID, Account: metaAcc, Page: metaPage})
	must(t, err)
	return f, connID
}

func TestMetaTokenSealed(t *testing.T) {
	f, connID := seedPublishable(t)
	id, sealed, err := f.st.MetaTokenSealed(ctx, f.ident.OrgID)
	if err != nil || id != connID || string(sealed) != "cifrado" {
		t.Fatalf("token = %d %q %v", id, sealed, err)
	}
	if _, _, err := f.st.MetaTokenSealed(ctx, f.ident.OrgID+999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sem conexão: %v", err)
	}
}

func TestPublicationLifecycle(t *testing.T) {
	f, connID := seedPublishable(t)
	org := f.ident.OrgID
	if _, err := f.st.LatestPublication(ctx, org); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sem publicação: %v", err)
	}
	// Rascunho de outra organização não abre publicação.
	if _, err := f.st.StartPublication(ctx, org+999, f.draftID, nil, connID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outra organização: %v", err)
	}
	p, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	must(t, err)
	if p.Status != "publishing" || p.DraftID != f.draftID || p.OrgID != org || p.ConnectionID != connID || p.ProjectID != nil ||
		len(p.AdIDs) != 0 || p.ActivatedAt != nil {
		t.Fatalf("aberta = %+v", p)
	}
	again, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	if !errors.Is(err, store.ErrAlreadyPublished) || again.ID != p.ID {
		t.Fatalf("segunda abertura = %+v %v", again, err)
	}
	// Ativar antes de criar na Meta não vale.
	if err := f.st.SetPublicationStatus(ctx, p.ID, "active"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ativar publicando: %v", err)
	}

	// Falha e nova tentativa reaproveitam a linha.
	must(t, f.st.FailPublication(ctx, p.ID, "HTTP 400 token=EAABsegredo123456 inválido"))
	l, err := f.st.LatestPublication(ctx, org)
	must(t, err)
	if l.Status != "failed" || strings.Contains(l.Error, "EAABsegredo") || !strings.Contains(l.Error, "HTTP 400") {
		t.Fatalf("falha = %+v", l)
	}
	if err := f.st.FinishPublication(ctx, p.ID, store.PublicationResult{CampaignID: "C1"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("concluir falhada: %v", err)
	}
	retry, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	if err != nil || retry.ID != p.ID || retry.Status != "publishing" || retry.Error != "" {
		t.Fatalf("nova tentativa = %+v %v", retry, err)
	}

	if err := f.st.FinishPublication(ctx, p.ID, store.PublicationResult{}); err == nil {
		t.Fatal("concluiu sem campanha")
	}
	must(t, f.st.FinishPublication(ctx, p.ID, store.PublicationResult{CampaignID: "C1", AdSetID: "S1",
		AdIDs: []string{"A1", "A2"}, Warnings: []string{"interesse não encontrado: x"}}))
	l, err = f.st.LatestPublication(ctx, org)
	must(t, err)
	if l.Status != "paused" || l.CampaignID != "C1" || l.AdSetID != "S1" || len(l.AdIDs) != 2 || l.AdIDs[1] != "A2" ||
		len(l.Warnings) != 1 || l.ActivatedAt != nil {
		t.Fatalf("pausada = %+v", l)
	}
	if _, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID); !errors.Is(err, store.ErrAlreadyPublished) {
		t.Fatalf("abrir depois de pausada: %v", err)
	}
	if err := f.st.FailPublication(ctx, p.ID, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("falhar pausada: %v", err)
	}

	if err := f.st.SetPublicationStatus(ctx, p.ID, "failed"); err == nil {
		t.Fatal("status inválido aceito")
	}
	must(t, f.st.SetPublicationStatus(ctx, p.ID, "active"))
	l, _ = f.st.LatestPublication(ctx, org)
	if l.Status != "active" || l.ActivatedAt == nil {
		t.Fatalf("ativa = %+v", l)
	}
	first := *l.ActivatedAt
	must(t, f.st.SetPublicationStatus(ctx, p.ID, "active"))
	l, _ = f.st.LatestPublication(ctx, org)
	if !l.ActivatedAt.Equal(first) {
		t.Fatal("ativar de novo mudou activated_at")
	}
	must(t, f.st.SetPublicationStatus(ctx, p.ID, "paused"))
	l, _ = f.st.LatestPublication(ctx, org)
	if l.Status != "paused" || l.ActivatedAt == nil {
		t.Fatalf("pausada de novo = %+v", l)
	}
}

func TestAppendClientCopy(t *testing.T) {
	f := seedDraft(t)
	must(t, f.st.AppendClientCopy(ctx, f.draftID, "  Promoção de sexta  "))
	must(t, f.st.AppendClientCopy(ctx, f.draftID, "   "))
	must(t, f.st.AppendClientCopy(ctx, f.draftID, "Pizza em dobro"))
	var got string
	must(t, f.pool.QueryRow(ctx, `SELECT client_copy FROM campaign_drafts WHERE id = $1`, f.draftID).Scan(&got))
	if got != "Promoção de sexta\nPizza em dobro" {
		t.Fatalf("referências = %q", got)
	}
	must(t, f.st.AppendClientCopy(ctx, f.draftID, strings.Repeat("ç", 3000)))
	must(t, f.pool.QueryRow(ctx, `SELECT client_copy FROM campaign_drafts WHERE id = $1`, f.draftID).Scan(&got))
	if n := utf8.RuneCountInString(got); n != 2000 || !strings.HasPrefix(got, "Promoção de sexta\n") {
		t.Fatalf("corte = %d runas", n)
	}
	if err := f.st.AppendClientCopy(ctx, f.draftID+999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rascunho inexistente: %v", err)
	}
}

func TestPublishData(t *testing.T) {
	f := seedDraft(t)
	if _, err := f.st.PublishData(ctx, f.draftID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("não aprovado: %v", err)
	}
	must(t, f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0))
	must(t, f.st.AppendClientCopy(ctx, f.draftID, "Melhor pizza da cidade"))
	d, err := f.st.PublishData(ctx, f.draftID)
	must(t, err)
	if d.DraftID != f.draftID || d.OrgID != f.ident.OrgID || d.OrgName == "" || d.ClientCopy != "Melhor pizza da cidade" {
		t.Fatalf("dados = %+v", d)
	}
	var p contract.CampaignProposal
	must(t, json.Unmarshal(d.Proposal, &p))
	var fl contract.Fields
	must(t, json.Unmarshal(d.Fields, &fl))
	if p.Objective != "OUTCOME_ENGAGEMENT" || p.DailyBudgetBRL != 40 || fl.Offer == nil || *fl.Offer != "Pizza" || fl.Location != nil {
		t.Fatalf("proposta = %+v campos = %s", p, d.Fields)
	}
}

// secondDraft copia o rascunho aprovado para um novo, ligado a um projeto com o nome dado.
func secondDraft(t *testing.T, f draftFixture, project string) (int64, *int64) {
	t.Helper()
	var projectID int64
	must(t, f.pool.QueryRow(ctx, `INSERT INTO projects (organization_id, name) VALUES ($1, $2) RETURNING id`, f.ident.OrgID, project).Scan(&projectID))
	var id int64
	must(t, f.pool.QueryRow(ctx, `INSERT INTO campaign_drafts (organization_id, conversation_id, briefing, proposal, proposal_hash, status, approved_by, approved_at, project_id)
		SELECT organization_id, conversation_id, briefing, proposal, proposal_hash, status, approved_by, approved_at, $2 FROM campaign_drafts WHERE id = $1
		RETURNING id`, f.draftID, projectID).Scan(&id))
	return id, &projectID
}

// C2: ATIVAR/PAUSAR olham todas as publicações da organização, não só a última.
func TestListPublicationsByStatus(t *testing.T) {
	f, connID := seedPublishable(t)
	org := f.ident.OrgID
	d2, p2 := secondDraft(t, f, "Rodízio")
	a, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	must(t, err)
	must(t, f.st.FinishPublication(ctx, a.ID, store.PublicationResult{CampaignID: "CA"}))
	b, err := f.st.StartPublication(ctx, org, d2, p2, connID)
	must(t, err)
	must(t, f.st.FinishPublication(ctx, b.ID, store.PublicationResult{CampaignID: "CB"}))

	paused, err := f.st.ListPublications(ctx, org, "paused")
	must(t, err)
	if len(paused) != 2 || paused[0].ID != a.ID || paused[1].ID != b.ID || paused[1].ProjectName != "Rodízio" || paused[0].ProjectName != "" {
		t.Fatalf("pausadas = %+v", paused)
	}
	must(t, f.st.SetPublicationStatus(ctx, a.ID, "active"))
	active, err := f.st.ListPublications(ctx, org, "active")
	must(t, err)
	if len(active) != 1 || active[0].CampaignID != "CA" {
		t.Fatalf("ativas = %+v", active)
	}
	if other, err := f.st.ListPublications(ctx, org+999, "active"); err != nil || len(other) != 0 {
		t.Fatalf("outra organização = %+v %v", other, err)
	}
}

// C2: nova tentativa de uma publicação que falhou passa a ser a mais recente.
func TestRetriedPublicationBecomesLatest(t *testing.T) {
	f, connID := seedPublishable(t)
	org := f.ident.OrgID
	d2, p2 := secondDraft(t, f, "Rodízio")
	a, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	must(t, err)
	must(t, f.st.FailPublication(ctx, a.ID, "falhou"))
	b, err := f.st.StartPublication(ctx, org, d2, p2, connID)
	must(t, err)
	must(t, f.st.FinishPublication(ctx, b.ID, store.PublicationResult{CampaignID: "CB"}))
	_, err = f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	must(t, err)
	l, err := f.st.LatestPublication(ctx, org)
	must(t, err)
	if l.ID != a.ID || l.Status != "publishing" {
		t.Fatalf("mais recente = %+v", l)
	}
}

// I2: publicação parada em publishing há mais de 30 min (worker morreu no meio) é retomada, com aviso.
func TestStartPublicationRecoversStale(t *testing.T) {
	f, connID := seedPublishable(t)
	org := f.ident.OrgID
	p, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	must(t, err)
	if p.Recovered {
		t.Fatal("publicação nova marcada como retomada")
	}
	if _, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID); !errors.Is(err, store.ErrAlreadyPublished) {
		t.Fatalf("publicando há pouco: %v", err)
	}
	_, err = f.pool.Exec(ctx, `UPDATE meta_publications SET updated_at = now() - interval '31 minutes' WHERE id = $1`, p.ID)
	must(t, err)
	again, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	if err != nil || again.ID != p.ID || again.Status != "publishing" || !again.Recovered {
		t.Fatalf("retomada = %+v %v", again, err)
	}
	if _, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID); !errors.Is(err, store.ErrAlreadyPublished) {
		t.Fatalf("retomada vale uma vez: %v", err)
	}
}
