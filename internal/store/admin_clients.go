package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ClientStages são as etapas do cliente no painel, na ordem. O índice é ClientRow.Stage.
var ClientStages = []string{"Contato", "Cadastro", "Coleta", "Proposta", "Aprovada", "Pronta"}

// Cliente travado: ativo, antes de "Pronta" e sem conversa há mais que isso.
const stuckAfter = 24 * time.Hour

// ClientRow é um cliente (organização) na lista do painel.
type ClientRow struct {
	OrgID          int64     `json:"org_id"`
	Name           string    `json:"name"`
	Segment        string    `json:"segment"`
	City           string    `json:"city"`
	UF             string    `json:"uf"`
	Status         string    `json:"status"`       // active ou blocked
	Registration   string    `json:"registration"` // pending ou complete
	CreatedAt      time.Time `json:"created_at"`
	Contact        string    `json:"contact"`
	Email          string    `json:"email"`
	Phone          string    `json:"phone"`
	IdentityID     int64     `json:"identity_id"`
	Plan           string    `json:"plan"`
	PlanName       string    `json:"plan_name"`
	SubStatus      string    `json:"sub_status"`
	Stage          int       `json:"stage"`
	StageLabel     string    `json:"stage_label"`
	MetaConnected  bool      `json:"meta_connected"`
	ConversationID int64     `json:"conversation_id"`
	ConvState      string    `json:"conv_state"`
	NeedsHuman     bool      `json:"needs_human"`
	HumanBy        string    `json:"human_by"`
	LastActivity   time.Time `json:"last_activity"`
	Stuck          bool      `json:"stuck"`
}

// clientBase é uma linha por organização, com etapa, contato principal e última atividade.
const clientBase = `WITH base AS (
	SELECT o.id AS org_id, coalesce(nullif(o.trade_name, ''), o.name) AS name,
		coalesce(nullif(o.segment, ''), o.niche, '') AS segment,
		coalesce(nullif(o.city, ''), o.profile->>'city', '') AS city, coalesce(o.uf, '') AS uf,
		o.status, o.registration_status, o.created_at,
		coalesce(nullif(pm.full_name, ''), pm.name, '') AS contact, coalesce(pm.email, '') AS email,
		coalesce(pw.phone_e164, '') AS phone, coalesce(pw.id, 0) AS identity_id,
		coalesce(s.plan_code, '') AS plan, coalesce(pl.name, '') AS plan_name, coalesce(s.status, '') AS sub_status,
		CASE WHEN o.registration_status <> 'complete' THEN 0 ELSE greatest(1, coalesce(ps.rank, 1)) END AS stage,
		EXISTS (SELECT 1 FROM meta_connections mc WHERE mc.organization_id = o.id AND mc.status = 'active') AS meta,
		coalesce(cv.id, 0) AS conv_id, coalesce(cv.state, '') AS conv_state, coalesce(cv.human_by, '') AS human_by,
		greatest(o.created_at, la.at) AS last_activity
	FROM organizations o
	LEFT JOIN LATERAL (SELECT m.* FROM members m WHERE m.organization_id = o.id ORDER BY m.role = 'owner' DESC, m.id LIMIT 1) pm ON true
	LEFT JOIN LATERAL (SELECT w.* FROM whatsapp_identities w WHERE w.member_id = pm.id ORDER BY w.id LIMIT 1) pw ON true
	LEFT JOIN conversations cv ON cv.identity_id = pw.id
	LEFT JOIN subscriptions s ON s.organization_id = o.id
	LEFT JOIN plans pl ON pl.code = s.plan_code
	LEFT JOIN LATERAL (SELECT max(CASE p.stage WHEN 'collecting' THEN 2 WHEN 'awaiting_proposal' THEN 3 WHEN 'awaiting_approval' THEN 3
		WHEN 'approved' THEN 4 WHEN 'awaiting_creatives' THEN 4 WHEN 'ready' THEN 5 ELSE 1 END) AS rank
		FROM projects p WHERE p.organization_id = o.id) ps ON true
	LEFT JOIN LATERAL (SELECT max(x.created_at) AS at FROM messages x
		JOIN conversations c2 ON c2.id = x.conversation_id
		JOIN whatsapp_identities w2 ON w2.id = c2.identity_id
		JOIN members m2 ON m2.id = w2.member_id
		WHERE m2.organization_id = o.id) la ON true
), cl AS (
	SELECT *, (status = 'active' AND stage < 5 AND last_activity < now() - interval '24 hours') AS stuck FROM base
)`

