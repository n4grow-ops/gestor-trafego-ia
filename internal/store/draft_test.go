package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

var t0 = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

const goodCode = "hash-do-codigo"

type draftFixture struct {
	st      *store.Store
	pool    *pgxpool.Pool
	ident   store.Identity
	conv    store.Conversation
	draftID int64
}

func seedDraft(t *testing.T) draftFixture {
	t.Helper()
	st, pool := newStore(t)
	ident := seedIdentity(t, st, "owner", "5511988887777")
	conv, err := st.GetOrCreateConversation(ctx, ident.ID, "a@c.us")
	must(t, err)
	must(t, st.UpdateConversation(ctx, conv.ID, store.StateAwaitingProposal, contract.Fields{Offer: contract.Str("Pizza")}))
	id, err := st.SaveProposal(ctx, store.ProposalRecord{
		OrgID: ident.OrgID, ConversationID: conv.ID,
		Briefing:     contract.Briefing{OrganizationID: ident.OrgID, Offer: "Pizza"},
		Proposal:     contract.CampaignProposal{Objective: "OUTCOME_ENGAGEMENT", DailyBudgetBRL: 40},
		ProposalHash: "hash-da-proposta", CodeHash: goodCode, ExpiresAt: t0.Add(30 * time.Minute),
		Send: store.JobSpec{Kind: "whatsapp.send", Payload: map[string]string{"text": "resumo"}},
	})
	must(t, err)
	return draftFixture{st: st, pool: pool, ident: ident, conv: conv, draftID: id}
}

func (f draftFixture) status(t *testing.T) (string, *int64) {
	t.Helper()
	var status string
	var by *int64
	must(t, f.pool.QueryRow(ctx, `SELECT status, approved_by FROM campaign_drafts WHERE id = $1`, f.draftID).Scan(&status, &by))
	return status, by
}

func TestSaveProposal(t *testing.T) {
	f := seedDraft(t)
	d, err := f.st.OpenDraft(ctx, f.conv.ID)
	must(t, err)
	if d.ID != f.draftID || d.Status != "proposed" || d.ProposalHash != "hash-da-proposta" ||
		d.Proposal.DailyBudgetBRL != 40 || d.Briefing.Offer != "Pizza" || d.OrgID != f.ident.OrgID {
		t.Errorf("draft = %+v", d)
	}
	conv, err := f.st.GetConversation(ctx, f.conv.ID)
	must(t, err)
	if conv.State != store.StateAwaitingApproval || *conv.Fields.Offer != "Pizza" {
		t.Errorf("conversa = %+v", conv)
	}
	job, ok, err := f.st.ClaimJob(ctx)
	must(t, err)
	if !ok || job.Kind != "whatsapp.send" {
		t.Errorf("job = %+v", job)
	}
}

func TestOpenDraftNotFound(t *testing.T) {
	st, _ := newStore(t)
	if _, err := st.OpenDraft(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestApproveDraft(t *testing.T) {
	f := seedDraft(t)
	must(t, f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0.Add(time.Minute)))
	status, by := f.status(t)
	if status != "approved" || by == nil || *by != f.ident.MemberID {
		t.Errorf("status = %s by = %v", status, by)
	}
	if err := f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0.Add(time.Minute)); !errors.Is(err, store.ErrCodeInvalid) {
		t.Errorf("reuso: err = %v", err)
	}
	if _, err := f.st.OpenDraft(ctx, f.conv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("rascunho aprovado não é mais aberto: %v", err)
	}
}

func TestApproveDraftLocksAfterFiveWrong(t *testing.T) {
	f := seedDraft(t)
	for i := 1; i <= 4; i++ {
		if err := f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, "errado", t0); !errors.Is(err, store.ErrCodeInvalid) {
			t.Fatalf("tentativa %d: err = %v", i, err)
		}
	}
	if err := f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, "errado", t0); !errors.Is(err, store.ErrCodeLocked) {
		t.Fatalf("5ª: err = %v", err)
	}
	if err := f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0); !errors.Is(err, store.ErrCodeLocked) {
		t.Fatalf("certo após bloqueio: err = %v", err)
	}
	if status, _ := f.status(t); status != "proposed" {
		t.Errorf("status = %s", status)
	}
}

