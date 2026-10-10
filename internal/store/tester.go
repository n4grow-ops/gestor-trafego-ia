package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Status do pedido de testador.
const (
	TesterRequested = "requested"
	TesterAdded     = "added"
	TesterAccepted  = "accepted"
	TesterCanceled  = "canceled"
)

// TesterRequest é o pedido para incluir o cliente como testador do app da Meta.
type TesterRequest struct {
	ID             int64
	OrgID          int64
	IdentityID     int64
	ConversationID int64
	ProfileURL     string
	RefKind        string // id | username
	RefValue       string
	Status         string
	RequestedAt    time.Time
	AddedAt        *time.Time
	AddedBy        string
	AcceptedAt     *time.Time
	RemindedAt     *time.Time
}

// TesterRequestView é a linha da fila no painel.
type TesterRequestView struct {
	TesterRequest
	MemberName string
	OrgName    string
	Phone      string
}

// Tamanho máximo das listas do painel e dos jobs.
const testerListLimit = 500

const testerCols = `t.id, t.organization_id, t.identity_id, coalesce(t.conversation_id, 0), t.profile_url, t.ref_kind, t.ref_value,
	t.status, t.requested_at, t.added_at, coalesce(t.added_by, ''), t.accepted_at, t.reminded_at`

// testerViewFrom completa o pedido (t) com nome, empresa e telefone.
const testerViewFrom = ` JOIN whatsapp_identities wi ON wi.id = t.identity_id
	JOIN members m ON m.id = wi.member_id
	JOIN organizations o ON o.id = t.organization_id`

const testerViewCols = testerCols + `, coalesce(nullif(m.full_name, ''), m.name), coalesce(nullif(o.trade_name, ''), o.name), wi.phone_e164`

func (r *TesterRequest) scanArgs() []any {
	return []any{&r.ID, &r.OrgID, &r.IdentityID, &r.ConversationID, &r.ProfileURL, &r.RefKind, &r.RefValue,
		&r.Status, &r.RequestedAt, &r.AddedAt, &r.AddedBy, &r.AcceptedAt, &r.RemindedAt}
}

func scanTesterView(row pgx.Row) (TesterRequestView, error) {
	var v TesterRequestView
	err := row.Scan(append(v.scanArgs(), &v.MemberName, &v.OrgName, &v.Phone)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return TesterRequestView{}, ErrNotFound
	}
	return v, err
}

