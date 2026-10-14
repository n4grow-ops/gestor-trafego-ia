package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ShieldState é o último estado de saúde da conta de anúncios de uma conexão (escudo anti-bloqueio).
type ShieldState struct {
	ConnectionID  int64           `json:"connection_id"`
	OrgID         int64           `json:"org_id"`
	State         string          `json:"state"`
	AccountStatus int             `json:"account_status"`
	DisableReason int             `json:"disable_reason"`
	NotifiedState string          `json:"notified_state"`
	NotifiedAt    *time.Time      `json:"notified_at"`
	CheckedAt     time.Time       `json:"checked_at"`
	ChangedAt     time.Time       `json:"changed_at"`
	Report        json.RawMessage `json:"report"`
}

const shieldCols = `connection_id, organization_id, state, account_status, disable_reason, notified_state, notified_at, checked_at, changed_at, report`

func scanShield(row pgx.Row) (ShieldState, error) {
	var st ShieldState
	err := row.Scan(&st.ConnectionID, &st.OrgID, &st.State, &st.AccountStatus, &st.DisableReason, &st.NotifiedState,
		&st.NotifiedAt, &st.CheckedAt, &st.ChangedAt, &st.Report)
	return st, err
}

// ShieldStateFor devolve o último estado guardado da conexão; ok=false se nunca foi checada.
func (s *Store) ShieldStateFor(ctx context.Context, connID int64) (ShieldState, bool, error) {
	st, err := scanShield(s.pool.QueryRow(ctx, `SELECT `+shieldCols+` FROM shield_account_state WHERE connection_id = $1`, connID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ShieldState{}, false, nil
	}
	return st, err == nil, err
}

// ShieldStateByOrg devolve o estado da conexão ativa da organização (painel do cliente).
func (s *Store) ShieldStateByOrg(ctx context.Context, orgID int64) (ShieldState, bool, error) {
	st, err := scanShield(s.pool.QueryRow(ctx, `SELECT sa.connection_id, sa.organization_id, sa.state, sa.account_status, sa.disable_reason,
			sa.notified_state, sa.notified_at, sa.checked_at, sa.changed_at, sa.report
		FROM shield_account_state sa JOIN meta_connections mc ON mc.id = sa.connection_id
		WHERE sa.organization_id = $1 AND mc.status = 'active'`, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ShieldState{}, false, nil
	}
	return st, err == nil, err
}

// SaveShieldState grava a leitura de agora. O notified_state não muda aqui (só MarkShieldNotified);
// changed_at só anda quando o estado muda.
func (s *Store) SaveShieldState(ctx context.Context, connID, orgID int64, state string, accountStatus, disableReason int, report any, at time.Time) error {
	var raw any
	if report != nil {
		b, err := json.Marshal(report)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO shield_account_state AS t
			(connection_id, organization_id, state, account_status, disable_reason, checked_at, changed_at, report)
		VALUES ($1, $2, $3, $4, $5, $6, $6, $7::jsonb)
		ON CONFLICT (connection_id) DO UPDATE SET
			state = EXCLUDED.state, account_status = EXCLUDED.account_status, disable_reason = EXCLUDED.disable_reason,
			checked_at = EXCLUDED.checked_at, report = coalesce(EXCLUDED.report, t.report),
			changed_at = CASE WHEN t.state <> EXCLUDED.state THEN EXCLUDED.changed_at ELSE t.changed_at END`,
		connID, orgID, state, accountStatus, disableReason, at, raw)
	return err
}

// MarkShieldNotified registra de que estado o cliente foi avisado ('ok' = nada pendente).
func (s *Store) MarkShieldNotified(ctx context.Context, connID int64, state string, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE shield_account_state SET notified_state = $2, notified_at = $3 WHERE connection_id = $1`, connID, state, at)
	return err
}

// ShieldTarget é uma conexão ativa a conferir.
type ShieldTarget struct {
	ConnectionID  int64
	OrgID         int64
	AdAccountID   string
	AdAccountName string
	PageID        string
	InstagramID   string
	Currency      string
}

// ShieldTargets lista as conexões ativas com token guardado.
func (s *Store) ShieldTargets(ctx context.Context) ([]ShieldTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, organization_id, coalesce(ad_account_id, ''), coalesce(ad_account_name, ''),
			coalesce(page_id, ''), coalesce(instagram_id, ''), coalesce(currency, '')
		FROM meta_connections WHERE status = 'active' AND token_ciphertext IS NOT NULL AND ad_account_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShieldTarget
	for rows.Next() {
		var t ShieldTarget
		if err := rows.Scan(&t.ConnectionID, &t.OrgID, &t.AdAccountID, &t.AdAccountName, &t.PageID, &t.InstagramID, &t.Currency); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RejectedPublication é uma publicação reprovada pela Meta.
type RejectedPublication struct {
	At     time.Time
	Reason string
}

// RecentRejected lista as reprovações da organização desde o instante dado (a mais nova primeiro).
func (s *Store) RecentRejected(ctx context.Context, orgID int64, since time.Time) ([]RejectedPublication, error) {
	rows, err := s.pool.Query(ctx, `SELECT updated_at, coalesce(error, '') FROM meta_publications
		WHERE organization_id = $1 AND status = 'rejected' AND updated_at >= $2 ORDER BY updated_at DESC LIMIT 20`, orgID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RejectedPublication
	for rows.Next() {
		var r RejectedPublication
		if err := rows.Scan(&r.At, &r.Reason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
