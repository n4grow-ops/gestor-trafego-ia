package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
)

func TestDailyStats(t *testing.T) {
	st, pool := newStore(t)
	pending := pendingIdentity(t, st, "owner", "5511988887777")
	must(t, st.CreateSignupLink(ctx, pending.ID, "h1", time.Now().Add(time.Hour)))
	done := seedIdentity(t, st, "owner", "5511977776666")
	must(t, st.SetRegistrationStatus(ctx, done.OrgID, "complete"))
	conv, err := st.GetOrCreateConversation(ctx, done.ID, "x@c.us")
	must(t, err)
	must(t, st.AddMessage(ctx, conv.ID, "in", "text", "oi"))
	must(t, st.AddMessage(ctx, conv.ID, "out", "text", "olá"))
	must(t, st.RecordUsage(ctx, done.OrgID, "attendant", llm.Usage{Model: "m", CostUSD: 0.25}))
	must(t, st.RecordUsage(ctx, done.OrgID, "manager", llm.Usage{Model: "m", CostUSD: 0.5}))
	// algo antigo fica fora da janela
	_, err = pool.Exec(ctx, `INSERT INTO llm_usage (organization_id, agent, model, input_tokens, output_tokens, cost_usd, created_at)
		VALUES ($1, 'manager', 'm', 1, 1, 9, now() - interval '3 days')`, done.OrgID)
	must(t, err)

	s, err := st.DailyStats(ctx, time.Now().Add(-24*time.Hour))
	must(t, err)
	if s.NewContacts != 2 || s.Registered != 1 || s.PendingWithLink != 1 || s.Inbound != 1 || s.CostUSD != 0.75 {
		t.Fatalf("estatísticas = %+v", s)
	}
}
