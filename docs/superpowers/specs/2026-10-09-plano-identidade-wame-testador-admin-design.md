# Plano: identidade nova, Wame, testador com retomada automática e admin com faturamento

Data: 2026-10-09 · Status: aguardando aprovação · Branch base: `feat/cadastro` (89c4763)

## Objetivo

Deixar o piloto do meuanuncio.io operável sem intervenção técnica:

- o bot no WhatsApp para de cair;
- a marca nova aparece em todas as telas;
- o cliente adicionado como testador volta sozinho ao fluxo;
- a equipe acompanha clientes, robô, WhatsApp e dinheiro num painel só.

## Estado atual (verificado em 2026-10-09)

- **Meta:**
  - o app `meuanuncio.ia` (1057529180620555) responde de novo; ficou com "API access blocked" por algumas horas em 2026-10-09 e voltou;
  - token do usuário do sistema válido, com as 6 permissões;
  - `GET /{app-id}/roles` funciona com o token do app, então dá para detectar o aceite de testador.
- **WhatsApp:**
  - WAHA próprio no Easypanel, que caiu 3 vezes em 10 dias;
  - o código já tem a interface `whatsapp.Provider` (`internal/whatsapp/whatsapp.go:41`), então trocar de provedor não mexe no domínio.
- **Cobrança:** as tabelas `plans`, `subscriptions` e `payments` existem (migration 0006), mas ainda não há quem cobre.
- **Admin:** existe só o protótipo visual (`docs/prototipos/admin/`).

## Ordem de entrega

| # | Bloco | Depende de | Pode andar junto com |
|---|---|---|---|
| 1 | WhatsApp pela Wame | conta Wame e key da instância | 2 |
| 2 | Identidade nova e landing | arquivos do logo | 1 |
| 3 | Admin (base + fila de testadores) | DNS `admin.meuanuncio.io` | — |
| 4 | Testador manual com retomada automática | Bloco 3 | — |
| 5 | Faturamento no admin | escolha do sistema de cobrança | — |

Cada bloco sai numa branch própria a partir da anterior, com testes, verificação no navegador quando tiver tela, e publicação com volta rápida (rollback).

---

## Bloco 1 — WhatsApp pela Wame

### Decisão
- Começar no **modo não oficial (QR ou código de pareamento)**, plano Pro, R$ 44/mês. Mantém o comportamento atual (voz, imagem, digitando, visto).
- O modo oficial (Cloud API pela Wame, que é parceira da Meta) fica para uma fase seguinte, junto com os modelos de mensagem aprovados.

### Peças
- **`internal/whatsapp/wame`** implementa `Provider` em cima de `https://server.wame.api.br/{key}`:

  | Método | Rota |
  |---|---|
  | `SendText` | `POST /message/text` |
  | `SendImage` | `POST /message/base64/image` (com legenda) |
  | `SendVoice` | `POST /message/base64/audio` (vira PTT ogg/opus) |
  | `SetPresence` | `POST /message/presence` (`composing`, `recording`, `paused`) |
  | `MarkSeen` | `POST /message/read` |
  | `DownloadMedia` | `GET /message/{id}/media?format=binary` |
  | `ParseWebhook` | envelope `webhookFormat=meta`; ignora grupos, `fromMe` e status de entrega |

- **Webhook:** `POST /webhooks/wame/{segredo}`.
  - Segredo novo em `WAME_WEBHOOK_SECRET`, comparado em tempo constante, porque a Wame não assina as chamadas.
  - Responde 200 rápido e processa pela fila que já existe.
- **Idempotência:** cada mensagem é processada uma vez só, pelo `id` da mensagem (a Wame reenvia e não garante ordem). Se o sistema ainda não tiver isso, entra uma restrição única no banco.
- **Conexão:**
  - Webhook `connection` com `close` grava o estado e dispara alerta para o admin e o `OPERATOR_PHONE`.
  - `open` limpa o alerta.
  - O QR (`POST /{key}/instance`) e o código de pareamento ficam disponíveis para o admin.
- **Seleção do provedor:**
  - `WHATSAPP_PROVIDER=waha|wame` na api e no worker; o padrão continua `waha` até o corte.
  - Volta rápida: trocar a variável e reiniciar.
  - O WAHA só é desligado depois de 7 dias estáveis na Wame.
