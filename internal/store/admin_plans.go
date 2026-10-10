package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrPlanExists: já há um plano com esse código.
	ErrPlanExists = errors.New("store: código de plano já existe")
	// ErrPlanInUse: o plano tem assinaturas; não dá para apagar nem trocar o código (desligue em vez disso).
	ErrPlanInUse = errors.New("store: plano com assinaturas")
)

// DefaultPlan é o plano que o cadastro usa quando ninguém escolhe outro: não pode sumir, ser
// desligado nem trocar de código.
const DefaultPlan = "free"

var planCodeRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,29}$`)

// AdminPlan é um plano como a aba Planos mostra e edita.
type AdminPlan struct {
	Code              string    `json:"code"`
	Name              string    `json:"name"`
	PriceCents        int       `json:"price_cents"`
	Currency          string    `json:"currency"`
	Interval          string    `json:"interval"` // month ou year
	DailyMessageLimit int       `json:"daily_message_limit"`
	MaxDailyBudgetBRL float64   `json:"max_daily_budget_brl"`
	MaxProjects       int       `json:"max_projects"`
	Available         bool      `json:"available"` // dá para assinar no cadastro
	Visible           bool      `json:"visible"`   // aparece no cadastro (indisponível = "em breve")
	SortOrder         int       `json:"sort_order"`
	UpdatedAt         time.Time `json:"updated_at"`
	Subscribers       int       `json:"subscribers"` // assinaturas em qualquer situação (inclusive canceladas)
	ActiveSubs        int       `json:"active_subs"` // ativas, em teste ou em atraso
}

// PlanInput é o que o painel manda para criar ou editar um plano.
type PlanInput struct {
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	PriceCents        int     `json:"price_cents"`
	Interval          string  `json:"interval"`
	DailyMessageLimit int     `json:"daily_message_limit"`
	MaxDailyBudgetBRL float64 `json:"max_daily_budget_brl"`
	MaxProjects       int     `json:"max_projects"`
	Available         bool    `json:"available"`
	Visible           bool    `json:"visible"`
	SortOrder         int     `json:"sort_order"`
}

// Normalize limpa espaços e caixa e confere os limites. Os tetos são de sanidade: evitam erro de
// digitação (um zero a mais), não regras de negócio.
func (in PlanInput) Normalize() (PlanInput, error) {
	in.Code = strings.ToLower(strings.TrimSpace(in.Code))
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	in.MaxDailyBudgetBRL = math.Round(in.MaxDailyBudgetBRL*100) / 100
	bad := func(what string) (PlanInput, error) { return in, fmt.Errorf("%w: %s", ErrInvalidInput, what) }
	switch {
	case !planCodeRe.MatchString(in.Code):
		return bad("código: de 2 a 30 caracteres, só letras minúsculas sem acento, números e hífen, começando por letra")
	case utf8.RuneCountInString(in.Name) < 2 || utf8.RuneCountInString(in.Name) > 40:
		return bad("nome: de 2 a 40 caracteres")
	case in.PriceCents < 0 || in.PriceCents > 100_000_00:
		return bad("preço: de R$ 0 a R$ 100.000")
	case in.Interval != "month" && in.Interval != "year":
		return bad("cobrança: mensal ou anual")
	case in.DailyMessageLimit < 1 || in.DailyMessageLimit > 100_000:
		return bad("mensagens por dia: de 1 a 100.000")
	case math.IsNaN(in.MaxDailyBudgetBRL) || in.MaxDailyBudgetBRL < 1 || in.MaxDailyBudgetBRL > 1_000_000:
		return bad("investimento por dia: de R$ 1 a R$ 1.000.000")
	case in.MaxProjects < 1 || in.MaxProjects > 1000:
		return bad("projetos: de 1 a 1.000")
	case in.SortOrder < 0 || in.SortOrder > 999:
		return bad("ordem: de 0 a 999")
	case in.Available && !in.Visible:
		return bad("plano disponível precisa aparecer no cadastro")
	case in.Code == DefaultPlan && !in.Available:
		return bad("o plano free é o padrão do cadastro e precisa ficar disponível")
	}
	return in, nil
}

const adminPlanCols = `p.code, p.name, p.price_cents, p.currency, p.billing_interval, p.daily_message_limit,
	p.max_daily_budget_brl::float8, p.max_projects, p.available, p.visible, p.sort_order, p.updated_at,
	(SELECT count(*) FROM subscriptions s WHERE s.plan_code = p.code),
	(SELECT count(*) FROM subscriptions s WHERE s.plan_code = p.code AND s.status IN ('active', 'trialing', 'past_due'))`

func scanAdminPlan(row pgx.Row) (AdminPlan, error) {
	var p AdminPlan
	err := row.Scan(&p.Code, &p.Name, &p.PriceCents, &p.Currency, &p.Interval, &p.DailyMessageLimit, &p.MaxDailyBudgetBRL,
		&p.MaxProjects, &p.Available, &p.Visible, &p.SortOrder, &p.UpdatedAt, &p.Subscribers, &p.ActiveSubs)
	return p, err
}

// AdminPlans lista todos os planos (inclusive ocultos) com quantas assinaturas cada um tem.
func (s *Store) AdminPlans(ctx context.Context) ([]AdminPlan, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+adminPlanCols+` FROM plans p ORDER BY p.sort_order, p.code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminPlan, error) { return scanAdminPlan(r) })
}

