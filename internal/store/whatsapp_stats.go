package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// DayCount são as mensagens de um dia (fuso de Brasília).
type DayCount struct {
	Day string `json:"day"` // AAAA-MM-DD
	In  int    `json:"in"`
	Out int    `json:"out"`
}

// FailedSend é um envio que esgotou as tentativas.
type FailedSend struct {
	JobID   int64     `json:"job_id"`
	OrgName string    `json:"org_name"`
	Preview string    `json:"preview"`
	Error   string    `json:"error"`
	At      time.Time `json:"at"`
}

// WhatsAppStats é o que a tela do WhatsApp mostra além do estado da conexão.
type WhatsAppStats struct {
	Statuses []WhatsAppStatus `json:"statuses"`
	Days     []DayCount       `json:"days"`
	Failed   []FailedSend     `json:"failed"`
}

func (s *Store) WhatsAppStats(ctx context.Context) (WhatsAppStats, error) {
	var w WhatsAppStats
	rows, err := s.pool.Query(ctx, `SELECT provider, connected, coalesce(code, 0), coalesce(reason, ''), updated_at FROM whatsapp_status ORDER BY provider`)
	if err != nil {
		return w, err
	}
	if w.Statuses, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (WhatsAppStatus, error) {
		var st WhatsAppStatus
		err := r.Scan(&st.Provider, &st.Connected, &st.Code, &st.Reason, &st.UpdatedAt)
		return st, err
	}); err != nil {
		return w, err
	}
	rows, err = s.pool.Query(ctx, `WITH d AS (
			SELECT generate_series((now() AT TIME ZONE 'America/Sao_Paulo')::date - 6, (now() AT TIME ZONE 'America/Sao_Paulo')::date, '1 day')::date AS day
		)
		SELECT to_char(d.day, 'YYYY-MM-DD'),
			count(m.id) FILTER (WHERE m.direction = 'in')::int, count(m.id) FILTER (WHERE m.direction = 'out')::int
		FROM d LEFT JOIN messages m ON m.created_at >= (d.day::timestamp AT TIME ZONE 'America/Sao_Paulo')
			AND m.created_at < ((d.day + 1)::timestamp AT TIME ZONE 'America/Sao_Paulo')
		GROUP BY d.day ORDER BY d.day`)
	if err != nil {
		return w, err
	}
	if w.Days, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (DayCount, error) {
		var d DayCount
		err := r.Scan(&d.Day, &d.In, &d.Out)
		return d, err
	}); err != nil {
		return w, err
	}
	rows, err = s.pool.Query(ctx, `SELECT j.id, coalesce(nullif(o.trade_name, ''), o.name, 'equipe'), coalesce(j.payload->>'text', ''),
			coalesce(j.last_error, ''), j.updated_at
		FROM outbox_jobs j
		LEFT JOIN conversations c ON c.id = CASE WHEN (j.payload->>'conversation_id') ~ '^[0-9]+$' THEN (j.payload->>'conversation_id')::bigint END
		LEFT JOIN whatsapp_identities wi ON wi.id = c.identity_id
		LEFT JOIN members m ON m.id = wi.member_id
		LEFT JOIN organizations o ON o.id = m.organization_id
		WHERE j.kind = 'whatsapp.send' AND j.status = 'failed' AND j.updated_at > now() - interval '24 hours'
		ORDER BY j.updated_at DESC LIMIT 20`)
	if err != nil {
		return w, err
	}
	w.Failed, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (FailedSend, error) {
		var f FailedSend
		err := r.Scan(&f.JobID, &f.OrgName, &f.Preview, &f.Error, &f.At)
		f.Preview = truncateRunes(redactLinkTokens(f.Preview), 80)
		f.Error = truncateRunes(RedactSecrets(f.Error), 160)
		return f, err
	})
	return w, err
}
