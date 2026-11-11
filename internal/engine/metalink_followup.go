package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// JobMetaLinkFollowup confere, um tempo depois de o cliente abrir o link de conexão, se o login voltou
// do Facebook. Com o app em desenvolvimento, quem não é testador vê "app indisponível" e nada volta:
// a Mia pede o link do perfil para a equipe liberar (o pedido cai na fila Testadores do painel).
const (
	JobMetaLinkFollowup   = "meta.link_followup"
	MetaLinkFollowupDelay = 3 * time.Minute
)

// MetaLinkFollowupJob é enfileirado pela api na primeira abertura do link.
type MetaLinkFollowupJob struct {
	LinkID int64 `json:"link_id"`
}

func (e *Engine) metaLinkFollowup(ctx context.Context, raw []byte) error {
	var j MetaLinkFollowupJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("engine: payload de link aberto inválido: %w", err)
	}
	if e.MetaConnectBaseURL == "" {
		return nil
	}
	f, err := e.Store.ClaimMetaLinkFollowup(ctx, j.LinkID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && f.ConversationID == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	unlock, err := e.Store.LockConversation(ctx, f.ConversationID)
	if err != nil {
		return err
	}
	defer unlock()
	conv, err := e.Store.GetConversation(ctx, f.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, f.IdentityID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ident.Status == "blocked" || conv.State == store.StateNeedsHuman || conv.IdentityID != ident.ID {
		return nil
	}
	if _, active, err := e.Store.ActiveMeta(ctx, ident.OrgID); err != nil || active {
		return err
	}
	// Já mandou o perfil: a equipe já está liberando.
	if _, err := e.testers().OpenTesterRequest(ctx, ident.ID); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if out, err := e.clientOutsideWindow(ctx, conv.ID); err != nil || out {
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "meta_link_followup", map[string]any{"link_id": j.LinkID})
	return e.Store.Enqueue(ctx, e.clientJob(ident.OrgID, conv, msgMetaAskProfile))
}
