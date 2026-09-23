---
tags: [spec, design, ia, whatsapp, easypanel, go]
status: em-revisao
created: 2026-09-22
---

# Spec — Duas IAs no WhatsApp rodando no Easypanel

Etapa 1 do [[gestor-trafego-ia-projeto|Gestor de Tráfego IA]]. Objetivo: o cliente conversa pelo WhatsApp, o **Atendente** responde, o **Gestor** monta a proposta de campanha nos bastidores e o cliente aprova o rascunho. Tudo em produção no Easypanel. **Meta Ads fica fora desta etapa**: o rascunho aprovado é o ponto de encaixe para a publicação futura.

## Decisões desta etapa

| Tema | Decisão |
|---|---|
| Separação da IA | duas instâncias: Atendente (fala com o cliente) e Gestor (especialista, bastidores) |
| Contato com o cliente | só o Atendente fala com o cliente |
| Motivos | qualidade, custo, segurança (prompt injection) e manutenção independente |
| Forma | dois agentes no mesmo backend Go, contratos tipados entre eles (sem microserviços) |
| Canal WhatsApp | open-wa (`@open-wa/wa-automate` v4.x, EASY API) nesta etapa, com número de teste descartável. Evolution sai do plano (R1). Produção com clientes usa Cloud API oficial, trocando só o adaptador |
| Hospedagem | Easypanel em VPS Hostinger (D6) |
| LLM | OpenRouter (API compatível com OpenAI); modelo de cada agente por variável de ambiente |
| Transcrição | modelo multimodal via OpenRouter |
| Aprovação | por texto: `SIM <código>`; botões ficam para a Cloud API |

## Fora desta etapa

Marketing API e publicação, OAuth Meta, painel web, login, planos e cobrança, Cloud API, relatório diário com métricas reais, geração de imagem.

## Componentes

```
WhatsApp ─► openwa (Node, Easypanel) ─webhook─► api (Go)
                                                   │ inbox (dedup) + outbox
                                                   ▼
                                               worker (Go)
                                   ┌───────────────┴───────────────┐
                            ATENDENTE (IA 1)                  GESTOR (IA 2)
                         modelo barato/rápido              modelo forte, sob demanda
                         vê: conversa                      vê: Briefing (JSON) — nunca texto cru
                         devolve: intenção, campos,        devolve: CampaignProposal
                         resposta ao cliente
                         ferramentas: nenhuma              ferramentas: nenhuma
                                   └──── agent/contract (tipos + JSON Schema) ───┘
                                                   │
                                   domínio Go: valida limites, cria rascunho,
                                   emite código de aprovação, registra auditoria
```

| Unidade | Responsabilidade | Depende de |
|---|---|---|
| `cmd/api` | HTTP: webhook do open-wa, `/healthz` | `inbox`, Postgres |
| `cmd/worker` | processa inbox e outbox, roda os agentes | todos os pacotes abaixo |
| `whatsapp` | interface `Provider` (`Send`, parse de webhook); adaptador `openwa` | HTTP do open-wa |
| `inbox` / `outbox` | idempotência de entrada; jobs com retry | Postgres |
| `conversation` | máquina de estados da conversa | `agent/contract` |
| `agent/contract` | `AttendantTurn`, `Briefing`, `CampaignProposal` + schemas | nada |
| `agent/attendant` | prompt, chamada e parse do Atendente | `llm`, `contract` |
| `agent/manager` | prompt, chamada e parse do Gestor | `llm`, `contract` |
| `llm` | interface `Provider` (`Complete`, `Transcribe`); adaptador OpenRouter; registro de tokens e custo | HTTP |
| `campaign` | valida proposta, cria `campaign_draft`, código de aprovação | `contract`, Postgres |
| `audit` | trilha de eventos | Postgres |

Regra central: Atendente e Gestor só se falam por `agent/contract`. Nenhum agente chama ferramenta; toda ação é do domínio Go.

## Contratos

**AttendantTurn** (saída do Atendente, a cada mensagem)
`intent` (`create_campaign` · `provide_info` · `approve` · `reject` · `question` · `human` · `other`), `fields` (parciais: `offer`, `audience`, `location`, `daily_budget_brl`, `objective_hint`), `missing[]`, `reply` (texto ao cliente).

**Briefing** (montado pelo Go a partir dos campos validados)
`organization_id`, `offer`, `audience`, `location`, `daily_budget_brl`, `objective_hint`, `media[]` (IDs), `constraints` (`max_daily_budget_brl`, `allowed_objectives[]`, `niche`).

**CampaignProposal** (saída do Gestor)
`objective`, `audience` (idade, gênero, interesses, raio), `daily_budget_brl`, `duration_days`, `copies[]` (título, texto, CTA), `rationale`, `risks[]`.

O Go valida o schema e depois as regras: orçamento ≤ `max_daily_budget_brl`, objetivo em `allowed_objectives`, tamanho de copy.

## Fluxos

