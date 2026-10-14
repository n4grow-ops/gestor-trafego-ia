package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Regras das proteções escolhidas pelo cliente.
const (
	GuardrailDailyCap = "daily_cap" // teto de gasto no dia (por campanha)
	GuardrailMaxCost  = "max_cost"  // custo máximo por conversa
)

// GuardrailRule é o que o cliente escolheu. Sem limite preenchido, nada é ligado.
type GuardrailRule struct {
	OrgID               int64
	DailyCapCents       *int64
	MaxCostPerConvCents *int64
	AutoPause           bool // false = só avisa, não pausa
	OfferedAt           *time.Time
}

// Active diz se há alguma regra do cliente valendo.
func (r GuardrailRule) Active() bool { return r.DailyCapCents != nil || r.MaxCostPerConvCents != nil }

// GetGuardrail lê a regra da organização (ok=false: nunca ofereceu nem o cliente definiu).
func (s *Store) GetGuardrail(ctx context.Context, orgID int64) (GuardrailRule, bool, error) {
	var r GuardrailRule
	err := s.pool.QueryRow(ctx, `SELECT organization_id, daily_cap_cents, max_cost_per_conv_cents, auto_pause, offered_at
		FROM guardrail_rules WHERE organization_id = $1`, orgID).
		Scan(&r.OrgID, &r.DailyCapCents, &r.MaxCostPerConvCents, &r.AutoPause, &r.OfferedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return GuardrailRule{}, false, nil
	}
	return r, err == nil, err
}

// ActiveGuardrails lista as organizações com algum limite definido pelo cliente.
func (s *Store) ActiveGuardrails(ctx context.Context) (map[int64]GuardrailRule, error) {
	rows, err := s.pool.Query(ctx, `SELECT organization_id, daily_cap_cents, max_cost_per_conv_cents, auto_pause, offered_at
		FROM guardrail_rules WHERE daily_cap_cents IS NOT NULL OR max_cost_per_conv_cents IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]GuardrailRule{}
	for rows.Next() {
		var r GuardrailRule
		if err := rows.Scan(&r.OrgID, &r.DailyCapCents, &r.MaxCostPerConvCents, &r.AutoPause, &r.OfferedAt); err != nil {
			return nil, err
		}
		out[r.OrgID] = r
	}
	return out, rows.Err()
}

// SetGuardrailLimit grava um limite (rule = GuardrailDailyCap ou GuardrailMaxCost) sem mexer no outro.
func (s *Store) SetGuardrailLimit(ctx context.Context, orgID int64, rule string, cents int64) error {
	if cents <= 0 {
		return fmt.Errorf("store: limite inválido: %d", cents)
	}
	var col string
	switch rule {
	case GuardrailDailyCap:
		col = "daily_cap_cents"
	case GuardrailMaxCost:
		col = "max_cost_per_conv_cents"
	default:
		return fmt.Errorf("store: regra desconhecida: %q", rule)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO guardrail_rules (organization_id, `+col+`) VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET `+col+` = EXCLUDED.`+col+`, updated_at = now()`, orgID, cents)
	return err
}

// SetGuardrailAutoPause escolhe entre pausar sozinha e só avisar.
func (s *Store) SetGuardrailAutoPause(ctx context.Context, orgID int64, auto bool) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO guardrail_rules (organization_id, auto_pause) VALUES ($1, $2)
		ON CONFLICT (organization_id) DO UPDATE SET auto_pause = EXCLUDED.auto_pause, updated_at = now()`, orgID, auto)
	return err
}

// ClearGuardrail tira os dois limites (a oferta continua registrada: não é feita de novo).
func (s *Store) ClearGuardrail(ctx context.Context, orgID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE guardrail_rules SET daily_cap_cents = NULL, max_cost_per_conv_cents = NULL,
		auto_pause = true, updated_at = now() WHERE organization_id = $1`, orgID)
	return err
}

// MarkGuardrailOffered registra que a Mia ofereceu as proteções; devolve true só na primeira vez
// (a oferta é uma só, e só para quem ainda não definiu nada).
func (s *Store) MarkGuardrailOffered(ctx context.Context, orgID int64) (bool, error) {
	var first bool
	err := s.pool.QueryRow(ctx, `INSERT INTO guardrail_rules (organization_id, offered_at) VALUES ($1, now())
		ON CONFLICT (organization_id) DO UPDATE SET offered_at = now(), updated_at = now()
		WHERE guardrail_rules.offered_at IS NULL AND guardrail_rules.daily_cap_cents IS NULL
			AND guardrail_rules.max_cost_per_conv_cents IS NULL
		RETURNING true`, orgID).Scan(&first)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return first, err
}

