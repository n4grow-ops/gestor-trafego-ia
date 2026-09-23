# Etapa 2 — Clientes, projetos e memória: Plano de Implementação

> Formato enxuto (aprovado). Execução inline com TDD; branch `feat/etapa-2` a partir de `feat/etapa-1`.

**Spec:** `docs/superpowers/specs/2026-09-23-etapa2-clientes-projetos-memoria-design.md`

## Global Constraints
- Mesmas da etapa 1 (pgx único, texto ao cliente em pt-BR, nenhum agente executa ação, TEST_DATABASE_URL obrigatório).
- Toda tabela nova com RLS; toda busca por projeto filtra por `organization_id`.
- Resumo e lista de projetos gerados em Go, sem IA.

## Review Focus
- Cliente troca de projeto durante `awaiting_proposal` → recusa, sem misturar campos.
- Nome de projeto com acento/caixa diferente ("pizzaria" x "Pizzaria") → mesmo projeto.
- `profile` com string vazia não apaga memória.
- Mensagem nº 61 do dia → um aviso só; 62+ silêncio.
- Projeto de outra organização com mesmo nome → nunca retornado.

### Task 1: Contrato
`internal/agent/contract`: `Profile{BusinessName, City, Audience, Offer *string}` + `Merge` (vazio não apaga, corta em 120 runas) + `Complete()` (nome e cidade); intents `new_project`, `switch_project`, `list_projects`, `project_status`; `AttendantTurn` ganha `Profile Profile` e `Project string` (≤ 60 runas); `Briefing` ganha `Business Profile`.

### Task 2: Migration 0004 + store de projetos e organização
Colunas e tabela da spec; estado `onboarding`; conversão "Campanha 1". Store: `CreateOrReopenProject`, `FindProjects(orgID, query)`, `ListProjects`, `GetProject`, `SetActiveProject` (salva snapshot), `SetProjectStage`, `ProjectSummaryData`, `UpdateOrgProfile`, `SetOrgStatus`, `SetOrgLimits`, `AutoSignup`, `CountInboundToday`; `Identity` ganha `Profile`, `Status`, `DailyMessageLimit`; `Conversation` ganha `ActiveProjectID`; `updateConversation` sincroniza o projeto ativo; `SaveProposal` grava `project_id`.

### Task 3: Atendente
Prompt com memória do negócio, projeto ativo e intents novas; `Input` ganha `Profile`, `ProjectName`, `Projects []string`.

### Task 4: Gestor
Briefing com `Business`; prompt usa a memória.

### Task 5: Engine
Cadastro automático, bloqueio, limite diário, onboarding, intents de projeto, troca com as regras da spec, resumo/lista em Go, memória mesclada a cada turno.

### Task 6: Admin, config e worker
`org-block`, `org-unblock`, `org-limits --max-budget --daily-limit`; config `AUTO_SIGNUP`, `AUTO_SIGNUP_MAX_BUDGET`, `AUTO_SIGNUP_DAILY_LIMIT`.

### Task 7: Deploy e aceite
Migration no Supabase, variáveis no worker, teste de aceite da spec com celular não cadastrado.
