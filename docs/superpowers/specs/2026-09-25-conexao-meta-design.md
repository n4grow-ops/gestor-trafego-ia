# Conexão da conta Meta pelo WhatsApp — design

Data: 2026-09-25 · Branch: `feat/meta-conexao` (sobre `feat/cadastro`)

## Objetivo

O cliente conecta a própria conta de anúncios Meta sem sair da conversa com o meuanuncio: o bot manda um link curto, o cliente faz o Facebook Login for Business no navegador, escolhe conta de anúncios, Página e Instagram, e volta ao WhatsApp com a confirmação e os avisos do que falta para anunciar.

Sucesso: o usuário (admin do app, Limited access) abre o link recebido no WhatsApp, conecta a conta `act_3538929729609697` e a Página "Clara Luz", e recebe no WhatsApp "Conectado" com os avisos de moeda USD e de Página sem WhatsApp.

## Fora do escopo

Publicar campanha pela API, parceria na BM (caminho B), trocar de conta pelo painel, App Review. "conectar meta" de novo substitui a conexão anterior.

## Pré-requisitos Meta (feitos em 2026-09-25)

App `1084921371182530`, configuração do Facebook Login for Business `META_LOGIN_CONFIG_ID` (token de usuário do sistema, sem validade, ativos: contas de anúncios, Páginas, Instagram; permissões `ads_management`, `ads_read`, `business_management`, `pages_show_list`, `pages_read_engagement`, `pages_manage_ads`), redirect `https://cadastro.meuanuncio.io/meta/callback` com modo estrito.

Até o App Review, só quem tem papel no app conclui o login.

## Fluxo

1. **Pedido do link (worker, `engine`).**
   - Gatilhos: (a) o cliente escreve algo que casa com `conectar (a )?(meta|facebook|conta de an[uú]ncios?)`, tratado antes da IA; (b) a IA (Atendente) devolve a intent nova `connect_meta` — o cliente pediu com outras palavras ou aceitou a oferta da IA; (c) depois que a prévia do anúncio é enviada (job `creatives.preview`), se a organização não tem conexão ativa.
   - **IA sabe da conexão:** `attendant.Input` ganha `MetaConnected bool` e `MetaAccount string` (nome da conta e da Página, sem IDs nem token). O prompt instrui: sem conexão, quando o assunto for publicar, anunciar, prazo para ir ao ar ou status do projeto pronto, avisar em uma frase que falta conectar a conta de anúncios e oferecer o link; se o cliente aceitar ou pedir, devolver `connect_meta`. Não repetir a oferta em toda mensagem (no máximo uma vez a cada 6 turnos, conferido pelo histórico). Com conexão, pode citar a conta conectada quando perguntarem.
   - Com `connect_meta`, o engine envia a resposta da IA e, em seguida, o link (a IA nunca escreve o link).
   - Só o `owner` recebe o link; outros membros recebem "peça ao responsável pela conta".
   - No máximo um link a cada 10 min por identidade (mesma regra do cadastro).
   - Link: `PUBLIC_BASE_URL/m/{token}`, token de 12 caracteres (`signup.NewToken`), só o hash no banco, validade 24 h, uso único. `redactLinks` passa a mascarar também `/m/`.
2. **Página do link (api, `GET /m/{token}`).** Valida o link (existe, não usado, não expirado). Gera um nonce, grava em cookie `HttpOnly; Secure; SameSite=Lax; Path=/meta` (10 min) e mostra uma página curta com o botão "Conectar com Facebook" apontando para `https://www.facebook.com/v23.0/dialog/oauth?client_id=…&config_id=…&redirect_uri=…&response_type=code&override_default_response_type=true&state={token}.{nonce}`. Link inválido: página de erro com botão para voltar ao WhatsApp e pedir outro.
3. **Retorno (api, `GET /meta/callback`).**
   - `error=access_denied`: página "conexão cancelada" com botão para voltar ao WhatsApp.
   - Confere `state` (token válido + nonce igual ao cookie). Falha → 400, página de erro.
   - Troca `code` por token (`/oauth/access_token` com `client_id`, `client_secret`, `redirect_uri`).
   - Lê `/me` (id do usuário no app), `/me/adaccounts` (`id,name,account_status,currency,timezone_name,disable_reason,business`), `/me/accounts` (`id,name`) e, por Página, `instagram_business_account{username}` e `whatsapp_number`. Todas as chamadas com `appsecret_proof`.
   - Grava a conexão como `pending` com o token criptografado e as listas.
   - Uma conta e uma Página: conclui direto (passo 5). Mais de uma: mostra a página de escolha (passo 4). Nenhuma conta ou nenhuma Página: página explicando o que liberar no login e botão "tentar de novo" (novo diálogo com o mesmo link).
