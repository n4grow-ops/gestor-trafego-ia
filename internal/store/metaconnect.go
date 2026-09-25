package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// ErrMetaLinkInvalid: link de conexão inexistente, expirado ou já usado.
var ErrMetaLinkInvalid = errors.New("store: link de conexão inválido ou expirado")

// CreateMetaLink guarda o hash de um link de conexão da conta Meta para a identidade.
func (s *Store) CreateMetaLink(ctx context.Context, identityID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO meta_connect_links (identity_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		identityID, tokenHash, expiresAt)
	return err
}

// MetaLinkSentWithin diz se a identidade recebeu link de conexão dentro do intervalo (relógio do banco).
func (s *Store) MetaLinkSentWithin(ctx context.Context, identityID int64, within time.Duration) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meta_connect_links
		WHERE identity_id = $1 AND created_at > now() - $2 * interval '1 millisecond')`, identityID, within.Milliseconds()).Scan(&ok)
	return ok, err
}

type MetaLink struct {
	ID         int64
	IdentityID int64
	OrgID      int64
}

// MetaLink confere o link: existe, não foi usado e não expirou.
func (s *Store) MetaLink(ctx context.Context, tokenHash string) (MetaLink, error) {
	var l MetaLink
	err := s.pool.QueryRow(ctx, `SELECT l.id, l.identity_id, m.organization_id
		FROM meta_connect_links l
		JOIN whatsapp_identities wi ON wi.id = l.identity_id
		JOIN members m ON m.id = wi.member_id
		WHERE l.token_hash = $1 AND l.used_at IS NULL AND l.expires_at > now()`, tokenHash).Scan(&l.ID, &l.IdentityID, &l.OrgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetaLink{}, ErrMetaLinkInvalid
	}
	return l, err
}

// MetaCandidates são as contas e Páginas liberadas no login, guardadas para a escolha.
type MetaCandidates struct {
	AdAccounts []meta.AdAccount `json:"ad_accounts"`
	Pages      []meta.Page      `json:"pages"`
}

// SavePendingMeta grava o retorno do login (token já cifrado) à espera da escolha da conta e da Página.
func (s *Store) SavePendingMeta(ctx context.Context, l MetaLink, metaUserID string, sealedToken []byte, c MetaCandidates) (int64, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	var id int64
	err = s.pool.QueryRow(ctx, `INSERT INTO meta_connections (organization_id, connected_by, link_id, meta_user_id, token_ciphertext, candidates)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, l.OrgID, l.IdentityID, l.ID, metaUserID, sealedToken, raw).Scan(&id)
	return id, err
}

