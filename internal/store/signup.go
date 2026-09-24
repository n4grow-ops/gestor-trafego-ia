package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
)

var (
	// ErrLinkInvalid: link de cadastro inexistente, expirado ou já usado.
	ErrLinkInvalid = errors.New("store: link de cadastro inválido ou expirado")
	// ErrAlreadyRegistered: a organização já tem cadastro completo.
	ErrAlreadyRegistered = errors.New("store: cadastro já concluído")
	// ErrPlanUnavailable: plano inexistente ou ainda não disponível.
	ErrPlanUnavailable = errors.New("store: plano indisponível")
	// ErrNotOwner: só o dono da conta conclui o cadastro da organização.
	ErrNotOwner = errors.New("store: cadastro só pode ser feito pelo responsável")
)

// SetRegistrationStatus muda o status do cadastro (pending ou complete).
func (s *Store) SetRegistrationStatus(ctx context.Context, orgID int64, status string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET registration_status = $2,
		registered_at = CASE WHEN $2 = 'complete' THEN coalesce(registered_at, now()) ELSE registered_at END WHERE id = $1`, orgID, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CreateSignupLink guarda o hash de um link de cadastro para a identidade.
func (s *Store) CreateSignupLink(ctx context.Context, identityID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO signup_links (identity_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		identityID, tokenHash, expiresAt)
	return err
}

// SignupLinkSentWithin diz se a identidade recebeu link de cadastro dentro do intervalo (relógio do banco).
func (s *Store) SignupLinkSentWithin(ctx context.Context, identityID int64, within time.Duration) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM signup_links
		WHERE identity_id = $1 AND created_at > now() - $2 * interval '1 millisecond')`, identityID, within.Milliseconds()).Scan(&ok)
	return ok, err
}

// SignupPrefill é o que a página mostra preenchido para o link.
type SignupPrefill struct {
	Phone        string `json:"phone"`
	FullName     string `json:"full_name"`
	BusinessName string `json:"business_name"`
	City         string `json:"city"`
	UF           string `json:"uf"`
}

// SignupPrefill confere o link e devolve os dados que a IA já coletou.
func (s *Store) SignupPrefill(ctx context.Context, tokenHash string) (SignupPrefill, error) {
	var p SignupPrefill
	var status string
	var usable bool
	err := s.pool.QueryRow(ctx, `SELECT o.registration_status, l.used_at IS NULL AND l.expires_at > now(),
			wi.phone_e164, coalesce(m.full_name, ''),
			coalesce(o.trade_name, o.profile->>'business_name', ''), coalesce(o.city, o.profile->>'city', ''), coalesce(o.uf, '')
		FROM signup_links l
		JOIN whatsapp_identities wi ON wi.id = l.identity_id
		JOIN members m ON m.id = wi.member_id
		JOIN organizations o ON o.id = m.organization_id
		WHERE l.token_hash = $1`, tokenHash).Scan(&status, &usable, &p.Phone, &p.FullName, &p.BusinessName, &p.City, &p.UF)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignupPrefill{}, ErrLinkInvalid
	}
	if err != nil {
		return SignupPrefill{}, err
	}
	if status == "complete" {
		return SignupPrefill{}, ErrAlreadyRegistered
	}
	if !usable {
		return SignupPrefill{}, ErrLinkInvalid
	}
	return p, nil
}

// Plan é um plano de assinatura.
type Plan struct {
	Code              string  `json:"code"`
	Name              string  `json:"name"`
	PriceCents        int     `json:"price_cents"`
	Currency          string  `json:"currency"`
	DailyMessageLimit int     `json:"daily_message_limit"`
	MaxDailyBudgetBRL float64 `json:"max_daily_budget_brl"`
	MaxProjects       int     `json:"max_projects"`
	Available         bool    `json:"available"`
}

