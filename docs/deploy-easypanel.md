# Deploy no Easypanel

Projeto `gestor-trafego-ia` com quatro serviços. Hostnames internos seguem o padrão `<projeto>_<serviço>`.

| Serviço | Tipo | Origem | Domínio | Observação |
|---|---|---|---|---|
| `postgres` | Postgres | serviço do Easypanel | não | backup diário para fora da VPS |
| `api` | App | GitHub `n4grow-ops/gestor-trafego-ia`, branch `main`, Dockerfile | sim (HTTPS), porta 8080 | health `/healthz` |
| `worker` | App | mesmo repositório e Dockerfile | não | comando `/app/worker` |
| `openwa` | App | imagem `openwa/wa-automate` (4.x estável, nunca a v5 alpha) | não | volume de sessão |

## Variáveis

`api`: `DATABASE_URL`, `WEBHOOK_SECRET`.

`worker`: `DATABASE_URL`, `OPENROUTER_API_KEY`, `ATTENDANT_MODEL`, `MANAGER_MODEL`, `TRANSCRIBE_MODEL`, `OPENWA_URL`, `OPENWA_API_KEY`, `OPERATOR_PHONE`.

Valores de referência em `.env.example`. Segredos só no Easypanel, nunca no repositório.

## open-wa

- Porta interna 8002; api key igual a `OPENWA_API_KEY` do worker.
- Webhook: `http://gestor-trafego-ia_api:8080/webhooks/openwa?token=<WEBHOOK_SECRET>`.
- Sessão persistente em volume (para não ler o QR a cada deploy).
- Conferir no container: `--help` (nomes exatos das opções) e `/api-docs` (formato de `sendText` e `decryptMedia`).
- Número de teste descartável. O open-wa usa o WhatsApp Web e não é oficial: não usar o número central de produção.

## Primeiro acesso

1. Ler o QR code do open-wa com o número de teste.
2. No console do `worker`:
   ```
   /app/admin org-create --name "Minha Empresa" --niche "restaurante" --max-budget 100
   /app/admin member-add --org 1 --name "Seu Nome" --role owner --phone 55DDDNUMERO
   ```
3. Mandar mensagem ao número de teste pedindo uma campanha.

## Operação

- Atendimento humano: o operador recebe aviso no `OPERATOR_PHONE`. Ao terminar: `/app/admin resolve-human --phone 55DDDNUMERO`.
- Custo por agente: `SELECT agent, model, sum(cost_usd) FROM llm_usage GROUP BY 1, 2;`
- Auditoria: `SELECT created_at, actor, action, data FROM audit_events ORDER BY id DESC LIMIT 50;`