func adminPlan(ctx context.Context, q querier, code string, lock bool) (AdminPlan, error) {
	sql := `SELECT ` + adminPlanCols + ` FROM plans p WHERE p.code = $1`
	if lock {
		sql += ` FOR UPDATE`
	}
	p, err := scanAdminPlan(q.QueryRow(ctx, sql, code))
	return p, notFound(err)
}

// AdminPlan devolve um plano pelo código.
func (s *Store) AdminPlan(ctx context.Context, code string) (AdminPlan, error) {
	return adminPlan(ctx, s.pool, code, false)
}

// planAudit é o plano como fica no registro de ações (sem as contagens, que não são editáveis).
func planAudit(p AdminPlan) map[string]any {
	return map[string]any{"code": p.Code, "name": p.Name, "price_cents": p.PriceCents, "interval": p.Interval,
		"daily_message_limit": p.DailyMessageLimit, "max_daily_budget_brl": p.MaxDailyBudgetBRL, "max_projects": p.MaxProjects,
		"available": p.Available, "visible": p.Visible, "sort_order": p.SortOrder}
}

func planChanges(before, after map[string]any) []string {
	var out []string
	for _, k := range []string{"code", "name", "price_cents", "interval", "daily_message_limit", "max_daily_budget_brl",
		"max_projects", "available", "visible", "sort_order"} {
		if before[k] != after[k] {
			out = append(out, k)
		}
	}
	return out
}

