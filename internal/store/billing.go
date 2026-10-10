package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// ErrNoSubscription: o cliente não tem assinatura para receber o pagamento.
var ErrNoSubscription = errors.New("store: cliente sem assinatura")

// PlanBreakdown são as assinaturas de um plano por situação.
type PlanBreakdown struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	PriceCents int    `json:"price_cents"`
	Interval   string `json:"interval"`
	Active     int    `json:"active"`
	Trialing   int    `json:"trialing"`
	PastDue    int    `json:"past_due"`
	Canceled   int    `json:"canceled"`
	MRRCents   int64  `json:"mrr_cents"`
}

// MonthPoint é um mês do gráfico: recebido e custo de IA, em centavos de real.
type MonthPoint struct {
	Month         string `json:"month"` // AAAA-MM
	ReceivedCents int64  `json:"received_cents"`
	AICostCents   int64  `json:"ai_cost_cents"`
}

// PaymentRow é um pagamento na lista do faturamento.
type PaymentRow struct {
	ID          int64      `json:"id"`
	OrgID       int64      `json:"org_id"`
	OrgName     string     `json:"org_name"`
	AmountCents int64      `json:"amount_cents"`
	Method      string     `json:"method"`
	Status      string     `json:"status"`
	Provider    string     `json:"provider"`
	PaidAt      *time.Time `json:"paid_at"`
	CreatedAt   time.Time  `json:"created_at"`
	Note        string     `json:"note"`
	RecordedBy  string     `json:"recorded_by"`
}

// BillingReport é o faturamento de um mês. Valores em centavos de real; custo de IA convertido
// pela cotação informada (USD_BRL).
type BillingReport struct {
	Month           string          `json:"month"`
	MRRCents        int64           `json:"mrr_cents"`   // assinaturas ativas e em atraso, mensalizadas
	PayingSubs      int             `json:"paying_subs"` // ativas e em atraso com preço > 0
	ARPACents       int64           `json:"arpa_cents"`  // MRR / pagantes
	Plans           []PlanBreakdown `json:"plans"`
	NewSubs         int             `json:"new_subs"`
	NewPaying       int             `json:"new_paying"`
	Canceled        int             `json:"canceled"` // canceladas no mês (pela última atualização)
	ReceivedCents   int64           `json:"received_cents"`
	PaidCount       int             `json:"paid_count"`
	TicketCents     int64           `json:"ticket_cents"`  // recebido / pagamentos
	PendingCents    int64           `json:"pending_cents"` // tudo em aberto, de qualquer mês
	PendingCount    int             `json:"pending_count"`
	FailedPayments  int             `json:"failed_payments"` // no mês
	PastDueSubs     int             `json:"past_due_subs"`
	PastDueMRRCents int64           `json:"past_due_mrr_cents"`
	DelinquencyRate float64         `json:"delinquency_rate"` // MRR em atraso / MRR
	ForecastCents   int64           `json:"forecast_cents"`
	AICostUSD       float64         `json:"ai_cost_usd"`
	AICostCents     int64           `json:"ai_cost_cents"`
	MarginCents     int64           `json:"margin_cents"` // recebido - custo de IA
	MarginPct       float64         `json:"margin_pct"`
	USDBRL          float64         `json:"usd_brl"`
	Series          []MonthPoint    `json:"series"`
	Recent          []PaymentRow    `json:"recent"`
}

// mensalizado: plano anual conta 1/12 por mês.
const monthlyPrice = `(CASE WHEN p.billing_interval = 'year' THEN p.price_cents / 12.0 ELSE p.price_cents END)`

