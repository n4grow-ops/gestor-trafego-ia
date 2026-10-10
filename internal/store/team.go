package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrInvalidInput: dado de entrada fora do formato aceito (e-mail, papel, valor…).
	ErrInvalidInput = errors.New("store: dado inválido")
	// ErrLastAdmin: a equipe ficaria sem nenhum administrador ativo.
	ErrLastAdmin = errors.New("store: a equipe precisa de ao menos um administrador")
)

// Papéis da equipe no painel.
const (
	RoleAdmin = "admin"
	RoleStaff = "staff"
)

// TeamMember é uma pessoa da equipe com acesso ao painel.
type TeamMember struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Role        string     `json:"role"`
	Active      bool       `json:"active"`
	AddedBy     string     `json:"added_by"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
	// SessionVersion vai no cookie; subir a versão derruba as sessões abertas.
	SessionVersion int `json:"-"`
}

// IsAdmin diz se o membro tem o papel de administrador.
func (m TeamMember) IsAdmin() bool { return m.Role == RoleAdmin }

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail deixa o e-mail em minúsculas e sem espaços; ok=false se não parece e-mail.
func NormalizeEmail(s string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(s))
	if len(e) > 254 || !emailPattern.MatchString(e) {
		return "", false
	}
	return e, true
}

const teamCols = `id, email, name, role, active, coalesce(added_by, ''), created_at, last_login_at, session_version`

func scanTeam(row pgx.Row) (TeamMember, error) {
	var m TeamMember
	err := row.Scan(&m.ID, &m.Email, &m.Name, &m.Role, &m.Active, &m.AddedBy, &m.CreatedAt, &m.LastLoginAt, &m.SessionVersion)
	return m, notFound(err)
}

// EnsureAdmins garante os e-mails como administradores ativos (ADMIN_EMAILS). Ignora os inválidos.
func (s *Store) EnsureAdmins(ctx context.Context, emails []string) error {
	for _, raw := range emails {
		e, ok := NormalizeEmail(raw)
		if !ok {
			continue
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO team_members (email, role, added_by) VALUES ($1, 'admin', 'ADMIN_EMAILS')
			ON CONFLICT (email) DO UPDATE SET role = 'admin', active = true`, e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) TeamMemberByEmail(ctx context.Context, email string) (TeamMember, error) {
	e, ok := NormalizeEmail(email)
	if !ok {
		return TeamMember{}, ErrNotFound
	}
	return scanTeam(s.pool.QueryRow(ctx, `SELECT `+teamCols+` FROM team_members WHERE email = $1`, e))
}

func (s *Store) TeamMemberByID(ctx context.Context, id int64) (TeamMember, error) {
	return scanTeam(s.pool.QueryRow(ctx, `SELECT `+teamCols+` FROM team_members WHERE id = $1`, id))
}

// ListTeam devolve a equipe, administradores primeiro.
func (s *Store) ListTeam(ctx context.Context) ([]TeamMember, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+teamCols+` FROM team_members ORDER BY role = 'admin' DESC, lower(name), email`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TeamMember, error) { return scanTeam(r) })
}

