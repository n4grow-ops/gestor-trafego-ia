package store

import (
	"context"
	"time"
)

// PurgeOld apaga eventos do webhook e jobs já encerrados há mais de olderThan. Pendentes ficam.
// O webhook bruto e o texto dos jobs carregam dado pessoal e token de link: não precisam viver para sempre.
func (s *Store) PurgeOld(ctx context.Context, olderThan time.Duration) (inbox, jobs int64, err error) {
	cut := time.Now().Add(-olderThan)
	tag, err := s.pool.Exec(ctx, `DELETE FROM inbox_events WHERE status <> 'pending' AND processed_at < $1`, cut)
	if err != nil {
		return 0, 0, err
	}
	inbox = tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `DELETE FROM outbox_jobs WHERE status <> 'pending' AND updated_at < $1`, cut)
	if err != nil {
		return inbox, 0, err
	}
	return inbox, tag.RowsAffected(), nil
}

// RedactInbox apaga o conteúdo de um evento já processado (ex.: resposta do formulário com CPF e e-mail).
func (s *Store) RedactInbox(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE inbox_events SET payload = '{"redacted": true}' WHERE id = $1`, id)
	return err
}
