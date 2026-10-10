# Deploy no Easypanel

Projeto `gestor-trafego-ia` com quatro serviços. Hostnames internos seguem o padrão `<projeto>_<serviço>`.

| Serviço | Tipo | Origem | Domínio | Observação |
|---|---|---|---|---|
| `postgres` | Postgres | serviço do Easypanel | não | backup diário para fora da VPS |
| `api` | App | GitHub `n4grow-ops/gestor-trafego-ia`, branch `main`, Dockerfile | sim (HTTPS), porta 8080 | health `/healthz` |
| `worker` | App | mesmo repositório e Dockerfile | não | comando `/app/worker`, **1 réplica** |
| `waha` | App | imagem `devlikeapro/waha:gows-<versão>` (motor GOWS, sem navegador) | sim, só para parear (painel com senha) | volumes de sessão e mídia |

## Variáveis

`api`: `DATABASE_URL`, `WEBHOOK_SECRET`.

`worker`: `DATABASE_URL`, `OPENROUTER_API_KEY`, `ATTENDANT_MODEL`, `MANAGER_MODEL`, `TRANSCRIBE_MODEL`, `WAHA_URL`, `WAHA_API_KEY`, `WAHA_SESSION`, `OPERATOR_PHONE`.

Valores de referência em `.env.example`. Segredos só no Easypanel, nunca no repositório.

Atendente humana (opcional no `worker`): `HUMANIZE` (padrão `true`), `REPLY_DEBOUNCE_SECONDS` (4), `VOICE_REPLIES` (`true`), `TTS_MODEL` (validado: `microsoft/mai-voice-2-flash`), `TTS_VOICE` (`pt-BR-FranciscaNeural`). Sem `TTS_MODEL`, a resposta é sempre em texto. `HUMANIZE=false` volta ao envio direto, sem visto, presença nem balões.

## Atendente humana

- Mensagens seguidas são respondidas juntas, depois de `REPLY_DEBOUNCE_SECONDS` sem mensagem nova.
- Antes de responder: visto azul, "digitando…" proporcional ao texto (1,5 a 7 s), até 3 balões com pausa curta entre eles.
- O worker não dorme: mostra a presença e agenda a entrega do balão no outbox (`run_after`). Resposta nova espera os balões da anterior; o Gestor começa depois dos balões, para a proposta chegar por último.
- Cliente mandou áudio: "gravando áudio…", voz gerada pelo TTS do OpenRouter (MP3) e enviada com `sendVoice` (`convert: true`, o WAHA converte para OGG/Opus). TTS falhou: vai em texto.
- Proposta, valores, código de aprovação e aviso ao operador sempre em texto.
- Requer WAHA `2026.6.1` ou mais novo (mídia no Core). Se o "auto presence" do WAHA estiver ligado, desligar para não brigar com a presença do worker.
- Teste: mandar texto (visto, digitando, balões); mandar três mensagens seguidas (uma resposta só); mandar áudio (gravando, voz).
- Custo do TTS: `SELECT sum(cost_usd), count(*) FROM llm_usage WHERE agent = 'tts';`

## Cadastro do cliente

Variáveis: `PUBLIC_BASE_URL`, `SUPABASE_URL`, `SUPABASE_PUBLISHABLE_KEY` na `api` e no `worker`; `BOT_WHATSAPP_NUMBER` na `api`.
Sem elas o cadastro fica desligado e ninguém é barrado.

Fluxo: cliente sem cadastro manda mensagem → recebe `PUBLIC_BASE_URL/cadastro/<token>` (24 h, uso único, reenvio a cada 10 min)
→ confirma o e-mail (Google ou código) → preenche dados → plano Free → volta ao WhatsApp com boas-vindas.

Configuração única no Supabase e no Google:

1. **Supabase → Authentication → URL Configuration**: Site URL = `PUBLIC_BASE_URL`; em Redirect URLs, adicionar
   `PUBLIC_BASE_URL/cadastro/*`.
2. **Google Cloud Console → APIs e serviços → Credenciais → Criar ID do cliente OAuth** (tipo "Aplicativo da Web"):
   - URI de redirecionamento autorizado: `https://jsafyoqvypkzqdtefpzp.supabase.co/auth/v1/callback`;
   - tela de consentimento: nome do app, e-mail de suporte, links de `PUBLIC_BASE_URL/termos` e `PUBLIC_BASE_URL/privacidade`;
   - copiar ID e segredo para **Supabase → Authentication → Sign In / Providers → Google** e ativar.
