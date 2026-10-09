package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// WhatsAppStatus é o último aviso de conexão do número do bot.
type WhatsAppStatus struct {
	Provider  string
	Connected bool
	Code      int
	Reason    string
	UpdatedAt time.Time
}

// SetWhatsAppStatus grava o aviso; changed=true quando conectado/desconectado mudou (ou é o primeiro).
func (s *Store) SetWhatsAppStatus(ctx context.Context, provider string, ev whatsapp.ConnectionEvent) (bool, error) {
	var before *bool
	err := s.pool.QueryRow(ctx, `
		WITH old AS (SELECT connected FROM whatsapp_status WHERE provider = $1),
		up AS (
		  INSERT INTO whatsapp_status (provider, connected, code, reason, updated_at)
		  VALUES ($1, $2, $3, NULLIF($4, ''), now())
		  ON CONFLICT (provider) DO UPDATE SET connected = EXCLUDED.connected, code = EXCLUDED.code,
		    reason = EXCLUDED.reason, updated_at = now()
		)
		SELECT (SELECT connected FROM old)`, provider, ev.Connected, ev.Code, ev.Reason).Scan(&before)
	if err != nil {
		return false, err
	}
	return before == nil || *before != ev.Connected, nil
}

func (s *Store) WhatsAppStatus(ctx context.Context, provider string) (WhatsAppStatus, error) {
	var w WhatsAppStatus
	var code *int
	var reason *string
	err := s.pool.QueryRow(ctx, `SELECT provider, connected, code, reason, updated_at FROM whatsapp_status WHERE provider = $1`,
		provider).Scan(&w.Provider, &w.Connected, &code, &reason, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WhatsAppStatus{}, ErrNotFound
	}
	if err != nil {
		return WhatsAppStatus{}, err
	}
	if code != nil {
		w.Code = *code
	}
	if reason != nil {
		w.Reason = *reason
	}
	return w, nil
}
