package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
)

type Conversation struct {
	ID              int64
	IdentityID      int64
	ChatID          string
	State           string
	Fields          contract.Fields
	ActiveProjectID *int64
}

type Message struct {
	ID        int64
	Direction string // in ou out
	Kind      string
	Body      string
}

const conversationCols = `id, identity_id, chat_id, state, fields, active_project_id`

func scanConversation(row pgx.Row) (Conversation, error) {
	var c Conversation
	var raw []byte
	if err := row.Scan(&c.ID, &c.IdentityID, &c.ChatID, &c.State, &raw, &c.ActiveProjectID); err != nil {
		return Conversation{}, notFound(err)
	}
	if err := json.Unmarshal(raw, &c.Fields); err != nil {
		return Conversation{}, fmt.Errorf("store: fields inválidos: %w", err)
	}
	return c, nil
}

// GetOrCreateConversation devolve a conversa da identidade e atualiza o chat_id (vazio não sobrescreve).
func (s *Store) GetOrCreateConversation(ctx context.Context, identityID int64, chatID string) (Conversation, error) {
	return scanConversation(s.pool.QueryRow(ctx, `INSERT INTO conversations (identity_id, chat_id) VALUES ($1, $2)
		ON CONFLICT (identity_id) DO UPDATE
		SET chat_id = CASE WHEN EXCLUDED.chat_id = '' THEN conversations.chat_id ELSE EXCLUDED.chat_id END
		RETURNING `+conversationCols, identityID, chatID))
}

func (s *Store) GetConversation(ctx context.Context, id int64) (Conversation, error) {
	return scanConversation(s.pool.QueryRow(ctx, `SELECT `+conversationCols+` FROM conversations WHERE id = $1`, id))
}

func (s *Store) UpdateConversation(ctx context.Context, id int64, state string, f contract.Fields) error {
	return updateConversation(ctx, s.pool, id, state, f)
}

func updateConversation(ctx context.Context, q querier, id int64, state string, f contract.Fields) error {
	fields, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	// Estados de trabalho espelham campos e estágio no projeto ativo, na mesma instrução.
	// idle, onboarding e needs_human não mexem no projeto (ex.: aprovar zera a conversa, não o projeto).
	var n int
	err = q.QueryRow(ctx, `WITH conv AS (
			UPDATE conversations SET state = $2, fields = $3, updated_at = now() WHERE id = $1
			RETURNING active_project_id
		), proj AS (
			UPDATE projects SET fields = $3, stage = $2, updated_at = now()
			WHERE id = (SELECT active_project_id FROM conv)
			  AND $2::text IN ('collecting', 'awaiting_proposal', 'awaiting_approval')
			RETURNING 1
		)
		SELECT count(*) FROM conv`, id, state, string(fields)).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddMessage(ctx context.Context, convID int64, direction, kind, body string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO messages (conversation_id, direction, kind, body) VALUES ($1, $2, $3, $4)`,
		convID, direction, kind, body)
	return err
}

// RecentMessages devolve as últimas limit mensagens, da mais antiga para a mais nova.
func (s *Store) RecentMessages(ctx context.Context, convID int64, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, direction, kind, body FROM (
		SELECT id, direction, kind, body FROM messages WHERE conversation_id = $1 ORDER BY id DESC LIMIT $2
	) t ORDER BY id`, convID, limit)
	if err != nil {
		return nil, err
	}
	return collectMessages(rows)
}

func collectMessages(rows pgx.Rows) ([]Message, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var m Message
		err := r.Scan(&m.ID, &m.Direction, &m.Kind, &m.Body)
		return m, err
	})
}

// AddInbound grava a mensagem do cliente e devolve o id (âncora do debounce).
func (s *Store) AddInbound(ctx context.Context, convID int64, kind, body string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, direction, kind, body)
		VALUES ($1, 'in', $2, $3) RETURNING id`, convID, kind, body).Scan(&id)
	return id, err
}

// KindSkipped marca entradas gravadas sem resposta da IA (acima do limite diário, mídia que falhou):
// contam no limite, mas não entram no lote nem mudam qual mensagem é a mais nova.
const KindSkipped = "skipped"

// LatestInboundID é o id da mensagem de entrada mais nova da conversa, sem as ignoradas (0 se não houver).
func (s *Store) LatestInboundID(ctx context.Context, convID int64) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM messages
		WHERE conversation_id = $1 AND direction = 'in' AND kind <> 'skipped'`, convID).Scan(&id)
	return id, err
}

// HasPendingSends diz se a conversa ainda tem envios na fila (balões de uma resposta anterior).
func (s *Store) HasPendingSends(ctx context.Context, convID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM outbox_jobs
		WHERE status = 'pending' AND kind = 'whatsapp.send' AND (payload->>'conversation_id')::bigint = $1)`, convID).Scan(&ok)
	return ok, err
}

// PendingInbound devolve as mensagens de entrada ainda sem resposta até upTo, as limit mais novas,
// da mais antiga para a mais nova.
func (s *Store) PendingInbound(ctx context.Context, convID, upTo int64, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, direction, kind, body FROM (
		SELECT m.id, m.direction, m.kind, m.body FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1 AND m.direction = 'in' AND m.kind <> 'skipped'
		  AND m.id > c.replied_through AND m.id <= $2
		ORDER BY m.id DESC LIMIT $3
	) t ORDER BY id`, convID, upTo, limit)
	if err != nil {
		return nil, err
	}
	return collectMessages(rows)
}

// MarkReplied avança a marca de respondido; nunca volta para trás.
func (s *Store) MarkReplied(ctx context.Context, convID, upTo int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET replied_through = greatest(replied_through, $2) WHERE id = $1`, convID, upTo)
	return err
}

// ResetConversation devolve a conversa da identidade ao estado inicial (usado ao encerrar atendimento humano).
func (s *Store) ResetConversation(ctx context.Context, identityID int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE conversations SET state = 'idle', fields = '{}', updated_at = now() WHERE identity_id = $1`, identityID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
