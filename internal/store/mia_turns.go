package store

import (
	"context"
	"encoding/json"
	"time"
)

// MiaTurn é o que a Mia entendeu de uma mensagem do cliente e o que o sistema fez com isso.
type MiaTurn struct {
	ConversationID int64           `json:"-"`
	MessageID      int64           `json:"message_id"`
	Intent         string          `json:"intent"`
	Project        string          `json:"project"`
	Fields         json.RawMessage `json:"fields"`
	Profile        json.RawMessage `json:"profile"`
	Reply          string          `json:"reply"`
	Action         string          `json:"action"`
	CreatedAt      time.Time       `json:"created_at"`
}

// AddMiaTurn grava o turno.
func (s *Store) AddMiaTurn(ctx context.Context, t MiaTurn) error {
	var msg any
	if t.MessageID > 0 {
		msg = t.MessageID
	}
	nilJSON := func(b json.RawMessage) any {
		if len(b) == 0 || string(b) == "null" || string(b) == "{}" {
			return nil
		}
		return string(b)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO mia_turns (conversation_id, message_id, intent, project, fields, profile, reply, action)
		VALUES ($1, $2, $3, $4, $5, $6, left($7, 2000), left($8, 500))`,
		t.ConversationID, msg, t.Intent, t.Project, nilJSON(t.Fields), nilJSON(t.Profile), t.Reply, t.Action)
	return err
}

// MiaTurns devolve os turnos recentes da conversa (do mais antigo para o mais novo).
func (s *Store) MiaTurns(ctx context.Context, convID int64) ([]MiaTurn, error) {
	rows, err := s.pool.Query(ctx, `SELECT coalesce(message_id, 0), intent, project, fields, profile, reply, action, created_at FROM (
			SELECT * FROM mia_turns WHERE conversation_id = $1 ORDER BY id DESC LIMIT 300
		) t ORDER BY id`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MiaTurn{}
	for rows.Next() {
		var t MiaTurn
		if err := rows.Scan(&t.MessageID, &t.Intent, &t.Project, &t.Fields, &t.Profile, &t.Reply, &t.Action, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PurgeMiaTurns apaga os turnos mais velhos que before.
func (s *Store) PurgeMiaTurns(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM mia_turns WHERE created_at < $1`, before)
	return tag.RowsAffected(), err
}
