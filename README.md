# Gestor de Tráfego IA

SaaS que transforma conversas no WhatsApp em campanhas de Meta Ads, com dois agentes de IA:

- **Atendente (IA 1)**: única voz com o cliente; coleta oferta, público, local e orçamento.
- **Gestor (IA 2)**: especialista em tráfego nos bastidores; recebe só o briefing estruturado e devolve a proposta.

Nenhuma IA executa ação. O código Go valida limites, gera o rascunho e só aceita aprovação por `SIM <código>`
(6 dígitos, 30 minutos, uso único, 5 tentativas) de membro `owner` ou `approver`.

## Binários

| Comando | Papel |
|---|---|
| `cmd/api` | webhook do WhatsApp (`POST /webhooks/whatsapp?token=...`), `GET /healthz`, cadastro e painel da equipe (`/admin`); roda migrations |
| `cmd/worker` | processa inbox e outbox; chama Atendente, Gestor e transcrição |
| `cmd/admin` | `org-create`, `member-add`, `resolve-human` |

Configuração: ver `.env.example`. Deploy: `docs/deploy-easypanel.md`.

## Painel da equipe (admin)

Servido pelo `cmd/api`: em `https://admin.meuanuncio.io/` (o host do painel responde só o painel) e em
`/admin/` em qualquer host. Login por código de 6 dígitos no e-mail (Supabase Auth), pedido e conferido pelo
servidor; só entra quem está ativo em `team_members`. Sessão em cookie `__Host-` HttpOnly/Secure/SameSite=Strict
assinado (HMAC, 12 h), token CSRF em toda ação e limite de tentativas no login. Toda ação vai para
`admin_audit_log`. Papel `staff` não acessa faturamento, equipe, aba Mia nem zerar contato (checado no servidor).

| Variável | Onde | Para quê |
|---|---|---|
| `ADMIN_SESSION_KEY` | api | segredo (32+ caracteres) que assina o cookie da sessão. Sem ele, o painel fica desligado em produção. Gere com `openssl rand -base64 48`. |
| `ADMIN_EMAILS` | api | e-mails separados por vírgula garantidos como administradores a cada partida (bootstrap). |
| `ADMIN_HOST` | api | host que responde só o painel (padrão `admin.meuanuncio.io`). |
| `ADMIN_DEV_LOGIN` | api | `true` = entrar só com o e-mail, sem código. **Só vale sem `SUPABASE_URL`** (desenvolvimento local); com Supabase configurado é ignorado. Usa cookie sem `Secure`. |
| `USD_BRL` | api | cotação para converter o custo de IA no faturamento (padrão `5.5`). |
| `OPENROUTER_API_KEY`, `ATTENDANT_MODEL` | api (opcional) | liga o "Testar a Mia" no painel (mesma IA do worker). |
| `TTS_MODEL`, `TTS_VOICE` | api (opcional) | liga o "Ouvir amostra" da voz no painel. |

Também usa `SUPABASE_URL`, `SUPABASE_PUBLISHABLE_KEY`, `PUBLIC_BASE_URL` (reenviar link de cadastro) e
`BOT_WHATSAPP_NUMBER` (número exibido). No Easypanel, aponte o domínio `admin.meuanuncio.io` para o mesmo serviço da api.

### Aba Mia (só administradores)

Textos fixos (primeira mensagem, link de cadastro, lembretes, correção do formulário, boas-vindas), campos que a
Mia coleta (ativo, obrigatório, ordem, rótulo, como perguntar), persona por blocos, módulos por segmento, funil por
estado da conversa e voz. Fica em `mia_settings` (uma versão por gravação, com autor e nota; restaurar e desfazer
gravam versão nova). Worker e api leem com cache de 30 s; sem configuração vale o padrão do código
(`internal/mia/defaults.go`, igual ao comportamento anterior), semeado como versão 1 na partida da api.
Formato JSON, intents, regras de aprovação e de orçamento e a regra de segurança continuam fixos no código.
Os campos `offer`, `audience`, `location` e `daily_budget_brl` são exigidos pela proposta e não podem ser desligados.

### Desenvolvimento local

```bash
docker exec gti-pg psql -U postgres -c "CREATE DATABASE gestor_admin_dev"
DATABASE_URL='postgres://postgres:pg@localhost:55432/gestor_admin_dev?sslmode=disable' go run ./scripts/seedadmin
DATABASE_URL='postgres://postgres:pg@localhost:55432/gestor_admin_dev?sslmode=disable' WEBHOOK_SECRET=dev   WAHA_URL=http://127.0.0.1:1 WAHA_API_KEY=x ADMIN_DEV_LOGIN=true ADMIN_EMAILS=dev@meuanuncio.io go run ./cmd/api
# abra http://localhost:8080/admin/ e entre com dev@meuanuncio.io (admin) ou marina@meuanuncio.io (staff)
```

`scripts/seedadmin` recusa URL do Supabase e banco que já tenha clientes (use `-force` para somar).

## Testes

```bash
docker run -d --name gti-pg -e POSTGRES_PASSWORD=pg -p 55432:5432 postgres:17
export TEST_DATABASE_URL='postgres://postgres:pg@localhost:55432/postgres?sslmode=disable'
go test ./...
```

Sem `TEST_DATABASE_URL` os testes de banco são pulados; trate `SKIP` como falha.

## Documentos

- Spec: `docs/superpowers/specs/2026-09-22-duas-ias-whatsapp-easypanel-design.md`
- Plano: `docs/superpowers/plans/2026-09-22-duas-ias-whatsapp-easypanel.md`
