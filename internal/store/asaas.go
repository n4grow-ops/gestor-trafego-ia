package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// brt é o fuso de São Paulo (sem horário de verão desde 2019): datas do Asaas são dias locais.
var brt = time.FixedZone("BRT", -3*3600)

// BillingState é a situação da cobrança de uma organização. SubID 0 = sem assinatura.
type BillingState struct {
	Unlimited     bool
	CustomerID    string // organizations.asaas_customer_id
	SubID         int64
	PlanCode      string
	Status        string
	Provider      string
	ProviderRef   string // assinatura no Asaas (sub_...)
	PendingPlan   string
	PendingMethod string // pix ou card
	CheckoutURL   string
	PastDueSince  *time.Time
	PeriodEnd     *time.Time
}

// Released diz se o sistema está liberado: org ilimitada, assinatura ativa/em teste/em atraso (o atraso
// só barra campanha nova) ou cancelada ainda dentro do período pago.
func (b BillingState) Released(now time.Time) bool {
	if b.Unlimited {
		return true
	}
	switch b.Status {
	case "active", "trialing", "past_due":
		return true
	case "canceled":
		return b.PeriodEnd != nil && b.PeriodEnd.After(now)
	}
	return false
}

// BillingState lê a cobrança da organização.
func (s *Store) BillingState(ctx context.Context, orgID int64) (BillingState, error) {
	var b BillingState
	var subID *int64
	var plan, status *string
	err := s.pool.QueryRow(ctx, `SELECT o.unlimited, coalesce(o.asaas_customer_id, ''), s.id, s.plan_code, s.status,
			coalesce(s.provider, ''), coalesce(s.provider_ref, ''), coalesce(s.pending_plan, ''), coalesce(s.pending_method, ''),
			coalesce(s.checkout_url, ''), s.past_due_since, s.current_period_end
		FROM organizations o LEFT JOIN subscriptions s ON s.organization_id = o.id WHERE o.id = $1`, orgID).
		Scan(&b.Unlimited, &b.CustomerID, &subID, &plan, &status, &b.Provider, &b.ProviderRef, &b.PendingPlan, &b.PendingMethod,
			&b.CheckoutURL, &b.PastDueSince, &b.PeriodEnd)
	if err != nil {
		return b, notFound(err)
	}
	if subID != nil {
		b.SubID, b.PlanCode, b.Status = *subID, *plan, *status
	}
	return b, nil
}

// ChooseBillingPlan grava o plano escolhido no WhatsApp (assinatura pending se ainda não houver uma
// liberada). Troca de plano zera a forma e o link: a cobrança antiga é trocada na escolha da forma.
func (s *Store) ChooseBillingPlan(ctx context.Context, orgID int64, planCode string) error {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM plans WHERE code = $1 AND available AND visible)`, planCode).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrPlanUnavailable
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status, pending_plan) VALUES ($1, $2, 'pending', $2)
		ON CONFLICT (organization_id) DO UPDATE SET pending_plan = $2, pending_method = NULL, checkout_url = NULL,
			status = CASE WHEN subscriptions.status IN ('active', 'trialing', 'past_due') THEN subscriptions.status ELSE 'pending' END,
			updated_at = now()`, orgID, planCode)
	return err
}

