package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	signupLinkTTL    = 24 * time.Hour
	signupLinkResend = 10 * time.Minute
)

// signupGate atende quem ainda não concluiu o cadastro: a mensagem fica gravada sem ir para a IA
// (nem para o lote seguinte) e o cliente recebe o link, no máximo a cada 10 minutos.
func (e *Engine) signupGate(ctx context.Context, ident store.Identity, conv store.Conversation, in whatsapp.Inbound) error {
	body := in.Text
	if body == "" {
		body = "[" + string(in.Kind) + "]"
	}
	if err := e.Store.AddMessage(ctx, conv.ID, "in", store.KindSkipped, body); err != nil {
		return err
	}
	recent, err := e.Store.SignupLinkSentWithin(ctx, ident.ID, signupLinkResend)
	if err != nil || recent {
		return err
	}
	raw, hash, err := signup.NewToken()
	if err != nil {
		return err
	}
	if err := e.Store.CreateSignupLink(ctx, ident.ID, hash, e.now().Add(signupLinkTTL)); err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "signup_link_sent", map[string]any{"identity_id": ident.ID})
	return e.send(ctx, conv, fmt.Sprintf(msgSignupLink, strings.TrimRight(e.SignupBaseURL, "/")+"/"+raw))
}

// WelcomeJob é a mensagem de boas-vindas depois do cadastro, enfileirada pela api na conclusão.
func WelcomeJob(convID int64, chatID, fullName string) store.JobSpec {
	first := strings.Fields(fullName)
	name := fullName
	if len(first) > 0 {
		name = first[0]
	}
	conv := store.Conversation{ID: convID, ChatID: chatID}
	return humanJob(0, conv, fmt.Sprintf(msgWelcome, name), false)
}