const clientCols = `org_id, name, segment, city, uf, status, registration_status, created_at, contact, email, phone, identity_id,
	plan, plan_name, sub_status, stage, meta, conv_id, conv_state, human_by, last_activity, stuck`

func scanClient(row pgx.Row) (ClientRow, error) {
	var c ClientRow
	err := row.Scan(&c.OrgID, &c.Name, &c.Segment, &c.City, &c.UF, &c.Status, &c.Registration, &c.CreatedAt, &c.Contact, &c.Email,
		&c.Phone, &c.IdentityID, &c.Plan, &c.PlanName, &c.SubStatus, &c.Stage, &c.MetaConnected, &c.ConversationID, &c.ConvState,
		&c.HumanBy, &c.LastActivity, &c.Stuck)
	if err != nil {
		return ClientRow{}, notFound(err)
	}
	if c.Stage >= 0 && c.Stage < len(ClientStages) {
		c.StageLabel = ClientStages[c.Stage]
	}
	c.NeedsHuman = c.ConvState == StateNeedsHuman
	return c, nil
}

// ClientFilter: Filter = travados | pendentes | conectados | bloqueados | humano; Stage -1 = todas.
type ClientFilter struct {
	Q      string
	Filter string
	Stage  int
	Limit  int
}

// ClientCounts são os números dos filtros da lista.
type ClientCounts struct {
	All       int `json:"all"`
	Stuck     int `json:"stuck"`
	Pending   int `json:"pending"`
	Connected int `json:"connected"`
	Blocked   int `json:"blocked"`
	Human     int `json:"human"`
}

type ClientList struct {
	Clients []ClientRow  `json:"clients"`
	Counts  ClientCounts `json:"counts"`
}

func (s *Store) Clients(ctx context.Context, f ClientFilter) (ClientList, error) {
	var out ClientList
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 500
	}
	var where []string
	var args []any
	switch f.Filter {
	case "travados":
		where = append(where, "stuck")
	case "pendentes":
		where = append(where, "registration_status <> 'complete'")
	case "conectados":
		where = append(where, "meta")
	case "bloqueados":
		where = append(where, "status = 'blocked'")
	case "humano":
		where = append(where, "conv_state = 'needs_human'")
	}
	if f.Stage >= 0 {
		args = append(args, f.Stage)
		where = append(where, fmt.Sprintf("stage = $%d", len(args)))
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		args = append(args, "%"+likeEscape(q)+"%")
		n := len(args)
		where = append(where, fmt.Sprintf("(name ILIKE $%[1]d OR contact ILIKE $%[1]d OR email ILIKE $%[1]d OR phone LIKE $%[1]d OR city ILIKE $%[1]d OR segment ILIKE $%[1]d)", n))
	}
	sql := clientBase + ` SELECT ` + clientCols + ` FROM cl`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit)
	sql += fmt.Sprintf(" ORDER BY stuck DESC, last_activity DESC, org_id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return out, err
	}
	out.Clients, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ClientRow, error) { return scanClient(r) })
	if err != nil {
		return out, err
	}
	err = s.pool.QueryRow(ctx, clientBase+` SELECT count(*), count(*) FILTER (WHERE stuck),
		count(*) FILTER (WHERE registration_status <> 'complete'), count(*) FILTER (WHERE meta),
		count(*) FILTER (WHERE status = 'blocked'), count(*) FILTER (WHERE conv_state = 'needs_human') FROM cl`).
		Scan(&out.Counts.All, &out.Counts.Stuck, &out.Counts.Pending, &out.Counts.Connected, &out.Counts.Blocked, &out.Counts.Human)
	return out, err
}