// SetBillingCheckout grava a assinatura criada no Asaas, a forma e o link da primeira cobrança
// (url vazia = o Asaas ainda não gerou; a próxima mensagem tenta de novo).
func (s *Store) SetBillingCheckout(ctx context.Context, orgID int64, method, providerRef, url string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE subscriptions SET provider = 'asaas', provider_ref = $3, pending_method = $2,
			checkout_url = nullif($4, ''), plan_code = coalesce(pending_plan, plan_code),
			status = CASE WHEN status IN ('active', 'trialing', 'past_due') THEN status ELSE 'pending' END, updated_at = now()
		WHERE organization_id = $1`, orgID, method, providerRef, url)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNoSubscription
	}
	return err
}

// SetBillingMethod guarda a forma escolhida quando ainda falta o CPF/CNPJ para criar a assinatura.
func (s *Store) SetBillingMethod(ctx context.Context, orgID int64, method string) error {
	_, err := s.pool.Exec(ctx, `UPDATE subscriptions SET pending_method = $2, checkout_url = NULL, updated_at = now()
		WHERE organization_id = $1`, orgID, method)
	return err
}

// SetBillingDocument grava o documento mandado no WhatsApp: 14 dígitos = CNPJ da empresa, 11 = CPF do dono.
func (s *Store) SetBillingDocument(ctx context.Context, orgID int64, doc string) error {
	var err error
	switch len(doc) {
	case 14:
		_, err = s.pool.Exec(ctx, `UPDATE organizations SET cnpj = $2 WHERE id = $1`, orgID, doc)
	case 11:
		_, err = s.pool.Exec(ctx, `UPDATE members SET cpf = $2 WHERE id = (SELECT id FROM members WHERE organization_id = $1
			ORDER BY role = 'owner' DESC, id LIMIT 1)`, orgID, doc)
	default:
		err = fmt.Errorf("%w: documento com %d dígitos", ErrInvalidInput, len(doc))
	}
	return err
}

// SetCheckoutURL grava o link que faltava.
func (s *Store) SetCheckoutURL(ctx context.Context, orgID int64, url string) error {
	_, err := s.pool.Exec(ctx, `UPDATE subscriptions SET checkout_url = $2, updated_at = now() WHERE organization_id = $1`, orgID, url)
	return err
}

// SetAsaasCustomer guarda o cliente do Asaas da organização.
func (s *Store) SetAsaasCustomer(ctx context.Context, orgID int64, customerID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE organizations SET asaas_customer_id = $2 WHERE id = $1`, orgID, customerID)
	return err
}

// BillingCustomer são os dados do cliente para o Asaas.
type BillingCustomer struct {
	Name    string
	CpfCnpj string // CNPJ da empresa, senão CPF do dono
	Email   string
	Phone   string // E.164 sem +
}

// BillingCustomer lê nome, documento, e-mail e telefone do dono.
func (s *Store) BillingCustomer(ctx context.Context, orgID int64) (BillingCustomer, error) {
	var c BillingCustomer
	err := s.pool.QueryRow(ctx, `SELECT coalesce(nullif(o.legal_name, ''), nullif(o.trade_name, ''), o.name),
			coalesce(nullif(o.cnpj, ''), nullif(m.cpf, ''), ''), coalesce(m.email, ''), coalesce(w.phone_e164, '')
		FROM organizations o
		LEFT JOIN LATERAL (SELECT * FROM members x WHERE x.organization_id = o.id ORDER BY x.role = 'owner' DESC, x.id LIMIT 1) m ON true
		LEFT JOIN LATERAL (SELECT * FROM whatsapp_identities y WHERE y.member_id = m.id ORDER BY y.id LIMIT 1) w ON true
		WHERE o.id = $1`, orgID).Scan(&c.Name, &c.CpfCnpj, &c.Email, &c.Phone)
	return c, notFound(err)
}

// AsaasEvent é o corpo do webhook do Asaas.
type AsaasEvent struct {
	ID           string             `json:"id"`
	Event        string             `json:"event"`
	Payment      *AsaasPayment      `json:"payment"`
	Subscription *AsaasSubscription `json:"subscription"`
}

type AsaasPayment struct {
	ID                string  `json:"id"`
	Customer          string  `json:"customer"`
	Subscription      string  `json:"subscription"`
	Value             float64 `json:"value"`
	BillingType       string  `json:"billingType"`
	Status            string  `json:"status"`
	DueDate           string  `json:"dueDate"`
	PaymentDate       string  `json:"paymentDate"`
	ConfirmedDate     string  `json:"confirmedDate"`
	InvoiceURL        string  `json:"invoiceUrl"`
	ExternalReference string  `json:"externalReference"`
}

type AsaasSubscription struct {
	ID                string `json:"id"`
	ExternalReference string `json:"externalReference"`
}

// Avisos ao dono depois de um evento do Asaas.
const (
	BillingNoticePaid    = "paid"
	BillingNoticeOverdue = "overdue"
	BillingNoticeDue     = "due" // mensalidade nova por Pix: o Pix não é cobrado sozinho como o cartão
)

// BillingNotice é o aviso ao dono que o evento gera (enfileirado na mesma transação).
type BillingNotice struct {
	OrgID      int64  `json:"org_id"`
	Kind       string `json:"kind"`
	PlanName   string `json:"plan_name,omitempty"`
	InvoiceURL string `json:"invoice_url,omitempty"`
	DueDate    string `json:"due_date,omitempty"` // AAAA-MM-DD
	// Cobrança (pay_...) e assinatura (sub_...) no Asaas: o checkout próprio monta o link com elas.
	PaymentID string `json:"payment_id,omitempty"`
	SubRef    string `json:"sub_ref,omitempty"`
}

