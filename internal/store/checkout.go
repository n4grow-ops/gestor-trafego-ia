package store

import (
	"context"
	"fmt"
	"time"
)

// CheckoutLink é o link /pagar/{token} de uma cobrança do Asaas. O token cru não fica no banco.
type CheckoutLink struct {
	ID             int64
	OrgID          int64
	SubID          int64
	AsaasSub       string // sub_...
	AsaasPayment   string // pay_...
	PlanName       string
	AmountCents    int64
	DueDate        time.Time
	ExpiresAt      time.Time
	FailedAttempts int
	LockedUntil    *time.Time
	Method         string // pix ou card: aba que a página abre primeiro
	Paid           bool   // pago pelo cartão aqui ou confirmado pelo webhook
}

// Usable diz se a página ainda pode cobrar: dentro da validade e sem pagamento.
func (c CheckoutLink) Usable(now time.Time) bool {
	return !c.Paid && now.Before(c.ExpiresAt)
}

// Locked diz se o cartão está bloqueado neste link (muitas recusas seguidas).
func (c CheckoutLink) Locked(now time.Time) bool {
	return c.LockedUntil != nil && now.Before(*c.LockedUntil)
}

// NewCheckoutLink são os dados de um link novo. TokenHash é o SHA-256 em hex do token.
type NewCheckoutLink struct {
	OrgID        int64
	TokenHash    string
	AsaasSub     string
	AsaasPayment string
	AmountCents  int64
	DueDate      time.Time
	ExpiresAt    time.Time
	Method       string // pix ou card ("" = pix)
}

// CreateCheckoutLink grava o link da cobrança, amarrado à assinatura da organização. O plano mostrado é o
// que está sendo assinado (pending_plan) ou o atual (renovação).
func (s *Store) CreateCheckoutLink(ctx context.Context, l NewCheckoutLink) error {
	if l.AmountCents <= 0 || l.AsaasPayment == "" || l.AsaasSub == "" {
		return fmt.Errorf("%w: link de pagamento incompleto", ErrInvalidInput)
	}
	if l.Method != "card" {
		l.Method = "pix"
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO checkout_links (token_hash, organization_id, subscription_id, asaas_subscription,
			asaas_payment, plan_name, amount_cents, due_date, expires_at, method)
		SELECT $2, s.organization_id, s.id, $3, $4, p.name, $5, $6, $7, $8
		FROM subscriptions s JOIN plans p ON p.code = coalesce(s.pending_plan, s.plan_code)
		WHERE s.organization_id = $1`,
		l.OrgID, l.TokenHash, l.AsaasSub, l.AsaasPayment, l.AmountCents, l.DueDate.Format("2006-01-02"), l.ExpiresAt, l.Method)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNoSubscription
	}
	return err
}

// CheckoutByHash lê o link pelo hash do token. Pago = cartão aprovado aqui ou a cobrança paga no webhook.
func (s *Store) CheckoutByHash(ctx context.Context, tokenHash string) (CheckoutLink, error) {
	var c CheckoutLink
	err := s.pool.QueryRow(ctx, `SELECT l.id, l.organization_id, l.subscription_id, l.asaas_subscription, l.asaas_payment,
			l.plan_name, l.amount_cents, l.due_date, l.expires_at, l.failed_attempts, l.locked_until, l.method,
			l.card_paid_at IS NOT NULL OR EXISTS (SELECT 1 FROM payments p WHERE p.provider = 'asaas'
				AND p.provider_ref = l.asaas_payment AND p.status = 'paid')
		FROM checkout_links l WHERE l.token_hash = $1`, tokenHash).
		Scan(&c.ID, &c.OrgID, &c.SubID, &c.AsaasSub, &c.AsaasPayment, &c.PlanName, &c.AmountCents, &c.DueDate,
			&c.ExpiresAt, &c.FailedAttempts, &c.LockedUntil, &c.Method, &c.Paid)
	return c, notFound(err)
}

// CheckoutCardFailed conta uma recusa do cartão; na max-ésima seguida, bloqueia o cartão no link por lock.
// Devolve se ficou bloqueado.
func (s *Store) CheckoutCardFailed(ctx context.Context, id int64, max int, lock time.Duration) (bool, error) {
	var locked bool
	err := s.pool.QueryRow(ctx, `UPDATE checkout_links SET failed_attempts = failed_attempts + 1,
			locked_until = CASE WHEN failed_attempts + 1 >= $2 THEN now() + make_interval(secs => $3) ELSE locked_until END
		WHERE id = $1 RETURNING locked_until IS NOT NULL AND locked_until > now()`, id, max, lock.Seconds()).Scan(&locked)
	return locked, notFound(err)
}

// CheckoutCardPaid marca o cartão aprovado no link e zera as recusas.
func (s *Store) CheckoutCardPaid(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE checkout_links SET card_paid_at = coalesce(card_paid_at, now()), failed_attempts = 0,
		locked_until = NULL WHERE id = $1`, id)
	return err
}