// ClientMember é uma pessoa do cliente com acesso pelo WhatsApp.
type ClientMember struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Role          string   `json:"role"`
	Email         string   `json:"email"`
	EmailVerified bool     `json:"email_verified"`
	Phones        []string `json:"phones"`
}

type ClientConsent struct {
	Kind       string    `json:"kind"`
	Version    string    `json:"version"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type ClientSubscription struct {
	Plan             string     `json:"plan"`
	PlanName         string     `json:"plan_name"`
	PriceCents       int        `json:"price_cents"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"started_at"`
	CurrentPeriodEnd *time.Time `json:"current_period_end"`
}

// ClientMeta é a conexão Meta ativa, sem o token.
type ClientMeta struct {
	AdAccountID       string     `json:"ad_account_id"`
	AdAccountName     string     `json:"ad_account_name"`
	Currency          string     `json:"currency"`
	Timezone          string     `json:"timezone"`
	AccountStatus     int        `json:"account_status"`
	PageName          string     `json:"page_name"`
	InstagramUsername string     `json:"instagram_username"`
	PageWhatsApp      bool       `json:"page_whatsapp"`
	ConnectedAt       *time.Time `json:"connected_at"`
}

type ClientDraft struct {
	ID             int64      `json:"id"`
	Status         string     `json:"status"`
	Objective      string     `json:"objective"`
	DailyBudgetBRL float64    `json:"daily_budget_brl"`
	DurationDays   int        `json:"duration_days"`
	CreatedAt      time.Time  `json:"created_at"`
	ApprovedAt     *time.Time `json:"approved_at"`
}

type ClientProject struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Stage     string          `json:"stage"`
	Fields    json.RawMessage `json:"fields"`
	UpdatedAt time.Time       `json:"updated_at"`
	Creatives int             `json:"creatives"`
	Draft     *ClientDraft    `json:"draft"`
	Media     []ProjectMedia  `json:"media"` // anúncios e prévia do último rascunho, com link para o painel
}

// TimelineEvent junta o que o sistema (audit_events) e a equipe (admin_audit_log) fizeram.
type TimelineEvent struct {
	At     time.Time       `json:"at"`
	Source string          `json:"source"` // sistema ou equipe
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
	Org    string          `json:"org,omitempty"`
	OrgID  int64           `json:"org_id,omitempty"`
}

// ClientDetail é a ficha completa do cliente.
type ClientDetail struct {
	ClientRow
	LegalName         string              `json:"legal_name"`
	CNPJ              string              `json:"cnpj"`
	Website           string              `json:"website"`
	Instagram         string              `json:"instagram"`
	AdsObjective      []string            `json:"ads_objective"`
	MonthlyBudget     string              `json:"monthly_budget"`
	AdsExperience     string              `json:"ads_experience"`
	HasMetaAdAccount  string              `json:"has_meta_ad_account"`
	Approver          string              `json:"approver"`
	ReferralSource    string              `json:"referral_source"`
	SignupSource      string              `json:"signup_source"`
	RegisteredAt      *time.Time          `json:"registered_at"`
	MaxDailyBudgetBRL float64             `json:"max_daily_budget_brl"`
	DailyMessageLimit int                 `json:"daily_message_limit"`
	Profile           json.RawMessage     `json:"profile"`
	Members           []ClientMember      `json:"members"`
	Consents          []ClientConsent     `json:"consents"`
	Subscription      *ClientSubscription `json:"subscription"`
	Meta              *ClientMeta         `json:"meta"`
	Projects          []ClientProject     `json:"projects"`
	Timeline          []TimelineEvent     `json:"timeline"`
	Messages30d       int                 `json:"messages_30d"`
	CostUSD30d        float64             `json:"cost_usd_30d"`
}

func (s *Store) ClientRow(ctx context.Context, orgID int64) (ClientRow, error) {
	return scanClient(s.pool.QueryRow(ctx, clientBase+` SELECT `+clientCols+` FROM cl WHERE org_id = $1`, orgID))
}

func (s *Store) ClientDetail(ctx context.Context, orgID int64) (ClientDetail, error) {
	var d ClientDetail
	row, err := s.ClientRow(ctx, orgID)
	if err != nil {
		return d, err
	}
	d.ClientRow = row
	err = s.pool.QueryRow(ctx, `SELECT coalesce(legal_name, ''), coalesce(cnpj, ''), coalesce(website, ''), coalesce(instagram, ''),
			coalesce(ads_objective, '{}'), coalesce(monthly_budget_range, ''), coalesce(ads_experience, ''), coalesce(has_meta_ad_account, ''),
			coalesce(approver, ''), coalesce(referral_source, ''), signup_source, registered_at, max_daily_budget_brl::float8,
			daily_message_limit, profile
		FROM organizations WHERE id = $1`, orgID).Scan(&d.LegalName, &d.CNPJ, &d.Website, &d.Instagram, &d.AdsObjective,
		&d.MonthlyBudget, &d.AdsExperience, &d.HasMetaAdAccount, &d.Approver, &d.ReferralSource, &d.SignupSource, &d.RegisteredAt,
		&d.MaxDailyBudgetBRL, &d.DailyMessageLimit, &d.Profile)
	if err != nil {
		return d, notFound(err)
	}

	rows, err := s.pool.Query(ctx, `SELECT m.id, coalesce(nullif(m.full_name, ''), m.name), m.role, coalesce(m.email, ''),
			m.email_verified_at IS NOT NULL, coalesce(array_agg(w.phone_e164 ORDER BY w.id) FILTER (WHERE w.id IS NOT NULL), '{}')
		FROM members m LEFT JOIN whatsapp_identities w ON w.member_id = m.id
		WHERE m.organization_id = $1 GROUP BY m.id ORDER BY m.role = 'owner' DESC, m.id`, orgID)
	if err != nil {
		return d, err
	}
	if d.Members, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ClientMember, error) {
		var m ClientMember
		err := r.Scan(&m.ID, &m.Name, &m.Role, &m.Email, &m.EmailVerified, &m.Phones)
		return m, err
	}); err != nil {
		return d, err
	}

	rows, err = s.pool.Query(ctx, `SELECT c.kind, c.version, c.accepted_at FROM consents c JOIN members m ON m.id = c.member_id
		WHERE m.organization_id = $1 ORDER BY c.accepted_at DESC`, orgID)
	if err != nil {
		return d, err
	}
	if d.Consents, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ClientConsent, error) {
		var c ClientConsent
		err := r.Scan(&c.Kind, &c.Version, &c.AcceptedAt)
		return c, err
	}); err != nil {
		return d, err
	}

	var sub ClientSubscription
	err = s.pool.QueryRow(ctx, `SELECT s.plan_code, p.name, p.price_cents, s.status, s.started_at, s.current_period_end
		FROM subscriptions s JOIN plans p ON p.code = s.plan_code WHERE s.organization_id = $1`, orgID).
		Scan(&sub.Plan, &sub.PlanName, &sub.PriceCents, &sub.Status, &sub.StartedAt, &sub.CurrentPeriodEnd)
	if err == nil {
		d.Subscription = &sub
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return d, err
	}

	var mc ClientMeta
	var accStatus *int
	err = s.pool.QueryRow(ctx, `SELECT coalesce(ad_account_id, ''), coalesce(ad_account_name, ''), coalesce(currency, ''),
			coalesce(timezone_name, ''), account_status, coalesce(page_name, ''), coalesce(instagram_username, ''), page_whatsapp, connected_at
		FROM meta_connections WHERE organization_id = $1 AND status = 'active'`, orgID).
		Scan(&mc.AdAccountID, &mc.AdAccountName, &mc.Currency, &mc.Timezone, &accStatus, &mc.PageName, &mc.InstagramUsername, &mc.PageWhatsApp, &mc.ConnectedAt)
	if err == nil {
		if accStatus != nil {
			mc.AccountStatus = *accStatus
		}
		d.Meta = &mc
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return d, err
	}

	rows, err = s.pool.Query(ctx, `SELECT p.id, p.name, p.stage, p.fields, p.updated_at,
			(SELECT count(*) FROM creatives c WHERE c.project_id = p.id)::int,
			d.id, d.status, coalesce(d.proposal->>'objective', ''), coalesce((d.proposal->>'daily_budget_brl')::float8, 0),
			coalesce((d.proposal->>'duration_days')::int, 0), d.created_at, d.approved_at
		FROM projects p
		LEFT JOIN LATERAL (SELECT * FROM campaign_drafts x WHERE x.project_id = p.id ORDER BY x.id DESC LIMIT 1) d ON true
		WHERE p.organization_id = $1 ORDER BY p.updated_at DESC`, orgID)
	if err != nil {
		return d, err
	}
	if d.Projects, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ClientProject, error) {
		var p ClientProject
		var draftID *int64
		var status, objective *string
		var budget *float64
		var days *int
		var created *time.Time
		var approved *time.Time
		err := r.Scan(&p.ID, &p.Name, &p.Stage, &p.Fields, &p.UpdatedAt, &p.Creatives, &draftID, &status, &objective, &budget, &days, &created, &approved)
		if err == nil && draftID != nil {
			p.Draft = &ClientDraft{ID: *draftID, Status: *status, Objective: *objective, DailyBudgetBRL: *budget, DurationDays: *days,
				CreatedAt: *created, ApprovedAt: approved}
		}
		return p, err
	}); err != nil {
		return d, err
	}

	for i := range d.Projects {
		if dr := d.Projects[i].Draft; dr != nil {
			if d.Projects[i].Media, err = s.ProjectMedia(ctx, dr.ID); err != nil {
				return d, err
			}
		}
	}

	if d.Timeline, err = s.timeline(ctx, orgID, 100); err != nil {
		return d, err
	}
	err = s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM messages x JOIN conversations c ON c.id = x.conversation_id JOIN whatsapp_identities w ON w.id = c.identity_id
			JOIN members m ON m.id = w.member_id WHERE m.organization_id = $1 AND x.created_at > now() - interval '30 days')::int,
		(SELECT coalesce(sum(cost_usd), 0)::float8 FROM llm_usage WHERE organization_id = $1 AND created_at > now() - interval '30 days')`,
		orgID).Scan(&d.Messages30d, &d.CostUSD30d)
	return d, err
}