// ApplyAsaasEvent grava o evento e aplica o efeito numa transação só. Evento repetido devolve false sem
// efeito. notify monta o job do aviso ao dono (nil = sem aviso). Erro = nada gravado (o Asaas reenvia).
func (s *Store) ApplyAsaasEvent(ctx context.Context, ev AsaasEvent, notify func(BillingNotice) JobSpec) (bool, error) {
	if ev.ID == "" || ev.Event == "" {
		return false, fmt.Errorf("%w: evento sem id", ErrInvalidInput)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO asaas_events (id, event) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, ev.ID, ev.Event)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	var notice *BillingNotice
	switch ev.Event {
	case "PAYMENT_CREATED", "PAYMENT_CONFIRMED", "PAYMENT_RECEIVED", "PAYMENT_OVERDUE", "PAYMENT_REFUNDED", "PAYMENT_CHARGEBACK_REQUESTED":
		if ev.Payment != nil {
			notice, err = applyAsaasPayment(ctx, tx, ev.Event, *ev.Payment)
		}
	case "SUBSCRIPTION_DELETED", "SUBSCRIPTION_INACTIVATED":
		if ev.Subscription != nil {
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET status = 'canceled', updated_at = now()
				WHERE provider = 'asaas' AND provider_ref = $1`, ev.Subscription.ID)
		}
	}
	if err != nil {
		return false, err
	}
	if notice != nil && notify != nil {
		if err := enqueue(ctx, tx, notify(*notice)); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

// asaasSub acha a assinatura do pagamento: pelo id da assinatura no Asaas, senão pela referência org-<id>.
func asaasSub(ctx context.Context, tx pgx.Tx, p AsaasPayment) (id, orgID int64, status, pendingMethod, planName string, err error) {
	q := `SELECT s.id, s.organization_id, s.status, coalesce(s.pending_method, ''), p.name
		FROM subscriptions s JOIN plans p ON p.code = s.plan_code `
	if p.Subscription != "" {
		err = tx.QueryRow(ctx, q+`WHERE s.provider = 'asaas' AND s.provider_ref = $1 FOR UPDATE OF s`, p.Subscription).
			Scan(&id, &orgID, &status, &pendingMethod, &planName)
		if !errors.Is(err, pgx.ErrNoRows) {
			return
		}
	}
	org, ok := orgFromRef(p.ExternalReference)
	if !ok {
		return 0, 0, "", "", "", pgx.ErrNoRows
	}
	err = tx.QueryRow(ctx, q+`WHERE s.organization_id = $1 FOR UPDATE OF s`, org).Scan(&id, &orgID, &status, &pendingMethod, &planName)
	return
}

func orgFromRef(ref string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimPrefix(ref, "org-"), 10, 64)
	return n, strings.HasPrefix(ref, "org-") && err == nil && n > 0
}

func applyAsaasPayment(ctx context.Context, tx pgx.Tx, event string, p AsaasPayment) (*BillingNotice, error) {
	subID, orgID, prev, pendingMethod, planName, err := asaasSub(ctx, tx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // cobrança de fora do sistema: só fica o registro do evento
	}
	if err != nil {
		return nil, err
	}
	method := map[string]string{"PIX": "pix", "CREDIT_CARD": "card", "BOLETO": "boleto"}[p.BillingType]
	if method == "" {
		method = pendingMethod
	}
	if method == "" {
		method = "pix"
	}
	status, paidAt := "pending", (*time.Time)(nil)
	switch event {
	case "PAYMENT_CONFIRMED", "PAYMENT_RECEIVED":
		status = "paid"
		at := asaasDate(p.PaymentDate, asaasDate(p.ConfirmedDate, time.Now()))
		if event == "PAYMENT_CONFIRMED" {
			at = asaasDate(p.ConfirmedDate, asaasDate(p.PaymentDate, time.Now()))
		}
		paidAt = &at
	case "PAYMENT_REFUNDED", "PAYMENT_CHARGEBACK_REQUESTED":
		status = "refunded"
	}
	saved := status
	if p.ID != "" {
		if err := tx.QueryRow(ctx, `INSERT INTO payments (subscription_id, amount_cents, method, status, provider, provider_ref, paid_at)
			VALUES ($1, $2, $3, $4, 'asaas', $5, $6)
			ON CONFLICT (provider, provider_ref) WHERE provider_ref IS NOT NULL DO UPDATE SET
				amount_cents = excluded.amount_cents, method = excluded.method,
				-- evento atrasado (CREATED depois do RECEIVED) não volta o pagamento para pendente
				status = CASE WHEN excluded.status = 'pending' THEN payments.status ELSE excluded.status END,
				paid_at = coalesce(excluded.paid_at, payments.paid_at)
			RETURNING status`,
			subID, int64(math.Round(p.Value*100)), method, status, p.ID, paidAt).Scan(&saved); err != nil {
			return nil, fmt.Errorf("store: pagamento asaas: %w", err)
		}
	}
	switch event {
	case "PAYMENT_CONFIRMED", "PAYMENT_RECEIVED":
		end := addMonth(asaasDate(p.DueDate, time.Now().In(brt)))
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status = 'active', past_due_since = NULL,
				current_period_end = greatest(coalesce(current_period_end, $2), $2),
				pending_plan = NULL, pending_method = NULL, checkout_url = NULL, updated_at = now()
			WHERE id = $1`, subID, end); err != nil {
			return nil, err
		}
		// Limites do plano pago, sem rebaixar quem já tinha mais (como no cadastro).
		if _, err := tx.Exec(ctx, `UPDATE organizations o SET max_daily_budget_brl = greatest(o.max_daily_budget_brl, p.max_daily_budget_brl),
				daily_message_limit = greatest(o.daily_message_limit, p.daily_message_limit)
			FROM subscriptions s JOIN plans p ON p.code = s.plan_code WHERE s.id = $1 AND o.id = s.organization_id`, subID); err != nil {
			return nil, err
		}
		if prev != "active" {
			return &BillingNotice{OrgID: orgID, Kind: BillingNoticePaid, PlanName: planName}, nil
		}
	case "PAYMENT_CREATED":
		// Renovação por Pix de quem já paga: a Mia manda o link do mês (o Asaas não avisa, as notificações
		// dele ficam desligadas). A primeira cobrança já sai com o link no checkout.
		if (prev == "active" || prev == "past_due") && p.BillingType == "PIX" && p.InvoiceURL != "" && saved == "pending" {
			return &BillingNotice{OrgID: orgID, Kind: BillingNoticeDue, PlanName: planName, InvoiceURL: p.InvoiceURL, DueDate: p.DueDate,
				PaymentID: p.ID, SubRef: p.Subscription}, nil
		}
	case "PAYMENT_OVERDUE":
		// Só quem já pagou vira past_due: a primeira cobrança vencida sem pagamento continua pending
		// (past_due libera o sistema).
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET status = 'past_due', past_due_since = coalesce(past_due_since, now()), updated_at = now()
			WHERE id = $1 AND status IN ('active', 'trialing', 'past_due')`, subID); err != nil {
			return nil, err
		}
		// O link da cobrança vencida fica guardado: a Mia reenvia quando barrar campanha nova.
		if p.InvoiceURL != "" {
			if _, err := tx.Exec(ctx, `UPDATE subscriptions SET checkout_url = $2 WHERE id = $1`, subID, p.InvoiceURL); err != nil {
				return nil, err
			}
		}
		return &BillingNotice{OrgID: orgID, Kind: BillingNoticeOverdue, PlanName: planName, InvoiceURL: p.InvoiceURL,
			PaymentID: p.ID, SubRef: p.Subscription}, nil
	}
	return nil, nil
}

// asaasDate lê AAAA-MM-DD (dia em São Paulo); vazio ou inválido = def.
func asaasDate(s string, def time.Time) time.Time {
	if len(s) >= 10 {
		if t, err := time.ParseInLocation("2006-01-02", s[:10], brt); err == nil {
			return t
		}
	}
	return def
}

// addMonth soma um mês sem pular o mês curto: 31/10 vira 30/11 (o Go daria 01/12).
func addMonth(t time.Time) time.Time {
	n := t.AddDate(0, 1, 0)
	if n.Day() < t.Day() {
		n = n.AddDate(0, 0, -n.Day())
	}
	return n
}