3. **Supabase → Authentication → Emails → Magic Link**: trocar o corpo para mostrar o código, por exemplo
   `Seu código de acesso: {{ .Token }}` (o código vale por alguns minutos).
4. **SMTP próprio** (Resend ou outro) em **Supabase → Authentication → Emails → SMTP Settings**: o envio padrão do
   Supabase é limitado a poucos e-mails por hora e não serve para produção.
5. Quando houver subdomínio: DNS apontando para a VPS (`2.25.215.158`), domínio no serviço `api` do Easypanel e
   `PUBLIC_BASE_URL` atualizado nos dois serviços (e nas URLs do passo 1).

Cuidados e limites conhecidos:
- Quem pede cadastro: só organizações do cadastro automático. As criadas pelo `admin org-create` já nascem cadastradas.
- Só o dono (`owner`) conclui o cadastro; outros membros recebem aviso para chamar o responsável.
- Ao concluir, os limites passam a ser o maior entre o que a organização tinha e o do plano Free.
- O Google bloqueia login dentro de navegadores embutidos de alguns apps. Se o WhatsApp abrir o link no navegador
  interno e o Google recusar, o cliente usa o código por e-mail (ou abre o link no navegador do celular). Testar em
  Android e iPhone.
- Ligar CAPTCHA em **Supabase → Authentication → Attack Protection** se aparecer abuso de envio de código.
- O token do link não fica no histórico (`messages` guarda `/cadastro/[link]`), mas fica no payload do job de envio
  em `outbox_jobs` até ele sair; vale 24 h e é de uso único.
- `members.auth_user_id` não é único: hoje nada faz login por ele. Revisar antes de criar painel web do cliente.
- Limitador de requisições do cadastro fica em memória (uma réplica da `api`).

Termos e Privacidade: rascunhos em `internal/httpapi/web/termos.html` e `privacidade.html`, com campos entre colchetes
(razão social, CNPJ, e-mail de contato, foro) para preencher e revisar com advogado. A versão aceita fica em `consents`.

Planos: tabela `plans` (Free disponível; Starter e Pro com valores provisórios e `available = false`). Assinatura em
`subscriptions`; `payments` fica vazia até ligar o provedor de pagamento (Pix e cartão).

## Conexão da conta Meta

Variáveis na `api` e no `worker` (com `PUBLIC_BASE_URL`):

| Variável | O que é |
|---|---|
| `META_APP_ID` | ID do app `meuanuncio.io` no developers.facebook.com |
| `META_APP_SECRET` | Chave secreta do app (confere `signed_request` e assina as chamadas com `appsecret_proof`) |
| `META_LOGIN_CONFIG_ID` | ID da configuração do Facebook Login for Business |
| `META_TOKEN_KEY` | 32 bytes em base64 que cifram os tokens dos clientes (`openssl rand -base64 32`). Trocar a chave invalida as conexões existentes |

Sem as quatro, a conexão fica desligada (nenhuma rota `/m/` ou `/meta/*`, nenhum link no WhatsApp).

No app Meta (Facebook Login for Business → Configurações):
- URI de redirecionamento OAuth válido: `PUBLIC_BASE_URL/meta/callback` (modo estrito);
- URL de retorno de desautorização: `PUBLIC_BASE_URL/meta/deauthorize`;
- URL de solicitação de exclusão de dados: `PUBLIC_BASE_URL/meta/exclusao-dados`.

Fluxo: o dono manda "conectar meta" (ou aceita a oferta da IA, ou termina a prévia do anúncio) → recebe `PUBLIC_BASE_URL/m/<token>` (24 h, uso único, reenvio a cada 10 min) → login no Facebook → escolhe conta e Página → confirmação com avisos no WhatsApp. Até o App Review, só quem tem papel no app conclui o login.

## Réplicas

`worker` com 1 réplica. As conversas têm trava por conversa no banco, mas uma réplica só mantém a ordem das mensagens e o custo previsível.

## WAHA

O open-wa (4.76, última imagem de fev/2025) não funciona mais com o WhatsApp Web atual: trava esperando uma função que não existe e reinicia em loop. Trocado pelo WAHA em 2026-09-23.

