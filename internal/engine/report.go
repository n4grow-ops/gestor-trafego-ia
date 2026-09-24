package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const (
	JobDailyReport = "report.daily"
	reportHour     = 8 // horário de Brasília
)

// NextReportAt é o próximo 8 h de Brasília depois de now.
func NextReportAt(now time.Time) time.Time {
	t := now.In(brt)
	y, m, d := t.Date()
	at := time.Date(y, m, d, reportHour, 0, 0, 0, brt)
	if !at.After(t) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}

// EnsureDailyReport garante um relatório diário agendado (chamado quando o worker sobe).
func (e *Engine) EnsureDailyReport(ctx context.Context) error {
	if e.ReportPhone == "" {
		return nil
	}
	pending, err := e.Store.HasPendingKind(ctx, JobDailyReport)
	if err != nil || pending {
		return err
	}
	return e.enqueueReport(ctx)
}

func (e *Engine) enqueueReport(ctx context.Context) error {
	now := e.now()
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobDailyReport, Payload: map[string]string{}, Delay: NextReportAt(now).Sub(now)})
}

// dailyReport manda o resumo das últimas 24 h para o operador e agenda o próximo.
func (e *Engine) dailyReport(ctx context.Context) error {
	if e.ReportPhone == "" {
		return nil
	}
	s, err := e.Store.DailyStats(ctx, e.now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	text := fmt.Sprintf(msgDailyReport, s.NewContacts, s.Registered, s.PendingWithLink, s.Inbound,
		s.Proposals, s.Approved, s.Creatives, strings.Replace(fmt.Sprintf("%.2f", s.CostUSD), ".", ",", 1))
	if err := e.Store.Enqueue(ctx, sendJob(0, e.WA.ChatIDForPhone(e.ReportPhone), text)); err != nil {
		return err
	}
	return e.enqueueReport(ctx)
}
