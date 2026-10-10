package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const (
	metaLinkTTL    = 24 * time.Hour
	metaLinkResend = 10 * time.Minute
)

// metaConnectAsk reconhece o pedido direto de conexão, sem gastar IA.
var metaConnectAsk = regexp.MustCompile(`(?i)\bconectar\s+(a\s+)?(meta|facebook|face|conta\s+de\s+an[uú]ncios?)\b`)

// isMetaConnectCommand diz se a mensagem é o pedido direto de conexão: frase curta e fora das etapas
// que tratam a mensagem de outro jeito (aprovação, anúncios, proposta, atendimento humano).
func isMetaConnectCommand(state, text string) bool {
	switch state {
	case store.StateIdle, store.StateCollecting, store.StateOnboarding:
	default:
		return false
	}
	return len(strings.Fields(text)) <= 6 && metaConnectAsk.MatchString(text)
}

// sendMetaLink manda o link de conexão da conta de anúncios; lead é a resposta da IA que vem antes (pode ser vazia).
func (e *Engine) sendMetaLink(ctx context.Context, ident store.Identity, conv store.Conversation, lead string) error {
	if ident.Role != "owner" {
		return e.send(ctx, conv, join(lead, msgMetaOwnerOnly))
	}
	recent, err := e.Store.MetaLinkSentWithin(ctx, ident.ID, metaLinkResend)
	if err != nil {
		return err
	}
	if recent {
		return e.send(ctx, conv, join(lead, msgMetaLinkRecent))
	}
	link, err := e.newMetaLink(ctx, ident)
	if err != nil {
		return err
	}
	return e.send(ctx, conv, join(lead, fmt.Sprintf(msgMetaLink, link)))
}

// newMetaLink cria um link de conexão da conta Meta para a identidade (vale 24 h).
func (e *Engine) newMetaLink(ctx context.Context, ident store.Identity) (string, error) {
	raw, hash, err := signup.NewToken()
	if err != nil {
		return "", err
	}
	if err := e.Store.CreateMetaLink(ctx, ident.ID, hash, e.now().Add(metaLinkTTL)); err != nil {
		return "", err
	}
	e.audit(ctx, ident.OrgID, "system", "meta_link_sent", map[string]any{"identity_id": ident.ID})
	return strings.TrimRight(e.MetaConnectBaseURL, "/") + "/" + raw, nil
}

// metaStatus é o estado da conta que vai para a IA; vazio com a conexão desligada.
func (e *Engine) metaStatus(ctx context.Context, orgID int64) (string, error) {
	if e.MetaConnectBaseURL == "" {
		return "", nil
	}
	c, ok, err := e.Store.ActiveMeta(ctx, orgID)
	if err != nil || !ok {
		return "não conectada", err
	}
	return fmt.Sprintf("conectada: %s · Página %s", c.AdAccountName, c.PageName), nil
}

// MetaSummary é o que a confirmação da conexão mostra.
type MetaSummary struct {
	AccountName   string
	Currency      string
	Timezone      string
	AccountStatus int
	DisableReason int
	PageName      string
	Instagram     string
	PageWhatsApp  bool
}

// MetaConnectedJob é a confirmação no WhatsApp, enfileirada pela api quando a conexão fecha.
func MetaConnectedJob(convID int64, chatID string, s MetaSummary) store.JobSpec {
	ig := "sem Instagram vinculado"
	if s.Instagram != "" {
		ig = "@" + s.Instagram
	}
	parts := []string{fmt.Sprintf(msgMetaConnected, s.AccountName, s.Currency, s.PageName, ig)}
	if s.AccountStatus != 1 {
		parts = append(parts, fmt.Sprintf(msgMetaWarnStatus, accountStatusText(s.AccountStatus)))
	}
	if s.Currency != "BRL" || (s.Timezone != "" && s.Timezone != "America/Sao_Paulo") {
		parts = append(parts, fmt.Sprintf(msgMetaWarnLocale, s.Currency, s.Timezone))
	}
	if !s.PageWhatsApp {
		parts = append(parts, msgMetaWarnWA)
	}
	job := humanJob(0, store.Conversation{ID: convID, ChatID: chatID}, join(parts...), false)
	p := job.Payload.(SendJob)
	p.MetaConnected = true // fecha o pedido de testador, se houver
	job.Payload = p
	return job
}

// MetaRevokedJob avisa que o cliente tirou o acesso do app no Facebook.
func MetaRevokedJob(convID int64, chatID string) store.JobSpec {
	return humanJob(0, store.Conversation{ID: convID, ChatID: chatID}, msgMetaRevoked, false)
}

// accountStatusText traduz o account_status da Marketing API.
func accountStatusText(status int) string {
	switch status {
	case 2:
		return "desativada"
	case 3:
		return "com pagamento pendente"
	case 7:
		return "em análise de risco"
	case 8:
		return "aguardando acerto de pagamento"
	case 9:
		return "em período de carência"
	case 100, 101:
		return "fechada"
	default:
		return fmt.Sprintf("com restrição (status %d)", status)
	}
}
