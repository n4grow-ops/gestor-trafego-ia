package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NotifyJobKind é o job de entrega de uma notificação a um destino (pacote notify). Fica aqui porque
// o store enfileira as entregas na mesma transação do evento.
const NotifyJobKind = "notify.deliver"

// NotifyKeepDeliveries é quantas entregas ficam guardadas por destino.
const NotifyKeepDeliveries = 100

// NotifyDeliveryAttempts: a primeira tentativa e mais duas, com espera crescente da fila (30 s, 60 s).
const NotifyDeliveryAttempts = 3

// NotifyDestination é um destino de notificações. URL é segredo: nunca vai para log nem para a listagem.
type NotifyDestination struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	URL       string    `json:"-"`
	Events    []string  `json:"events"`
	Active    bool      `json:"active"`
	TitleTpl  string    `json:"title_tpl"`
	TextTpl   string    `json:"text_tpl"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Last *NotifyDelivery `json:"last_delivery,omitempty"` // só na listagem
}

// NotifyDelivery é uma tentativa de entrega.
type NotifyDelivery struct {
	ID            int64     `json:"id"`
	DestinationID int64     `json:"destination_id"`
	Event         string    `json:"event"`
	Attempt       int       `json:"attempt"`
	OK            bool      `json:"ok"`
	StatusCode    int       `json:"status_code"`
	Error         string    `json:"error"`
	CreatedAt     time.Time `json:"created_at"`
}

// NotifyDeliverJob é o payload do job notify.deliver.
type NotifyDeliverJob struct {
	DestinationID int64             `json:"destination_id"`
	Event         string            `json:"event"`
	Data          map[string]string `json:"data"`
	At            time.Time         `json:"at"`
}

const notifyDestCols = `d.id, d.name, d.kind, d.url, d.events, d.active, d.title_tpl, d.text_tpl, d.created_by, d.created_at, d.updated_at`

func scanNotifyDest(row pgx.Row, extra ...any) (NotifyDestination, error) {
	var d NotifyDestination
	dst := append([]any{&d.ID, &d.Name, &d.Kind, &d.URL, &d.Events, &d.Active, &d.TitleTpl, &d.TextTpl, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt}, extra...)
	if err := row.Scan(dst...); err != nil {
		return NotifyDestination{}, notFound(err)
	}
	return d, nil
}

// NotifyDestinations lista os destinos com a última entrega de cada um.
func (s *Store) NotifyDestinations(ctx context.Context) ([]NotifyDestination, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+notifyDestCols+`,
			l.id, l.event, l.attempt, l.ok, l.status_code, l.error, l.created_at
		FROM notify_destinations d
		LEFT JOIN LATERAL (SELECT * FROM notify_deliveries x WHERE x.destination_id = d.id ORDER BY x.id DESC LIMIT 1) l ON true
		ORDER BY d.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (NotifyDestination, error) {
		var (
			lid           *int64
			lev, lerr     *string
			latt, lstatus *int
			lok           *bool
			lat           *time.Time
		)
		d, err := scanNotifyDest(r, &lid, &lev, &latt, &lok, &lstatus, &lerr, &lat)
		if err != nil {
			return d, err
		}
		if lid != nil {
			d.Last = &NotifyDelivery{ID: *lid, DestinationID: d.ID, Event: *lev, Attempt: *latt, OK: *lok, StatusCode: *lstatus, Error: *lerr, CreatedAt: *lat}
		}
		return d, nil
	})
}

func (s *Store) NotifyDestination(ctx context.Context, id int64) (NotifyDestination, error) {
	return scanNotifyDest(s.pool.QueryRow(ctx, `SELECT `+notifyDestCols+` FROM notify_destinations d WHERE d.id = $1`, id))
}

// NotifyDestinationInput é o que o painel grava. URL vazia na edição mantém a atual.
type NotifyDestinationInput struct {
	Name     string
	Kind     string
	URL      string
	Events   []string
	Active   bool
	TitleTpl string
	TextTpl  string
	By       string
}

