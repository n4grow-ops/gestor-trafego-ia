# Gestor de Tráfego IA

SaaS que transforma conversas no WhatsApp em campanhas de Meta Ads, com dois agentes de IA:

- **Atendente (IA 1)**: única voz com o cliente; coleta oferta, público, local e orçamento.
- **Gestor (IA 2)**: especialista em tráfego nos bastidores; recebe só o briefing estruturado e devolve a proposta.

Nenhuma IA executa ação. O código Go valida limites, gera o rascunho e só aceita aprovação por `SIM <código>`
(6 dígitos, 30 minutos, uso único, 5 tentativas) de membro `owner` ou `approver`.

## Binários

| Comando | Papel |
|---|---|
| `cmd/api` | webhook do WAHA (`POST /webhooks/whatsapp?token=...`) e `GET /healthz`; roda migrations |
| `cmd/worker` | processa inbox e outbox; chama Atendente, Gestor e transcrição |
| `cmd/admin` | `org-create`, `member-add`, `resolve-human` |

Configuração: ver `.env.example`. Deploy: `docs/deploy-easypanel.md`.

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
