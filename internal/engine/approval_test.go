package engine_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const validProposal = `{"objective":"OUTCOME_ENGAGEMENT","audience":{"age_min":25,"age_max":55,"genders":[],"interests":["pizza"],"radius_km":8},"daily_budget_brl":40,"duration_days":7,"copies":[{"headline":"Pizza quentinha","text":"Peça pelo WhatsApp","cta":"WHATSAPP_MESSAGE"}],"rationale":"Público local.","risks":[]}`

var codeRe = regexp.MustCompile(`SIM (\d{6})`)

// runJobs executa os jobs prontos, como o worker faria.
func (h *harness) runJobs() {
	h.t.Helper()
	for {
		job, ok, err := h.st.ClaimJob(ctx)
		h.must(err)
		if !ok {
			return
		}
		jobErr := h.eng.HandleJob(ctx, job)
		failed, err := h.st.FinishJob(ctx, job.ID, jobErr)
		h.must(err)
		if failed {
			h.must(h.eng.OnJobFailed(ctx, job))
		}
	}
}

// reachApproval leva a conversa até a proposta enviada e devolve o código.
func (h *harness) reachApproval() string {
	h.t.Helper()
	h.att.Responses = append(h.att.Responses, turnOffer, turnRest)
	h.mgr.Responses = append(h.mgr.Responses, validProposal)
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	h.runJobs()
	m := codeRe.FindStringSubmatch(h.wa.last().Text)
	if m == nil {
		h.t.Fatalf("resumo sem código: %q", h.wa.last().Text)
	}
	return m[1]
}

func (h *harness) draftStatuses() map[string]int {
	h.t.Helper()
	rows, err := h.pool.Query(ctx, `SELECT status, count(*) FROM campaign_drafts GROUP BY status`)
	h.must(err)
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		h.must(rows.Scan(&s, &n))
		out[s] = n
	}
	return out
}

func wrongCode(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func TestProposalIsSummarizedWithCode(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	if h.conv().State != store.StateAwaitingApproval {
		t.Fatalf("estado = %s", h.conv().State)
	}
	summary := h.wa.last().Text
	for _, want := range []string{"R$ 40,00", "Pizza quentinha", "30 minutos"} {
		if !strings.Contains(summary, want) {
			t.Errorf("resumo sem %q", want)
		}
	}
	brief := h.mgr.Requests[0].Messages[1].Content
	if !strings.Contains(brief, "Campinas") || strings.Contains(brief, "quero anunciar pizza") {
		t.Errorf("briefing ao gestor:\n%s", brief)
	}
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action = 'proposal_created'`); n != 1 {
		t.Errorf("auditoria = %d", n)
	}
	if n := h.count(`SELECT count(*) FROM llm_usage WHERE agent = 'manager'`); n != 1 {
		t.Errorf("uso do gestor = %d", n)
	}
}

func TestApprovalWithValidCode(t *testing.T) {
	h := newHarness(t, "owner")
	code := h.reachApproval()
	calls := h.att.Calls()
	h.say("SIM " + code)
	if h.att.Calls() != calls {
		t.Fatal("aprovação não deveria chamar a IA")
	}
	var status string
	var by *int64
	h.must(h.pool.QueryRow(ctx, `SELECT status, approved_by FROM campaign_drafts`).Scan(&status, &by))
	if status != "approved" || by == nil {
		t.Fatalf("status = %s by = %v", status, by)
	}
	c := h.conv()
	if c.State != store.StateIdle || c.Fields.HasData() || !strings.Contains(h.wa.last().Text, "Campanha aprovada") {
		t.Errorf("conversa = %+v resposta = %q", c, h.wa.last().Text)
	}
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action = 'approval_granted'`); n != 1 {
		t.Errorf("auditoria = %d", n)
	}
}

func TestWrongCodeIsRefusedAndLocksAfterFive(t *testing.T) {
	h := newHarness(t, "owner")
	wrong := wrongCode(h.reachApproval())
	for i := 1; i <= 4; i++ {
		h.say("SIM " + wrong)
		if !strings.Contains(h.wa.last().Text, "não confere") {
			t.Fatalf("tentativa %d: %q", i, h.wa.last().Text)
		}
	}
	h.say("SIM " + wrong)
	if !strings.Contains(h.wa.last().Text, "muitas tentativas") {
		t.Fatalf("5ª: %q", h.wa.last().Text)
	}
	if h.conv().State != store.StateAwaitingProposal || h.pendingJobs(engine.JobPropose) != 1 {
		t.Errorf("estado = %s jobs = %d", h.conv().State, h.pendingJobs(engine.JobPropose))
	}
	if s := h.draftStatuses(); s["expired"] != 1 {
		t.Errorf("rascunhos = %v", s)
	}
}