func (s *Store) CreateNotifyDestination(ctx context.Context, in NotifyDestinationInput) (NotifyDestination, error) {
	if in.URL == "" {
		return NotifyDestination{}, fmt.Errorf("%w: url", ErrInvalidInput)
	}
	return scanNotifyDest(s.pool.QueryRow(ctx, `INSERT INTO notify_destinations AS d (name, kind, url, events, active, title_tpl, text_tpl, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+notifyDestCols,
		in.Name, in.Kind, in.URL, notNilStrings(in.Events), in.Active, in.TitleTpl, in.TextTpl, in.By))
}

func (s *Store) UpdateNotifyDestination(ctx context.Context, id int64, in NotifyDestinationInput) (NotifyDestination, error) {
	return scanNotifyDest(s.pool.QueryRow(ctx, `UPDATE notify_destinations AS d SET name = $2, kind = $3, url = coalesce(nullif($4, ''), d.url),
			events = $5, active = $6, title_tpl = $7, text_tpl = $8, updated_at = now()
		WHERE d.id = $1 RETURNING `+notifyDestCols,
		id, in.Name, in.Kind, in.URL, notNilStrings(in.Events), in.Active, in.TitleTpl, in.TextTpl))
}

func (s *Store) SetNotifyDestinationActive(ctx context.Context, id int64, active bool) (NotifyDestination, error) {
	return scanNotifyDest(s.pool.QueryRow(ctx, `UPDATE notify_destinations AS d SET active = $2, updated_at = now()
		WHERE d.id = $1 RETURNING `+notifyDestCols, id, active))
}

func (s *Store) DeleteNotifyDestination(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notify_destinations WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// RecordNotifyDelivery grava a tentativa e apaga as mais velhas que as últimas 100 do destino.
func (s *Store) RecordNotifyDelivery(ctx context.Context, d NotifyDelivery) error {
	if len(d.Error) > 300 {
		d.Error = d.Error[:300]
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO notify_deliveries (destination_id, event, attempt, ok, status_code, error)
		SELECT id, $2, $3, $4, $5, $6 FROM notify_destinations WHERE id = $1`,
		d.DestinationID, d.Event, max(d.Attempt, 1), d.OK, d.StatusCode, d.Error)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil // destino apagado no meio da entrega
	}
	if _, err := tx.Exec(ctx, `DELETE FROM notify_deliveries WHERE destination_id = $1 AND id < (
			SELECT min(id) FROM (SELECT id FROM notify_deliveries WHERE destination_id = $1 ORDER BY id DESC LIMIT $2) k)`,
		d.DestinationID, NotifyKeepDeliveries); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NotifyDeliveries devolve as últimas entregas do destino, da mais nova para a mais velha.
func (s *Store) NotifyDeliveries(ctx context.Context, destID int64, limit int) ([]NotifyDelivery, error) {
	if limit <= 0 || limit > NotifyKeepDeliveries {
		limit = NotifyKeepDeliveries
	}
	rows, err := s.pool.Query(ctx, `SELECT id, destination_id, event, attempt, ok, status_code, error, created_at
		FROM notify_deliveries WHERE destination_id = $1 ORDER BY id DESC LIMIT $2`, destID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (NotifyDelivery, error) {
		var d NotifyDelivery
		err := r.Scan(&d.ID, &d.DestinationID, &d.Event, &d.Attempt, &d.OK, &d.StatusCode, &d.Error, &d.CreatedAt)
		return d, err
	})
}

// EmitNotify enfileira uma entrega para cada destino ligado que assina o evento. Sem destino, não
// grava nada. Nunca espera a entrega: quem chama segue na hora.
func (s *Store) EmitNotify(ctx context.Context, event string, data map[string]string) error {
	return emitNotify(ctx, s.pool, event, data)
}

// emitNotify é o EmitNotify dentro de uma transação (a entrega só existe se o evento for gravado).
func emitNotify(ctx context.Context, q querier, event string, data map[string]string) error {
	if data == nil {
		data = map[string]string{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	_, err = q.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload, max_attempts, run_after)
		SELECT $1, jsonb_build_object('destination_id', d.id, 'event', $2::text, 'data', $3::jsonb, 'at', now()), $4, now()
		FROM notify_destinations d WHERE d.active AND $2 = ANY (d.events)`,
		NotifyJobKind, event, string(raw), NotifyDeliveryAttempts)
	return err
}

// orgNotifyData: nome da pessoa, empresa e WhatsApp do dono da organização, para os eventos.
func orgNotifyData(ctx context.Context, q querier, orgID int64) (map[string]string, error) {
	var nome, empresa, phone string
	err := q.QueryRow(ctx, `SELECT coalesce(nullif(m.full_name, ''), m.name, ''), o.name, coalesce(wi.phone_e164, '')
		FROM organizations o
		LEFT JOIN LATERAL (SELECT * FROM members x WHERE x.organization_id = o.id ORDER BY (x.role = 'owner') DESC, x.id LIMIT 1) m ON true
		LEFT JOIN LATERAL (SELECT phone_e164 FROM whatsapp_identities w WHERE w.member_id = m.id ORDER BY w.id LIMIT 1) wi ON true
		WHERE o.id = $1`, orgID).Scan(&nome, &empresa, &phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if nome == "" || nome == "Cliente" {
		nome = empresa
	}
	return map[string]string{"nome": nome, "empresa": empresa, "whatsapp": phone}, nil
}

// OrgNotifyData é o orgNotifyData fora de transação.
func (s *Store) OrgNotifyData(ctx context.Context, orgID int64) (map[string]string, error) {
	return orgNotifyData(ctx, s.pool, orgID)
}

// FormatBRL escreve centavos como "R$ 1.234,56".
func FormatBRL(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	reais, cent := cents/100, cents%100
	s := fmt.Sprintf("%d", reais)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	out := fmt.Sprintf("R$ %s,%02d", s, cent)
	if neg {
		out = "-" + out
	}
	return out
}

/* ---------- token do POST /api/hooks/venda ---------- */

// NotifySaleToken é a situação do token (o token em si só aparece uma vez, ao gerar).
type NotifySaleToken struct {
	Exists    bool       `json:"exists"`
	Last4     string     `json:"last4,omitempty"`
	CreatedBy string     `json:"created_by,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
}

func (s *Store) NotifySaleTokenStatus(ctx context.Context) (NotifySaleToken, error) {
	var t NotifySaleToken
	var at time.Time
	err := s.pool.QueryRow(ctx, `SELECT last4, created_by, created_at, used_at FROM notify_sale_token WHERE id = 1`).
		Scan(&t.Last4, &t.CreatedBy, &at, &t.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotifySaleToken{}, nil
	}
	if err != nil {
		return NotifySaleToken{}, err
	}
	t.Exists, t.CreatedAt = true, &at
	return t, nil
}

// SetNotifySaleToken troca o token (o anterior para de valer na hora).
func (s *Store) SetNotifySaleToken(ctx context.Context, hash, last4, by string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO notify_sale_token (id, token_hash, last4, created_by) VALUES (1, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET token_hash = excluded.token_hash, last4 = excluded.last4,
			created_by = excluded.created_by, created_at = now(), used_at = NULL`, hash, last4, by)
	return err
}

// UseNotifySaleToken confere o hash e marca o uso; false se não confere ou não há token.
func (s *Store) UseNotifySaleToken(ctx context.Context, hash string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE notify_sale_token SET used_at = now() WHERE id = 1 AND token_hash = $1`, hash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func notNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