// CheckoutUnlock zera as recusas depois que o bloqueio venceu (a contagem recomeça).
func (s *Store) CheckoutUnlock(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE checkout_links SET failed_attempts = 0, locked_until = NULL
		WHERE id = $1 AND locked_until IS NOT NULL AND locked_until <= now()`, id)
	return err
}

// CheckoutHolder são os dados do titular que já temos, para pré-preencher o cartão.
type CheckoutHolder struct {
	Name    string // nome do dono (pessoa), não da empresa
	CpfCnpj string
	Email   string
	Phone   string // DDD + número, sem o 55
}

// CheckoutHolder lê o dono da organização: nome, documento (CNPJ da empresa, senão CPF), e-mail e telefone.
func (s *Store) CheckoutHolder(ctx context.Context, orgID int64) (CheckoutHolder, error) {
	var h CheckoutHolder
	err := s.pool.QueryRow(ctx, `SELECT coalesce(m.name, ''), coalesce(nullif(o.cnpj, ''), nullif(m.cpf, ''), ''),
			coalesce(m.email, ''), coalesce(w.phone_e164, '')
		FROM organizations o
		LEFT JOIN LATERAL (SELECT * FROM members x WHERE x.organization_id = o.id ORDER BY x.role = 'owner' DESC, x.id LIMIT 1) m ON true
		LEFT JOIN LATERAL (SELECT * FROM whatsapp_identities y WHERE y.member_id = m.id ORDER BY y.id LIMIT 1) w ON true
		WHERE o.id = $1`, orgID).Scan(&h.Name, &h.CpfCnpj, &h.Email, &h.Phone)
	if len(h.Phone) >= 12 && h.Phone[:2] == "55" {
		h.Phone = h.Phone[2:]
	}
	return h, notFound(err)
}

// LatestCheckoutExpiry é a validade do link mais novo da assinatura (sub_...). ErrNotFound = nenhum.
func (s *Store) LatestCheckoutExpiry(ctx context.Context, orgID int64, asaasSub string) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `SELECT expires_at FROM checkout_links WHERE organization_id = $1 AND asaas_subscription = $2
		ORDER BY expires_at DESC LIMIT 1`, orgID, asaasSub).Scan(&t)
	return t, notFound(err)
}

// CheckoutPixSwitched marca que a cobrança de cartão deste link foi trocada para Pix na página.
func (s *Store) CheckoutPixSwitched(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE checkout_links SET pix_switched_at = coalesce(pix_switched_at, now()) WHERE id = $1`, id)
	return err
}

// CheckoutPixSwitchedSub devolve a assinatura (sub_...) cuja cobrança paymentID foi trocada de cartão para
// Pix na página. "" = não houve troca.
func (s *Store) CheckoutPixSwitchedSub(ctx context.Context, paymentID string) (string, error) {
	var sub string
	err := s.pool.QueryRow(ctx, `SELECT asaas_subscription FROM checkout_links WHERE asaas_payment = $1 AND pix_switched_at IS NOT NULL
		ORDER BY id DESC LIMIT 1`, paymentID).Scan(&sub)
	if err != nil && notFound(err) == ErrNotFound {
		return "", nil
	}
	return sub, err
}

// CheckoutBeginCharge pega a trava da cobrança no cartão deste link (atômico no banco, vale entre réplicas).
// false = já há uma cobrança em andamento. A trava vence sozinha depois de ttl (processo que caiu no meio).
func (s *Store) CheckoutBeginCharge(ctx context.Context, id int64, ttl time.Duration) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE checkout_links SET charging_until = now() + make_interval(secs => $2)
		WHERE id = $1 AND (charging_until IS NULL OR charging_until < now())`, id, ttl.Seconds())
	return err == nil && tag.RowsAffected() == 1, err
}

// CheckoutEndCharge solta a trava da cobrança no cartão.
func (s *Store) CheckoutEndCharge(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE checkout_links SET charging_until = NULL WHERE id = $1`, id)
	return err
}
