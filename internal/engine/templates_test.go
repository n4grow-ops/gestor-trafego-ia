package engine_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// tplWA é o fake com modelos aprovados (Cloud API).
type tplWA struct {
	*fakeWA
	mu   sync.Mutex
	tpls []whatsapp.Template
}

func (f *tplWA) NeedsTemplates() bool { return true }
func (f *tplWA) SendTemplate(_ context.Context, _ string, t whatsapp.Template) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tpls = append(f.tpls, t)
	return nil
}

func runSend(t *testing.T, h *harness, s engine.SendJob) {
	t.Helper()
	b, _ := json.Marshal(s)
	h.must(h.eng.HandleJob(ctx, store.Job{Kind: engine.JobSend, Payload: b}))
}

func inboundAgo(h *harness, conv store.Conversation, interval string) {
	_, err := h.st.AddInbound(ctx, conv.ID, "text", "oi")
	h.must(err)
	_, err = h.pool.Exec(ctx, `UPDATE messages SET created_at = $3::timestamptz - $1::interval WHERE conversation_id = $2`, interval, conv.ID, h.now)
	h.must(err)
}

func TestSendJobOutsideWindowUsesTemplate(t *testing.T) {
	h := newHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	conv := h.conv()
	inboundAgo(h, conv, "30 hours")
	tpl := &whatsapp.Template{Name: engine.TplSignupReminder, Params: []string{"https://x/c/AbC123xYz789"}}
	runSend(t, h, engine.SendJob{ConversationID: conv.ID, ChatID: chatID, Text: "lembrete https://x/c/AbC123xYz789", Template: tpl})
	if len(wa.tpls) != 1 || wa.tpls[0].Name != engine.TplSignupReminder || h.wa.count() != 0 {
		t.Fatalf("modelos=%v textos=%d", wa.tpls, h.wa.count())
	}
	msgs, _ := h.st.RecentMessages(ctx, conv.ID, 5)
	if last := msgs[len(msgs)-1]; last.Direction != "out" || strings.Contains(last.Body, "AbC123xYz789") {
		t.Fatalf("envio deve ficar no histórico, sem o link: %+v", last)
	}
}

func TestSendJobInsideWindowUsesText(t *testing.T) {
	h := newHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	conv := h.conv()
	inboundAgo(h, conv, "2 hours")
	runSend(t, h, engine.SendJob{ConversationID: conv.ID, ChatID: chatID, Text: "lembrete", Template: &whatsapp.Template{Name: engine.TplSignupReminder}})
	if len(wa.tpls) != 0 || h.wa.count() != 1 {
		t.Fatalf("modelos=%v textos=%d", wa.tpls, h.wa.count())
	}
}

func TestSendJobToTeamAlwaysTemplate(t *testing.T) {
	h := newHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	runSend(t, h, engine.SendJob{ChatID: "5511900000000", Text: "aviso", Template: &whatsapp.Template{Name: engine.TplTeamAlert, Params: []string{"a", "b", "c", "d"}}})
	if len(wa.tpls) != 1 || h.wa.count() != 0 {
		t.Fatalf("modelos=%v textos=%d", wa.tpls, h.wa.count())
	}
}

func TestSendJobWithoutTemplateSupportUsesText(t *testing.T) {
	h := newHarness(t, "owner")
	runSend(t, h, engine.SendJob{ChatID: "5511900000000", Text: "aviso", Template: &whatsapp.Template{Name: engine.TplTeamAlert}})
	if h.wa.count() != 1 || h.wa.last().Text != "aviso" {
		t.Fatalf("enviado = %+v", h.wa.last())
	}
}

func TestEscalateAlertsTeamWithTemplate(t *testing.T) {
	h := newHarness(t, "owner")
	tpl := engine.TeamAlertTemplate("Ana", "Pizzaria Boa", "5511988887777", "pediu humano")
	if tpl.Name != engine.TplTeamAlert || len(tpl.Params) != 4 || tpl.Params[3] != "pediu humano" {
		t.Fatalf("%+v", tpl)
	}
	_ = h
}

func TestTemplateDefinitionsMatchParams(t *testing.T) {
	want := map[string]int{engine.TplSignupReminder: 1, engine.TplDailyReport: 8, engine.TplTeamAlert: 4, engine.TplMetaExpiring: 2}
	for _, d := range engine.TemplateDefinitions {
		n, ok := want[d.Name]
		if !ok {
			t.Errorf("modelo inesperado %q", d.Name)
			continue
		}
		if strings.Count(d.Body, "{{") != n || len(d.Example) != n {
			t.Errorf("%s: variáveis no corpo=%d exemplo=%d, want %d", d.Name, strings.Count(d.Body, "{{"), len(d.Example), n)
		}
		delete(want, d.Name)
	}
	if len(want) != 0 {
		t.Errorf("faltam definições: %v", want)
	}
}

func TestDailyReportUsesTemplateOnCloudAPI(t *testing.T) {
	h := newHarness(t, "owner")
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.eng.ReportPhone = "5511900000000"
	h.att.Responses = []string{turnOffer}
	h.say("quero anunciar pizza")
	h.must(h.eng.EnsureDailyReport(ctx))
	_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = now() WHERE kind = 'report.daily'`)
	h.must(err)
	h.runJobs()
	var report []whatsapp.Template
	for _, tp := range wa.tpls {
		if tp.Name == engine.TplDailyReport {
			report = append(report, tp)
		}
	}
	if len(report) != 1 || len(report[0].Params) != 8 || report[0].Params[3] != "1" {
		t.Fatalf("relatório = %+v", report)
	}
}

func TestSignupReminderUsesTemplateOutsideWindow(t *testing.T) {
	h := newSignupHarness(t)
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.say("oi")
	conv := h.conv()
	_, err := h.pool.Exec(ctx, `UPDATE messages SET created_at = $1::timestamptz - interval '25 hours' WHERE conversation_id = $2`, h.now, conv.ID)
	h.must(err)
	h.runRemindJobs()
	if len(wa.tpls) != 1 || wa.tpls[0].Name != engine.TplSignupReminder || !strings.HasPrefix(wa.tpls[0].Params[0], signupBase+"/") {
		t.Fatalf("modelos = %+v", wa.tpls)
	}
}

func TestSignupReminderInsideWindowStaysText(t *testing.T) {
	h := newSignupHarness(t)
	wa := &tplWA{fakeWA: h.wa}
	h.eng.WA = wa
	h.say("oi")
	sent := h.wa.count()
	h.runRemindJobs()
	if len(wa.tpls) != 0 || h.wa.count() != sent+1 || !strings.Contains(h.wa.last().Text, "Faltou pouquinho") {
		t.Fatalf("modelos=%+v último=%q", wa.tpls, h.wa.last().Text)
	}
}
