---
tags: [spec, design, etapa-2, projetos, memoria, cadastro]
status: aprovado
created: 2026-09-23
---

# Spec — Etapa 2: cadastro automático, projetos por cliente e memória do negócio

Continuação da [[2026-09-22-duas-ias-whatsapp-easypanel-design|etapa 1]]. Banco já no Supabase (schema `app`, RLS em todas as tabelas).

## Objetivo

- Número novo vira cliente sozinho (sem `admin`), com limites de custo.
- Cada cliente tem vários projetos (ex.: "Pizzaria", "Delivery"), cada um com seus dados, estado e propostas.
- O cliente retoma um projeto depois, lista os projetos e consulta o resumo de um projeto.
- O bot guarda a memória do negócio (nome, cidade, público, o que vende) e não pergunta de novo.
- Conta Meta continua fora desta etapa.

## Decisões

| Tema | Decisão |
|---|---|
| Projeto | entidade própria (`projects`); a conversa aponta o projeto ativo |
| Estado de trabalho | continua na conversa (máquina de estados da etapa 1); o projeto guarda o snapshot (campos + estágio) |
| Cadastro automático | ligado por `AUTO_SIGNUP=true`; cliente nasce com teto R$ 50/dia (`AUTO_SIGNUP_MAX_BUDGET`) e 60 mensagens/dia (`AUTO_SIGNUP_DAILY_LIMIT`) |
| Cadastro manual | `admin` continua; ganha `org-block`, `org-unblock`, `org-limits` |
| Resumo e lista de projetos | montados pelo Go a partir do banco (sem IA, números exatos) |
| Busca de projeto por nome | Go, por semelhança (sem acento, sem caixa, trecho contido); ambíguo → pergunta |
| Memória do negócio | Atendente extrai `profile`; Go valida e mescla (vazio não apaga) |

## Modelo de dados (migration 0004)

- `organizations`: `profile jsonb` (business_name, city, audience, offer), `status` (`active`/`blocked`), `daily_message_limit` (padrão 60), `signup_source` (`admin`/`auto`).
- `projects` (nova, RLS): `id`, `organization_id`, `name`, `stage` (`collecting`, `awaiting_proposal`, `awaiting_approval`, `approved`, `archived`), `fields jsonb`, `created_at`, `updated_at`; `unique(organization_id, lower(name))`.
- `conversations`: `active_project_id` (FK); estado `onboarding` passa a ser válido.
- `campaign_drafts`: `project_id` (FK), preenchido a partir do projeto ativo.
- Conversão: conversa existente com campos vira projeto "Campanha 1" ativo.

## Conversa

1. **Número novo** (com `AUTO_SIGNUP`): cria organização (`signup_source=auto`), membro `owner` e identidade numa transação; conversa em `onboarding`. O Atendente pergunta nome do negócio e cidade; com os dois no `profile`, conversa vai para `idle`.
2. **Bloqueado** (`status=blocked`): silêncio, sem IA.
3. **Limite diário**: ao passar de `daily_message_limit` mensagens recebidas no dia (fuso de São Paulo), responde uma única vez que o limite acabou; depois silêncio até o dia seguinte. Sem IA.
4. **Intents novas do Atendente** (com campo `project` = nome citado):
   - `new_project`: salva o projeto atual, cria (ou reabre, se já existe) o projeto citado e o ativa. Campos iniciais: cidade e público vindos da memória do negócio.
   - `switch_project`: salva o atual, ativa o citado e retoma com a mensagem "Retomei <nome>" + o que falta (ou convite para gerar a proposta, se completo).
   - `list_projects`: lista nome e situação de cada projeto.
   - `project_status`: resumo do projeto citado (ou do ativo): situação, dados coletados, última proposta, aprovação.
5. **Troca de projeto**:
   - em `awaiting_proposal`: recusada ("estou montando a proposta de <nome>"), porque o job usa a conversa;
   - em `awaiting_approval`: o rascunho aberto vira `cancelled` e o projeto volta a `collecting` com os campos completos.
6. **Sem projeto ativo** e conversa de campanha: cria o projeto com o nome citado ou "Campanha N".
7. **Sincronização**: toda gravação da conversa em `collecting`, `awaiting_proposal` ou `awaiting_approval` atualiza campos e estágio do projeto ativo na mesma transação. Aprovação marca o projeto `approved`.
8. **Memória no contexto**: `profile` entra no prompt do Atendente (não pergunta o que já sabe) e no `Briefing` do Gestor.

## Erros e segurança

| Situação | Tratamento |
|---|---|
| Cadastro automático abusado | teto R$ 50/dia, 60 mensagens/dia, `org-block` no admin |
| Nome de projeto ambíguo ou inexistente | Go pergunta qual, listando os projetos; nada é criado por engano em `switch_project` |
| Projeto com nome repetido | `unique(organization_id, lower(name))`; `new_project` reabre o existente |
| Profile com texto vazio ou longo | vazio não apaga; cada campo limitado a 120 caracteres |
| Projeto de outra organização | toda busca de projeto filtra por `organization_id` da identidade |
| Tabela nova | RLS obrigatório (teste da etapa 1 cobre `projects`) |

## Testes

- Store: criação/reabertura de projeto, unicidade sem caixa, busca por semelhança, sincronização conversa→projeto, `project_id` no rascunho, cadastro automático transacional, contagem diária de mensagens, isolamento por organização.
- Engine: número novo em onboarding; perfil completo encerra onboarding; bloqueado em silêncio; limite diário responde uma vez; criar, trocar (inclusive recusa em `awaiting_proposal` e cancelamento em `awaiting_approval`), listar e consultar projetos; memória no prompt do Atendente e no briefing do Gestor.
- Contrato: `Profile.Merge` não apaga com vazio e corta em 120 caracteres; intents novas válidas.
- Admin: `org-block`, `org-unblock`, `org-limits`.

## Critério de saída

No número de teste, com um celular não cadastrado: o bot pergunta nome e cidade, cria o cliente; o cliente cria "Pizzaria", troca para "Delivery", volta para "Pizzaria" e retoma de onde parou; "quais projetos eu tenho?" lista os dois; "como está a pizzaria?" traz o resumo; a proposta da Pizzaria já vem com a cidade da memória.