4. **Escolha (api, `POST /api/meta/{token}/escolha`).** Recebe `ad_account_id` e `page_id`; valida que estão na lista gravada; conclui.
5. **Conclusão.** Numa transação: conexão anterior da organização vira `replaced`, a nova vira `active` com os IDs escolhidos, o link é marcado usado, auditoria `meta_connected`, e um job de mensagem humana (padrão `WelcomeJob`) com o resumo e os avisos. Página final "Pronto, pode voltar pro WhatsApp" com botão `https://wa.me/{BOT_WHATSAPP_NUMBER}`.
6. **Mensagem no WhatsApp.**
   - "Conectado ✅ Conta: {nome} ({moeda}) · Página: {nome} · Instagram: @{user} (ou 'sem Instagram vinculado')".
   - Avisos, se houver: conta não ativa (`account_status != 1`, com o motivo); moeda diferente de BRL ou fuso diferente de America/Sao_Paulo; Página sem `whatsapp_number` → passo a passo curto do WhatsApp Business (Configurações → Ferramentas comerciais → Facebook e Instagram).
7. **Desconexão (api, `POST /meta/deauthorize` e `POST /meta/exclusao-dados`).** Recebem `signed_request`, conferido com HMAC-SHA256 do `META_APP_SECRET`. Conexões ativas com aquele `meta_user_id` viram `revoked` e o token é apagado. Exclusão de dados responde `{"url": PUBLIC_BASE_URL + "/meta/exclusao/{codigo}", "confirmation_code": codigo}`; `GET /meta/exclusao/{codigo}` mostra o status. O bot avisa no WhatsApp que a conta foi desconectada.

## Dados (migration 0010, schema `app`, RLS ligada)

- `meta_connect_links`: `id`, `identity_id` FK, `token_hash` UNIQUE, `expires_at`, `used_at`, `created_at`.
- `meta_connections`: `id`, `organization_id` FK, `connected_by` (identity FK), `meta_user_id`, `business_id`, `ad_account_id`, `ad_account_name`, `currency`, `timezone_name`, `account_status`, `page_id`, `page_name`, `instagram_id`, `instagram_username`, `page_whatsapp` (bool), `token_ciphertext` bytea, `scopes` text[], `candidates` jsonb (listas para a escolha), `status` CHECK (`pending`, `active`, `replaced`, `revoked`), `deletion_code`, `created_at`, `connected_at`, `revoked_at`. Índice único parcial: uma `active` por organização.
- `reset contato` (`scripts/resetcontato`) passa a apagar as duas tabelas.

## Segurança

- Token Meta criptografado com AES-256-GCM (nonce aleatório prefixado), chave `META_TOKEN_KEY` (32 bytes em base64) só nas variáveis de ambiente. Pacote novo `internal/secretbox`.
- Token nunca vai para log, auditoria, histórico ou IA.
- Páginas `/m/` e `/meta/*` com os mesmos cabeçalhos das páginas de cadastro (no-store, no-referrer, CSP) e limite por IP.
- `state` com nonce em cookie contra CSRF no retorno; link de uso único.

## Configuração

Novas variáveis (api e worker): `META_APP_ID`, `META_APP_SECRET`, `META_LOGIN_CONFIG_ID`, `META_TOKEN_KEY`. `MetaConnectEnabled()` exige as quatro mais `PUBLIC_BASE_URL`. Desligada: nenhuma rota `/m/` ou `/meta/*`, nenhum gatilho no worker.

## Componentes

- `internal/meta`: cliente HTTP da Graph API v23.0 (troca de código, `/me`, contas, Páginas, `signed_request`), timeout 15 s, `appsecret_proof`. Interface para o fake dos testes.
- `internal/secretbox`: `Seal`/`Open`.
- `internal/store/metaconnect.go`: links, conexões, conclusão, revogação.
- `internal/httpapi/metaconnect.go` + `web/meta.html` (uma página com estados: botão, escolha, pronto, erro).
- `internal/engine/metaconnect.go`: gatilhos, envio do link, `MetaConnectedJob`, mensagens.

## Testes

- `secretbox`: ida e volta; chave errada falha; texto adulterado falha.
- `meta`: `signed_request` válido e adulterado; parse das respostas com `httptest.Server`.
- `store` (`testdb`): link expirado/usado; conclusão substitui a anterior; revogação apaga token; RLS nas tabelas novas.
- `httpapi`: link inválido; `state` sem cookie → 400; callback com uma conta conclui e enfileira o job; com duas contas mostra a escolha; escolha fora da lista → 400; cancelamento; deauthorize com assinatura inválida → 400.
- `attendant`: prompt recebe o estado da conexão; `connect_meta` é intent válida.
- `engine`: intent `connect_meta` envia resposta + link; "conectar meta" do owner gera link; de membro recebe aviso; reenvio em menos de 10 min não gera outro; após a prévia sem conexão pede a conexão; com conexão ativa não pede; mensagem com cada aviso.

## Validação ao vivo

Ligar as quatro variáveis na api e no worker, mandar "conectar meta" do 5511971214993 (que precisa ser dono de uma organização com cadastro completo) e concluir com a conta do usuário. Conferir `is_business_login` e o tipo do token recebido (`debug_token` → `SYSTEM_USER`).
