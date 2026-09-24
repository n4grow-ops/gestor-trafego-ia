# Cadastro do cliente — design

Data: 2026-09-23. Branch: `feat/cadastro` (a partir de `feat/atendente-humana`, que está no ar).

## Objetivo

Todo cliente passa por um cadastro completo, com e-mail verificado, antes de conversar com a IA. O
cadastro abre a partir do WhatsApp, guarda tudo no Supabase (schema `app`) e termina com o cliente no
plano Free, de volta ao WhatsApp, conversando normalmente.

Pedido do usuário (resumo): "pop-up" de cadastro a partir do WhatsApp; banco com todos os dados de
cadastro; verificação do e-mail pelo Google; perguntas específicas sobre a gestão de anúncios; toda
mensagem verifica se o cliente está cadastrado (se sim, continua de onde parou; se não, cadastra);
CPF/CNPJ opcionais com preenchimento automático; pagamento (cartão, Pix) depois, mas com a estrutura
pronta; planos Free/Starter/Pro, cadastro entra no Free.

Decisões do usuário:
- Pop-up: em fases. Agora link para página (funciona com o WAHA); WhatsApp Flows quando migrar para a
  Cloud API oficial, reaproveitando as mesmas tabelas.
- E-mail: Google ou código de 6 dígitos por e-mail (os dois pelo Supabase Auth).
- Sem cadastro completo, a IA não conversa: só manda o link.
- Clientes que já existem: recebem o link na próxima mensagem, com os dados já coletados preenchidos.
- Endereço: subdomínio próprio. Enquanto não existir, `PUBLIC_BASE_URL` aponta para o domínio do
  Easypanel da `api`.
- Obrigatórios: pessoa e negócio, perfil de anúncios, aceite de termos/LGPD. CPF e CNPJ opcionais.
  Sem upload de arquivos.
- Termos de Uso e Política de Privacidade: rascunho escrito por nós, para revisão.

Fora de escopo: WhatsApp Flows, conexão com a Meta, cobrança real, painel web do cliente, limite de
projetos por plano (guardado, não aplicado).

## Limitação: CPF

Não existe consulta pública e gratuita de nome por CPF (o caminho oficial é a Serpro, paga e com
contrato). O nome vem da conta Google quando o cliente entra com Google; o CPF só tem os dígitos
verificadores validados. CNPJ preenche sozinho pela BrasilAPI (pública e gratuita): razão social, nome
fantasia, cidade, UF e atividade principal.

## Fluxo

1. **Mensagem chega** (worker, `HandleInbound`). Identidade encontrada ou criada pelo cadastro
   automático. Se `organizations.registration_status <> 'complete'`:
   - grava a mensagem como `skipped` (não entra em lote nem gasta IA);
   - se nenhum link foi criado para essa identidade nos últimos 10 min, cria um link novo e envia
     (texto fixo, envio humano): "Oi! Pra começar, faz seu cadastro rapidinho aqui: <link>. Leva 2
     minutos e depois a gente continua por aqui.";
   - senão, não responde.
   Cliente bloqueado continua sem resposta. Limite diário vale antes (já existe).
2. **Link** `PUBLIC_BASE_URL/cadastro/<token>`: token aleatório de 32 bytes (base64url), guardado só
   como SHA-256 em `signup_links`, válido 24 h, uso único, preso à identidade do WhatsApp.
3. **Página** (servida pela `api`, HTML embutido, `supabase-js` do jsDelivr, sem framework):
   - Passo 1, e-mail: "Continuar com Google" (`signInWithOAuth`, volta para a mesma URL) ou "Receber
     código por e-mail" (`signInWithOtp` + `verifyOtp` com 6 dígitos).
   - Passo 2, dados (ver Campos). Clientes existentes chegam com nome do negócio e cidade preenchidos;
     quem entrou com Google chega com nome e e-mail.
   - Passo 3, plano: Free marcado; Starter e Pro aparecem como "em breve". Botão "Concluir".
   - Tela final: "Cadastro concluído" e botão "Voltar para o WhatsApp" (`https://wa.me/<BOT_WHATSAPP_NUMBER>`).
4. **Conclusão** (`POST /api/cadastro/<token>`, cabeçalho `Authorization: Bearer <access token do
   Supabase>`):
   - a `api` confere o token do Supabase chamando `GET <SUPABASE_URL>/auth/v1/user` (com a chave
     publishable): pega `id`, `email` e confirmação do e-mail. E-mail nunca vem do formulário;
   - valida os campos no Go;
   - numa transação: grava pessoa e negócio, aceite de termos, assinatura Free (e aplica os limites do
     plano na organização), marca o cadastro completo e o link usado, põe a conversa em `idle` com a
     memória do negócio preenchida e enfileira a mensagem de boas-vindas no WhatsApp: "Cadastro
     concluído, <nome>! Você está no plano Free. Me conta: o que você quer anunciar primeiro?";
   - responde com o link de volta ao WhatsApp.
5. A próxima mensagem do cliente vai para a IA normalmente, com o histórico de sempre.

## Campos

Obrigatórios: nome completo; e-mail verificado; nome do negócio; segmento (lista + "outro"); cidade;
UF; objetivo com anúncios (mensagens no WhatsApp, vendas no site, cadastros/leads, visitas à loja,
seguidores/engajamento); orçamento mensal pretendido (faixas: até R$ 500, R$ 500–1.500,
R$ 1.500–5.000, acima de R$ 5.000); já anuncia? (não, sim há menos de 6 meses, sim há mais de 6
meses); tem conta de anúncios na Meta? (sim, não, não sei); quem aprova os gastos (eu mesmo / outra
pessoa, com nome); aceite de Termos e Privacidade.