func (s *Store) queryTesterViews(ctx context.Context, sql string, args ...any) ([]TesterRequestView, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TesterRequestView
	for rows.Next() {
		v, err := scanTesterView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpsertTesterRequest abre (ou atualiza o perfil do) pedido aberto da identidade.
// A organização vem sempre da identidade (r.OrgID é ignorado); sem ConversationID, usa a conversa
// do telefone. Perfil diferente num pedido já adicionado volta para requested: a equipe adiciona de novo.
func (s *Store) UpsertTesterRequest(ctx context.Context, r TesterRequest) (TesterRequest, error) {
	r.ProfileURL, r.RefValue = strings.TrimSpace(r.ProfileURL), strings.TrimSpace(r.RefValue)
	if r.RefKind != "id" && r.RefKind != "username" {
		return TesterRequest{}, fmt.Errorf("store: testador: tipo de perfil inválido %q", r.RefKind)
	}
	if r.ProfileURL == "" || r.RefValue == "" {
		return TesterRequest{}, errors.New("store: testador: perfil vazio")
	}
	if r.ConversationID != 0 {
		var ok bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM conversations WHERE id = $1 AND identity_id = $2)`,
			r.ConversationID, r.IdentityID).Scan(&ok); err != nil {
			return TesterRequest{}, err
		}
		if !ok {
			return TesterRequest{}, errors.New("store: testador: conversa não é deste telefone")
		}
	}
	var out TesterRequest
	err := s.pool.QueryRow(ctx, `WITH who AS (
			SELECT m.organization_id AS org, wi.id AS ident,
				coalesce(nullif($2::bigint, 0), (SELECT c.id FROM conversations c WHERE c.identity_id = wi.id)) AS conv
			FROM whatsapp_identities wi JOIN members m ON m.id = wi.member_id WHERE wi.id = $1
		)
		INSERT INTO tester_requests AS t (organization_id, identity_id, conversation_id, profile_url, ref_kind, ref_value)
		SELECT org, ident, conv, $3, $4, $5 FROM who
		ON CONFLICT (identity_id) WHERE status IN ('requested', 'added') DO UPDATE SET
			conversation_id = coalesce(EXCLUDED.conversation_id, t.conversation_id),
			profile_url = EXCLUDED.profile_url,
			status = CASE WHEN (t.ref_kind, t.ref_value) = (EXCLUDED.ref_kind, EXCLUDED.ref_value) THEN t.status ELSE 'requested' END,
			added_at = CASE WHEN (t.ref_kind, t.ref_value) = (EXCLUDED.ref_kind, EXCLUDED.ref_value) THEN t.added_at END,
			added_by = CASE WHEN (t.ref_kind, t.ref_value) = (EXCLUDED.ref_kind, EXCLUDED.ref_value) THEN t.added_by END,
			reminded_at = CASE WHEN (t.ref_kind, t.ref_value) = (EXCLUDED.ref_kind, EXCLUDED.ref_value) THEN t.reminded_at END,
			ref_kind = EXCLUDED.ref_kind,
			ref_value = EXCLUDED.ref_value
		RETURNING `+testerCols, r.IdentityID, r.ConversationID, r.ProfileURL, r.RefKind, r.RefValue).Scan(out.scanArgs()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return TesterRequest{}, ErrNotFound
	}
	return out, err
}

// OpenTesterRequest devolve o pedido requested/added da identidade (ErrNotFound se não houver).
func (s *Store) OpenTesterRequest(ctx context.Context, identityID int64) (TesterRequest, error) {
	var r TesterRequest
	err := s.pool.QueryRow(ctx, `SELECT `+testerCols+` FROM tester_requests t
		WHERE t.identity_id = $1 AND t.status IN ('requested', 'added')`, identityID).Scan(r.scanArgs()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return TesterRequest{}, ErrNotFound
	}
	return r, err
}

// ListTesterRequests lista a fila (status vazio = abertos: requested e added), mais antigos primeiro.
// Devolve no máximo 500 linhas.
func (s *Store) ListTesterRequests(ctx context.Context, status string) ([]TesterRequestView, error) {
	where, args := `t.status IN ('requested', 'added')`, []any{testerListLimit}
	if status != "" {
		where, args = `t.status = $2`, append(args, status)
	}
	return s.queryTesterViews(ctx, `SELECT `+testerViewCols+` FROM tester_requests t`+testerViewFrom+`
		WHERE `+where+` ORDER BY t.requested_at, t.id LIMIT $1`, args...)
}

// MarkTesterAdded passa de requested para added (ErrNotFound se não estiver requested).
func (s *Store) MarkTesterAdded(ctx context.Context, id int64, by string) (TesterRequestView, error) {
	return scanTesterView(s.pool.QueryRow(ctx, `WITH t AS (
			UPDATE tester_requests SET status = 'added', added_at = now(), added_by = nullif($2, '')
			WHERE id = $1 AND status = 'requested' RETURNING *
		)
		SELECT `+testerViewCols+` FROM t`+testerViewFrom, id, by))
}

// MarkTesterAcceptedByIdentity fecha como accepted o pedido aberto da identidade (sem pedido: nada).
func (s *Store) MarkTesterAcceptedByIdentity(ctx context.Context, identityID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE tester_requests SET status = 'accepted', accepted_at = now()
		WHERE identity_id = $1 AND status IN ('requested', 'added')`, identityID)
	return err
}

// TestersAwaitingAcceptance lista os pedidos added sem conexão Meta ativa na organização
// (adicionados há mais tempo primeiro, no máximo 500).
func (s *Store) TestersAwaitingAcceptance(ctx context.Context) ([]TesterRequestView, error) {
	return s.queryTesterViews(ctx, `SELECT `+testerViewCols+` FROM tester_requests t`+testerViewFrom+`
		WHERE t.status = 'added' AND NOT EXISTS (
			SELECT 1 FROM meta_connections mc WHERE mc.organization_id = t.organization_id AND mc.status = 'active')
		ORDER BY t.added_at, t.id LIMIT $1`, testerListLimit)
}

// MarkTesterReminded grava o lembrete (uma vez só). ErrNotFound se o pedido não existe ou já foi
// lembrado: serve de trava para o lembrete não sair duas vezes.
func (s *Store) MarkTesterReminded(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tester_requests SET reminded_at = now() WHERE id = $1 AND reminded_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SyncAppTesters grava os testadores vistos na Meta e devolve os que não eram conhecidos.
// Na primeira chamada (tabela vazia) grava todos e devolve nenhum.
func (s *Store) SyncAppTesters(ctx context.Context, userIDs []string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, id := range userIDs {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Duas leituras ao mesmo tempo não podem ver o mesmo testador como novo.
	if _, err := tx.Exec(ctx, `LOCK TABLE meta_app_testers IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, err
	}
	var known bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meta_app_testers)`).Scan(&known); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `INSERT INTO meta_app_testers (user_id) SELECT unnest($1::text[])
		ON CONFLICT (user_id) DO NOTHING RETURNING user_id`, ids)
	if err != nil {
		return nil, err
	}
	inserted, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if !known {
		return nil, nil
	}
	isNew := map[string]bool{}
	for _, id := range inserted {
		isNew[id] = true
	}
	var fresh []string
	for _, id := range ids {
		if isNew[id] {
			fresh = append(fresh, id)
		}
	}
	return fresh, nil
}
