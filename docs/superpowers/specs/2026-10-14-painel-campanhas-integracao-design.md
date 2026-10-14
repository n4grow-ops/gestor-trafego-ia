# Painel de campanhas e integração Meta: Prioridades 1 e 2

Data: 2026-10-14. Repo: gestor-trafego-ia, branch feat/wame.

## Objetivo

A equipe do meuanuncio.io precisa enxergar e destravar sozinha o que hoje só se resolve pelo banco: campanhas na Meta, tarefas que falharam, pagamento da conta, anúncios guardados e o raciocínio da Mia. A integração precisa avisar antes que a campanha pare ou falhe em silêncio: conexão vencendo, anúncio reprovado, resultado do dia, pagamento confirmado.

Sucesso: nenhum caso desta rodada (prévia travada, saldo, foto torta, ajuste ignorado) exige SQL na mão ou leitura do banco para ser entendido e resolvido.

## Decisões do usuário (2026-10-14)

- Fora do escopo: a Prioridade 3 (alertas na tela inicial, custo de IA por cliente, modelo de mensagem pelo painel).
- Mídia permanente: o worker guarda cópia no Storage e grava um link temporário renovado. A api não recebe a chave de serviço do Supabase.
- Resultados: diários, às 8h de Brasília, para quem tem campanha ativa.
- Permissões no painel: a equipe pausa campanha, edita dados do projeto e pede proposta nova. ATIVAR continua só com o cliente, pelo WhatsApp.
- Anúncio reprovado: a Mia avisa o cliente com o motivo e oferece ajustar, e a equipe é avisada.

## Restrições que valem para tudo

- A api não ganha chave nova. Tudo que precisa de token da Meta ou da chave do Storage roda no worker; o painel enfileira um job e lê o resultado no banco.
- Toda migração é idempotente (`IF NOT EXISTS`). A api roda local contra o banco de produção antes de cada deploy.
- Mensagem que o sistema inicia fora da janela de 24h usa modelo aprovado (`templates.go`). Os modelos novos precisam de aprovação da Meta, um passo externo que fica registrado como dependência.
- Todo caso real vira teste automático antes da correção (TDD). A suíte inteira passa antes de cada commit.
- Ações da equipe vão para o `admin_audit_log`. As do sistema vão para o `audit_events`.

## Base comum: link temporário renovado (B6, usado também pelo A4)

O worker já tem `storage.TempLinks`. Fica uma regra só, para mídia da conversa, anúncios e prévias:

- Tabela `media_links` (migração 0033): `id`, `kind` (`message` | `creative` | `preview`), `ref_id`, `bucket`, `path`, `mime`, `url`, `url_until`, `created_at`.
- O worker sobe o arquivo e grava o link assinado de 30 dias. O job diário `maintenance.cleanup` renova os links que vencem em menos de 7 dias.
- A api serve `GET /admin/api/media/{id}`: busca a `url` do lado do servidor, sem chave, e repassa com nosniff, sandbox e Range. O CSP do painel continua bloqueando domínio externo, por isso a api faz o meio de campo.
- Mensagem recebida (B6): depois de gravar a mensagem, o worker enfileira `media.archive` (baixa da Wame, sobe no bucket `chat-media` e cria o link). O painel usa o link quando existe e, enquanto não existe, continua buscando na Wame.
- Anúncio (A4): o `keepCreative` cria o link depois do upload. A prévia passa a ser guardada em `previews` com o link, ligada ao rascunho.

## Prioridade 1: painel admin

### A1. Campanhas

- `GET /admin/api/campaigns?status=&org=`: publicações com cliente, projeto, status (publicando, pausada, ativa, com erro, reprovada), valor por dia, criada em, ativada em, `campaign_id`, último erro, último resultado (B3), pagamento (A3) e link para o Gerenciador de Anúncios (`https://adsmanager.facebook.com/adsmanager/manage/campaigns?act=<conta>&selected_campaign_ids=<campanha>`).
- `POST /admin/api/campaigns/{id}/pause` (toda a equipe): enfileira `campaign.pause`. O worker pausa na Meta, grava o status e avisa o cliente: "A equipe pausou a campanha X". ATIVAR não aparece no painel.
- Tela nova "Campanhas" no menu, com filtro por status e chips de pagamento e reprovação.

