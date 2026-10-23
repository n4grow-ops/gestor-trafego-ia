package store_test

import "testing"

// Plano Básico: 4 campanhas novas por mês; ajuste de campanha já aprovada não conta.
func TestCampaignQuota(t *testing.T) {
	st, pool := newStore(t)
	id := seedIdentity(t, st, "owner", "5511900000077")
	q, err := st.CampaignQuota(ctx, id.OrgID, nil)
	must(t, err)
	if q.Max != 0 || q.Blocked() {
		t.Fatalf("sem assinatura = %+v", q)
	}
	_, err = pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'free', 'active')`, id.OrgID)
	must(t, err)
	conv, err := st.GetOrCreateConversation(ctx, id.ID, "5511900000077@c.us")
	must(t, err)
	var projects []int64
	for i := range 4 {
		var pid int64
		must(t, pool.QueryRow(ctx, `INSERT INTO projects (organization_id, name, fields) VALUES ($1, $2, '{}') RETURNING id`,
			id.OrgID, string(rune('A'+i))).Scan(&pid))
		projects = append(projects, pid)
		_, err = pool.Exec(ctx, `INSERT INTO campaign_drafts (organization_id, conversation_id, project_id, briefing, proposal, proposal_hash,
			status, approved_by, approved_at) VALUES ($1, $2, $3, '{}', '{}', 'h', 'approved', $4, now())`, id.OrgID, conv.ID, pid, id.MemberID)
		must(t, err)
	}
	q, err = st.CampaignQuota(ctx, id.OrgID, nil)
	must(t, err)
	if q.Plan != "Básico" || q.Max != 4 || q.Used != 4 || !q.Blocked() {
		t.Fatalf("quinta campanha = %+v", q)
	}
	q, err = st.CampaignQuota(ctx, id.OrgID, &projects[0])
	must(t, err)
	if !q.Edit || q.Blocked() {
		t.Fatalf("ajuste de campanha existente = %+v", q)
	}
}