func (s *Store) Plans(ctx context.Context) ([]Plan, error) {
	rows, err := s.pool.Query(ctx, `SELECT code, name, price_cents, currency, daily_message_limit,
		max_daily_budget_brl::float8, max_projects, available FROM plans ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Plan, error) {
		var p Plan
		err := r.Scan(&p.Code, &p.Name, &p.PriceCents, &p.Currency, &p.DailyMessageLimit, &p.MaxDailyBudgetBRL, &p.MaxProjects, &p.Available)
		return p, err
	})
}

// Registration é a conclusão do cadastro. Form já normalizado; e-mail e id vêm do Supabase Auth.
type Registration struct {
	TokenHash       string
	AuthUserID      string
	Email           string
	EmailVerifiedAt time.Time
	Form            signup.Form
	PlanCode        string
	TermsVersion    string
	IP              string
	UserAgent       string
	// Welcome monta a mensagem de boas-vindas no WhatsApp, enfileirada na mesma transação.
	Welcome func(convID int64, chatID, fullName, email string) JobSpec
}

type RegistrationResult struct {
	OrgID      int64
	MemberID   int64
	IdentityID int64
	FullName   string
}

// CompleteRegistration grava o cadastro numa transação: pessoa, negócio, aceites, assinatura com os
// limites do plano, link usado, conversa pronta e boas-vindas na fila.
func (s *Store) CompleteRegistration(ctx context.Context, r Registration) (RegistrationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegistrationResult{}, err
	}
	defer tx.Rollback(ctx)

	var res RegistrationResult
	var linkID int64
	var usable bool
	var status, role string
	err = tx.QueryRow(ctx, `SELECT l.id, l.identity_id, l.used_at IS NULL AND l.expires_at > now(), m.id, m.role, o.id, o.registration_status
		FROM signup_links l
		JOIN whatsapp_identities wi ON wi.id = l.identity_id
		JOIN members m ON m.id = wi.member_id
		JOIN organizations o ON o.id = m.organization_id
		WHERE l.token_hash = $1
		FOR UPDATE OF l, o`, r.TokenHash).Scan(&linkID, &res.IdentityID, &usable, &res.MemberID, &role, &res.OrgID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return RegistrationResult{}, ErrLinkInvalid
	}
	if err != nil {
		return RegistrationResult{}, err
	}
	if status == "complete" {
		return RegistrationResult{}, ErrAlreadyRegistered
	}
	if !usable {
		return RegistrationResult{}, ErrLinkInvalid
	}
	if role != "owner" {
		return RegistrationResult{}, ErrNotOwner
	}

	var plan Plan
	err = tx.QueryRow(ctx, `SELECT daily_message_limit, max_daily_budget_brl::float8 FROM plans WHERE code = $1 AND available`,
		r.PlanCode).Scan(&plan.DailyMessageLimit, &plan.MaxDailyBudgetBRL)
	if errors.Is(err, pgx.ErrNoRows) {
		return RegistrationResult{}, ErrPlanUnavailable
	}
	if err != nil {
		return RegistrationResult{}, err
	}

	f := r.Form
	res.FullName = f.FullName
	if _, err := tx.Exec(ctx, `UPDATE members SET name = $2, full_name = $2, email = $3, email_verified_at = $4,
		auth_user_id = $5, cpf = nullif($6, '') WHERE id = $1`,
		res.MemberID, f.FullName, r.Email, r.EmailVerifiedAt, r.AuthUserID, f.CPF); err != nil {
		return RegistrationResult{}, fmt.Errorf("store: membro: %w", err)
	}
	approver := "Eu mesmo"
	if f.Approver == "other" {
		approver = f.ApproverName
	}
	if _, err := tx.Exec(ctx, `UPDATE organizations SET
			name = $2, trade_name = $2, legal_name = nullif($3, ''), cnpj = nullif($4, ''), segment = $5,
			niche = CASE WHEN niche = '' THEN $5 ELSE niche END, city = $6, uf = $7, website = nullif($8, ''), instagram = nullif($9, ''),
			ads_objective = $10, monthly_budget_range = $11, ads_experience = $12, has_meta_ad_account = $13,
			approver = $14, referral_source = nullif($15, ''),
			profile = profile || jsonb_build_object('business_name', $2::text, 'city', $6::text),
			-- Plano Free não rebaixa quem já tinha limite maior (clientes trazidos pelo admin).
			max_daily_budget_brl = greatest(max_daily_budget_brl, $16), daily_message_limit = greatest(daily_message_limit, $17),
			registration_status = 'complete', registered_at = now()
		WHERE id = $1`,
		res.OrgID, f.BusinessName, f.LegalName, f.CNPJ, f.SegmentLabel(), f.City, f.UF, f.Website, f.Instagram,
		f.Objectives, f.MonthlyBudget, f.AdsExperience, f.MetaAdAccount, approver, f.ReferralSource,
		plan.MaxDailyBudgetBRL, plan.DailyMessageLimit); err != nil {
		return RegistrationResult{}, fmt.Errorf("store: organização: %w", err)
	}
	for _, kind := range []string{"terms", "privacy"} {
		if _, err := tx.Exec(ctx, `INSERT INTO consents (member_id, kind, version, ip, user_agent) VALUES ($1, $2, $3, nullif($4, ''), nullif($5, ''))`,
			res.MemberID, kind, r.TermsVersion, r.IP, r.UserAgent); err != nil {
			return RegistrationResult{}, fmt.Errorf("store: aceite: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, $2, 'active')
		ON CONFLICT (organization_id) DO UPDATE SET plan_code = excluded.plan_code, status = 'active', updated_at = now()`,
		res.OrgID, r.PlanCode); err != nil {
		return RegistrationResult{}, fmt.Errorf("store: assinatura: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE signup_links SET used_at = now() WHERE id = $1`, linkID); err != nil {
		return RegistrationResult{}, err
	}
	// Conversa: vai conhecer o negócio (onboarding) antes de falar de anúncio; conversa em andamento fica como está.
	var convID int64
	var chatID string
	err = tx.QueryRow(ctx, `UPDATE conversations SET state = CASE WHEN state IN ('idle', 'onboarding') THEN 'onboarding' ELSE state END, updated_at = now()
		WHERE identity_id = $1 RETURNING id, chat_id`, res.IdentityID).Scan(&convID, &chatID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RegistrationResult{}, err
	}
	if err == nil && chatID != "" && r.Welcome != nil {
		if err := enqueue(ctx, tx, r.Welcome(convID, chatID, f.FullName, r.Email)); err != nil {
			return RegistrationResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return RegistrationResult{}, err
	}
	return res, nil
}