// timeline do cliente (orgID > 0) ou de todos (orgID = 0, para o feed da visão geral).
func (s *Store) timeline(ctx context.Context, orgID int64, limit int) ([]TimelineEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT at, source, actor, action, data, org, org_id FROM (
			SELECT a.created_at AS at, 'sistema' AS source, a.actor, a.action, a.data,
				coalesce(nullif(o.trade_name, ''), o.name, '') AS org, coalesce(a.organization_id, 0) AS org_id
			FROM audit_events a LEFT JOIN organizations o ON o.id = a.organization_id
			WHERE $1 = 0 OR a.organization_id = $1
			UNION ALL
			SELECT l.created_at, 'equipe', coalesce(nullif(t.name, ''), split_part(l.actor_email, '@', 1)), l.action, l.data,
				l.target_label, coalesce(oc.org_id, 0)
			FROM admin_audit_log l
			LEFT JOIN team_members t ON t.email = l.actor_email
			LEFT JOIN LATERAL (
				SELECT CASE WHEN l.target_type = 'organization' AND l.target_id ~ '^[0-9]+$' THEN l.target_id::bigint
					WHEN l.target_type = 'conversation' AND l.target_id ~ '^[0-9]+$' THEN (
						SELECT m.organization_id FROM conversations c JOIN whatsapp_identities w ON w.id = c.identity_id
						JOIN members m ON m.id = w.member_id WHERE c.id = l.target_id::bigint)
					END AS org_id) oc ON true
			WHERE $1 = 0 OR oc.org_id = $1
		) t ORDER BY at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TimelineEvent, error) {
		var e TimelineEvent
		err := r.Scan(&e.At, &e.Source, &e.Actor, &e.Action, &e.Data, &e.Org, &e.OrgID)
		return e, err
	})
}