### A2. Fila de tarefas

- `GET /admin/api/jobs?status=failed&days=7`: tipo, cliente, tentativas, erro sem segredos (`RedactSecrets`), criado e atualizado em. O payload aparece resumido, sem texto de mensagem.
- `POST /admin/api/jobs/{id}/retry` (só admin): job `failed` volta para `pending` com 0 tentativas.
- `POST /admin/api/jobs/{id}/discard` (só admin): o job vai para o status novo `discarded`. A migração estende o CHECK da tabela, se houver um.
- Tela "Fila" com as falhas dos últimos 7 dias e os dois botões. As duas ações entram no log da equipe.

### A3. Pagamento da conta de anúncios

- `meta_connections` ganha `funding jsonb` e `funding_checked_at`, com o resultado de `meta.Funding`.
- O worker atualiza esses campos ao publicar, no ATIVAR, no "paguei" (B4) e no job diário de resultados (B3).
- `POST /admin/api/clients/{id}/funding/refresh` enfileira `meta.funding`.
- A aba "Meta" da ficha mostra a forma de pagamento, o saldo, o limite e o status, com chips "sem saldo", "pagamento pendente" e "saldo baixo" (menos que um dia do orçamento ativo).

### A4. Anúncios do projeto

- A aba "Projetos" da ficha mostra, por rascunho aprovado, os anúncios em miniatura (imagem ou player), com formato, lugar e origem, e a prévia gerada. Tudo é servido por `/admin/api/media/{id}`.

### A5. Por que a Mia respondeu assim

- Tabela `mia_turns` (migração 0034): `conversation_id`, `message_id` (a mensagem do cliente que a IA respondeu), `intent`, `project`, `fields jsonb`, `profile jsonb`, `reply`, `action`, `created_at`.
- `action` é o que o sistema fez: "pediu proposta", "ajuste: proposta nova", "link Meta", "link pagamento", "arquivamento", "fala da IA" e assim por diante.
- Os atalhos sem IA (comandos, lembrete do SIM) gravam linha com intent `sistema`.
- O `Thread` devolve os turnos por `message_id`. Clicar no balão do cliente mostra o que a IA entendeu e o que o sistema fez.
- A limpeza diária apaga turnos com mais de 30 dias.

### A6. Ações no projeto

- `POST /admin/api/projects/{id}/archive`: usa `ArchiveProject`.
- `POST /admin/api/projects/{id}/rename {name}`: nome único na organização.
- `POST /admin/api/projects/{id}/fields`: oferta, público, local, orçamento por dia (até o limite da conta) e lugares (`NormalizePlacements`).
- `POST /admin/api/projects/{id}/propose`: enfileira `project.propose`. O worker põe a conversa nesse projeto, cancela a proposta aberta e pede proposta nova ao especialista. O cliente recebe o resumo normal com o código.
  - Fora da janela de 24h a api recusa com o erro `janela_fechada`.
- Todas as ações (equipe) entram no log.

## Prioridade 2: integração

### B1. Conexão Meta que vence

- No `CompleteMeta`, a api chama `debug_token` com o token do app e grava `meta_connections.token_expires_at` (nulo quando não vence).
- Job diário `meta.token_check` às 9h:
  - conexão ativa que vence em até 7 dias e ainda não foi avisada: manda ao cliente o link de reconexão (`msgMetaRelink`) e avisa a equipe; o aviso fica em `token_warned_at`;
  - conexão que já venceu: avisa de novo, uma vez.
  - Fora da janela de 24h vai o modelo novo `conexao_meta_vencendo`.

### B2. Anúncio reprovado

- Job `ads.review`:
  - agendado 30 min depois de publicar e depois do ATIVAR;
  - repete a cada 6h, por até 3 dias, enquanto houver anúncio em revisão;
  - lê `effective_status` e `ad_review_feedback` de cada anúncio.
