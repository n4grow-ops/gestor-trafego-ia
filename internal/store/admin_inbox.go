package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNotHuman: a conversa não está em atendimento humano (a IA está respondendo).
var ErrNotHuman = errors.New("store: conversa não está em atendimento humano")

// ErrNotYours: a conversa está em atendimento humano, mas quem assumiu foi outra pessoa (ou ninguém).
var ErrNotYours = errors.New("store: conversa assumida por outra pessoa")

// KindStaff marca a mensagem escrita por alguém da equipe pelo painel.
const KindStaff = "staff"

// InboxItem é uma conversa na caixa de entrada do painel.
type InboxItem struct {
	ConversationID int64      `json:"conversation_id"`
	OrgID          int64      `json:"org_id"`
	OrgName        string     `json:"org_name"`
	ClientName     string     `json:"client_name"`
	Phone          string     `json:"phone"`
	State          string     `json:"state"`
	NeedsHuman     bool       `json:"needs_human"`
	HumanBy        string     `json:"human_by"`
	LastBody       string     `json:"last_body"`
	LastKind       string     `json:"last_kind"`
	LastDirection  string     `json:"last_direction"`
	LastAt         *time.Time `json:"last_at"`
	Waiting        int        `json:"waiting"` // mensagens do cliente depois da última resposta
	Blocked        bool       `json:"blocked"`
}

// InboxFilter: Filter vazio (todas), "humano" ou "sem_resposta"; Q busca nome, negócio ou telefone.
type InboxFilter struct {
	Filter string
	Q      string
	Limit  int
}

const inboxSelect = `SELECT c.id, o.id, coalesce(nullif(o.trade_name, ''), o.name), coalesce(nullif(m.full_name, ''), m.name), wi.phone_e164,
	c.state, coalesce(c.human_by, ''), coalesce(lm.body, ''), coalesce(lm.kind, ''), coalesce(lm.direction, ''), lm.created_at,
	(SELECT count(*) FROM messages x WHERE x.conversation_id = c.id AND x.direction = 'in' AND x.id > coalesce(lo.id, 0))::int,
	o.status = 'blocked'
	FROM conversations c
	JOIN whatsapp_identities wi ON wi.id = c.identity_id
	JOIN members m ON m.id = wi.member_id
	JOIN organizations o ON o.id = m.organization_id
	LEFT JOIN LATERAL (SELECT body, kind, direction, created_at FROM messages WHERE conversation_id = c.id ORDER BY id DESC LIMIT 1) lm ON true
	LEFT JOIN LATERAL (SELECT id FROM messages WHERE conversation_id = c.id AND direction = 'out' ORDER BY id DESC LIMIT 1) lo ON true`

func (s *Store) Inbox(ctx context.Context, f InboxFilter) ([]InboxItem, error) {
	if f.Limit <= 0 || f.Limit > 300 {
		f.Limit = 150
	}
	var where []string
	args := []any{}
	switch f.Filter {
	case "humano":
		where = append(where, "c.state = 'needs_human'")
	case "sem_resposta":
		where = append(where, "lm.direction = 'in'")
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		args = append(args, "%"+likeEscape(q)+"%")
		n := fmt.Sprint(len(args))
		where = append(where, "(o.name ILIKE $"+n+" OR coalesce(o.trade_name, '') ILIKE $"+n+" OR coalesce(m.full_name, m.name) ILIKE $"+n+" OR wi.phone_e164 LIKE $"+n+")")
	}
	sql := inboxSelect
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit)
	sql += fmt.Sprintf(" ORDER BY c.state = 'needs_human' DESC, lm.created_at DESC NULLS LAST, c.id DESC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxItem, error) {
		var it InboxItem
		err := r.Scan(&it.ConversationID, &it.OrgID, &it.OrgName, &it.ClientName, &it.Phone, &it.State, &it.HumanBy,
			&it.LastBody, &it.LastKind, &it.LastDirection, &it.LastAt, &it.Waiting, &it.Blocked)
		it.NeedsHuman = it.State == StateNeedsHuman
		it.LastBody = truncateRunes(it.LastBody, 140)
		return it, err
	})
}

// InboxCounts são os números do menu e dos filtros da caixa de entrada.
type InboxCounts struct {
	NeedsHuman int `json:"needs_human"` // em atendimento humano ou pedindo
	Unassigned int `json:"unassigned"`  // pediram humano e ninguém assumiu
	Waiting    int `json:"waiting"`     // última mensagem é do cliente
}

