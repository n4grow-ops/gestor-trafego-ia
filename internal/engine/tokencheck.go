package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	// JobTokenCheck avisa quem tem a conexão com a Meta vencendo (o acesso dura ~60 dias).
	JobTokenCheck   = "meta.token_check"
	tokenWarnBefore = 7 * 24 * time.Hour
	tokenCheckHour  = 9 // horário de Brasília

	TplMetaExpiring = "conexao_meta_vencendo"
)

// EnsureTokenCheck garante a checagem diária agendada (chamado quando o worker sobe).
func (e *Engine) EnsureTokenCheck(ctx context.Context) error {
	pending, err := e.Store.HasPendingKind(ctx, JobTokenCheck)
	if err != nil || pending {
		return err
	}
	return e.enqueueDaily(ctx, JobTokenCheck, tokenCheckHour)
}

// enqueueDaily agenda o job para o próximo horário de Brasília dado.
func (e *Engine) enqueueDaily(ctx context.Context, kind string, hour int) error {
	now := e.now()
	t := now.In(brt)
	y, m, d := t.Date()
	at := time.Date(y, m, d, hour, 0, 0, 0, brt)
	if !at.After(t) {
		at = at.AddDate(0, 0, 1)
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: kind, Payload: map[string]string{}, Delay: at.Sub(now)})
}

// tokenCheck avisa o cliente (link de reconexão) e a equipe sobre as conexões que vencem em até 7 dias.
func (e *Engine) tokenCheck(ctx context.Context) error {
	now := e.now()
	e.fillTokenExpiry(ctx)
	list, err := e.Store.ExpiringMetaConnections(ctx, now, now.Add(tokenWarnBefore))
	if err != nil {
		return err
	}
	for _, m := range list {
		if err := e.warnTokenExpiring(ctx, m, now); err != nil {
			e.Log.Error("aviso de conexão vencendo", "org_id", m.OrgID, "erro", err)
		}
	}
	return e.enqueueDaily(ctx, JobTokenCheck, tokenCheckHour)
}

func (e *Engine) warnTokenExpiring(ctx context.Context, m store.ExpiringMeta, now time.Time) error {
	conv, err := e.Store.OwnerConversation(ctx, m.OrgID)
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	date := m.ExpiresAt.In(brt).Format("02/01")
	reason := fmt.Sprintf("o acesso do cliente à Meta vence em %s; sem reconectar, ATIVAR, PAUSAR e novas publicações param", date)
	if !m.ExpiresAt.After(now) {
		reason = "o acesso do cliente à Meta venceu; ATIVAR, PAUSAR e novas publicações estão parados até reconectar"
	}
	if e.MetaConnectBaseURL != "" && ident.Role == "owner" {
		link, err := e.newMetaLink(ctx, ident)
		if err != nil {
			return err
		}
		text := fmt.Sprintf(msgMetaExpiring, date, link)
		if !m.ExpiresAt.After(now) {
			text = fmt.Sprintf(msgMetaExpired, link)
		}
		tpl := &whatsapp.Template{Name: TplMetaExpiring, Params: []string{templateParam(date), templateParam(link)}}
		if err := e.Store.Enqueue(ctx, systemSendJob(conv.ID, conv.ChatID, text, tpl)); err != nil {
			return err
		}
	}
	e.alertTeam(ctx, ident, reason)
	e.audit(ctx, m.OrgID, "system", "meta_token_expiring", map[string]any{"connection_id": m.ConnectionID, "expires_at": m.ExpiresAt})
	return e.Store.MarkMetaTokenWarned(ctx, m.ConnectionID, now)
}

// tokenExpiryReader lê a validade do acesso (o cliente da Meta do worker implementa).
type tokenExpiryReader interface {
	TokenExpiry(ctx context.Context, token string) (time.Time, error)
}

// fillTokenExpiry lê a validade das conexões feitas antes de ela ser gravada na conexão.
func (e *Engine) fillTokenExpiry(ctx context.Context) {
	tr, ok := e.Publisher.(tokenExpiryReader)
	if !ok || e.MetaTokenKey == nil {
		return
	}
	list, err := e.Store.MetaWithoutExpiry(ctx)
	if err != nil {
		e.Log.Warn("conexões sem validade não listadas", "erro", err)
		return
	}
	for _, m := range list {
		_, sealed, err := e.pubs().MetaTokenSealed(ctx, m.OrgID) // a conexão ativa da organização (só há uma)
		if err != nil {
			continue
		}
		token, err := e.MetaTokenKey.Open(sealed)
		if err != nil {
			continue
		}
		at, err := tr.TokenExpiry(ctx, string(token))
		if err != nil {
			e.Log.Warn("validade do token não lida", "org_id", m.OrgID, "erro", err)
			continue
		}
		if err := e.Store.SetMetaTokenExpiry(ctx, m.ConnectionID, at); err != nil {
			e.Log.Warn("validade do token não gravada", "org_id", m.OrgID, "erro", err)
		}
	}
}