// FunnelStep é uma etapa do funil: quantos clientes já chegaram nela.
type FunnelStep struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// DailySeries são as contagens por dia (fuso de Brasília), do mais antigo para hoje.
type DailySeries struct {
	Days     []string `json:"days"`
	Signups  []int    `json:"signups"`
	Meta     []int    `json:"meta"`
	Approved []int    `json:"approved"`
	Ready    []int    `json:"ready"`
	Messages []int    `json:"messages"`
}

// Overview é a visão geral do painel para os últimos Days dias.
type Overview struct {
	Days       int             `json:"days"`
	Signups    int             `json:"signups"`     // cadastros concluídos no período
	MetaNew    int             `json:"meta_new"`    // contas conectadas no período
	MetaActive int             `json:"meta_active"` // conexões ativas hoje
	Approved   int             `json:"approved"`    // propostas aprovadas no período
	Ready      int             `json:"ready"`       // projetos prontos para publicar (hoje)
	Messages   int             `json:"messages"`    // mensagens trocadas no período
	Series     DailySeries     `json:"series"`
	Funnel     []FunnelStep    `json:"funnel"`
	Stuck      []ClientRow     `json:"stuck"`
	StuckCount int             `json:"stuck_count"`
	Inbox      InboxCounts     `json:"inbox"`
	Feed       []TimelineEvent `json:"feed"`
}