func (s *Store) InboxCounts(ctx context.Context) (InboxCounts, error) {
	var c InboxCounts
	err := s.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE c.state = 'needs_human'),
		count(*) FILTER (WHERE c.state = 'needs_human' AND c.human_by IS NULL),
		count(*) FILTER (WHERE lm.direction = 'in')
		FROM conversations c
		LEFT JOIN LATERAL (SELECT direction FROM messages WHERE conversation_id = c.id ORDER BY id DESC LIMIT 1) lm ON true`).
		Scan(&c.NeedsHuman, &c.Unassigned, &c.Waiting)
	return c, err
}

// ThreadMessage é uma mensagem da conversa no painel.
type ThreadMessage struct {
	ID        int64     `json:"id"`
	Direction string    `json:"direction"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

// PendingSend é uma mensagem da equipe ainda na fila (ou que falhou).
type PendingSend struct {
	JobID     int64     `json:"job_id"`
	Text      string    `json:"text"`
	Author    string    `json:"author"`
	Status    string    `json:"status"` // pending ou failed
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"created_at"`
}

// Thread é a conversa aberta no painel.
type Thread struct {
	ConversationID int64           `json:"conversation_id"`
	OrgID          int64           `json:"org_id"`
	OrgName        string          `json:"org_name"`
	ClientName     string          `json:"client_name"`
	Phone          string          `json:"phone"`
	State          string          `json:"state"`
	HumanBy        string          `json:"human_by"`
	HumanSince     *time.Time      `json:"human_since"`
	LastInboundAt  *time.Time      `json:"last_inbound_at"`
	ProjectName    string          `json:"project_name"`
	ProjectStage   string          `json:"project_stage"`
	Blocked        bool            `json:"blocked"`
	Messages       []ThreadMessage `json:"messages"`
	Pending        []PendingSend   `json:"pending"`
}

// Thread devolve a conversa; com after > 0, só as mensagens com id maior (atualização ao vivo).
func (s *Store) Thread(ctx context.Context, convID, after int64) (Thread, error) {
	var t Thread
	err := s.pool.QueryRow(ctx, `SELECT c.id, o.id, coalesce(nullif(o.trade_name, ''), o.name), coalesce(nullif(m.full_name, ''), m.name),
			wi.phone_e164, c.state, coalesce(c.human_by, ''), c.human_since,
			(SELECT max(created_at) FROM messages WHERE conversation_id = c.id AND direction = 'in'),
			coalesce(p.name, ''), coalesce(p.stage, ''), o.status = 'blocked'
		FROM conversations c
		JOIN whatsapp_identities wi ON wi.id = c.identity_id
		JOIN members m ON m.id = wi.member_id
		JOIN organizations o ON o.id = m.organization_id
		LEFT JOIN projects p ON p.id = c.active_project_id
		WHERE c.id = $1`, convID).Scan(&t.ConversationID, &t.OrgID, &t.OrgName, &t.ClientName, &t.Phone, &t.State,
		&t.HumanBy, &t.HumanSince, &t.LastInboundAt, &t.ProjectName, &t.ProjectStage, &t.Blocked)
	if err != nil {
		return Thread{}, notFound(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT id, direction, kind, body, coalesce(author, ''), created_at FROM (
			SELECT * FROM messages WHERE conversation_id = $1 AND id > $2 ORDER BY id DESC LIMIT 300
		) t ORDER BY id`, convID, after)
	if err != nil {
		return Thread{}, err
	}
	t.Messages, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ThreadMessage, error) {
		var m ThreadMessage
		err := r.Scan(&m.ID, &m.Direction, &m.Kind, &m.Body, &m.Author, &m.CreatedAt)
		return m, err
	})
	if err != nil {
		return Thread{}, err
	}
	// Envios da equipe ainda não entregues; os que falharam ficam 24 h à vista.
	rows, err = s.pool.Query(ctx, `SELECT id, payload->>'text', payload->>'author', status, coalesce(last_error, ''), created_at
		FROM outbox_jobs
		WHERE kind = 'whatsapp.send' AND (payload->>'conversation_id')::bigint = $1 AND payload ? 'author'
		  AND (status = 'pending' OR (status = 'failed' AND updated_at > now() - interval '24 hours'))
		ORDER BY id`, convID)
	if err != nil {
		return Thread{}, err
	}
	t.Pending, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingSend, error) {
		var p PendingSend
		err := r.Scan(&p.JobID, &p.Text, &p.Author, &p.Status, &p.Error, &p.CreatedAt)
		p.Error = truncateRunes(p.Error, 200)
		return p, err
	})
	return t, err
}