func TestApproveDraftExpired(t *testing.T) {
	f := seedDraft(t)
	if err := f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0.Add(30*time.Minute)); !errors.Is(err, store.ErrCodeExpired) {
		t.Fatalf("err = %v", err)
	}
}

func TestApproveDraftRejectsChangedProposal(t *testing.T) {
	f := seedDraft(t)
	f.pool.Exec(ctx, `UPDATE campaign_drafts SET proposal_hash = 'outra' WHERE id = $1`, f.draftID)
	if err := f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0); !errors.Is(err, store.ErrCodeInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestSetDraftStatusOnlyFromProposed(t *testing.T) {
	f := seedDraft(t)
	must(t, f.st.ApproveDraft(ctx, f.draftID, f.ident.MemberID, goodCode, t0))
	must(t, f.st.SetDraftStatus(ctx, f.draftID, "cancelled"))
	if status, _ := f.status(t); status != "approved" {
		t.Errorf("rascunho aprovado mudou para %s", status)
	}
	g := seedDraft(t)
	must(t, g.st.SetDraftStatus(ctx, g.draftID, "rejected"))
	if status, _ := g.status(t); status != "rejected" {
		t.Errorf("status = %s", status)
	}
}

func TestRecordUsageAndAudit(t *testing.T) {
	f := seedDraft(t)
	must(t, f.st.RecordUsage(ctx, f.ident.OrgID, "manager", llm.Usage{Model: "m", InputTokens: 100, OutputTokens: 50, CostUSD: 0.0123}))
	must(t, f.st.Audit(ctx, f.ident.OrgID, "member:1", "approval_granted", map[string]any{"draft_id": f.draftID}))
	var tokens int
	var cost float64
	must(t, f.pool.QueryRow(ctx, `SELECT input_tokens, cost_usd::float8 FROM llm_usage WHERE agent = 'manager'`).Scan(&tokens, &cost))
	if tokens != 100 || cost != 0.0123 {
		t.Errorf("tokens = %d cost = %v", tokens, cost)
	}
	var action string
	var draftID int64
	must(t, f.pool.QueryRow(ctx, `SELECT action, (data->>'draft_id')::bigint FROM audit_events`).Scan(&action, &draftID))
	if action != "approval_granted" || draftID != f.draftID {
		t.Errorf("audit = %s %d", action, draftID)
	}
}

// Aprovação, etapa do projeto e estado da conversa vão juntos: falha em um desfaz todos.
func TestApproveDraftAdvanceIsAtomic(t *testing.T) {
	f := seedDraft(t)
	bad := &store.ApprovalAdvance{ConversationID: f.conv.ID, ConversationState: "estado-que-nao-existe"}
	if err := f.st.ApproveDraftAdvance(ctx, f.draftID, f.ident.MemberID, goodCode, t0.Add(time.Minute), bad); err == nil {
		t.Fatal("estado inválido deveria falhar")
	}
	if status, _ := f.status(t); status != "proposed" {
		t.Fatalf("aprovação ficou gravada pela metade: %s", status)
	}
	adv := &store.ApprovalAdvance{ConversationID: f.conv.ID, ConversationState: store.StateIdle}
	must(t, f.st.ApproveDraftAdvance(ctx, f.draftID, f.ident.MemberID, goodCode, t0.Add(time.Minute), adv))
	if status, _ := f.status(t); status != "approved" {
		t.Fatalf("status = %s", status)
	}
	conv, err := f.st.GetConversation(ctx, f.conv.ID)
	must(t, err)
	if conv.State != store.StateIdle {
		t.Fatalf("conversa = %s", conv.State)
	}
}
