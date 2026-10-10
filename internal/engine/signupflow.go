package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	msgSignupFlow      = "Oi! Eu sou a Mia, do Meu Anúncio. Antes de montarmos seus anúncios, preciso do seu cadastro, que é feito aqui mesmo no WhatsApp e leva uns 2 minutos. Assim que terminar, seguimos a conversa."
	msgSignupFlowFix   = "Quase lá! Só falta ajustar: %s. Toca no botão de novo pra corrigir."
	msgSignupFlowRetry = "Não consegui ler seu cadastro. Toca no botão abaixo pra preencher de novo, por favor."
	msgSignupDone      = "Seu cadastro já está concluído 🙂 Pode seguir por aqui."
	signupFlowCTA      = "Fazer meu cadastro"
)

// fieldNames traduz os campos do cadastro para a mensagem de correção.
var fieldNames = map[string]string{
	"full_name": "nome completo", "email": "e-mail", "cpf": "CPF", "cnpj": "CNPJ", "business_name": "nome do negócio",
	"segment": "segmento", "segment_other": "qual é o segmento", "city": "cidade", "uf": "UF", "objectives": "objetivo dos anúncios",
	"monthly_budget": "investimento por mês", "ads_experience": "se já anuncia", "meta_ad_account": "conta de anúncios",
	"approver": "quem aprova os gastos", "approver_name": "nome de quem aprova", "accept_terms": "aceite dos termos",
}

// sendSignupFlow manda o formulário do WhatsApp; false quando não há formulário ou o envio falhou
// (aí o chamador manda o link da página).
func (e *Engine) sendSignupFlow(ctx context.Context, conv store.Conversation, token, body string) bool {
	fs, ok := e.WA.(whatsapp.FlowSender)
	if !ok || e.SignupFlowID == "" {
		return false
	}
	err := fs.SendFlow(ctx, conv.ChatID, whatsapp.Flow{
		ID: e.SignupFlowID, Token: token, CTA: signupFlowCTA, Body: body, Screen: signup.FlowFirstScreen, Draft: e.SignupFlowDraft,
	})
	if err != nil {
		e.Log.Error("formulário de cadastro não enviado; vai o link", "conversation_id", conv.ID, "erro", err)
		return false
	}
	if err := e.Store.AddMessage(ctx, conv.ID, "out", "flow", "[formulário de cadastro] "+body); err != nil {
		e.Log.Error("falha ao gravar envio do formulário", "erro", err)
	}
	return true
}

// signupFlowAnswer conclui o cadastro com a resposta do formulário. O flow_token é o token do link,
// então valem as mesmas regras da página: link válido, só o responsável, plano disponível.
func (e *Engine) signupFlowAnswer(ctx context.Context, ident store.Identity, conv store.Conversation, in whatsapp.Inbound) error {
	if err := e.Store.AddMessage(ctx, conv.ID, "in", store.KindSkipped, "[formulário de cadastro]"); err != nil {
		return err
	}
	if ident.RegistrationStatus == "complete" {
		return e.send(ctx, conv, msgSignupDone)
	}
	sub, err := signup.ParseFlowResponse([]byte(in.Text))
	var ve *signup.ValidationError
	switch {
	case errors.As(err, &ve):
		return e.askSignupFix(ctx, conv, sub.Token, ve)
	case err != nil:
		e.Log.Warn("resposta do formulário ilegível", "conversation_id", conv.ID, "erro", err)
		return e.resendSignup(ctx, ident, conv)
	}
	form, err := sub.Form.Normalize()
	if errors.As(err, &ve) {
		return e.askSignupFix(ctx, conv, sub.Token, ve)
	}
	if err != nil {
		return err
	}
	_, err = e.Store.CompleteRegistration(ctx, store.Registration{
		TokenHash: signup.HashToken(sub.Token), Email: sub.Email, Form: form, PlanCode: "free",
		TermsVersion: e.TermsVersion, UserAgent: "whatsapp-flow", Welcome: WelcomeJob,
	})
	switch {
	case errors.Is(err, store.ErrAlreadyRegistered):
		return e.send(ctx, conv, msgSignupDone)
	case errors.Is(err, store.ErrNotOwner):
		return e.send(ctx, conv, msgSignupOwner)
	case errors.Is(err, store.ErrLinkInvalid):
		return e.resendSignup(ctx, ident, conv)
	case err != nil:
		return err
	}
	e.audit(ctx, ident.OrgID, "system", "signup_completed_flow", map[string]any{"identity_id": ident.ID})
	return nil
}

// askSignupFix diz o que corrigir e reabre o formulário com o mesmo link (ainda válido).
func (e *Engine) askSignupFix(ctx context.Context, conv store.Conversation, token string, ve *signup.ValidationError) error {
	names := make([]string, 0, len(ve.Fields))
	for f := range ve.Fields {
		if n, ok := fieldNames[f]; ok {
			names = append(names, n)
		} else {
			names = append(names, f)
		}
	}
	sort.Strings(names)
	msg := fmt.Sprintf(msgSignupFlowFix, strings.Join(names, ", "))
	if token != "" && e.sendSignupFlow(ctx, conv, token, msg) {
		return nil
	}
	return e.send(ctx, conv, msg)
}

// resendSignup manda um formulário novo (link novo) quando a resposta não serve.
func (e *Engine) resendSignup(ctx context.Context, ident store.Identity, conv store.Conversation) error {
	if ident.Role != "owner" {
		return e.send(ctx, conv, msgSignupOwner)
	}
	raw, hash, err := signup.NewToken()
	if err != nil {
		return err
	}
	if err := e.Store.CreateSignupLink(ctx, ident.ID, hash, e.now().Add(signupLinkTTL)); err != nil {
		return err
	}
	if e.sendSignupFlow(ctx, conv, raw, msgSignupFlowRetry) {
		return nil
	}
	return e.send(ctx, conv, fmt.Sprintf(msgSignupLink, strings.TrimRight(e.SignupBaseURL, "/")+"/"+raw))
}
