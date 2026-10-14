package store_test

import (
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

func TestGuardrailRulesDefaultOffAndOfferedOnce(t *testing.T) {
	f := seedDraft(t)
	org := f.ident.OrgID
	if _, ok, err := f.st.GetGuardrail(ctx, org); err != nil || ok {
		t.Fatalf("sem regra: ok=%v err=%v", ok, err)
	}
	// oferta: só a primeira vez
	first, err := f.st.MarkGuardrailOffered(ctx, org)
	must(t, err)
	again, err := f.st.MarkGuardrailOffered(ctx, org)
	must(t, err)
	if !first || again {
		t.Fatalf("oferta first=%v again=%v", first, again)
	}
	r, ok, err := f.st.GetGuardrail(ctx, org)
	must(t, err)
	if !ok || r.Active() || r.OfferedAt == nil || !r.AutoPause {
		t.Fatalf("ofertar não liga nada: %+v", r)
	}
	if all, err := f.st.ActiveGuardrails(ctx); err != nil || len(all) != 0 {
		t.Fatalf("regra só ofertada não vale: %v %v", all, err)
	}

	must(t, f.st.SetGuardrailLimit(ctx, org, store.GuardrailMaxCost, 1500))
	must(t, f.st.SetGuardrailLimit(ctx, org, store.GuardrailDailyCap, 10000))
	must(t, f.st.SetGuardrailLimit(ctx, org, store.GuardrailMaxCost, 2000)) // troca só o custo
	r, _, _ = f.st.GetGuardrail(ctx, org)
	if r.MaxCostPerConvCents == nil || *r.MaxCostPerConvCents != 2000 || r.DailyCapCents == nil || *r.DailyCapCents != 10000 || !r.Active() {
		t.Fatalf("limites = %+v", r)
	}
	if all, _ := f.st.ActiveGuardrails(ctx); len(all) != 1 || all[org].MaxCostPerConvCents == nil {
		t.Fatalf("ativas = %+v", all)
	}
	must(t, f.st.SetGuardrailAutoPause(ctx, org, false))
	if r, _, _ = f.st.GetGuardrail(ctx, org); r.AutoPause {
		t.Fatal("só avisar não gravou")
	}
	// quem já definiu limite não recebe a oferta
	if again, _ := f.st.MarkGuardrailOffered(ctx, org); again {
		t.Fatal("ofereceu de novo")
	}

	must(t, f.st.ClearGuardrail(ctx, org))
	r, _, _ = f.st.GetGuardrail(ctx, org)
	if r.Active() || !r.AutoPause || r.OfferedAt == nil {
		t.Fatalf("depois de limpar: %+v", r)
	}
	if err := f.st.SetGuardrailLimit(ctx, org, store.GuardrailMaxCost, 0); err == nil {
		t.Fatal("limite zero deveria ser recusado")
	}
	if err := f.st.SetGuardrailLimit(ctx, org, "outra", 100); err == nil {
		t.Fatal("regra desconhecida deveria ser recusada")
	}
}

func TestGuardrailOfferDoesNotOverwriteExistingLimit(t *testing.T) {
	f := seedDraft(t)
	must(t, f.st.SetGuardrailLimit(ctx, f.ident.OrgID, store.GuardrailDailyCap, 5000))
	if first, err := f.st.MarkGuardrailOffered(ctx, f.ident.OrgID); err != nil || first {
		t.Fatalf("first=%v err=%v", first, err)
	}
}

func TestGuardrailEventIsIdempotentAndCompletesOnce(t *testing.T) {
	f, connID := seedPublishable(t)
	org := f.ident.OrgID
	p, err := f.st.StartPublication(ctx, org, f.draftID, nil, connID)
	must(t, err)
	must(t, f.st.FinishPublication(ctx, p.ID, store.PublicationResult{CampaignID: "C1", AdSetID: "S1", AdIDs: []string{"A1"}}))
	must(t, f.st.SetPublicationStatus(ctx, p.ID, "active"))

	win := time.Date(2026, 10, 13, 15, 0, 0, 0, time.UTC)
	ev := store.GuardrailEvent{OrgID: org, PublicationID: p.ID, Rule: store.GuardrailMaxCost, WindowStart: win,
		ThresholdCents: 1500, ObservedCents: 2000, SpendCents: 6000, Conversations: 3, Reason: "gastou 60 e gerou 3", AutoPause: true}
	got, created, err := f.st.OpenGuardrailEvent(ctx, ev)
	must(t, err)
	if !created || got.ID == 0 || got.CompletedAt != nil || got.ObservedCents != 2000 || got.Conversations != 3 {
		t.Fatalf("primeiro: %+v created=%v", got, created)
	}
	same, created, err := f.st.OpenGuardrailEvent(ctx, ev)
	must(t, err)
	if created || same.ID != got.ID {
		t.Fatalf("mesma violação criou outra: %+v created=%v", same, created)
	}
	// outra janela (nova ativação) é outra violação
	ev2 := ev
	ev2.WindowStart = win.Add(48 * time.Hour)
	other, created, err := f.st.OpenGuardrailEvent(ctx, ev2)
	must(t, err)
	if !created || other.ID == got.ID {
		t.Fatalf("nova janela: %+v created=%v", other, created)
	}
	// outra regra na mesma janela também
	ev3 := ev
	ev3.Rule = store.GuardrailDailyCap
	if _, created, err = f.st.OpenGuardrailEvent(ctx, ev3); err != nil || !created {
		t.Fatalf("outra regra: created=%v err=%v", created, err)
	}

	open, err := f.st.OpenGuardrailEvents(ctx)
	must(t, err)
	if len(open) != 3 {
		t.Fatalf("abertos = %d", len(open))
	}

	var base int
	must(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = 'guardrail.notice'`).Scan(&base))
	notice := &store.JobSpec{Kind: "guardrail.notice", Payload: map[string]string{"text": "pausei"}}
	done, err := f.st.CompleteGuardrailEvent(ctx, got.ID, "paused", notice)
	must(t, err)
	again, err := f.st.CompleteGuardrailEvent(ctx, got.ID, "paused", notice)
	must(t, err)
	if !done || again {
		t.Fatalf("fechou done=%v again=%v", done, again)
	}
	var jobs int
	must(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = 'guardrail.notice'`).Scan(&jobs))
	if jobs != base+1 {
		t.Fatalf("aviso enfileirado %d vezes", jobs)
	}
	closed, _, _ := f.st.OpenGuardrailEvent(ctx, ev)
	if closed.CompletedAt == nil || closed.Action != "paused" {
		t.Fatalf("fechado: %+v", closed)
	}
	if open, _ = f.st.OpenGuardrailEvents(ctx); len(open) != 2 {
		t.Fatalf("abertos depois de fechar = %d", len(open))
	}
	list, err := f.st.GuardrailEvents(ctx, org, 10)
	must(t, err)
	if len(list) != 3 || list[0].ID < list[1].ID {
		t.Fatalf("lista = %+v", list)
	}
	// sem aviso (notice nil) fecha sem enfileirar
	if ok, err := f.st.CompleteGuardrailEvent(ctx, other.ID, "cancelled", nil); err != nil || !ok {
		t.Fatalf("sem aviso: %v %v", ok, err)
	}
	must(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = 'guardrail.notice'`).Scan(&jobs))
	if jobs != base+1 {
		t.Fatalf("fechar sem aviso enfileirou: %d", jobs)
	}
}

func TestDiagnosticClaimOncePerConnectionAndRelease(t *testing.T) {
	f, connID := seedPublishable(t)
	org := f.ident.OrgID
	first, err := f.st.ClaimDiagnostic(ctx, org, connID)
	must(t, err)
	second, err := f.st.ClaimDiagnostic(ctx, org, connID)
	must(t, err)
	if !first || second {
		t.Fatalf("first=%v second=%v", first, second)
	}
	must(t, f.st.SaveDiagnostic(ctx, connID, map[string]any{"spend_cents": 1234}))
	var spend int64
	must(t, f.pool.QueryRow(ctx, `SELECT (summary->>'spend_cents')::bigint FROM account_diagnostics WHERE connection_id = $1`, connID).Scan(&spend))
	if spend != 1234 {
		t.Fatalf("resumo = %d", spend)
	}
	must(t, f.st.ReleaseDiagnostic(ctx, connID))
	if again, _ := f.st.ClaimDiagnostic(ctx, org, connID); !again {
		t.Fatal("depois de liberar, deveria reivindicar de novo")
	}
}
