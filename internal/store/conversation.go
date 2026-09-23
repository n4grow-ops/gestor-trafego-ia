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
	rows, err := s.pool.Query(ctx, `SELECT direction, kind, body FROM (
		SELECT id, direction, kind, body FROM messages WHERE conversation_id = $1 ORDER BY id DESC LIMIT $2
	) t ORDER BY id`, convID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Message, error) {
		var m Message
		err := r.Scan(&m.Direction, &m.Kind, &m.Body)
		return m, err
	})
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