// PendingMeta devolve a conexão pendente mais recente do link, com as opções da escolha.
func (s *Store) PendingMeta(ctx context.Context, linkID int64) (int64, MetaCandidates, error) {
	var id int64
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id, candidates FROM meta_connections WHERE link_id = $1 AND status = 'pending'
		ORDER BY created_at DESC, id DESC LIMIT 1`, linkID).Scan(&id, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, MetaCandidates{}, ErrMetaLinkInvalid
	}
	if err != nil {
		return 0, MetaCandidates{}, err
	}
	var c MetaCandidates
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, MetaCandidates{}, fmt.Errorf("store: %w", err)
	}
	return id, c, nil
}

// MetaChoice fecha a conexão com a conta e a Página escolhidas.
type MetaChoice struct {
	ConnectionID int64
	LinkID       int64
	Account      meta.AdAccount
	Page         meta.Page
	// Message monta a confirmação no WhatsApp, enfileirada na mesma transação.
	Message func(convID int64, chatID string) JobSpec
}

// CompleteMeta ativa a conexão numa transação: link usado, conexão ativa anterior substituída
// (token apagado), escolha gravada e confirmação na fila. Devolve a organização.
func (s *Store) CompleteMeta(ctx context.Context, ch MetaChoice) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE meta_connect_links SET used_at = now() WHERE id = $1 AND used_at IS NULL AND expires_at > now()`, ch.LinkID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrMetaLinkInvalid
	}
	var orgID, identityID int64
	err = tx.QueryRow(ctx, `SELECT organization_id, connected_by FROM meta_connections WHERE id = $1 AND link_id = $2 AND status = 'pending'`,
		ch.ConnectionID, ch.LinkID).Scan(&orgID, &identityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrMetaLinkInvalid
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE meta_connections SET status = 'replaced', token_ciphertext = NULL
		WHERE organization_id = $1 AND status = 'active'`, orgID); err != nil {
		return 0, err
	}
	a, p := ch.Account, ch.Page
	if _, err := tx.Exec(ctx, `UPDATE meta_connections SET status = 'active', connected_at = now(), candidates = '{}',
			business_id = nullif($2, ''), ad_account_id = $3, ad_account_name = $4, currency = $5, timezone_name = $6, account_status = $7,
			page_id = $8, page_name = $9, instagram_id = nullif($10, ''), instagram_username = nullif($11, ''), page_whatsapp = $12
		WHERE id = $1`, ch.ConnectionID, a.BusinessID, a.ID, a.Name, a.Currency, a.Timezone, a.Status,
		p.ID, p.Name, p.InstagramID, p.InstagramUsername, p.WhatsApp); err != nil {
		return 0, err
	}
	var convID int64
	var chatID string
	err = tx.QueryRow(ctx, `SELECT id, chat_id FROM conversations WHERE identity_id = $1`, identityID).Scan(&convID, &chatID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if err == nil && chatID != "" && ch.Message != nil {
		if err := enqueue(ctx, tx, ch.Message(convID, chatID)); err != nil {
			return 0, err
		}
	}
	return orgID, tx.Commit(ctx)
}

// MetaConnection é a conexão ativa, sem o token.
type MetaConnection struct {
	ID                int64
	AdAccountID       string
	AdAccountName     string
	Currency          string
	PageID            string
	PageName          string
	InstagramUsername string
	PageWhatsApp      bool
}

// ActiveMeta devolve a conexão ativa da organização, se houver.
func (s *Store) ActiveMeta(ctx context.Context, orgID int64) (MetaConnection, bool, error) {
	var c MetaConnection
	err := s.pool.QueryRow(ctx, `SELECT id, ad_account_id, coalesce(ad_account_name, ''), coalesce(currency, ''), page_id, coalesce(page_name, ''),
			coalesce(instagram_username, ''), page_whatsapp
		FROM meta_connections WHERE organization_id = $1 AND status = 'active'`, orgID).
		Scan(&c.ID, &c.AdAccountID, &c.AdAccountName, &c.Currency, &c.PageID, &c.PageName, &c.InstagramUsername, &c.PageWhatsApp)
	if errors.Is(err, pgx.ErrNoRows) {
		return MetaConnection{}, false, nil
	}
	return c, err == nil, err
}

// RevokeMeta desliga as conexões do usuário Meta (removeu o app ou pediu exclusão): token e opções
// apagados, código da exclusão gravado e aviso no WhatsApp para cada conexão que estava ativa.
func (s *Store) RevokeMeta(ctx context.Context, metaUserID, deletionCode string, notify func(convID int64, chatID string) JobSpec) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	// A junção com old lê a linha antes do UPDATE: old.status é o status anterior.
	rows, err := tx.Query(ctx, `UPDATE meta_connections mc SET status = 'revoked', token_ciphertext = NULL, candidates = '{}',
			revoked_at = now(), deletion_code = coalesce(nullif($2, ''), mc.deletion_code)
		FROM meta_connections old
		WHERE old.id = mc.id AND mc.meta_user_id = $1 AND mc.status IN ('active', 'pending')
		RETURNING old.status, mc.connected_by`, metaUserID, deletionCode)
	if err != nil {
		return 0, err
	}
	type revoked struct {
		status     string
		identityID int64
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (revoked, error) {
		var v revoked
		return v, r.Scan(&v.status, &v.identityID)
	})
	if err != nil {
		return 0, err
	}
	for _, v := range list {
		if v.status != "active" || notify == nil {
			continue
		}
		var convID int64
		var chatID string
		err := tx.QueryRow(ctx, `SELECT id, chat_id FROM conversations WHERE identity_id = $1`, v.identityID).Scan(&convID, &chatID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && chatID == "") {
			continue
		}
		if err != nil {
			return 0, err
		}
		if err := enqueue(ctx, tx, notify(convID, chatID)); err != nil {
			return 0, err
		}
	}
	return len(list), tx.Commit(ctx)
}

// MetaDeletionDone diz se o pedido de exclusão com esse código já foi atendido.
func (s *Store) MetaDeletionDone(ctx context.Context, code string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM meta_connections WHERE deletion_code = $1 AND status = 'revoked')`, code).Scan(&ok)
	return ok, err
}