// GuardrailEvent é uma violação de regra do cliente: o motivo, os números e o que foi feito.
type GuardrailEvent struct {
	ID             int64
	OrgID          int64
	PublicationID  int64
	Rule           string
	WindowStart    time.Time
	ThresholdCents int64
	ObservedCents  int64
	SpendCents     int64
	Conversations  int64
	Reason         string
	AutoPause      bool
	Action         string // paused | notified | cancelled | skipped; vazio = em andamento
	CreatedAt      time.Time
	CompletedAt    *time.Time
}

const guardrailEventCols = `id, organization_id, publication_id, rule, window_start, threshold_cents, observed_cents, spend_cents,
	conversations, reason, auto_pause, action, created_at, completed_at`

func scanGuardrailEvent(row pgx.Row) (GuardrailEvent, error) {
	var e GuardrailEvent
	err := row.Scan(&e.ID, &e.OrgID, &e.PublicationID, &e.Rule, &e.WindowStart, &e.ThresholdCents, &e.ObservedCents, &e.SpendCents,
		&e.Conversations, &e.Reason, &e.AutoPause, &e.Action, &e.CreatedAt, &e.CompletedAt)
	return e, err
}

// OpenGuardrailEvent registra a violação. Se a mesma (campanha, regra, janela) já foi registrada, devolve a
// que existe com created=false: quem chama continua dela, sem pausar nem avisar de novo.
func (s *Store) OpenGuardrailEvent(ctx context.Context, ev GuardrailEvent) (GuardrailEvent, bool, error) {
	got, err := scanGuardrailEvent(s.pool.QueryRow(ctx, `INSERT INTO guardrail_events
			(organization_id, publication_id, rule, window_start, threshold_cents, observed_cents, spend_cents, conversations, reason, auto_pause)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (publication_id, rule, window_start) DO NOTHING RETURNING `+guardrailEventCols,
		ev.OrgID, ev.PublicationID, ev.Rule, ev.WindowStart, ev.ThresholdCents, ev.ObservedCents, ev.SpendCents, ev.Conversations,
		truncRunes(ev.Reason, 1000), ev.AutoPause))
	if err == nil {
		return got, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return GuardrailEvent{}, false, err
	}
	got, err = scanGuardrailEvent(s.pool.QueryRow(ctx, `SELECT `+guardrailEventCols+` FROM guardrail_events
		WHERE publication_id = $1 AND rule = $2 AND window_start = $3`, ev.PublicationID, ev.Rule, ev.WindowStart))
	return got, false, err
}

func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// OpenGuardrailEvents lista os eventos ainda em andamento (a pausa ou o aviso não terminou, ex.: Meta fora do ar).
func (s *Store) OpenGuardrailEvents(ctx context.Context) ([]GuardrailEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+guardrailEventCols+` FROM guardrail_events WHERE completed_at IS NULL ORDER BY id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuardrailEvent
	for rows.Next() {
		e, err := scanGuardrailEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CompleteGuardrailEvent fecha o evento com o que foi feito e, na mesma transação, enfileira o aviso ao cliente
// (notice nil = sem aviso). Só o primeiro fechamento enfileira: false = já estava fechado.
func (s *Store) CompleteGuardrailEvent(ctx context.Context, id int64, action string, notice *JobSpec) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE guardrail_events SET action = $2, completed_at = now() WHERE id = $1 AND completed_at IS NULL`, id, action)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if notice != nil {
		if err := enqueue(ctx, tx, *notice); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

// GuardrailEvents lista os eventos da organização, do mais novo para o mais antigo.
func (s *Store) GuardrailEvents(ctx context.Context, orgID int64, limit int) ([]GuardrailEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+guardrailEventCols+` FROM guardrail_events
		WHERE organization_id = $1 ORDER BY id DESC LIMIT $2`, orgID, max(limit, 1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuardrailEvent
	for rows.Next() {
		e, err := scanGuardrailEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClaimDiagnostic reivindica o diagnóstico grátis da conexão: true só na primeira vez.
func (s *Store) ClaimDiagnostic(ctx context.Context, orgID, connID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO account_diagnostics (connection_id, organization_id) VALUES ($1, $2)
		ON CONFLICT (connection_id) DO NOTHING`, connID, orgID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SaveDiagnostic guarda o resumo entregue (os números, para o painel e para conferir depois).
func (s *Store) SaveDiagnostic(ctx context.Context, connID int64, summary any) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_, err = s.pool.Exec(ctx, `UPDATE account_diagnostics SET summary = $2 WHERE connection_id = $1`, connID, string(raw))
	return err
}

// ReleaseDiagnostic desfaz a reivindicação quando a leitura da Meta falhou: o job tenta de novo.
func (s *Store) ReleaseDiagnostic(ctx context.Context, connID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM account_diagnostics WHERE connection_id = $1`, connID)
	return err
}

// CountPendingKind conta os jobs pendentes do tipo (inclui o que está rodando agora).
func (s *Store) CountPendingKind(ctx context.Context, kind string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox_jobs WHERE kind = $1 AND status = 'pending'`, kind).Scan(&n)
	return n, err
}
