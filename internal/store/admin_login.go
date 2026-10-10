package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrLoginLocked: o e-mail errou o código vezes demais e está bloqueado por um tempo.
	ErrLoginLocked = errors.New("store: login bloqueado por excesso de tentativas")
	// ErrTooManyCodes: passou do teto de códigos por hora (do e-mail ou do painel inteiro).
	ErrTooManyCodes = errors.New("store: limite de códigos por hora")
)

// Limites do login do painel.
const (
	AdminLoginMaxFailures   = 5  // falhas com o mesmo código: queima o código e bloqueia
	AdminLoginLockMinutes   = 15 // duração do bloqueio
	AdminLoginDailyFailures = 20 // falhas por e-mail em 24 h: bloqueia até sair da janela
	AdminCodesPerEmailHour  = 5  // pedidos de código por e-mail por hora
	AdminCodesPerHour       = 20 // códigos enviados por hora, somando todos os e-mails
)

// lockLogin serializa as operações do login: a contagem e o registro não podem se cruzar.
func lockLogin(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('admin_login'))`)
	return err
}

// loginLocked diz se o e-mail está bloqueado (bloqueio de 15 min ou teto diário de falhas).
func loginLocked(ctx context.Context, q pgx.Tx, email string) (bool, error) {
	var locked bool
	err := q.QueryRow(ctx, `SELECT
			EXISTS (SELECT 1 FROM admin_login_state WHERE email = $1 AND locked_until > now())
			OR (SELECT count(*) FROM admin_login_events WHERE email = $1 AND kind = 'failure' AND created_at > now() - interval '24 hours') >= $2`,
		email, AdminLoginDailyFailures).Scan(&locked)
	return locked, err
}

// AdminLoginGate é conferido antes do código: ErrLoginLocked se o e-mail está bloqueado; valid diz
// se há um código pedido que ainda vale (não usado e sem 5 falhas).
func (s *Store) AdminLoginGate(ctx context.Context, email string) (valid bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	locked, err := loginLocked(ctx, tx, email)
	if err != nil {
		return false, err
	}
	if locked {
		return false, ErrLoginLocked
	}
	err = tx.QueryRow(ctx, `SELECT coalesce((SELECT code_valid FROM admin_login_state WHERE email = $1), false)`, email).Scan(&valid)
	return valid, err
}

// AdminCodeRequest registra um pedido de código: zera as falhas e passa a valer o código novo.
// send=true quando o e-mail é da equipe e o código vai sair. Pedidos de quem não é da equipe contam
// no limite do e-mail (a resposta é a mesma), mas não no teto total de envios.
func (s *Store) AdminCodeRequest(ctx context.Context, email string, send bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockLogin(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM admin_login_events WHERE created_at < now() - interval '2 days'`); err != nil {
		return err
	}
	locked, err := loginLocked(ctx, tx, email)
	if err != nil {
		return err
	}
	if locked {
		return ErrLoginLocked
	}
	var mine, total int
	if err := tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM admin_login_events WHERE email = $1 AND kind IN ('code_request', 'code_sent') AND created_at > now() - interval '1 hour'),
			(SELECT count(*) FROM admin_login_events WHERE kind = 'code_sent' AND created_at > now() - interval '1 hour')`, email).Scan(&mine, &total); err != nil {
		return err
	}
	if mine >= AdminCodesPerEmailHour || total >= AdminCodesPerHour {
		return ErrTooManyCodes
	}
	kind := "code_request"
	if send {
		kind = "code_sent"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO admin_login_events (email, kind) VALUES ($1, $2)`, email, kind); err != nil {
		return err
	}
	// Mesmo sem envio (fora da equipe) o "código" passa a valer: as próximas respostas e o bloqueio
	// ficam iguais aos de quem é da equipe.
	if _, err := tx.Exec(ctx, `INSERT INTO admin_login_state (email, failures, code_valid) VALUES ($1, 0, true)
		ON CONFLICT (email) DO UPDATE SET failures = 0, code_valid = true, updated_at = now()`, email); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminLoginFailed conta uma falha. Na quinta falha do código (ou no teto diário) o código deixa de
// valer e o e-mail fica bloqueado; locked=true quando esta falha bloqueou.
func (s *Store) AdminLoginFailed(ctx context.Context, email string) (locked bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err := lockLogin(ctx, tx); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO admin_login_events (email, kind) VALUES ($1, 'failure')`, email); err != nil {
		return false, err
	}
	// A falha conta contra o código só se há um código valendo; sem código (já queimado ou nunca
	// pedido) entra só no teto diário.
	var failures, daily int
	var codeValid bool
	if err := tx.QueryRow(ctx, `INSERT INTO admin_login_state (email) VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET failures = admin_login_state.failures + CASE WHEN admin_login_state.code_valid THEN 1 ELSE 0 END,
		  updated_at = now()
		RETURNING failures, code_valid, (SELECT count(*) FROM admin_login_events WHERE email = $1 AND kind = 'failure' AND created_at > now() - interval '24 hours')`,
		email).Scan(&failures, &codeValid, &daily); err != nil {
		return false, err
	}
	// Durante o bloqueio ninguém chega aqui (AdminLoginGate recusa antes): passar do limite agora é
	// um bloqueio novo.
	if (codeValid && failures >= AdminLoginMaxFailures) || daily >= AdminLoginDailyFailures {
		locked = true
		if _, err := tx.Exec(ctx, `UPDATE admin_login_state SET code_valid = false,
			locked_until = greatest(coalesce(locked_until, now()), now() + make_interval(mins => $2)) WHERE email = $1`,
			email, AdminLoginLockMinutes); err != nil {
			return false, err
		}
	}
	return locked, tx.Commit(ctx)
}

// AdminLoginSucceeded consome o código usado e zera as falhas.
func (s *Store) AdminLoginSucceeded(ctx context.Context, email string) error {
	_, err := s.pool.Exec(ctx, `UPDATE admin_login_state SET failures = 0, code_valid = false, locked_until = NULL, updated_at = now()
		WHERE email = $1`, email)
	return err
}