// Billing calcula o faturamento do mês que começa em month (início do mês no fuso de Brasília).
// Previsão do mês = recebido + pendente criado no mês + assinaturas pagas (ativas ou em atraso)
// sem nenhuma cobrança no mês.
func (s *Store) Billing(ctx context.Context, month time.Time, usdBRL float64) (BillingReport, error) {
	from := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, month.Location())
	to := from.AddDate(0, 1, 0)
	r := BillingReport{Month: from.Format("2006-01"), USDBRL: usdBRL}

	rows, err := s.pool.Query(ctx, `SELECT p.code, p.name, p.price_cents, p.billing_interval,
			count(s.id) FILTER (WHERE s.status = 'active'), count(s.id) FILTER (WHERE s.status = 'trialing'),
			count(s.id) FILTER (WHERE s.status = 'past_due'), count(s.id) FILTER (WHERE s.status = 'canceled'),
			coalesce(round(sum(`+monthlyPrice+`) FILTER (WHERE s.status IN ('active', 'past_due'))), 0)::bigint
		FROM plans p LEFT JOIN subscriptions s ON s.plan_code = p.code
		GROUP BY p.code ORDER BY p.sort_order, p.code`)
	if err != nil {
		return r, err
	}
	r.Plans, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (PlanBreakdown, error) {
		var p PlanBreakdown
		err := row.Scan(&p.Code, &p.Name, &p.PriceCents, &p.Interval, &p.Active, &p.Trialing, &p.PastDue, &p.Canceled, &p.MRRCents)
		return p, err
	})
	if err != nil {
		return r, err
	}

	err = s.pool.QueryRow(ctx, `SELECT
		coalesce(round(sum(`+monthlyPrice+`) FILTER (WHERE s.status IN ('active', 'past_due'))), 0)::bigint,
		count(*) FILTER (WHERE s.status IN ('active', 'past_due') AND p.price_cents > 0),
		count(*) FILTER (WHERE s.started_at >= $1 AND s.started_at < $2),
		count(*) FILTER (WHERE s.started_at >= $1 AND s.started_at < $2 AND p.price_cents > 0),
		count(*) FILTER (WHERE s.status = 'canceled' AND s.updated_at >= $1 AND s.updated_at < $2),
		count(*) FILTER (WHERE s.status = 'past_due' AND p.price_cents > 0),
		coalesce(round(sum(`+monthlyPrice+`) FILTER (WHERE s.status = 'past_due')), 0)::bigint,
		-- assinaturas pagas sem cobrança (paga ou pendente) no mês entram na previsão
		coalesce(round(sum(`+monthlyPrice+`) FILTER (WHERE s.status IN ('active', 'past_due') AND p.price_cents > 0
			AND NOT EXISTS (SELECT 1 FROM payments x WHERE x.subscription_id = s.id AND x.status IN ('paid', 'pending')
				AND coalesce(x.paid_at, x.created_at) >= $1 AND coalesce(x.paid_at, x.created_at) < $2))), 0)::bigint
		FROM subscriptions s JOIN plans p ON p.code = s.plan_code`, from, to).
		Scan(&r.MRRCents, &r.PayingSubs, &r.NewSubs, &r.NewPaying, &r.Canceled, &r.PastDueSubs, &r.PastDueMRRCents, &r.ForecastCents)
	if err != nil {
		return r, err
	}

	var pendingInMonth int64
	err = s.pool.QueryRow(ctx, `SELECT
		coalesce(sum(amount_cents) FILTER (WHERE status = 'paid' AND paid_at >= $1 AND paid_at < $2), 0)::bigint,
		count(*) FILTER (WHERE status = 'paid' AND paid_at >= $1 AND paid_at < $2),
		coalesce(sum(amount_cents) FILTER (WHERE status = 'pending'), 0)::bigint,
		count(*) FILTER (WHERE status = 'pending'),
		coalesce(sum(amount_cents) FILTER (WHERE status = 'pending' AND created_at >= $1 AND created_at < $2), 0)::bigint,
		count(*) FILTER (WHERE status = 'failed' AND created_at >= $1 AND created_at < $2)
		FROM payments`, from, to).
		Scan(&r.ReceivedCents, &r.PaidCount, &r.PendingCents, &r.PendingCount, &pendingInMonth, &r.FailedPayments)
	if err != nil {
		return r, err
	}
	r.ForecastCents += r.ReceivedCents + pendingInMonth

	if err := s.pool.QueryRow(ctx, `SELECT coalesce(sum(cost_usd), 0)::float8 FROM llm_usage WHERE created_at >= $1 AND created_at < $2`,
		from, to).Scan(&r.AICostUSD); err != nil {
		return r, err
	}
	r.AICostCents = usdToCents(r.AICostUSD, usdBRL)
	r.MarginCents = r.ReceivedCents - r.AICostCents
	if r.ReceivedCents > 0 {
		r.MarginPct = float64(r.MarginCents) / float64(r.ReceivedCents)
		r.TicketCents = r.ReceivedCents / int64(r.PaidCount)
	}
	if r.PayingSubs > 0 {
		r.ARPACents = r.MRRCents / int64(r.PayingSubs)
	}
	if r.MRRCents > 0 {
		r.DelinquencyRate = float64(r.PastDueMRRCents) / float64(r.MRRCents)
	}

	// Seis meses até o mês escolhido, inclusive.
	for i := 5; i >= 0; i-- {
		mFrom := from.AddDate(0, -i, 0)
		mTo := mFrom.AddDate(0, 1, 0)
		var received int64
		var usd float64
		err := s.pool.QueryRow(ctx, `SELECT
			(SELECT coalesce(sum(amount_cents), 0)::bigint FROM payments WHERE status = 'paid' AND paid_at >= $1 AND paid_at < $2),
			(SELECT coalesce(sum(cost_usd), 0)::float8 FROM llm_usage WHERE created_at >= $1 AND created_at < $2)`, mFrom, mTo).Scan(&received, &usd)
		if err != nil {
			return r, err
		}
		r.Series = append(r.Series, MonthPoint{Month: mFrom.Format("2006-01"), ReceivedCents: received, AICostCents: usdToCents(usd, usdBRL)})
	}

	rows, err = s.pool.Query(ctx, `SELECT x.id, o.id, coalesce(nullif(o.trade_name, ''), o.name), x.amount_cents, x.method, x.status, x.provider,
			x.paid_at, x.created_at, coalesce(x.note, ''), coalesce(x.recorded_by, '')
		FROM payments x JOIN subscriptions s ON s.id = x.subscription_id JOIN organizations o ON o.id = s.organization_id
		ORDER BY coalesce(x.paid_at, x.created_at) DESC, x.id DESC LIMIT 30`)
	if err != nil {
		return r, err
	}
	r.Recent, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (PaymentRow, error) {
		var p PaymentRow
		err := row.Scan(&p.ID, &p.OrgID, &p.OrgName, &p.AmountCents, &p.Method, &p.Status, &p.Provider, &p.PaidAt, &p.CreatedAt, &p.Note, &p.RecordedBy)
		return p, err
	})
	return r, err
}