- **Config:** `WAME_BASE_URL`, `WAME_KEY`, `WAME_TOKEN` (opcional, `Authorization: Bearer`) e `WAME_WEBHOOK_SECRET`.

### Testes
- Testes de unidade com os payloads de exemplo da Wame: texto, áudio, imagem, citação, grupo, `fromMe` e status.
- Cliente HTTP falso para os envios.
- Teste de idempotência: o mesmo id chegando duas vezes.
- Teste do segredo errado: responde 404.
- Teste ao vivo com o número de teste antes do corte. Checklist:
  - texto;
  - áudio recebido e transcrito;
  - voz enviada;
  - prévia em imagem;
  - digitando;
  - visto azul.

### Riscos
- **Ban no modo não oficial.** Mitigação:
  - ritmo humano, que já existe (`humanize`);
  - sem disparo em massa;
  - número aquecido.
- **Formato exato das respostas da Wame não documentado.** Ler o corpo de forma tolerante e registrar o que vier no primeiro teste ao vivo.

---

## Bloco 2 — Identidade nova e landing

### Marca
- **Logo:** "M" azul com seta (símbolo) + "meu anúncio.io".
- **Paleta (OKLCH):**

  | Papel | Cor |
  |---|---|
  | Fundo | azul-marinho profundo (~#06122e) |
  | Superfícies | marinho mais claro |
  | Cor principal | azul elétrico (~#1f6bff) |
  | Destaque e ".io" | ciano (~#2fb8ff) |
  | Texto | quase branco levemente azulado |

  Estados (erro, aviso, sucesso) em tons próprios. O verde atual sai de todo o sistema.
- **Tipografia:**
  - títulos numa sans geométrica arredondada que conversa com o logo (escolha final no protótipo, servida pelo próprio site);
  - texto corrido em Figtree, que é mantida.
- **Tokens únicos** num arquivo CSS compartilhado, usados por landing, cadastro, páginas da Meta, termos, privacidade e admin.

### Landing (`/`)
- **Topo:** "Mais que anúncios. É movimento." Ao lado, a conversa de WhatsApp acontecendo e o anúncio "nascendo" ao lado dela.
- **Como funciona:** 3 passos reais (conversa, aprova, publica). A seta do logo conduz a rolagem.
- **Pilares:** Tráfego, WhatsApp, Controle e Resultados. Cada um com uma demonstração própria, não com cartões iguais.
- **Demais seções:** planos (Free, Starter, Pro), perguntas frequentes e chamada final para o WhatsApp.
- **Movimento:**
  - o símbolo é desenhado na entrada;
  - revelações ligadas à rolagem;
  - contadores animados;
  - transições suaves entre seções;
  - tudo com versão sem animação para quem desliga movimento no aparelho;
  - nenhuma seção depende da animação para aparecer.
- **Qualidade:**
  - contraste AA;
  - Lighthouse ≥ 90 no celular;
  - sem rolagem lateral;
  - testado em 390, 768, 1280 e 1440 px, com print de cada um.
- **Restrição:** a CSP atual da página proíbe `style=""` no HTML; animações só por classe e CSS.

### Depende de
Arquivos do logo (SVG, ou PNG transparente):
- símbolo;
- horizontal positiva;
- horizontal negativa;
- ícone.

---

## Bloco 3 — Admin (base)

- **Endereço:** `admin.meuanuncio.io`, servido pela api (DNS A → 2.25.215.158; Let's Encrypt pelo Easypanel).
- **Acesso:**
  - login com Supabase (código por e-mail ou Google);
  - tabela `team_members` com papel `admin` ou `staff`, que é a lista de quem entra;
  - sessão de 7 dias;
  - remover alguém corta o acesso na hora.
- **Telas da primeira entrega:** Visão geral, Clientes (lista e ficha), Conversas (assumir e devolver à IA), WhatsApp (estado, QR e pareamento), Meta, Configurações e Registro de ações.
- **Permissões checadas no servidor**, não só escondidas na tela. Funcionário não:
  - zera contato;
  - desconecta Meta;
  - vê tokens;
  - mexe em configurações ou na equipe.
- **Registro de ações:** tabela `admin_audit_log` com quem fez, o quê, sobre quem e quando.
- **Segurança:**
  - CSRF nas ações;
  - limite de tentativas;
  - nenhum segredo aparece na tela;
  - RLS mantida no schema `app`.

---

## Bloco 4 — Testador manual com retomada automática

### Fluxo
1. Depois do cadastro e do plano, o bot pede o **link do perfil do Facebook** e explica como copiar.
2. O sistema valida o formato (`facebook.com/...` ou `profile.php?id=`) e grava o pedido como `requested` na nova tabela `tester_invites`, com:
   - `organization_id`;
   - perfil informado;
   - estado e datas.
3. O admin mostra a fila **"Adicionar como testador"**. Em cada item:
   - copiar perfil;
   - abrir a página de papéis do app;
   - **"Convidei"**.

   O `OPERATOR_PHONE` recebe um aviso.
4. **"Convidei"** muda o estado para `invited`, guarda quem convidou e o bot manda ao cliente como aceitar (texto editável nas Configurações).
5. **Detecção do aceite** (worker, a cada 2 minutos, enquanto houver `invited`):
   - lê `GET /{app-id}/roles` e compara com a última leitura gravada;
   - um testador novo e **um único** convite `invited` → liga os dois sozinho e passa para `accepted`;
   - mais de um pendente → o admin pede para a equipe escolher com um clique a quem pertence o aceite novo;
   - **reforço pelo cliente:** se ele responde "aceitei", o sistema força uma leitura na hora.
6. **Retomada:**
   - com `accepted`, o bot manda o link `/m/{token}` (fluxo atual);
   - quando a conta conecta (`meta_connections`), o motor volta ao ponto em que o projeto parou (ficha, proposta ou anúncios) e manda a próxima pergunta;
   - o atalho para quem já conectou continua valendo.
7. **Lembretes e travas:**
   - 24 h em `invited` sem aceite → um lembrete ao cliente;
   - 48 h → aparece em "Travados" no admin;
   - perfil inválido 2 vezes → passa para a equipe.

### Por que assim
- A Meta não deixa adicionar testador pela API (`POST /{app}/roles` não existe mais) e automatizar o painel arrisca a BM verificada.
- Ler os papéis é permitido.
- A conexão já gravada continua sendo a prova final.

### Testes
- Engine:
  - pede o perfil;
  - valida o formato;
  - avança no "aceitei";
  - retoma de cada etapa.
- Worker:
  - diff dos papéis com um, nenhum e vários pendentes;
  - lembrete de 24 h fora do horário proibido (21h–8h BRT).
- Ao vivo: um testador real até a conta conectada.

### Fim do bloco
Quando a Meta aprovar o app (Live + acesso avançado), a etapa de testador é desligada por configuração (`TESTER_FLOW=false`). O bot vai direto ao link de conexão.

---

## Bloco 5 — Faturamento no admin (só administradores)

- **Receita:**
  - receita recorrente do mês (MRR);
  - assinaturas ativas por plano;
  - novos e cancelados;
  - inadimplentes;
  - pagamentos recebidos e pendentes;
  - ticket médio;
  - previsão do mês.

  Gráfico de 6 meses.
- **Custos:**
  - Wame (fixo por instância);
  - IA, com o custo de cada chamada passando a ser gravado (tabela `ai_usage`: modelo, tokens, custo, organização);
  - infraestrutura (valor fixo nas Configurações).

  Mostra a **margem**.
- **Investimento dos clientes:** gasto em anúncios nas contas conectadas, lido da Meta (`insights`) uma vez por dia e guardado.
- **Cobrança (decisão pendente):**
  - **Asaas** (recomendado): Pix, boleto e cartão com recorrência e webhook de pagamento que alimenta `payments`;
  - alternativas: Mercado Pago ou Stripe;
  - ou registro manual no admin até escolher.
- **Testes:**
  - cálculos (MRR, inadimplência, margem) com dados fixos;
  - webhook do sistema de cobrança idempotente.

---

## Fora deste plano

- Robô automático de testadores (n8n ou Playwright): descartado, porque arrisca a BM.
- Modo oficial do WhatsApp.
- Pedido de análise da Meta.

Os dois últimos ficam como próximos passos.

## Decisões pendentes do usuário

1. **Wame:** modo não oficial agora (recomendado)?
2. **Cobrança:** Asaas, Mercado Pago, Stripe ou manual?
3. **Arquivos do logo.**
4. **Acessos:** conta Wame com instância criada (key) e, depois, o DNS do `admin`.