func (s *Store) Overview(ctx context.Context, days int) (Overview, error) {
	if days <= 0 || days > 90 {
		days = 7
	}
	ov := Overview{Days: days}
	since := fmt.Sprintf("%d days", days)
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM organizations WHERE registration_status = 'complete' AND registered_at > now() - $1::interval),
		(SELECT count(*) FROM meta_connections WHERE status = 'active' AND connected_at > now() - $1::interval),
		(SELECT count(*) FROM meta_connections WHERE status = 'active'),
		(SELECT count(*) FROM campaign_drafts WHERE approved_at > now() - $1::interval),
		(SELECT count(*) FROM projects WHERE stage = 'ready'),
		(SELECT count(*) FROM messages WHERE created_at > now() - $1::interval)`, since).
		Scan(&ov.Signups, &ov.MetaNew, &ov.MetaActive, &ov.Approved, &ov.Ready, &ov.Messages)
	if err != nil {
		return ov, err
	}

	n := days
	if n < 7 {
		n = 7
	}
	rows, err := s.pool.Query(ctx, `WITH d AS (
			SELECT generate_series((now() AT TIME ZONE 'America/Sao_Paulo')::date - ($1::int - 1), (now() AT TIME ZONE 'America/Sao_Paulo')::date, '1 day')::date AS day
		)
		SELECT to_char(d.day, 'YYYY-MM-DD'),
			(SELECT count(*) FROM organizations WHERE registration_status = 'complete' AND (registered_at AT TIME ZONE 'America/Sao_Paulo')::date = d.day)::int,
			(SELECT count(*) FROM meta_connections WHERE status = 'active' AND (connected_at AT TIME ZONE 'America/Sao_Paulo')::date = d.day)::int,
			(SELECT count(*) FROM campaign_drafts WHERE (approved_at AT TIME ZONE 'America/Sao_Paulo')::date = d.day)::int,
			(SELECT count(*) FROM projects WHERE stage = 'ready' AND (updated_at AT TIME ZONE 'America/Sao_Paulo')::date = d.day)::int,
			(SELECT count(*) FROM messages WHERE created_at >= (d.day::timestamp AT TIME ZONE 'America/Sao_Paulo')
				AND created_at < ((d.day + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo'))::int
		FROM d ORDER BY d.day`, n)
	if err != nil {
		return ov, err
	}
	for rows.Next() {
		var day string
		var a, b, c, e, m int
		if err := rows.Scan(&day, &a, &b, &c, &e, &m); err != nil {
			rows.Close()
			return ov, err
		}
		ov.Series.Days = append(ov.Series.Days, day)
		ov.Series.Signups = append(ov.Series.Signups, a)
		ov.Series.Meta = append(ov.Series.Meta, b)
		ov.Series.Approved = append(ov.Series.Approved, c)
		ov.Series.Ready = append(ov.Series.Ready, e)
		ov.Series.Messages = append(ov.Series.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return ov, err
	}

	counts := make([]int, 7)
	err = s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM organizations),
		(SELECT count(*) FROM organizations WHERE registration_status = 'complete'),
		(SELECT count(DISTINCT organization_id) FROM projects),
		(SELECT count(DISTINCT organization_id) FROM campaign_drafts),
		(SELECT count(DISTINCT organization_id) FROM campaign_drafts WHERE status = 'approved'),
		(SELECT count(DISTINCT organization_id) FROM projects WHERE stage = 'ready'),
		(SELECT count(DISTINCT organization_id) FROM meta_connections WHERE status = 'active')`).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5], &counts[6])
	if err != nil {
		return ov, err
	}
	for i, label := range []string{"Contatos", "Cadastro concluído", "Projeto iniciado", "Proposta gerada", "Proposta aprovada", "Anúncios prontos", "Meta conectada"} {
		ov.Funnel = append(ov.Funnel, FunnelStep{Label: label, Count: counts[i]})
	}

	rows, err = s.pool.Query(ctx, clientBase+` SELECT `+clientCols+` FROM cl WHERE stuck ORDER BY last_activity LIMIT 8`)
	if err != nil {
		return ov, err
	}
	if ov.Stuck, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ClientRow, error) { return scanClient(r) }); err != nil {
		return ov, err
	}
	if err := s.pool.QueryRow(ctx, clientBase+` SELECT count(*) FROM cl WHERE stuck`).Scan(&ov.StuckCount); err != nil {
		return ov, err
	}
	if ov.Inbox, err = s.InboxCounts(ctx); err != nil {
		return ov, err
	}
	ov.Feed, err = s.timeline(ctx, 0, 15)
	return ov, err
}