Opcionais: CPF (validado), CNPJ (validado e com preenchimento automático), site, Instagram, como
conheceu o serviço.

## Banco (migration 0006, schema `app`, RLS em tudo)

- `members`: `full_name`, `email`, `email_verified_at`, `auth_user_id uuid` (id do Supabase Auth, sem
  FK entre schemas), `cpf`.
- `organizations`: `registration_status` (`pending` | `complete`, padrão `pending`; todas as existentes
  ficam `pending`), `legal_name`, `trade_name`, `cnpj`, `segment`, `city`, `uf`, `website`,
  `instagram`, `ads_objective`, `monthly_budget_range`, `ads_experience`, `has_meta_ad_account`,
  `approver`, `referral_source`, `registered_at`.
- `signup_links`: `id`, `identity_id`, `token_hash` (único), `expires_at`, `used_at`, `created_at`.
- `consents`: `member_id`, `kind` (`terms` | `privacy`), `version`, `accepted_at`, `ip`, `user_agent`.
- `plans`: `code` (PK: `free`, `starter`, `pro`), `name`, `price_cents`, `currency` (`BRL`),
  `billing_interval` (`month`), `daily_message_limit`, `max_daily_budget_brl`, `max_projects`,
  `available` (só Free `true` agora), `sort_order`. Free: 60 mensagens/dia, R$ 50/dia, 3 projetos,
  preço 0. Starter e Pro: valores provisórios, `available = false`.
- `subscriptions`: `organization_id` (único: uma assinatura atual por organização), `plan_code`,
  `status` (`active` | `trialing` | `past_due` | `canceled`), `started_at`, `current_period_end`,
  `provider`, `provider_ref`.
- `payments`: `subscription_id`, `amount_cents`, `currency`, `method` (`card` | `pix` | `boleto`),
  `status` (`pending` | `paid` | `failed` | `refunded`), `provider`, `provider_ref`, `paid_at`,
  `created_at`. Fica vazia até ligar o provedor (Asaas ou Mercado Pago, a decidir).

## Componentes

- `internal/signup` (novo): validação de CPF/CNPJ, normalização e validação do formulário, token do
  link (gerar e hash), cliente da BrasilAPI (CNPJ), cliente do Supabase Auth (`GetUser`).
- `internal/store`: `CreateSignupLink`, `LastSignupLinkAt`, `SignupLinkByToken`, `CompleteRegistration`
  (transação da conclusão), `Plans`, campos novos em `Identity` (`RegistrationStatus`).
- `internal/engine`: portão de cadastro em `HandleInbound` (antes do limite diário não; depois dele).
- `internal/httpapi`: `GET /cadastro/{token}`, `GET /api/cadastro/{token}` (pré-preenchimento, planos e
  config pública do Supabase), `GET /api/cadastro/{token}/cnpj/{cnpj}`, `POST /api/cadastro/{token}`,
  `GET /termos`, `GET /privacidade`. Página e textos com `embed`.
- Config: `PUBLIC_BASE_URL`, `SUPABASE_URL`, `SUPABASE_PUBLISHABLE_KEY` (api e worker),
  `BOT_WHATSAPP_NUMBER` (api). Sem `PUBLIC_BASE_URL` o portão fica desligado (comportamento de hoje).

## Erros e segurança

- Link inválido, expirado ou usado: página explica e pede para mandar mensagem no WhatsApp (novo link).
- Token do Supabase inválido ou e-mail não confirmado: 401, a página volta ao passo 1.
- BrasilAPI fora ou CNPJ não encontrado: o cliente preenche à mão.
- `POST` repetido depois de concluído: 409 com o link de volta ao WhatsApp (idempotente para o cliente).
- A chave publishable do Supabase é pública por natureza; o schema `app` não é exposto pela API do
  Supabase, então ela não lê nada do nosso banco.
- Corpo do POST limitado a 64 KB; textos cortados no tamanho máximo de cada coluna.

## O que o usuário configura (passo a passo no doc de deploy)

1. Google Cloud: cliente OAuth "Aplicativo da Web" com o redirect do Supabase; colar ID e segredo em
   Supabase → Authentication → Providers → Google.
2. Supabase → Authentication → URL Configuration: `PUBLIC_BASE_URL` como Site URL e
   `PUBLIC_BASE_URL/cadastro/*` em Redirect URLs.
3. Supabase → Emails → template "Magic Link": incluir `{{ .Token }}` (código de 6 dígitos).
4. SMTP próprio (Resend) em Supabase → Emails → SMTP (o padrão do Supabase envia poucos e-mails por hora).
5. DNS do subdomínio quando existir, e trocar `PUBLIC_BASE_URL`.

## Testes

- `signup`: CPF/CNPJ válidos e inválidos; formulário (obrigatórios, opções fora da lista, tamanhos);
  token (hash estável, tamanho); BrasilAPI e Supabase Auth com `httptest`.
- `store`: link (criar, achar pelo token, expirado, usado); conclusão em transação (assinatura Free,
  limites do plano, consentimentos, conversa em `idle`, mensagem enfileirada); planos semeados.
- `engine`: cliente pendente recebe link e não chama a IA; segunda mensagem em 10 min não reenvia;
  cliente completo segue normal; existente pendente recebe link; portão desligado sem `PUBLIC_BASE_URL`.
- `httpapi`: página com token válido/inválido; GET de pré-preenchimento; POST sem token do Supabase
  (401), com dados inválidos (422), válido (200 e efeitos), repetido (409).
