package store

import (
	"context"
	"time"
)

// SetMetaTokenExpiry grava quando o acesso da conexão vence (zero = não vence).
func (s *Store) SetMetaTokenExpiry(ctx context.Context, connID int64, at time.Time) error {
	var v any
	if !at.IsZero() {
		v = at
	}
	_, err := s.pool.Exec(ctx, `UPDATE meta_connections SET token_expires_at = $2, token_warned_at = NULL WHERE id = $1`, connID, v)
	return err
}

// ExpiringMeta é uma conexão ativa que vence logo (ou já venceu) e ainda não teve o aviso dessa fase.
type ExpiringMeta struct {
	ConnectionID int64
	OrgID        int64
	ExpiresAt    time.Time
}

// ExpiringMetaConnections: ativas que vencem antes de before e não foram avisadas; as já vencidas são avisadas
// de novo uma vez (aviso dado antes do vencimento).
func (s *Store) ExpiringMetaConnections(ctx context.Context, now, before time.Time) ([]ExpiringMeta, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, organization_id, token_expires_at FROM meta_connections
		WHERE status = 'active' AND token_expires_at IS NOT NULL AND token_expires_at < $2
		  AND (token_warned_at IS NULL OR (token_expires_at <= $1 AND token_warned_at < token_expires_at))
		ORDER BY token_expires_at`, now, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExpiringMeta
	for rows.Next() {
		var m ExpiringMeta
		if err := rows.Scan(&m.ConnectionID, &m.OrgID, &m.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkMetaTokenWarned registra o aviso de vencimento.
func (s *Store) MarkMetaTokenWarned(ctx context.Context, connID int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE meta_connections SET token_warned_at = $2 WHERE id = $1`, connID, at)
	return err
}

// OwnerConversation é a conversa mais recente do dono da organização (para avisos do sistema).
func (s *Store) OwnerConversation(ctx context.Context, orgID int64) (Conversation, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT c.id FROM conversations c
		JOIN whatsapp_identities w ON w.id = c.identity_id
		JOIN members m ON m.id = w.member_id
		WHERE m.organization_id = $1
		ORDER BY (m.role = 'owner') DESC, c.updated_at DESC LIMIT 1`, orgID).Scan(&id)
	if err != nil {
		return Conversation{}, notFound(err)
	}
	return s.GetConversation(ctx, id)
}

// MetaWithoutExpiry lista as organizações com conexão ativa sem validade lida (conexões antigas).
func (s *Store) MetaWithoutExpiry(ctx context.Context) ([]ExpiringMeta, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, organization_id FROM meta_connections
		WHERE status = 'active' AND token_expires_at IS NULL AND token_ciphertext IS NOT NULL LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExpiringMeta
	for rows.Next() {
		var m ExpiringMeta
		if err := rows.Scan(&m.ConnectionID, &m.OrgID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
