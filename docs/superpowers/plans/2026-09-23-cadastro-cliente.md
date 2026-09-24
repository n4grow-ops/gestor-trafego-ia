# Cadastro do cliente — plano de implementação

> Execução nativa (o próprio Claude implementa, TDD por tarefa, revisão independente no fim).

**Goal:** cliente só conversa com a IA depois de um cadastro completo (link → página → Google ou código
por e-mail → dados → plano Free → volta ao WhatsApp), tudo no Supabase.

**Spec:** `docs/superpowers/specs/2026-09-23-cadastro-cliente-design.md`

## Global Constraints

- Schema `app`, RLS em toda tabela nova (o teste `TestEveryTableHasRowLevelSecurity` cobra).
- Token do link: 32 bytes aleatórios, base64url, guardado só como SHA-256 hex; 24 h; uso único.
- E-mail e id do usuário vêm do Supabase Auth (`GET /auth/v1/user`), nunca do formulário.
- Sem `PUBLIC_BASE_URL`, nada muda no comportamento atual.
- Reenvio do link no máximo a cada 10 min por identidade.
- Texto fixo em `internal/engine/messages.go`; página e termos em português do Brasil.

## Review Focus

- Link de um telefone usado para cadastrar outro (token preso à identidade; conclusão usa a identidade do link).
- Conclusão repetida (duplo clique, voltar do navegador): 409, sem duplicar assinatura nem mensagem.
- Cliente existente com conversa em andamento: depois do cadastro, a conversa segue (estado vai para
  `idle` só se estava em `onboarding`; projetos e histórico ficam).
- Supabase Auth devolve usuário sem e-mail confirmado: 401.
- Mensagens enquanto pendente não viram lote depois do cadastro (gravadas como `skipped`).

## Tarefas

1. **`internal/signup`: documentos e formulário.** `ValidCPF`, `ValidCNPJ`, `OnlyDigits`,
   `type Form`, `(Form) Normalize() (Form, error)` com listas fechadas (segmentos, objetivos, faixas,
   experiência, conta Meta). Testes de tabela.
2. **`internal/signup`: token e clientes externos.** `NewToken() (raw, hash string, err)`,
   `HashToken(raw) string`; `BrasilAPI.LookupCNPJ(ctx, cnpj) (Company, error)`;
   `SupabaseAuth.GetUser(ctx, accessToken) (User, error)` (erro se e-mail não confirmado). `httptest`.
3. **Migration 0006 + store.** Tabelas e colunas do spec, planos semeados, organizações existentes
   `pending`. Store: `CreateSignupLink`, `LastSignupLinkAt`, `SignupLinkByToken`, `SignupPrefill`,
   `Plans`, `CompleteRegistration` (transação, `ErrAlreadyRegistered`), `Identity.RegistrationStatus`.
4. **Engine: portão de cadastro.** Campos `SignupBaseURL`; em `HandleInbound`, depois do limite diário,
   pendente → `skipped` + link (throttle 10 min). Testes com o harness atual (modo direto e humano).
5. **HTTP: rotas do cadastro, página e termos.** `httpapi.Signup` com `Store`, `Auth`, `CNPJ`,
   `BotNumber`, `BaseURL`; HTML embutido (`internal/httpapi/web/`). Testes com `httptest` + banco.
6. **Config, cmd e docs.** `PUBLIC_BASE_URL`, `SUPABASE_URL`, `SUPABASE_PUBLISHABLE_KEY`,
   `BOT_WHATSAPP_NUMBER`; ligar em `cmd/api` e `cmd/worker`; `.env.example`; passo a passo do Google,
   Supabase Auth e SMTP em `docs/deploy-easypanel.md`.
7. **Revisão final independente, correções, deploy com as variáveis.**