func usdToCents(usd, rate float64) int64 { return int64(math.Round(usd * rate * 100)) }

// ManualPayment é o pagamento registrado à mão pela equipe (pix, boleto ou cartão fora do sistema).
type ManualPayment struct {
	OrgID       int64
	AmountCents int64
	Method      string // pix, boleto ou card
	Status      string // paid, pending, failed ou refunded
	Date        time.Time
	Note        string
	RecordedBy  string
}

// RecordManualPayment grava o pagamento na assinatura do cliente (provider = manual).
func (s *Store) RecordManualPayment(ctx context.Context, p ManualPayment) (int64, error) {
	switch {
	case p.AmountCents <= 0 || p.AmountCents > 100_000_00:
		return 0, fmt.Errorf("%w: valor", ErrInvalidInput)
	case p.Method != "pix" && p.Method != "boleto" && p.Method != "card":
		return 0, fmt.Errorf("%w: método", ErrInvalidInput)
	case p.Status != "paid" && p.Status != "pending" && p.Status != "failed" && p.Status != "refunded":
		return 0, fmt.Errorf("%w: situação", ErrInvalidInput)
	case p.Date.IsZero():
		return 0, fmt.Errorf("%w: data", ErrInvalidInput)
	case utf8.RuneCountInString(p.Note) > 200:
		return 0, fmt.Errorf("%w: observação", ErrInvalidInput)
	}
	var subID int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM subscriptions WHERE organization_id = $1`, p.OrgID).Scan(&subID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNoSubscription
	}
	if err != nil {
		return 0, err
	}
	var paidAt *time.Time
	if p.Status == "paid" {
		paidAt = &p.Date
	}
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO payments (subscription_id, amount_cents, method, status, provider, paid_at, created_at, note, recorded_by)
		VALUES ($1, $2, $3, $4, 'manual', $5, $6, nullif($7, ''), nullif($8, '')) RETURNING id`,
		subID, p.AmountCents, p.Method, p.Status, paidAt, p.Date, strings.TrimSpace(p.Note), p.RecordedBy).Scan(&id)
	return id, err
}