- Se algum anúncio estiver `DISAPPROVED`:
  - a publicação vai para o status `rejected` (CHECK estendido) e o motivo é gravado;
  - a campanha é pausada na Meta;
  - a equipe é avisada;
  - o cliente recebe o motivo da Meta e o convite: "Me manda outra imagem ou me diz o que mudar no texto que eu refaço o anúncio".
- A conversa volta para `awaiting_creatives` com o mesmo rascunho aprovado e os anúncios atuais são apagados. No PRONTO, a publicação cria campanha nova e a antiga fica pausada.
  - O `StartPublication` passa a aceitar rascunho cuja última publicação foi `rejected`.

### B3. Resultados diários

- Job `results.daily` às 8h, para cada publicação `active`: insights de ontem (`spend`, `impressions`, `reach` e conversas iniciadas pela ação `onsite_conversion.messaging_conversation_started_7d`) gravados em `campaign_results` (`publication_id`, `day`, `spend_cents`, `impressions`, `reach`, `conversations`).
- Mensagem ao cliente: "Ontem: R$ X gastos, N conversas, R$ Y por conversa", mais o saldo (A3) quando é pré-pago.
  - Fora da janela vai o modelo novo `resultado_diario`.
  - Dia sem gasto não gera mensagem.
- O painel (A1) mostra os últimos 7 dias.

### B4. "Paguei"

- Comando sem IA (`paguei`, `já paguei`, `coloquei saldo`, `adicionei o cartão` e variações) com conta conectada:
  - lê o pagamento da conta (A3) e responde "Confirmado" ou "Ainda aparece pendente na Meta", com o link de pagamento;
  - se estiver tudo certo e houver campanha pausada pronta, lembra o ATIVAR.

### B5. Prévia com o texto que vai ao ar

- `campaign_drafts.final_copies jsonb` (migração).
- A reescrita dos textos com as referências do cliente (`publishCopies`) passa para o fim da etapa dos anúncios, antes da prévia. O resultado fica gravado no rascunho.
- A prévia mostra `final_copies[0]`, a publicação usa `final_copies` e a mensagem "Os textos que vão no ar" é a mesma lista. Se a reescrita falhar, valem os textos da proposta nos três lugares.

### B6. Mídia guardada para sempre

Descrita na base comum.

## Ordem de entrega (cada bloco: commit, api local, deploy)

1. **Base:** links renovados (B6), fila de tarefas (A2) e mídia do projeto (A4).
2. **Avisos:** conexão vencendo (B1), anúncio reprovado (B2), "paguei" (B4) e prévia com o texto final (B5).
3. **Números:** resultados diários (B3), pagamento no painel (A3) e campanhas (A1).
4. **Mia e projetos:** turnos da Mia (A5) e ações no projeto (A6).

## Testes

- Cada item tem teste de motor ou de store com o caso real.
  - B2: anúncio reprovado volta para os anúncios e cria campanha nova no PRONTO.
  - B5: a prévia e a publicação usam o mesmo texto.
  - A2: o retry só funciona com admin.
- O painel tem teste HTTP de cada rota nova, com permissões: equipe pausa e edita, só admin reprocessa e descarta.
- Lado Meta: fake Graph para `debug_token`, `ad_review_feedback` e `insights`.

## Riscos e dependências

- **Modelos novos:** `conexao_meta_vencendo` e `resultado_diario` precisam de aprovação da Meta. Até lá, os avisos saem só dentro da janela de 24h; fora dela vão para a equipe.
- **Campo de conversas:** o nome da ação de conversas nos insights pode variar por objetivo. Se não vier, a mensagem mostra só o gasto e o alcance.
- **Token sem validade:** o `debug_token` pode não trazer validade para o token do Login for Business. Nesse caso B1 só age quando a Meta recusar o token (erro 190), avisando na hora.
- **Volume de jobs:** as checagens de revisão e de resultados são poucas por cliente (no máximo 12 de revisão por publicação e 1 de resultado por dia).