// WorkerError é o último erro registrado pelo worker (job ou evento do webhook).
type WorkerError struct {
	Source string    `json:"source"` // outbox ou inbox
	Kind   string    `json:"kind"`
	Error  string    `json:"error"`
	At     time.Time `json:"at"`
}

// WorkerHealth mostra se o worker está andando: fila, falhas e último erro.
type WorkerHealth struct {
	InboxPending    int          `json:"inbox_pending"`
	OutboxPending   int          `json:"outbox_pending"` // prontos para rodar
	OldestPendingAt *time.Time   `json:"oldest_pending_at"`
	FailedLast24h   int          `json:"failed_last_24h"`
	LastError       *WorkerError `json:"last_error"`
}

func (s *Store) WorkerHealth(ctx context.Context) (WorkerHealth, error) {
	var h WorkerHealth
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM inbox_events WHERE status = 'pending')::int,
		(SELECT count(*) FROM outbox_jobs WHERE status = 'pending' AND run_after <= now())::int,
		(SELECT min(run_after) FROM outbox_jobs WHERE status = 'pending' AND run_after <= now()),
		((SELECT count(*) FROM outbox_jobs WHERE status = 'failed' AND updated_at > now() - interval '24 hours') +
		 (SELECT count(*) FROM inbox_events WHERE status = 'failed' AND received_at > now() - interval '24 hours'))::int`).
		Scan(&h.InboxPending, &h.OutboxPending, &h.OldestPendingAt, &h.FailedLast24h)
	if err != nil {
		return h, err
	}
	var e WorkerError
	err = s.pool.QueryRow(ctx, `SELECT source, kind, err, at FROM (
			(SELECT 'outbox' AS source, kind, last_error AS err, updated_at AS at FROM outbox_jobs WHERE last_error IS NOT NULL ORDER BY updated_at DESC LIMIT 1)
			UNION ALL
			(SELECT 'inbox', provider, last_error, coalesce(locked_until, received_at) FROM inbox_events WHERE last_error IS NOT NULL ORDER BY id DESC LIMIT 1)
		) t ORDER BY at DESC LIMIT 1`).Scan(&e.Source, &e.Kind, &e.Error, &e.At)
	if err == nil {
		e.Error = truncateRunes(RedactSecrets(e.Error), 240)
		h.LastError = &e
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return h, err
	}
	return h, nil
}