// TakeOverConversation põe a conversa em atendimento humano: a IA para de responder.
// Devolve o estado anterior (o mesmo da primeira vez, se alguém já tinha assumido).
func (s *Store) TakeOverConversation(ctx context.Context, convID int64, by string) (string, error) {
	unlock, err := s.LockConversation(ctx, convID)
	if err != nil {
		return "", err
	}
	defer unlock()
	var prev string
	err = s.pool.QueryRow(ctx, `UPDATE conversations c SET
			human_prev_state = CASE WHEN c.state = 'needs_human' THEN c.human_prev_state ELSE c.state END,
			state = 'needs_human', human_by = $2, human_since = now(), updated_at = now(),
			-- o que o cliente já mandou fica com a equipe: a IA não responde isso depois
			replied_through = greatest(c.replied_through, (SELECT coalesce(max(id), 0) FROM messages WHERE conversation_id = c.id AND direction = 'in'))
		FROM conversations old WHERE old.id = c.id AND c.id = $1
		RETURNING old.state`, convID, by).Scan(&prev)
	return prev, notFound(err)
}

// ReleaseConversation devolve a conversa à IA e diz o estado em que ela ficou. Volta ao estado de antes
// de a equipe assumir; esperando proposta vira coleta (o job da proposta já desistiu). Sem estado guardado
// (o próprio engine escalou), faz como o resolve-human: começa do zero.
func (s *Store) ReleaseConversation(ctx context.Context, convID int64) (string, error) {
	unlock, err := s.LockConversation(ctx, convID)
	if err != nil {
		return "", err
	}
	defer unlock()
	var state string
	err = s.pool.QueryRow(ctx, `UPDATE conversations SET
			state = CASE
				WHEN human_prev_state IN ('idle', 'onboarding', 'collecting', 'awaiting_approval', 'awaiting_creatives') THEN human_prev_state
				WHEN human_prev_state = 'awaiting_proposal' THEN 'collecting'
				ELSE 'idle' END,
			fields = CASE WHEN human_prev_state IS NULL THEN '{}'::jsonb ELSE fields END,
			human_by = NULL, human_since = NULL, human_prev_state = NULL, updated_at = now(),
			replied_through = greatest(replied_through, (SELECT coalesce(max(id), 0) FROM messages WHERE conversation_id = $1 AND direction = 'in'))
		WHERE id = $1 AND state = 'needs_human' RETURNING state`, convID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, gerr := s.GetConversation(ctx, convID); gerr != nil {
			return "", gerr
		}
		return "", ErrNotHuman
	}
	return state, err
}

// EnqueueStaffMessage enfileira o envio de uma mensagem da equipe. Só com a conversa em atendimento
// humano (com a IA respondendo, as duas falariam ao mesmo tempo) e só por quem a assumiu (email).
func (s *Store) EnqueueStaffMessage(ctx context.Context, convID int64, email string, job func(chatID string) JobSpec) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var state, chatID, humanBy string
	if err := tx.QueryRow(ctx, `SELECT state, chat_id, coalesce(human_by, '') FROM conversations WHERE id = $1 FOR UPDATE`, convID).Scan(&state, &chatID, &humanBy); err != nil {
		return notFound(err)
	}
	if state != StateNeedsHuman {
		return ErrNotHuman
	}
	if humanBy == "" || humanBy != email {
		return ErrNotYours
	}
	if chatID == "" {
		return fmt.Errorf("%w: conversa sem destino no WhatsApp", ErrInvalidInput)
	}
	if err := enqueue(ctx, tx, job(chatID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AddStaffMessage grava a mensagem da equipe depois de entregue (chamado pelo worker).
func (s *Store) AddStaffMessage(ctx context.Context, convID int64, body, author string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO messages (conversation_id, direction, kind, body, author) VALUES ($1, 'out', 'staff', $2, $3)`,
		convID, body, author)
	return err
}

// likeEscape protege % e _ digitados na busca.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