// AddTeamMember inclui (ou reativa e atualiza) uma pessoa da equipe.
func (s *Store) AddTeamMember(ctx context.Context, email, name, role, addedBy string) (TeamMember, error) {
	e, ok := NormalizeEmail(email)
	if !ok {
		return TeamMember{}, fmt.Errorf("%w: e-mail", ErrInvalidInput)
	}
	if role != RoleAdmin && role != RoleStaff {
		return TeamMember{}, fmt.Errorf("%w: papel", ErrInvalidInput)
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	return scanTeam(s.pool.QueryRow(ctx, `INSERT INTO team_members (email, name, role, added_by) VALUES ($1, $2, $3, nullif($4, ''))
		ON CONFLICT (email) DO UPDATE SET role = EXCLUDED.role, active = true,
		  name = CASE WHEN EXCLUDED.name = '' THEN team_members.name ELSE EXCLUDED.name END
		RETURNING `+teamCols, e, name, role, addedBy))
}

// RemoveTeamMember tira a pessoa da equipe; recusa tirar o último administrador ativo.
func (s *Store) RemoveTeamMember(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Trava a equipe toda: dois administradores não se removem ao mesmo tempo.
	if _, err := tx.Exec(ctx, `LOCK TABLE team_members IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var role string
	var active bool
	if err := tx.QueryRow(ctx, `SELECT role, active FROM team_members WHERE id = $1`, id).Scan(&role, &active); err != nil {
		return notFound(err)
	}
	if role == RoleAdmin && active {
		var others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM team_members WHERE role = 'admin' AND active AND id <> $1`, id).Scan(&others); err != nil {
			return err
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM team_members WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) TouchTeamLogin(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE team_members SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// BumpSessionVersion derruba todas as sessões abertas da pessoa (logout).
func (s *Store) BumpSessionVersion(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE team_members SET session_version = session_version + 1 WHERE id = $1`, id)
	return err
}

// AdminAction é uma ação feita pelo painel, para o registro.
type AdminAction struct {
	ActorID     int64
	ActorEmail  string
	Action      string
	TargetType  string
	TargetID    string
	TargetLabel string
	Data        any
	IP          string
}

// AdminLogEntry é uma linha do registro de ações do painel.
type AdminLogEntry struct {
	ID          int64           `json:"id"`
	ActorEmail  string          `json:"actor_email"`
	ActorName   string          `json:"actor_name"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	TargetLabel string          `json:"target_label"`
	Data        json.RawMessage `json:"data"`
	IP          string          `json:"-"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AdminLogFilter limita o registro a um autor e/ou a um alvo.
type AdminLogFilter struct {
	ActorEmail string
	TargetType string
	TargetID   string
	Limit      int
}

func (s *Store) LogAdminAction(ctx context.Context, a AdminAction) error {
	return logAdminAction(ctx, s.pool, a)
}

// logAdminAction grava no registro do painel pelo pool ou dentro de uma transação.
func logAdminAction(ctx context.Context, q querier, a AdminAction) error {
	if a.ActorEmail == "" || a.Action == "" {
		return fmt.Errorf("%w: ação sem autor", ErrInvalidInput)
	}
	data := a.Data
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	var actor *int64
	if a.ActorID > 0 {
		actor = &a.ActorID
	}
	_, err = q.Exec(ctx, `INSERT INTO admin_audit_log (actor_id, actor_email, action, target_type, target_id, target_label, data, ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, ''))`,
		actor, a.ActorEmail, a.Action, a.TargetType, a.TargetID, truncateRunes(a.TargetLabel, 120), string(raw), a.IP)
	return err
}

func (s *Store) AdminLog(ctx context.Context, f AdminLogFilter) ([]AdminLogEntry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT l.id, l.actor_email, coalesce(nullif(t.name, ''), split_part(l.actor_email, '@', 1)),
			l.action, l.target_type, l.target_id, l.target_label, l.data, coalesce(l.ip, ''), l.created_at
		FROM admin_audit_log l LEFT JOIN team_members t ON t.email = l.actor_email
		WHERE ($1 = '' OR l.actor_email = $1) AND ($2 = '' OR l.target_type = $2) AND ($3 = '' OR l.target_id = $3)
		ORDER BY l.created_at DESC, l.id DESC LIMIT $4`, f.ActorEmail, f.TargetType, f.TargetID, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminLogEntry, error) {
		var e AdminLogEntry
		err := r.Scan(&e.ID, &e.ActorEmail, &e.ActorName, &e.Action, &e.TargetType, &e.TargetID, &e.TargetLabel, &e.Data, &e.IP, &e.CreatedAt)
		return e, err
	})
}

func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
