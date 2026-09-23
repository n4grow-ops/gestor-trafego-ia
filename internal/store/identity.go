package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Identity é o telefone de WhatsApp com o membro e a organização a que pertence.
type Identity struct {
	ID                int64
	Provider          string
	Phone             string
	MemberID          int64
	MemberName        string
	Role              string
	OrgID             int64
	OrgName           string
	Niche             string
	MaxDailyBudgetBRL float64
	AllowedObjectives []string
}

// CanApprove diz se o membro pode aprovar gasto.
func (i Identity) CanApprove() bool { return i.Role == "owner" || i.Role == "approver" }

const identitySelect = `SELECT wi.id, wi.provider, wi.phone_e164, m.id, m.name, m.role,
	o.id, o.name, o.niche, o.max_daily_budget_brl::float8, o.allowed_objectives
	FROM whatsapp_identities wi
	JOIN members m ON m.id = wi.member_id
	JOIN organizations o ON o.id = m.organization_id`

func scanIdentity(row pgx.Row) (Identity, error) {
	var i Identity
	err := row.Scan(&i.ID, &i.Provider, &i.Phone, &i.MemberID, &i.MemberName, &i.Role,
		&i.OrgID, &i.OrgName, &i.Niche, &i.MaxDailyBudgetBRL, &i.AllowedObjectives)
	if err != nil {
		return Identity{}, notFound(err)
	}
	return i, nil
}

func (s *Store) CreateOrganization(ctx context.Context, name, niche string, maxBudget float64, objectives []string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO organizations (name, niche, max_daily_budget_brl, allowed_objectives)
		VALUES ($1, $2, $3, $4) RETURNING id`, name, niche, maxBudget, objectives).Scan(&id)
	return id, err
}

func (s *Store) CreateMember(ctx context.Context, orgID int64, name, role string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO members (organization_id, name, role) VALUES ($1, $2, $3) RETURNING id`,
		orgID, name, role).Scan(&id)
	return id, err
}

func (s *Store) LinkPhone(ctx context.Context, provider, phone string, memberID int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO whatsapp_identities (provider, phone_e164, member_id) VALUES ($1, $2, $3)`,
		provider, phone, memberID)
	return err
}

func (s *Store) FindIdentity(ctx context.Context, provider, phone string) (Identity, error) {
	return scanIdentity(s.pool.QueryRow(ctx, identitySelect+` WHERE wi.provider = $1 AND wi.phone_e164 = $2`, provider, phone))
}

func (s *Store) GetIdentity(ctx context.Context, id int64) (Identity, error) {
	return scanIdentity(s.pool.QueryRow(ctx, identitySelect+` WHERE wi.id = $1`, id))
}
