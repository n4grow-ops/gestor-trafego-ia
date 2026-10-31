# Asaas: assinatura mensal com liberação pelo pagamento

Data: 2026-10-31. Decisões do usuário:
- só **Pix e cartão**;
- **assinatura mensal**;
- atraso: avisa e, **3 dias** depois do vencimento, a Mia **não cria campanha nova** (as que rodam seguem);
- fluxo: cadastro pelo WhatsApp → sem plano ativo, a Mia avisa e manda os **3 planos visíveis** (hoje `free` Básico R$147, `starter` Intermediário R$397, `pro` Avançado R$997) → cliente escolhe → paga → sistema liberado quando o pagamento confirma.

## Regras

- `ASAAS_API_KEY` vazio: nada muda (o cadastro continua ativando o plano `free` como hoje). Billing ligado = `cfg.AsaasEnabled()`.
- Clientes já existentes com assinatura `active` e `provider` nulo/manual continuam liberados (legado). Org `unlimited` sempre liberada.
- Liberado = assinatura `active`/`trialing`, ou `past_due` (atraso só bloqueia campanha nova, ver abaixo), ou `canceled` com `current_period_end > now()`.
- Sem plano liberado: toda mensagem do cliente (depois do portão do cadastro e da transcrição do áudio) cai no **portão do plano**, antes da Mia:
  1. Mensagem escolhe plano (número 1/2/3 da lista, nome do plano, ou código) → grava a escolha e pergunta "Pix ou cartão?".
  2. Com plano escolhido, mensagem escolhe forma ("pix" / "cartão|cartao|crédito|credito") → cria (ou reaproveita) o cliente no Asaas e a assinatura, e manda o link da primeira cobrança.
  3. Já tem link pendente e manda outra coisa → reenvia o link e diz que libera assim que o pagamento confirmar (e que pode trocar o plano dizendo o nome).
  4. Qualquer outra coisa → mensagem "Você ainda não tem um plano ativo" + lista dos planos (nome, preço/mês, campanhas/mês, projetos) e "responda com o número".
- Atendimento humano (`StateNeedsHuman`) não passa pelo portão.
- Fim do cadastro (`signupFlowAnswer`): com billing ligado, `CompleteRegistration` grava a assinatura como `pending` (não `active`) e a Mia já manda a lista de planos logo depois das boas-vindas.
- `past_due`: grava `past_due_since`. Em `requestProposal`, `past_due` há mais de 3 dias bloqueia com mensagem própria (pagar o link; campanhas no ar continuam). Antes de 3 dias passa.

## Asaas (API v3)

- Base: sandbox `https://api-sandbox.asaas.com/v3`, produção `https://api.asaas.com/v3` (`ASAAS_ENV=sandbox|producao`, padrão sandbox).
- Cabeçalhos: `access_token: <chave>`, `User-Agent: meuanuncio.io`, `Content-Type: application/json`.
- `GET /customers?externalReference=org-<id>`; `POST /customers {name, cpfCnpj, email, mobilePhone, externalReference: "org-<id>", notificationDisabled: true}`. cpfCnpj: `organizations.cnpj`, senão `members.cpf` do dono. Nome: `legal_name`/`trade_name`/`name`.
- `POST /subscriptions {customer, billingType: "PIX"|"CREDIT_CARD", value: reais, nextDueDate: hoje (America/Sao_Paulo, AAAA-MM-DD), cycle: "MONTHLY", description: "meuanuncio.io - <plano>", externalReference: "org-<id>"}` → `id` (sub_...).
- Link: `GET /subscriptions/<id>/payments` → `data[0].invoiceUrl`.
- Trocar plano/forma com assinatura pendente: `DELETE /subscriptions/<id>` e cria outra.
- Erro do Asaas: loga, avisa a equipe pelo canal de alertas existente, e responde ao cliente que o link sai em instantes / a equipe vai ajudar (sem detalhes técnicos).

## Webhook `POST /webhooks/asaas` (api)

- Desligado (sem token) → 503. Cabeçalho `asaas-access-token` comparado em tempo constante com `ASAAS_WEBHOOK_TOKEN`; diferente → 401.
- Corpo: `{id: "evt_...", event, payment: {id, customer, subscription, value, billingType, status, dueDate, paymentDate, confirmedDate, invoiceUrl, externalReference}}` ou `{id, event, subscription: {id, ...}}`.
- Idempotente: tabela `asaas_events(id text primary key, event text, received_at)`; evento repetido → 200 sem efeito.
- Eventos:
  - `PAYMENT_CREATED`: upsert `payments` (provider `asaas`, provider_ref = payment.id, status `pending`).
  - `PAYMENT_CONFIRMED`, `PAYMENT_RECEIVED`: upsert pago (`paid_at`), método PIX→pix, CREDIT_CARD→card, BOLETO→boleto, valor em centavos; assinatura (pelo `provider_ref` = payment.subscription) → `active`, `current_period_end = dueDate + 1 mês`, `past_due_since = null`. Se antes não estava `active`: enfileira aviso ao dono pelo WhatsApp "Pagamento confirmado, sistema liberado" (job novo `billing.notify`, executado pelo worker).
  - `PAYMENT_OVERDUE`: assinatura → `past_due`, `past_due_since` = agora se nulo; aviso ao dono com o link (`invoiceUrl`).
  - `PAYMENT_REFUNDED`/`PAYMENT_CHARGEBACK_REQUESTED`: payment `refunded`.
  - `SUBSCRIPTION_DELETED`/`SUBSCRIPTION_INACTIVATED`: assinatura → `canceled` (vale até `current_period_end`).
  - Desconhecido: 200, só registra o evento.
- Sempre 200 depois de gravar (o Asaas pausa a fila em erro); erro de banco → 500 para o Asaas tentar de novo.

## Banco (migration 0052)

- `subscriptions.status` aceita `pending`; `subscriptions.past_due_since timestamptz`; `subscriptions.pending_plan text`, `subscriptions.pending_method text`, `subscriptions.checkout_url text`.
- `organizations.asaas_customer_id text`.
- `payments`: índice único `(provider, provider_ref) WHERE provider_ref IS NOT NULL`.
- `asaas_events`.
- Permissões/RLS iguais às das tabelas vizinhas (schema `app`).

## Config

`ASAAS_API_KEY`, `ASAAS_ENV`, `ASAAS_WEBHOOK_TOKEN` em `config.Config`; api (webhook) e worker (cria cliente/assinatura no portão, avisos).
