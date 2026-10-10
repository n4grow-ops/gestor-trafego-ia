# Cadastro dentro do WhatsApp (WhatsApp Flows) — Implementation Plan

> Executado em linha (superpowers:executing-plans), com TDD em cada tarefa.

**Goal:** quem ainda não tem cadastro recebe um botão "Fazer meu cadastro" que abre o formulário oficial da Meta dentro do WhatsApp. Não há link nem navegador. A página web continua como reserva.

**Architecture:**
- **Formulário e token:** o formulário é estático, sem endpoint de dados. O `flow_token` é o mesmo token do link de cadastro (`signup_links`). Por isso a resposta conclui o cadastro por `store.CompleteRegistration`, com as mesmas regras de link válido, responsável e plano.
- **Conector:** a Wame envia o formulário por `POST /{key}/message/flow`. A resposta chega como `interactive.nfm_reply` no webhook normal.
- **Aprovação:** decisão do usuário em 2026-10-10, opção A. O e-mail vem do formulário, sem confirmação (`email_verified_at` e `auth_user_id` ficam NULL).

**Spec:** conversa de 2026-10-10 (opção A aprovada) + `docs/superpowers/specs/2026-10-09-plano-identidade-wame-testador-admin-design.md`.

## Global Constraints

- **Campos e listas:** os mesmos de `internal/signup/form.go`. Os códigos (`Segments`, `Objectives`, `MonthlyBudgets`, `AdsExperiences`, `MetaAdAccounts`, `Approvers`, `UFs`) são a fonte única: o JSON do formulário é gerado a partir deles.
- **Validação:** `signup.Form.Normalize` continua sendo a única regra. Erro de validação → o bot diz o que corrigir e reenvia o formulário.
- **Quando o formulário entra no lugar do link:** só com o provedor implementando `whatsapp.FlowSender` e `SIGNUP_FLOW_ID` configurado. Se o envio do formulário falhar, o bot manda o link da página.
- **Plano:** `free`. **Versão dos termos:** a mesma da página (`termsVersion` da api, passada ao engine como `TermsVersion`).

## Tarefas

1. **wame:**
   - `ParseWebhook` reconhece `type:"interactive"` + `nfm_reply` e devolve `KindFlow`, com `Text` = `response_json`;
   - `SendFlow(ctx, chatID, whatsapp.Flow)`;
   - interface opcional `whatsapp.FlowSender`.
2. **signup:** `ParseFlowResponse([]byte) (FlowSubmission, error)` lê o `response_json` e devolve `{Token, Email, Form}`. O e-mail é validado com uma regra simples; o `Form` vai cru, porque o `Normalize` roda depois. `FlowJSON()` gera o JSON do formulário a partir das listas.
3. **store:** `CompleteRegistration` aceita `AuthUserID` vazio e `EmailVerifiedAt` zero, gravando NULL.
4. **engine:**
   - `signupGate` manda o formulário (`flowToken` = token cru) quando houver `SignupFlowID` e `FlowSender`;
   - `KindFlow` conclui o cadastro antes do portão;
   - erros de validação, link vencido e cadastro já concluído geram mensagens próprias;
   - novo campo `Engine.SignupFlowID` e config `SIGNUP_FLOW_ID`.
5. **script `scripts/wameflow`:** cria, envia o JSON, publica e imprime o `flowId`. Depois, configurar `SIGNUP_FLOW_ID` no worker e publicar.

## Review Focus

1. Resposta do formulário de um número que nunca recebeu o formulário (token desconhecido) → mensagem de link inválido, sem cadastro.
2. Resposta enviada duas vezes → a segunda não recria nada (`ErrAlreadyRegistered`).
3. CPF ou CNPJ inválido no formulário → o bot explica e reenvia, sem travar o cliente.
4. Membro que não é o responsável responde o formulário → `ErrNotOwner` vira mensagem.
5. Provedor WAHA (sem formulário) → o fluxo do link continua igual.