func budgetParam(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// CreatePlan cria um plano novo. O registro de ações vai na mesma transação.
func (s *Store) CreatePlan(ctx context.Context, in PlanInput, audit AdminAction) (AdminPlan, error) {
	in, err := in.Normalize()
	if err != nil {
		return AdminPlan{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminPlan{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO plans (code, name, price_cents, billing_interval, daily_message_limit,
			max_daily_budget_brl, max_projects, available, visible, sort_order, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7, $8, $9, $10, now()) ON CONFLICT (code) DO NOTHING`,
		in.Code, in.Name, in.PriceCents, in.Interval, in.DailyMessageLimit, budgetParam(in.MaxDailyBudgetBRL),
		in.MaxProjects, in.Available, in.Visible, in.SortOrder)
	if err != nil {
		return AdminPlan{}, err
	}
	if tag.RowsAffected() == 0 {
		return AdminPlan{}, ErrPlanExists
	}
	p, err := adminPlan(ctx, tx, in.Code, false)
	if err != nil {
		return AdminPlan{}, err
	}
	audit.TargetType, audit.TargetID, audit.TargetLabel = "plan", p.Code, p.Name
	audit.Data = map[string]any{"after": planAudit(p)}
	if err := logAdminAction(ctx, tx, audit); err != nil {
		return AdminPlan{}, err
	}
	return p, tx.Commit(ctx)
}

// UpdatePlan grava a edição do plano code. base é o updated_at que o painel leu: se outra pessoa
// salvou no meio, devolve ErrConflict (zero = não confere). O código só muda se o plano não tem
// nenhuma assinatura. Os limites e o preço novos valem dali em diante: assinaturas e pagamentos já
// existentes não são reescritos.
func (s *Store) UpdatePlan(ctx context.Context, code string, in PlanInput, base time.Time, audit AdminAction) (AdminPlan, error) {
	in, err := in.Normalize()
	if err != nil {
		return AdminPlan{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AdminPlan{}, err
	}
	defer tx.Rollback(ctx)
	cur, err := adminPlan(ctx, tx, code, true)
	if err != nil {
		return AdminPlan{}, err
	}
	if !base.IsZero() && !cur.UpdatedAt.Equal(base) {
		return AdminPlan{}, ErrConflict
	}
	if in.Code != cur.Code {
		switch {
		case cur.Code == DefaultPlan:
			return AdminPlan{}, fmt.Errorf("%w: o código do plano free não muda", ErrInvalidInput)
		case cur.Subscribers > 0:
			return AdminPlan{}, ErrPlanInUse
		}
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM plans WHERE code = $1)`, in.Code).Scan(&taken); err != nil {
			return AdminPlan{}, err
		}
		if taken {
			return AdminPlan{}, ErrPlanExists
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE plans SET code = $2, name = $3, price_cents = $4, billing_interval = $5,
			daily_message_limit = $6, max_daily_budget_brl = $7::numeric, max_projects = $8, available = $9, visible = $10,
			sort_order = $11, updated_at = now()
		WHERE code = $1`,
		cur.Code, in.Code, in.Name, in.PriceCents, in.Interval, in.DailyMessageLimit, budgetParam(in.MaxDailyBudgetBRL),
		in.MaxProjects, in.Available, in.Visible, in.SortOrder); err != nil {
		return AdminPlan{}, err
	}
	p, err := adminPlan(ctx, tx, in.Code, false)
	if err != nil {
		return AdminPlan{}, err
	}
	before, after := planAudit(cur), planAudit(p)
	changed := planChanges(before, after)
	if len(changed) == 0 {
		return cur, nil // nada mudou: não grava nem registra
	}
	audit.TargetType, audit.TargetID, audit.TargetLabel = "plan", p.Code, p.Name
	audit.Data = map[string]any{"before": before, "after": after, "changed": changed, "subscribers": cur.Subscribers}
	if err := logAdminAction(ctx, tx, audit); err != nil {
		return AdminPlan{}, err
	}
	return p, tx.Commit(ctx)
}

// DeletePlan apaga um plano que nunca teve assinatura. Com assinatura (mesmo cancelada), o caminho
// é ocultar o plano: o histórico de quem assinou continua apontando para ele.
func (s *Store) DeletePlan(ctx context.Context, code string, audit AdminAction) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cur, err := adminPlan(ctx, tx, code, true)
	if err != nil {
		return err
	}
	switch {
	case cur.Code == DefaultPlan:
		return fmt.Errorf("%w: o plano free é o padrão do cadastro e não pode ser apagado", ErrInvalidInput)
	case cur.Subscribers > 0:
		return ErrPlanInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM plans WHERE code = $1`, cur.Code); err != nil {
		return err
	}
	audit.TargetType, audit.TargetID, audit.TargetLabel = "plan", cur.Code, cur.Name
	audit.Data = map[string]any{"before": planAudit(cur)}
	if err := logAdminAction(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