func TestExpiredCodeRequestsNewProposal(t *testing.T) {
	h := newHarness(t, "owner")
	code := h.reachApproval()
	h.now = h.now.Add(31 * time.Minute)
	h.say("SIM " + code)
	if !strings.Contains(h.wa.last().Text, "expirou") || h.conv().State != store.StateAwaitingProposal {
		t.Fatalf("resposta = %q estado = %s", h.wa.last().Text, h.conv().State)
	}
	h.mgr.Responses = append(h.mgr.Responses, validProposal)
	h.runJobs()
	if !codeRe.MatchString(h.wa.last().Text) || h.conv().State != store.StateAwaitingApproval {
		t.Errorf("nova proposta não chegou: %q", h.wa.last().Text)
	}
}

func TestViewerCannotApprove(t *testing.T) {
	h := newHarness(t, "viewer")
	code := h.reachApproval()
	h.say("SIM " + code)
	if !strings.Contains(h.wa.last().Text, "não tem permissão") {
		t.Fatalf("resposta = %q", h.wa.last().Text)
	}
	if s := h.draftStatuses(); s["proposed"] != 1 {
		t.Errorf("rascunhos = %v", s)
	}
}

func TestAdjustmentCancelsDraftAndRequestsNewProposal(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	h.att.Responses = append(h.att.Responses, `{"intent":"provide_info","fields":{"daily_budget_brl":30},"reply":"Certo, vou ajustar."}`)
	h.say("pode ser 30 por dia")
	if s := h.draftStatuses(); s["cancelled"] != 1 {
		t.Errorf("rascunhos = %v", s)
	}
	c := h.conv()
	if c.State != store.StateAwaitingProposal || *c.Fields.DailyBudgetBRL != 30 {
		t.Fatalf("conversa = %+v", c)
	}
	var adj string
	h.must(h.pool.QueryRow(ctx, `SELECT payload->>'adjustment' FROM outbox_jobs WHERE status = 'pending' AND kind = $1`, engine.JobPropose).Scan(&adj))
	if adj != "pode ser 30 por dia" {
		t.Errorf("adjustment = %q", adj)
	}
}

func TestQuestionDuringApprovalKeepsDraft(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	h.att.Responses = append(h.att.Responses, `{"intent":"approve","fields":{},"reply":"Para aprovar, responda SIM e o código."}`)
	h.say("aprovo")
	if h.conv().State != store.StateAwaitingApproval || h.draftStatuses()["proposed"] != 1 {
		t.Errorf("estado = %s rascunhos = %v", h.conv().State, h.draftStatuses())
	}
}

func TestRejectDiscardsDraft(t *testing.T) {
	h := newHarness(t, "owner")
	h.reachApproval()
	h.att.Responses = append(h.att.Responses, `{"intent":"reject","fields":{},"reply":"ok"}`)
	h.say("não quero")
	if !strings.Contains(h.wa.last().Text, "descartei") || h.conv().State != store.StateIdle {
		t.Errorf("resposta = %q estado = %s", h.wa.last().Text, h.conv().State)
	}
	if s := h.draftStatuses(); s["rejected"] != 1 {
		t.Errorf("rascunhos = %v", s)
	}
}

func TestInvalidProposalsEscalate(t *testing.T) {
	h := newHarness(t, "owner")
	bad := strings.Replace(validProposal, `"daily_budget_brl":40`, `"daily_budget_brl":10000`, 1)
	h.att.Responses = []string{turnOffer, turnRest}
	h.mgr.Responses = []string{bad, bad, bad}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	h.runJobs()
	if h.conv().State != store.StateNeedsHuman {
		t.Fatalf("estado = %s", h.conv().State)
	}
	if len(h.draftStatuses()) != 0 {
		t.Errorf("não deveria haver rascunho: %v", h.draftStatuses())
	}
	if !strings.Contains(h.wa.to(chatID)[len(h.wa.to(chatID))-1], "proposta segura") || len(h.wa.to(operatorChat)) != 1 {
		t.Errorf("cliente = %v operador = %v", h.wa.to(chatID), h.wa.to(operatorChat))
	}
}

func TestOnJobFailedEscalates(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer, turnRest}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	job, ok, err := h.st.ClaimJob(ctx)
	h.must(err)
	if !ok {
		t.Fatal("sem job")
	}
	h.must(h.eng.OnJobFailed(ctx, job))
	h.runJobs()
	if h.conv().State != store.StateNeedsHuman || len(h.wa.to(operatorChat)) != 1 {
		t.Errorf("estado = %s operador = %v", h.conv().State, h.wa.to(operatorChat))
	}
}

func TestStaleProposeJobIsIgnored(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Responses = []string{turnOffer, turnRest}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	ident, _ := h.st.FindIdentity(ctx, "openwa", phone)
	c, _ := h.st.GetOrCreateConversation(ctx, ident.ID, "")
	h.must(h.st.UpdateConversation(ctx, c.ID, store.StateIdle, c.Fields))
	h.runJobs()
	if h.mgr.Calls() != 0 {
		t.Error("job obsoleto não deveria chamar o gestor")
	}
}