**A — Pedido de campanha**
1. Webhook entra no inbox com chave única (`provider` + `message_id`); duplicada é descartada.
2. Áudio → transcrição. Atendente recebe histórico recente + estado → `AttendantTurn`.
3. Faltam campos → Atendente pergunta; volta ao passo 2.
4. Campos completos → Go monta `Briefing` e enfileira `manager.propose` no outbox.
5. Gestor → `CampaignProposal`. Inválida → nova tentativa com o erro (máx. 2); persistindo, escala para humano e não gera rascunho.
6. Go cria `campaign_draft` (hash da proposta) e código de aprovação (6 dígitos, 30 min, uso único). Atendente resume a proposta e pede `SIM <código>`.
7. `SIM` + código correto + telefone autorizado → rascunho `approved`, auditado. (Publicação: próxima etapa.) `NÃO` ou pedido de ajuste → volta ao Gestor com o ajuste como campo.

**B — Pergunta geral**: Atendente responde direto (`question`). Sem Gestor.

**C — Pedir humano** (`human`): conversa marcada `needs_human`, bot para de responder, aviso ao operador (mensagem para o número do operador configurado).

## Estados

- `conversations.state`: `idle` → `collecting` → `awaiting_proposal` → `awaiting_approval` → `idle`; mais `needs_human`.
- `campaign_drafts.status`: `proposed` → `approved` · `rejected` · `expired` · `cancelled`.

## Modelo de dados (mínimo)

`organizations` (limites: `max_daily_budget_brl`, `allowed_objectives`, `niche`) · `members` (nome, papel `owner|approver|viewer`) · `whatsapp_identities` (`unique(provider, phone_e164)`, `member_id`) · `conversations` · `messages` · `inbox_events` (`unique(provider, external_id)`) · `outbox_jobs` · `campaign_drafts` (`proposal jsonb`, `proposal_hash`, `approved_by`, `approved_at`) · `approval_codes` · `llm_usage` (agente, modelo, tokens, custo, org) · `audit_events`.

Cadastro de organização e telefone nesta etapa: comando `cmd/admin` (sem painel). Telefone desconhecido recebe mensagem padrão e não aciona IA.

## Erros e segurança

| Falha | Tratamento |
|---|---|
| LLM fora ou timeout | 2 retentativas com backoff; cliente recebe "volto em instantes"; job fica no outbox |
| Proposta inválida | nova tentativa com erro no prompt (máx. 2); depois humano |
| Manipulação ("ignore as regras…") | texto só chega ao Gestor como valor de campo; limites validados no Go |
| Código errado, vencido, repetido ou de outro telefone | recusa + auditoria |
| Webhook duplicado | chave única no inbox |
| open-wa cai ou desloga | health check + restart no Easypanel; sessão em volume persistente |
| Webhook forjado | segredo compartilhado no header, comparação em tempo constante |

Segredos só em variáveis de ambiente do Easypanel. Logs sem conteúdo de mensagem em nível `info`.

## Implantação no Easypanel

Projeto `gestor-trafego-ia` com serviços:

| Serviço | Tipo | Notas |
|---|---|---|
| `api` | App (Dockerfile, Git) | domínio HTTPS; expõe webhook e `/healthz` |
| `worker` | App (mesmo repositório, outro comando) | sem domínio |
| `postgres` | Postgres do Easypanel | backup diário para fora da VPS |
| `openwa` | App (imagem `openwa/wa-automate`) | volume para sessão; webhook → `api`; só rede interna, sem domínio público |

Migrations rodam no start do `api`. Variáveis: `DATABASE_URL`, `OPENROUTER_API_KEY`, `ATTENDANT_MODEL`, `MANAGER_MODEL`, `TRANSCRIBE_MODEL`, `OPENWA_URL`, `OPENWA_API_KEY`, `WEBHOOK_SECRET`, `OPERATOR_PHONE`.

## Testes

- Contratos: schemas com casos válidos e inválidos.
- Agentes: `llm.Provider` falso com respostas gravadas; nenhum teste gasta tokens.
- Segurança: conjunto fixo de tentativas de manipulação; nenhuma gera rascunho acima do limite.
- Aprovação: código errado, vencido, repetido, de outro telefone → recusado.
- Integração: Postgres real em Docker; inbox com duplicatas.
- Avaliação manual antes do piloto: 20 conversas de exemplo, nota da proposta do Gestor.

## Critério de saída

No Easypanel, com o número de teste: mandar texto e áudio pedindo uma campanha, receber perguntas do que falta, receber a proposta resumida, responder `SIM <código>` e ver o rascunho `approved` com auditoria e custo de tokens por agente.

## Pendências

- Repositório Git do código (GitHub `n4grow-ops`?) para o Easypanel puxar.
- Número de WhatsApp de teste descartável.
- Modelos iniciais de cada agente (sugestão: Atendente em modelo "flash"; Gestor em modelo forte).
- Rotacionar o token do MCP do Easypanel (exposto no chat em 2026-09-22).
