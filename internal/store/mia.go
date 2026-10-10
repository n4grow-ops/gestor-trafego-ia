package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
)

// ErrConflict: outra pessoa salvou uma versão nova enquanto esta era editada.
var ErrConflict = errors.New("store: a configuração mudou enquanto você editava")

// MiaVersion é uma linha do histórico da configuração da Mia.
type MiaVersion struct {
	Version   int       `json:"version"`
	Note      string    `json:"note"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// MiaCurrent devolve a configuração em vigor e a versão; mia.ErrNoConfig se nunca foi salva.
func (s *Store) MiaCurrent(ctx context.Context) (mia.Config, int, error) {
	var raw []byte
	var v int
	err := s.pool.QueryRow(ctx, `SELECT version, config FROM mia_settings ORDER BY version DESC LIMIT 1`).Scan(&v, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return mia.Config{}, 0, mia.ErrNoConfig
	}
	if err != nil {
		return mia.Config{}, 0, err
	}
	var c mia.Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return mia.Config{}, 0, fmt.Errorf("store: configuração da Mia ilegível: %w", err)
	}
	return c, v, nil
}

// MiaVersionConfig devolve a configuração de uma versão do histórico.
func (s *Store) MiaVersionConfig(ctx context.Context, version int) (mia.Config, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT config FROM mia_settings WHERE version = $1`, version).Scan(&raw); err != nil {
		return mia.Config{}, notFound(err)
	}
	var c mia.Config
	err := json.Unmarshal(raw, &c)
	return c, err
}

// SaveMia grava uma versão nova. base é a versão que estava aberta na edição: se outra pessoa salvou
// depois, devolve ErrConflict (ninguém perde trabalho sem saber). Valida antes de gravar.
func (s *Store) SaveMia(ctx context.Context, c mia.Config, base int, by, note string) (int, error) {
	if err := c.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	c = c.Normalize()
	raw, err := json.Marshal(c)
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE mia_settings IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, err
	}
	var latest int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) FROM mia_settings`).Scan(&latest); err != nil {
		return 0, err
	}
	if latest != base {
		return 0, ErrConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mia_settings (version, config, note, created_by) VALUES ($1, $2, nullif($3, ''), $4)`,
		latest+1, string(raw), truncateRunes(note, 200), by); err != nil {
		return 0, err
	}
	return latest + 1, tx.Commit(ctx)
}

// EnsureMiaSeed grava o padrão como versão 1 se ainda não houver configuração. true = semeou.
func (s *Store) EnsureMiaSeed(ctx context.Context, c mia.Config) (bool, error) {
	raw, err := json.Marshal(c.Normalize())
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO mia_settings (version, config, note, created_by)
		SELECT 1, $1, 'padrão do código', 'sistema' WHERE NOT EXISTS (SELECT 1 FROM mia_settings)
		ON CONFLICT (version) DO NOTHING`, string(raw))
	return err == nil && tag.RowsAffected() == 1, err
}

func (s *Store) MiaVersions(ctx context.Context, limit int) ([]MiaVersion, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT version, coalesce(note, ''), created_by, created_at FROM mia_settings ORDER BY version DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (MiaVersion, error) {
		var v MiaVersion
		err := r.Scan(&v.Version, &v.Note, &v.CreatedBy, &v.CreatedAt)
		return v, err
	})
}

// ConversationSnapshot é o estado e os campos de uma conversa, para contar clientes por etapa do funil.
type ConversationSnapshot struct {
	State  string
	Fields contract.Fields
}

// ConversationSnapshots devolve o estado e os campos das conversas de clientes ativos.
func (s *Store) ConversationSnapshots(ctx context.Context) ([]ConversationSnapshot, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.state, c.fields FROM conversations c
		JOIN whatsapp_identities w ON w.id = c.identity_id JOIN members m ON m.id = w.member_id
		JOIN organizations o ON o.id = m.organization_id WHERE o.status = 'active'`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ConversationSnapshot, error) {
		var c ConversationSnapshot
		var raw []byte
		if err := r.Scan(&c.State, &raw); err != nil {
			return c, err
		}
		return c, json.Unmarshal(raw, &c.Fields)
	})
}
