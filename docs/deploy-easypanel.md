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

- Atendimento humano: o operador recebe aviso no `OPERATOR_PHONE` (número exatamente como registrado no WhatsApp; se o aviso não chegar, teste com ou sem o nono dígito). Ao terminar: `/app/admin resolve-human --phone 55DDDNUMERO`.
- Custo por agente: `SELECT agent, model, sum(cost_usd) FROM llm_usage GROUP BY 1, 2;`
- Auditoria: `SELECT created_at, actor, action, data FROM audit_events ORDER BY id DESC LIMIT 50;`