Variáveis do serviço `waha`:

```
WAHA_API_KEY=<igual ao WAHA_API_KEY do worker>
WHATSAPP_DEFAULT_ENGINE=GOWS
WHATSAPP_START_SESSION=default
WHATSAPP_HOOK_URL=http://gestor-trafego-ia_api:8080/webhooks/whatsapp?token=<WEBHOOK_SECRET>
WHATSAPP_HOOK_EVENTS=message
WAHA_BASE_URL=http://gestor-trafego-ia_waha:3000
WAHA_DASHBOARD_USERNAME / WAHA_DASHBOARD_PASSWORD
WHATSAPP_SWAGGER_USERNAME / WHATSAPP_SWAGGER_PASSWORD
```

- Volumes: `/app/.sessions` (sessão pareada) e `/app/.media` (arquivos recebidos).
- Pareamento: painel `https://<domínio do waha>/dashboard` (usuário e senha do painel) mostra o QR da sessão `default`. Alternativa sem QR: `POST /api/default/auth/request-code` com `{"phoneNumber":"55..."}` devolve um código para digitar em WhatsApp → Aparelhos conectados → Conectar com número de telefone.
- Versão Core (grátis): uma sessão, `default`.
- Número de teste descartável. O WAHA usa o protocolo do WhatsApp Web e não é oficial: não usar o número central de produção.

## Primeiro acesso

1. Parear o número de teste pelo painel do WAHA (QR ou código por número).
2. No console do `worker`:
   ```
   /app/admin org-create --name "Minha Empresa" --niche "restaurante" --max-budget 100
   /app/admin member-add --org 1 --name "Seu Nome" --role owner --phone 55DDDNUMERO
   ```
3. Mandar mensagem ao número de teste pedindo uma campanha.

## Operação

- Atendimento humano: cada número de `TEAM_PHONES` (vírgulas; vazio = `OPERATOR_PHONE`) recebe aviso no privado com o motivo (última fala do cliente, cortada e sem segredos) e o link do painel (números exatamente como registrados no WhatsApp; se o aviso não chegar, teste com ou sem o nono dígito). Fora da janela de 24 h vai o modelo `aviso_equipe`. Opcional: grupo da equipe por instância Wame não oficial com `TEAM_GROUP_JID` (`…@g.us`) + `TEAM_GROUP_WAME_KEY` (+ `TEAM_GROUP_WAME_TOKEN`). Ao terminar: `/app/admin resolve-human --phone 55DDDNUMERO`.
- Custo por agente: `SELECT agent, model, sum(cost_usd) FROM llm_usage GROUP BY 1, 2;`
- Auditoria: `SELECT created_at, actor, action, data FROM audit_events ORDER BY id DESC LIMIT 50;`

## Banco no Supabase

Projeto `jsafyoqvypkzqdtefpzp`, região Canada Central (ca-central-1), pooler `aws-0-ca-central-1`.

1. Rodar `docs/supabase/setup.sql` uma vez no SQL Editor. Ele cria o usuário `gestor_app` (sem superusuário, sem BYPASSRLS), o schema privado `app` e mostra a senha gerada uma única vez.
2. `DATABASE_URL` de `api` e `worker` (modo sessão do Supavisor, porta 5432, TLS com verificação de certificado e de host):

   ```
   postgres://gestor_app.jsafyoqvypkzqdtefpzp:<SENHA>@aws-0-ca-central-1.pooler.supabase.com:5432/postgres?sslmode=verify-full&sslrootcert=/app/certs/supabase-root-2021.crt
   ```

3. Network Restrictions: permitir só o IP da VPS (`2.25.215.158/32`).
4. Settings → API → Exposed schemas: `app` não pode aparecer.
5. Todas as tabelas nascem com RLS (migration 0003) e o teste `TestEveryTableHasRowLevelSecurity` impede tabela nova sem RLS.

Certificado: `deploy/supabase-root-2021.crt` (Supabase Root 2021 CA, válido até 2031),
SHA-256 `80:70:25:AD:50:D4:ED:21:9D:2C:9C:7D:29:9C:00:4F:82:4E:B0:0C:F7:F6:5A:FE:F6:07:D0:7B:72:E6:CA:FA`.
