# Ciclo fechado CTWA: do anúncio à venda, de volta para a Meta

Data: 2026-10-14 · Branch: `feat/wame` · Status: proposta para decisão (nenhum código alterado).

Objetivo: capturar o `ctwa_clid` do clique em anúncio Click-to-WhatsApp (CTWA) e devolver à Meta, pela Conversions API for Business Messaging (CAPI), os eventos "lead qualificado" e "compra", para o algoritmo otimizar por venda real e não só por conversa iniciada.

Legenda de confiança usada no texto:

- **[Meta]** confirmado em página oficial da Meta lida em 2026-10-14.
- **[Wame]** confirmado em página ou referência oficial da Wame (`llms-full.txt`, `/docs/webhooks`, blog oficial).
- **[AWS]** confirmado na documentação da AWS End User Messaging Social, que repassa o formato da Meta (fonte secundária, porém técnica).
- **[Terceiro]** blog de fornecedor ou agência; indício, não prova.
- **[NÃO CONFIRMADO]** não achei fonte; precisa de teste ao vivo (seção 9).

---

## 1. Resumo executivo

1. O `ctwa_clid` só existe no webhook do número que **recebe** a mensagem do lead, dentro de `messages[].referral` **[Meta]**. Hoje o anúncio aponta para o WhatsApp do cliente (`promoted_object.whatsapp_phone_number`, `internal/meta/publish.go:240`), e a Mia só vê a conversa dono↔Mia. Logo, hoje não há como ler o clid. Isso não se resolve com código nosso: exige mudar por onde o lead entra.
2. A CAPI de mensagens exige `ctwa_clid` e `whatsapp_business_account_id` em cada evento de WhatsApp **[Meta, AWS]**. Sem clid, não há evento de mensagens; só sobra a alternativa de evento padrão com telefone hasheado (arquitetura D), que não está confirmada.
3. O que já temos de graça: o webhook atual grava o corpo bruto em `inbox_events.payload` (`internal/httpapi/httpapi.go:73-85`). Se o `referral` chegar pelo número da Mia, ele já está salvo; só falta ler.
4. Recomendação: construir primeiro a **camada de captura de venda** (comando do dono + pergunta diária), que gera ROI real por campanha mesmo sem a Meta, e pilotar a **coexistência no número do cliente (A)** com um cliente. Manter **B (leads para a Mia)** como plano alternativo para clientes sem WhatsApp Business app. Detalhes na seção 7.
5. Dois bloqueios externos decidem tudo e dependem de resposta de terceiros: (i) quem tem o token com `whatsapp_business_manage_events` na WABA do cliente (Wame ou nós); (ii) se o `referral` chega no webhook quando o número está em coexistência.

---

## 2. Estado atual do código (o que existe e o que muda)

| Peça | Arquivo | Fato relevante |
|---|---|---|
| Destino do anúncio | `internal/meta/publish.go:240-247` | `promoted_object = {page_id, whatsapp_phone_number}`, `destination_type=WHATSAPP`, `optimization_goal=CONVERSATIONS` |
| Número do cliente | `internal/meta/meta.go:155`, coluna `meta_connections.page_whatsapp_number` (0025) | número vinculado à Página, lido do Graph |
| Webhook | `internal/httpapi/httpapi.go` | `POST /webhooks/whatsapp?token=`; separa mensagens (`Splitter`), grava `inbox_events(provider, external_id, payload)` com `UNIQUE(provider, external_id)` |
| Parser Wame | `internal/whatsapp/wame/wame.go` | struct `message` **não tem** `referral`; `toInbound` **descarta** `from_me` e grupo |
| Contrato do canal | `internal/whatsapp/whatsapp.go` | `Provider` + interfaces opcionais (`ConnectionParser`, `Splitter`, `TemplateSender`): padrão a seguir |
| Resultado diário | `internal/engine/results.go`, `internal/meta/insights.go` | lê `messaging_conversation_started_7d`; grava `campaign_results(conversations)` |
| Jobs | `internal/engine/jobs.go`, outbox (`outbox_jobs`) | padrão `enqueueDaily` + `HasPendingKind` |
| Mapa anúncio→campanha | `meta_publications.ad_ids text[]`, `campaign_id`, `adset_id` (0023) | permite casar `referral.source_id` (id do anúncio) com a publicação e a organização |
| Consentimento | `consents(member_id, kind CHECK IN ('terms','privacy'))` (0006) | precisa de novo `kind` |
| Escopos Meta | `docs/.../2026-09-25-conexao-meta-design.md` | `ads_management, ads_read, business_management, pages_show_list, pages_read_engagement, pages_manage_ads`; app ainda sem App Review |
| Migrations | `internal/db/migrations` | última existente: `0038_turnos_da_mia.sql`. A faixa 0039-0059 não está nesta branch; as propostas abaixo usam 0060+ como pedido (conferir colisão com outras branches antes de criar) |

Risco de projeto já visível: o webhook atual trata **toda** mensagem que chega como conversa de um membro (dono) com a Mia. Se um número de cliente for apontado para esse endpoint, a Mia responderia aos leads do cliente. Os leads precisam de rota e handler **próprios** (seção 5.3).

---

## 3. Arquiteturas viáveis

### A. Coexistência no número do cliente (WABA do cliente, Cloud API + app no celular)

Como funciona: o cliente conecta o número pelo Embedded Signup da Wame em modo coexistência; o número continua no WhatsApp Business app e passa a falar também com a Cloud API **[Meta, Wame]**. O anúncio não muda. Nós recebemos o webhook de mensagens (com `referral` na primeira mensagem do clique) e os ecos do que o dono digita no celular.

Fatos confirmados:

- Requisitos: app WhatsApp Business versão 2.24.17 ou superior; webhooks `history`, `smb_app_state_sync`, `smb_message_echoes` **[Meta]**.
- Fluxo da Wame: "Conectar com a Meta", login seguro, o celular continua funcionando; sem criar app na Meta nem virar Tech Provider **[Wame]**.
- Limitações: vazão fixa de 20 mensagens por segundo; sem grupos, chamadas de voz/vídeo, ferramentas de negócio, listas de transmissão; companion apps são desvinculados no onboarding (a Meta diz que todos são suportados exceto WhatsApp para Windows e WearOS) **[Meta]**.
- Abrir o app ao menos a cada 14 dias para manter a API ativa **[Wame]** (fornecedores citam 13 dias **[Terceiro]**; a Meta não diz na página lida).
- Histórico de conversas e contatos só chegam se for chamado explicitamente (uma vez, em até 24 h do onboarding) **[Wame]**.
- Anúncios CTWA em número onboardado por coexistência são permitidos e os sinais de conversão são reportados pela CAPI **[Meta]**.
- A página de coexistência da Meta **não** diz que o `referral`/`ctwa_clid` é entregue nesse modo **[NÃO CONFIRMADO]**.
- Cobrança: a partir de outubro de 2026, mensagem de serviço enviada **pela API** na janela de 24 h passa a ser cobrada; digitada no app continua grátis **[Terceiro: blog da Wame]**. Como só receberíamos e não responderíamos pelo número do cliente, o custo da Meta tende a zero.

| Item | Avaliação |
|---|---|
| Prós | Não muda o produto nem o anúncio; o cliente segue atendendo no celular; temos o clid, o texto do lead e o eco do "fechei" |
| Contras | Cliente precisa ter WhatsApp Business app atualizado, aceitar o fluxo da Meta e abrir o app a cada 14 dias; WhatsApp Web/Desktop desvinculado; uma instância Wame por cliente; ingestão de **todas** as conversas do cliente (LGPD, seção 6) |
| Exige do cliente | Login Meta pelo Embedded Signup (conta Business, ser admin do número); app atualizado; aceitar consentimento nosso (seção 6) |
| Exige do app Meta / permissões | Para **ler** o webhook: nada além do que a Wame já tem (ela é a Tech Provider). Para **enviar** a CAPI: token com `whatsapp_business_management` e `whatsapp_business_manage_events` na WABA do cliente, com acesso avançado **[Meta]**. Quem detém esse token é a incógnita nº 1 (ver abaixo) |
| Custo | Instância Wame por cliente (planos não oficiais: R$ 28,99 a R$ 55,00/mês; preço da oficial não publicado **[Wame]**); Meta ~zero se não respondermos pela API; engenharia ~3 semanas (seção 7) |
| Risco | Alto-médio: dependência de (a) referral em coexistência, (b) acesso ao token de CAPI, (c) cliente desvinculando o número ou esquecendo o app |

Incógnita nº 1, caminhos para a CAPI nesta arquitetura:

1. A Wame expõe um proxy de CAPI. Busquei em `llms-full.txt`: **não há** endpoint de dataset ou conversão **[Wame]**. Perguntar ao suporte.
2. Nosso app Meta obtém acesso à WABA do cliente pelo Facebook Login for Business que já usamos (a configuração `META_LOGIN_CONFIG_ID` ganharia o ativo "contas do WhatsApp" e a permissão `whatsapp_business_manage_events`). Exige App Review com acesso avançado **[Meta: "apply for advanced access for whatsapp_business_manage_events"]**. Não confirmei se o Login for Business aceita esse ativo/permissão **[NÃO CONFIRMADO]**.
3. Virar Tech Provider próprio com Embedded Signup. Descartado: peso de aprovação alto, e a Wame existe para evitar isso.

### B. Leads para o número da Mia ("a Mia atende o lead")

Como funciona: o anúncio passa a apontar para o número da Mia (+55 49 9191-1891, WABA nossa, Cloud API via Wame). Todo referral cai no nosso webhook, na nossa WABA, com nosso dataset e nosso token. Casamos `referral.source_id` com `meta_publications.ad_ids` para saber de qual cliente é o lead. A Mia qualifica e repassa ao dono.

| Item | Avaliação |
|---|---|
| Prós | Controle total do clid, do dataset e do token (resolve a incógnita nº 1); qualificação real do lead pela Mia gera o `QualifiedLead`; sem depender do app do cliente |
| Contras | Muda o produto: o lead conversa com a marca da Mia, não com o cliente; o dono precisa receber o lead de volta (aviso com telefone, resumo e link `wa.me`); mensagens da Mia no número nosso passam a ter custo Meta a partir de outubro **[Terceiro]**; um número só para todos os clientes concentra risco de bloqueio e de qualidade (quality rating) |
| Exige do cliente | Aceitar o modelo; autorizar a Mia a falar com os leads dele; ficar atento ao aviso de handoff |
| Exige do app Meta / permissões | Nossa WABA: `whatsapp_business_manage_events` com acesso avançado; é a nossa conta, sem terceiros |
| Custo | Meta por conversa/mensagem de serviço da Mia (valores não confirmados); LLM por lead; engenharia ~3 semanas, com menos incógnitas externas |
| Risco | Alto de produto e de validade técnica: ver incógnitas 3 e 4 |

Incógnitas específicas de B, todas **[NÃO CONFIRMADO]** e eliminatórias:

- O guia de CTWA da Meta exige "uma Página do Facebook com número de WhatsApp vinculado" **[Meta]**. Não sei se o número da Mia pode ficar vinculado às Páginas de vários clientes, nem se o `promoted_object.whatsapp_phone_number` aceita um número que não pertence à Página.
- A CAPI usa o dataset da **nossa** WABA, mas o anúncio roda na conta de anúncios do **cliente**. Não sei se a atribuição e a otimização do anúncio enxergam um dataset de outra empresa. Em Messenger/Instagram a Meta fala em dataset "owned by the business, partners granted access" **[Meta]**; para WhatsApp diz que o cliente é dono do dataset **[Meta]**, o que sugere que o dataset da WABA do cliente é o que o anúncio do cliente enxerga.

Esse segundo ponto também pesa contra B: pelo texto da Meta, o dataset de WhatsApp pertence ao dono da WABA. Se o dono é a N4G, o anúncio do cliente pode não receber o sinal. Tratar B como hipótese a testar antes de qualquer construção.

### C. Captura assistida (comando do dono + referral quando existir)

Como funciona: não mexemos no caminho do lead. O dono avisa a Mia ("fechei venda de R$ 1.500 com a Maria") e/ou a Mia pergunta no fim do dia "dos X leads de hoje, quem fechou?". Cada venda é registrada e atribuída à campanha. Quando houver referral (A, B ou outro caminho), a venda é ligada a um clid e vira evento CAPI; quando não houver, vira só relatório interno.

| Item | Avaliação |
|---|---|
| Prós | Funciona hoje, sem depender de Meta nem Wame; entrega ROAS real por campanha no relatório diário; é a camada de "fechou" de que A e B precisam de qualquer jeito |
| Contras | Sozinha **não** devolve nada à Meta (sem clid não há evento de mensagens); depende da disciplina do dono; atribuição por campanha sem clid é estimada (ex.: por janela de tempo e por lead listado) |
| Exige do cliente | Responder à Mia (já faz parte do hábito dele) |
| Exige do app Meta / permissões | Nada novo |
| Custo | Engenharia ~1 semana; LLM mínimo (Jev classifica "isso é confirmação de venda?") |
| Risco | Baixo |

Variante C2 (hipótese): instância Wame **não oficial** (QR) no número do cliente só para ler referral e eco. Os exemplos da Wame mostram `message-referral` com `"official": false` em payload de WhatsApp **[Wame]**, e existe `POST /{key}/message/ad` com contexto de anúncio **[Wame]**, mas a página não explica o referral e não diz que o clid da via não oficial é aceito pela Meta. Além disso a CAPI exige `whatsapp_business_account_id`, e número não oficial pode não ter WABA **[NÃO CONFIRMADO]**. Risco de banimento do número do cliente por uso não oficial. Não recomendo sem teste e sem aceite explícito do cliente.

### D. Só sinais de lead sem `ctwa_clid`

Como funciona: sem clid, não existe evento de WhatsApp na CAPI de mensagens **[Meta: o clid é campo obrigatório]**. Resta o evento padrão da CAPI com telefone do comprador hasheado (SHA-256) para a Meta casar com usuários, ligado a pixel/dataset da conta de anúncios, e um adset com objetivo OUTCOME_SALES e `optimization_goal=OFFSITE_CONVERSIONS` (única meta de otimização por conversão listada para CTWA **[Meta]**).

| Item | Avaliação |
|---|---|
| Prós | Não depende de webhook do número do cliente |
| Contras | Taxa de casamento por telefone desconhecida; precisa do telefone do lead (o dono teria de informar, com custo de LGPD alto); `custom_event_type`, `pixel_id` e campos do `promoted_object` para esse caso não estão documentados na página que li **[NÃO CONFIRMADO]** |
| Exige | Pixel/dataset na conta de anúncios do cliente; novo tratamento do `promoted_object` |
| Custo / risco | Engenharia ~2 semanas; risco alto (técnica não confirmada e menos aderente ao contrato de "ciclo fechado CTWA") |

Uso recomendado de D: apenas como último recurso, e só depois de um teste curto de casamento.

### Comparativo

| Critério | A Coexistência | B Leads na Mia | C Captura assistida | D Sem clid |
|---|---|---|---|---|
| Devolve evento à Meta | sim (se referral e token ok) | sim (se dataset visível ao anúncio) | só com A/B | talvez |
| Muda a experiência do lead | não | sim, muito | não | não |
| Esforço de onboarding do cliente | médio | baixo | baixo | médio |
| Dependência externa crítica | Wame + Meta (token, referral) | Meta (vínculo de número, dataset) | nenhuma | Meta (casamento) |
| Valor entregue mesmo se a Meta falhar | sim (leads e vendas internas) | parcial | sim | pouco |
| Esforço de engenharia | 3 semanas | 3 semanas | 1 semana | 2 semanas |
| Risco geral | médio-alto | alto | baixo | alto |

---

## 4. Requisitos da API de eventos (CAPI for Business Messaging)

Tudo abaixo vem da página oficial de onboarding da Meta (`developers.facebook.com/docs/marketing-api/conversions-api/business-messaging`) e da página da AWS que repassa o formato, salvo onde marcado.

**Endpoint de eventos [Meta]:** `POST https://graph.facebook.com/{versão}/{DATASET_ID}/events` com token de acesso. A página exemplifica `v16.0`; usar a versão do Graph que `internal/meta` já adota e validar em teste **[NÃO CONFIRMADO qual versão atual]**.

**Dataset [Meta]:** `POST https://graph.facebook.com/{versão}/{WHATSAPP_BUSINESS_ACCOUNT_ID}/dataset` cria ou devolve o dataset da WABA; `GET` com o mesmo caminho lê. Uma WABA tem um dataset. Para WhatsApp, o cliente é dono do dataset e parceiros com permissão acessam. Não é o pixel do site.

**Permissões [Meta]:** `whatsapp_business_management` e `whatsapp_business_manage_events`; pedir acesso avançado a `whatsapp_business_manage_events` (quem já tem avançado em `whatsapp_business_messaging` tende a ser aprovado em seguida). Token do Embedded Signup, ou usuário do sistema / usuário com essas permissões. A Meta fala também de nível de acesso da Marketing API (1.500 chamadas bem-sucedidas em 15 dias; faixa em reforma, banner cita 500 em "Full Access").

**Corpo do evento [Meta, AWS]:**

```json
{
  "data": [{
    "event_name": "Purchase",
    "event_time": 1720000000,
    "action_source": "business_messaging",
    "messaging_channel": "whatsapp",
    "user_data": {
      "whatsapp_business_account_id": "<WABA_ID numérico>",
      "ctwa_clid": "<do referral, sem hash>"
    },
    "custom_data": { "currency": "BRL", "value": 1500.00 }
  }],
  "partner_agent": "<nome de parceiro atribuído à N4G>"
}
```

| Campo | Regra |
|---|---|
| `event_name` | Aceitos pela Meta: Purchase, LeadSubmitted, InitiateCheckout, AddToCart, ViewContent, OrderCreated, OrderShipped, OrderDelivered, OrderCanceled, OrderReturned, CartAbandoned, QualifiedLead, RatingProvided, ReviewProvided **[Meta]**. Nome é sensível a maiúsculas **[AWS]**. `Lead` puro é rejeitado por intermediários; usar `LeadSubmitted` **[Terceiro]** |
| `event_time` | segundos Unix; **dentro dos últimos 7 dias e não no futuro** **[AWS]**. A Meta não declara a janela na página lida; não sei se a contagem é a partir do clique ou do envio **[NÃO CONFIRMADO]** |
| `action_source` | `business_messaging` |
| `messaging_channel` | `whatsapp` |
| `user_data.ctwa_clid` | obrigatório, **sem hash**; a Meta valida e ele não pode ser forjado **[AWS]**; é "identificador pessoal, único por clique" **[Meta]** |
| `user_data.whatsapp_business_account_id` | obrigatório, ID numérico da WABA (não é Page ID nem phone_number_id) **[AWS, Terceiro]** |
| `custom_data.currency`, `.value` | obrigatórios para Purchase **[AWS]**; os exemplos da Meta só mostram esses dois campos |
| `partner_agent` | a Meta pede o nome de agente de parceiro atribuído; como obtê-lo para a N4G é **[NÃO CONFIRMADO]** |
| Lote | até 1.000 eventos por requisição **[AWS]** |
| Deduplicação | "A Meta não ajuda a deduplicar eventos de Business Messaging"; deduplicar antes de enviar **[Meta]**. Um fornecedor afirma que `event_id` é honrado **[Terceiro]**; a página da Meta não menciona `event_id` |
| Dados pessoais | Se incluir e-mail ou telefone em `user_data`, enviar SHA-256; a Meta rejeita PII sem hash **[AWS]**. **Decisão de projeto: não enviar PII**, só clid + WABA + valor |
| Verificação | O evento aparece no Gerenciador de Eventos do dataset; o cliente precisa saber abrir lá **[Meta]** |

**Otimização [Meta]:** a CAPI "dá acesso à otimização por compra para anúncios que clicam para Messenger e WhatsApp"; a página não cita otimização por lead. A WhatsApp Business descreve opção de otimizar por lead e por compra no Gerenciador de Anúncios, com 24% menos custo por lead e 10% menos custo por compra em seus testes (dado de marketing da própria Meta, tratar como indicativo) **[Terceiro/Meta marketing]**, com o fluxo: objetivo Vendas ou Engajamento em configuração manual para compra, objetivo Leads para lead, destino WhatsApp. **Não sei** quais campos do adset (`promoted_object.custom_event_type`, dataset/pixel) a Marketing API exige para ligar essa otimização **[NÃO CONFIRMADO]**; a tabela de metas válidas da Meta mostra `OUTCOME_LEADS` só com `CONVERSATIONS` e `OUTCOME_SALES` com `OFFSITE_CONVERSIONS` **[Meta]**. Consequência: enviar eventos não basta; trocar a otimização exige mudar `planFor` (objetivo e meta) e provavelmente exige volume mínimo de eventos (limite **[NÃO CONFIRMADO]**).

**Janela de 7 dias e vendas longas:** se a janela é de fato de 7 dias, venda fechada no dia 8 em diante não pode ser reportada. Em nichos de ciclo longo (imóveis, como os TOTNT; consultorias) isso corta boa parte do valor. Mitigação: enviar `QualifiedLead` cedo (dentro da janela) e `Purchase` só quando couber; mostrar no painel "vendas fora da janela" para o cliente entender.

---

## 5. Como detectar "fechou" com pouco esforço

### 5.1 Camadas, da mais certa para a mais barata

1. **Comando do dono para a Mia.** Nova intent `record_sale` no contrato do atendente (`internal/agent/contract/contract.go`, adição ao `Valid()`), com frases como "fechei venda de 1500 com a Maria", "vendi", "fechou o João por 2 mil". A Mia extrai valor e referência e, se ambíguo, lista os últimos leads (até 5, últimos 7 dias) para o dono responder com o número. Confirma de volta: "Registrei R$ 1.500 (lead de ontem, anúncio X). Certo?". Só após o "sim" cria o evento. Segue o padrão de gatilho por regex antes da IA já usado em `conectar meta`.
2. **Pergunta diária ativa.** Job `ctwa.followup` às 18h: "Hoje chegaram 7 conversas dos anúncios. Alguma virou venda? Responda 'fechou 1 por 800' ou 'nenhuma'". Reaproveita o padrão de `enqueueDaily` e a janela silenciosa de `report.Quiet`. Para quem nunca responde, reduzir a frequência (3 silêncios seguidos = semanal).
3. **Eco do dono no celular (só arquitetura A).** Mensagem `from_me` na conversa do lead com palavras-chave ("pix recebido", "comprovante", "fechado", "contrato assinado", "R$ ..."). Vira **candidato**, nunca evento direto: a Mia pergunta ao dono "o lead Maria fechou por R$ 1.500?". Modo automático só por opção explícita do cliente. A classificação "esta frase confirma venda? sim/não" é decisão fechada de alto volume e passa primeiro pelo Jev (`decide`), como manda a regra global; só a lista curta sobe ao Claude.
4. **Comprovante do lead.** Lead manda imagem/PDF com legenda "comprovante"/"pix"; a Mia (ou o Jev, para triagem) marca como candidato, mesma confirmação do dono. Extração de valor por OCR/LLM como segunda fase.
5. **Pix na fonte.** Webhook do PSP do cliente (Asaas, Efí, Mercado Pago etc.) casando pagador/valor com a conversa. É a evidência mais forte, mas exige integração por cliente. Fase posterior, opcional.

### 5.2 Estados do lead

`novo` → `qualificado` → `ganho` ou `perdido` ou `expirado`.

- `qualificado`: o dono diz "esse é bom", ou a Mia/regra marca (pediu orçamento, deu orçamento, pediu visita). Gera `QualifiedLead` (ou `LeadSubmitted`; escolha de nome é decisão aberta, ver seção 8).
- `ganho`: venda confirmada pelo dono (camada 1 a 4). Gera `Purchase` com valor.
- `expirado`: passou da janela de 7 dias sem fechamento; o clid é apagado (seção 6).

### 5.3 Roteamento seguro do webhook

Hoje o webhook assume que quem fala é um membro. Para números de cliente (arquitetura A ou C2):

- Rota própria `POST /webhooks/wame/leads/{slug}?token=` com segredo por cliente (a Wame gera uma URL de webhook por instância com segredo próprio **[Wame]**; ela não assina as chamadas, então manter a comparação em tempo constante já usada).
- O handler **não** cria conversa nem aciona a Mia. Faz só: ler `referral`; gravar o lead; gravar o eco de interesse (palavras-chave); descartar o resto sem persistir o corpo da mensagem.
- Não chamar a sincronização de histórico/contatos da coexistência (`smb` history/contacts); a Wame só envia depois dessa chamada explícita **[Wame]**.

---

## 6. Riscos de LGPD e consentimento

Pontos de partida: o `ctwa_clid` é identificador pessoal único por clique **[Meta]**; o telefone do lead é dado pessoal; devolver compra à Meta é compartilhamento com a Meta (inclui transferência internacional).

| Risco | Tratamento proposto |
|---|---|
| Papéis | Cliente = controlador dos dados dos leads dele; N4G = operador. Meta atua com finalidade própria no uso do sinal. Formalizar em aditivo ao Termo de Uso e à Política de Privacidade (versão nova) |
| Consentimento do cliente (dono) | Novo `kind` em `consents` (ex.: `ctwa_capi`) com versão, data, IP e user agent, aceito antes de ligar o recurso. Sem aceite, nenhuma captura nem envio |
| Base legal perante o lead | Legítimo interesse (medição de anúncio) com informação clara; o cliente deve citar o uso da Meta na política dele. Oferecer texto-modelo. Revisão jurídica externa recomendada: esta spec não substitui parecer |
| Minimização | Enviar à Meta só `ctwa_clid`, WABA e valor/moeda; **nunca** conteúdo de mensagem, nome ou telefone, mesmo hasheados. Em nichos de saúde, jurídico e financeiro sensíveis, bloquear o envio de valor por padrão (a Meta restringe dados sensíveis em eventos; confirmar política vigente **[NÃO CONFIRMADO]**) |
| Ingestão excessiva (A) | O número do cliente entra com todas as conversas pessoais e comerciais. O handler descarta tudo que não seja referral ou candidato de venda, não pede histórico, e o painel nunca mostra o texto bruto |
| Retenção | `ctwa_clid` apagado em até 8 dias (janela de 7 + folga) ou ao ganhar/perder; telefone do lead mantido no máximo 90 dias; valor de venda e campanha mantidos sem identificação direta para o relatório de ROI. Executar dentro de `maintenance.cleanup` (`internal/engine/cleanup.go`) |
| Direitos do titular | Rotina "esquecer telefone X" no admin que apaga lead, vendas e eventos pendentes dele e grava em `audit_events`; evento já enviado à Meta não é recuperável, e isso deve estar dito no aditivo |
| Tokens | Token de CAPI cifrado como o da Meta já é (`secretbox`, `META_TOKEN_KEY`); nunca em log |
| Transparência na Mia | Frase quando registrar venda: "vou informar a Meta que essa conversa virou venda, sem seu nome ou telefone". Dono pode desligar por campanha |

---

## 7. Recomendação, esquema de dados, jobs, telas e fases

### 7.1 Recomendação

1. Construir a **captura de venda (C)** e o **funil interno** primeiro. Entrega ROAS real por campanha mesmo que a Meta não aceite nada, e é pré-requisito de A e B.
2. Fazer um **piloto de A (coexistência)** com um cliente e **três perguntas à Wame** antes de investir na CAPI: referral chega em coexistência? Há acesso ao token ou proxy de CAPI? Qual o preço da instância oficial por cliente?
3. Manter **B** como plano para clientes sem WhatsApp Business app, mas só depois de testar as duas incógnitas eliminatórias (vínculo do número da Mia a várias Páginas e visibilidade do dataset pelo anúncio do cliente).
4. **D** e **C2** só como experimentos de última linha.
5. A troca da otimização do adset (para compra) só entra depois de haver eventos reais fluindo e um teste da Marketing API; até lá, o adset segue em `CONVERSATIONS` e os eventos alimentam relatório e Gerenciador de Eventos.

### 7.2 Esquema de dados e migrations

Todas idempotentes (`IF NOT EXISTS`), com `ENABLE ROW LEVEL SECURITY` como nas demais (padrão de 0010, 0023, 0037). Numeração conforme pedido; conferir colisão com 0039-0059.

**`0060_ctwa_leads.sql`**

```sql
CREATE TABLE IF NOT EXISTS ctwa_leads (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  project_id bigint REFERENCES projects(id),
  publication_id bigint REFERENCES meta_publications(id),
  ad_id text NOT NULL DEFAULT '',            -- referral.source_id
  ctwa_clid text,                            -- apagado ao expirar/ganhar/perder; sem hash
  waba_id text NOT NULL DEFAULT '',
  phone_number_id text NOT NULL DEFAULT '',
  lead_phone text NOT NULL DEFAULT '',       -- canônico (NormalizePhone); retenção curta
  lead_name text NOT NULL DEFAULT '',
  origin text NOT NULL CHECK (origin IN ('webhook','manual','history')),
  source_message_id text,                    -- id da mensagem que trouxe o referral
  referral jsonb NOT NULL DEFAULT '{}',      -- só campos do anúncio (headline, source_url...), nunca o texto do lead
  state text NOT NULL DEFAULT 'new' CHECK (state IN ('new','qualified','won','lost','expired')),
  first_contact_at timestamptz NOT NULL,
  qualified_at timestamptz,
  closed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (organization_id, source_message_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS ctwa_leads_clid_idx ON ctwa_leads (ctwa_clid) WHERE ctwa_clid IS NOT NULL;
CREATE INDEX IF NOT EXISTS ctwa_leads_org_idx ON ctwa_leads (organization_id, first_contact_at DESC);
CREATE INDEX IF NOT EXISTS ctwa_leads_pub_idx ON ctwa_leads (publication_id);
ALTER TABLE ctwa_leads ENABLE ROW LEVEL SECURITY;
```

**`0061_ctwa_sales.sql`**: uma venda pode existir sem lead ligado (captura sem clid).

```sql
CREATE TABLE IF NOT EXISTS ctwa_sales (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  lead_id bigint REFERENCES ctwa_leads(id),
  publication_id bigint REFERENCES meta_publications(id),  -- atribuição estimada quando lead_id é nulo
  amount_cents bigint NOT NULL CHECK (amount_cents >= 0),
  currency text NOT NULL DEFAULT 'BRL',
  source text NOT NULL CHECK (source IN ('owner_command','daily_followup','echo_keyword','receipt','pix','admin')),
  confirmed_by bigint REFERENCES members(id),
  confirmed_at timestamptz,                   -- nulo = candidato, ainda não confirmado
  note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ctwa_sales_org_idx ON ctwa_sales (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ctwa_sales_lead_idx ON ctwa_sales (lead_id);
ALTER TABLE ctwa_sales ENABLE ROW LEVEL SECURITY;
```

**`0062_capi_events.sql`**: outbox próprio de eventos para a Meta (o `outbox_jobs` agenda; esta tabela guarda o estado e a prova do que foi enviado).

```sql
CREATE TABLE IF NOT EXISTS capi_events (
  id bigserial PRIMARY KEY,
  organization_id bigint NOT NULL REFERENCES organizations(id),
  lead_id bigint NOT NULL REFERENCES ctwa_leads(id),
  sale_id bigint REFERENCES ctwa_sales(id),
  event_name text NOT NULL CHECK (event_name IN ('LeadSubmitted','QualifiedLead','Purchase')),
  event_id text NOT NULL UNIQUE,              -- "wa:{event_name}:{lead_id}:{sale_id|0}" determinístico
  event_time timestamptz NOT NULL,
  value_cents bigint,
  currency text NOT NULL DEFAULT 'BRL',
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed','expired','skipped')),
  attempts int NOT NULL DEFAULT 0,
  last_error text NOT NULL DEFAULT '',
  fbtrace_id text NOT NULL DEFAULT '',
  sent_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS capi_events_pending_idx ON capi_events (status, created_at) WHERE status IN ('pending','failed');
CREATE INDEX IF NOT EXISTS capi_events_org_idx ON capi_events (organization_id, created_at DESC);
ALTER TABLE capi_events ENABLE ROW LEVEL SECURITY;
```

**`0063_ctwa_conexao.sql`**: estado por organização.

```sql
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS waba_id text NOT NULL DEFAULT '';
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS capi_dataset_id text NOT NULL DEFAULT '';
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS capi_token_ciphertext bytea;   -- só se o token vier de nós (caminho A2/B)
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS capi_checked_at timestamptz;
ALTER TABLE meta_connections ADD COLUMN IF NOT EXISTS capi_error text NOT NULL DEFAULT '';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS ctwa_mode text NOT NULL DEFAULT 'off'
  CHECK (ctwa_mode IN ('off','manual','coexistence','mia'));
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS ctwa_auto_confirm boolean NOT NULL DEFAULT false;
```

**`0064_consentimento_ctwa.sql`**: o `CHECK` de `consents.kind` é inline; a migration remove a restrição pelo nome gerado (`consents_kind_check`, confirmar em ambiente) e recria incluindo `'ctwa_capi'`. Sem alterar linhas existentes.

**`0065_ctwa_resultado_diario.sql`** (opcional): colunas `leads`, `qualified`, `sales`, `revenue_cents` em uma tabela nova `campaign_funnel_daily(publication_id, day, ...)` com a mesma chave de `campaign_results`, para o relatório diário somar sem reprocessar. Não alterar `campaign_results`.

Contrato de mudança em código existente: **só adições**. `Inbound` ganha campo opcional `Referral`; nova interface opcional `ReferralParser` (padrão de `ConnectionParser`); `Intent` ganha `record_sale`; `Valid()` inclui o novo caso. Nenhuma assinatura pública existente muda. `planFor` e `Publish` só mudam na fase 5.

### 7.3 Jobs (outbox)

| Job | Quando | O que faz |
|---|---|---|
| `ctwa.capture` | por webhook de lead | grava/atualiza `ctwa_leads`; casa `source_id` com `meta_publications.ad_ids` |
| `ctwa.followup` | diário 18h (pula janela silenciosa) | pergunta ao dono quem fechou; lista leads sem estado final |
| `ctwa.sale` | ao confirmar venda | cria `ctwa_sales`, move o lead a `won`, enfileira `capi_events` |
| `capi.send` | contínuo, lote ≤ 1.000 | envia pendentes; backoff exponencial; 4xx de validação → `failed` sem repetir; evento com `event_time` além de 7 dias → `expired`; 429/5xx → repete |
| `capi.dataset_check` | na conexão e diário | `GET /{waba}/dataset`; grava `capi_dataset_id`, `capi_error` |
| `ctwa.expire` | dentro de `maintenance.cleanup` | apaga clid > 8 dias, telefone > 90 dias, marca `expired` |

Idempotência: `capi_events.event_id UNIQUE`; um lead só gera um `QualifiedLead`; `Purchase` por venda. Nunca enviar sem `ctwa_clid` ativo no lead.

### 7.4 Telas do admin (`internal/httpapi/web/admin`, rotas `admin_ctwa.go`)

1. **Funil por campanha**: cliques/conversas (já em `campaign_results`) → leads com clid → qualificados → vendas → receita → ROAS; filtro por cliente, campanha, anúncio e período. Alinhado ao painel de campanhas da spec `2026-10-14-painel-campanhas-integracao-design.md`.
2. **Leads**: lista com estado, origem, anúncio, idade do clid (cor quando faltam menos de 2 dias), ações "qualificar", "fechou R$ X", "perdeu"; telefone mascarado por padrão.
3. **Fila de eventos**: `capi_events` com status, erro, `fbtrace_id`, botão reenviar e descartar (mesmo padrão de `jobs/{id}/retry`).
4. **Conexão CTWA por cliente**: modo (`off/manual/coexistence/mia`), WABA, dataset, última verificação, "enviar evento de teste" (usando o código de teste do Gerenciador de Eventos, **[NÃO CONFIRMADO]** se a CAPI de mensagens aceita `test_event_code`), estado do consentimento.
5. **LGPD**: busca por telefone, botão "esquecer", exportação do histórico de eventos enviados.

### 7.5 Fases e estimativa

| Fase | Entrega | Estimativa | Depende de |
|---|---|---|---|
| 0. Observação | Ler `referral` no parser da Wame (campo novo, aditivo) e logar/guardar se aparecer nas conversas da Mia; consulta SQL em `inbox_events` por `payload->...->referral`; envio das 3 perguntas à Wame | 0,5 a 1 dia | nada |
| 1. Piloto de coexistência | Instância Wame oficial em coexistência num cliente-piloto; confirmar referral, eco e perda de companion; sem CAPI ainda | 2 a 3 dias de trabalho + espera externa | cliente piloto, Wame |
| 2. Captura de venda e funil interno | Migrations 0060-0061, 0063-0064, intent `record_sale`, `ctwa.followup`, telas 1, 2 e 5; relatório diário com receita e ROAS | 4 a 5 dias | nada externo |
| 3. CAPI | Migration 0062, cliente Graph para dataset e eventos, `capi.send`, tela 3 e 4, envio de teste | 4 a 6 dias | resposta sobre o token (Wame/App Review) |
| 4. Roteamento de lead no webhook | Rota `/webhooks/wame/leads/{slug}`, handler de descarte, eco `from_me`, camada 3 de "fechou" | 3 a 4 dias | resultado da fase 1 |
| 5. Otimização por compra | Teste da Marketing API (adset `OUTCOME_SALES` + `OFFSITE_CONVERSIONS`), nova opção em `planFor`, guarda para só habilitar com volume | 2 a 3 dias + semanas de coleta | fases 3 e 4 com volume real |
| App Review | `whatsapp_business_manage_events` avançado, se o caminho do token for o nosso app | calendário externo, semanas | fluxo de revisão que já bloqueia o produto |

Total de engenharia: cerca de 3 a 4 semanas corridas, sem contar a espera de terceiros. As fases 0 e 2 não dependem de ninguém e já geram valor.

### 7.6 Testes (guarda-regressão)

Cada fase com testes antes da entrega; contratos protegidos: `Provider`, `Inbound`, `ParseWebhook`/`Split` da Wame (testes existentes em `wame_test.go` devem continuar verdes), `Intent.Valid`, `planFor`, `PublishInput`. Fixtures: payload de referral da Meta/AWS (seção 4) e os exemplos `message-referral`/`message-referral-promo` da Wame. Casos: referral ausente (anúncio de Status não traz clid **[Meta]**), duas mensagens no mesmo envelope, clid repetido, evento fora de 7 dias, retry sem duplicar, esquecer telefone com evento pendente.

---

## 8. Decisões abertas para o usuário

1. Aceita começar pela captura de venda (fase 2) mesmo antes de a Meta receber qualquer evento?
2. Qual evento de "lead qualificado" usar: `QualifiedLead` (lista da Meta) ou `LeadSubmitted`? Sugestão: `QualifiedLead` para qualificação real e nenhum evento para "primeira mensagem" (já é a conversa que a Meta conta).
3. Valor padrão no `Purchase`: sempre o valor informado pelo dono, ou permitir só `QualifiedLead` em nichos sensíveis?
4. Modo automático para o eco do celular (A) fica desligado por padrão; ok?
5. Cliente-piloto para a fase 1.

---

## 9. O que não sei e precisa de teste ao vivo

| # | Pergunta | Como testar | Impacto |
|---|---|---|---|
| 1 | O `referral` com `ctwa_clid` chega no webhook de um número em **coexistência**? | Piloto fase 1 com um anúncio real de R$ 10 | decide A |
| 2 | Quem tem token com `whatsapp_business_manage_events` na WABA do cliente (Wame proxy, nosso app ou ninguém)? | Pergunta ao suporte da Wame + tentar a chamada de dataset com nosso token | decide A e fase 3 |
| 3 | O Facebook Login for Business aceita ativo "conta do WhatsApp" e a permissão `whatsapp_business_manage_events`? | Ajustar a configuração no app em modo desenvolvimento | caminho A2 |
| 4 | O número da Mia pode ser vinculado a Páginas de vários clientes e usado em `promoted_object.whatsapp_phone_number`? | Criar adset de teste em conta de teste | decide B |
| 5 | O dataset da WABA da N4G é visto pelo anúncio da conta do cliente? | Enviar evento de teste e ver se aparece no Gerenciador do cliente e no relatório do anúncio | decide B |
| 6 | A janela de 7 dias conta do clique ou do envio do evento? | Enviar evento de um clid de 6 e de 8 dias | impacto em vendas longas |
| 7 | A CAPI de mensagens honra `event_id` e `test_event_code`? | Duplicar um evento de teste | desenho de dedup e botão de teste |
| 8 | Qual `partner_agent` usar e como obtê-lo? | Contato com a Meta/Wame | pode bloquear envio |
| 9 | Quais campos do adset ligam a otimização por compra/lead via Marketing API? | Criar adset de teste `OUTCOME_SALES`, `OFFSITE_CONVERSIONS` | fase 5 |
| 10 | Volume mínimo de eventos para a otimização aprender em CTWA | Observação em Ads Manager | quando trocar a otimização |
| 11 | Versão atual do Graph para dataset e eventos (a página mostra v16.0) | Chamada em teste | código do cliente |
| 12 | Eco `from_me` de coexistência na Wame: formato exato e se vem pelo `webhookMessageFromMe` | Piloto fase 1 | camada 3 de "fechou" |
| 13 | A Wame entrega o referral também no modo oficial exatamente como a Meta (`referral` dentro de `messages[]`)? | Fase 0, olhando `inbox_events.payload` | parser |
| 14 | Preço da instância oficial da Wame por cliente e cobrança de mensagem de serviço a partir de outubro | Pergunta à Wame; conferir pricing da Meta | custo de A e B |
| 15 | Política da Meta para eventos em nichos sensíveis (saúde, financeiro) | Ler a política vigente de dados sensíveis | travas de produto |
| 16 | A via não oficial (C2) entrega clid válido para a CAPI e existe WABA nesse caso? | Só se A falhar | viabilidade de C2 |

---

## 10. Fontes consultadas (2026-10-14)

- Meta, Conversions API for Business Messaging: Onboarding Guide: https://developers.facebook.com/docs/marketing-api/conversions-api/business-messaging
- Meta, referência de mensagens do webhook (objeto `referral`): https://developers.facebook.com/documentation/business-messaging/whatsapp/webhooks/reference/messages/text.md
- Meta, onboarding de usuários do Business app (coexistência): https://developers.facebook.com/documentation/business-messaging/whatsapp/embedded-signup/onboarding-business-app-users.md
- Meta, Click to WhatsApp (Marketing API): https://developers.facebook.com/docs/marketing-api/ad-creative/messaging-ads/click-to-whatsapp/
- Meta, medição de conversões em mensagens de marketing (não cobre a CAPI de mensagens): https://developers.facebook.com/documentation/business-messaging/whatsapp/marketing-messages/measure-conversion.md
- WhatsApp Business, otimização por lead e compra: https://whatsappbusiness.com/resources/resource-library/down-funnel-optimizations-whatsapp/
- AWS End User Messaging Social, eventos de conversão (formato, janela de 7 dias, lote de 1.000, PII com hash): https://docs.aws.amazon.com/social-messaging/latest/userguide/conversions-api.html
- Wame, referência completa: https://wame.api.br/llms-full.txt
- Wame, webhooks: https://wame.api.br/docs/webhooks.md
- Wame, API oficial e coexistência: https://wame.api.br/oficial e https://wame.api.br/blog/whatsapp-api-oficial-manter-no-celular.md
- Wame, cobrança de outubro de 2026 (conteúdo promocional da própria Wame): https://wame.api.br/blog/coexistencia-nova-cobranca-whatsapp-2026.md
- Wame, provisionar clientes: https://wame.api.br/blog/provisionar-whatsapp-multiplos-clientes-api.md
- Terceiros (indício): Zernio (https://docs.zernio.com/whatsapp/send-whatsapp-conversion), Metricfixer (https://metricfixer.com/publications/analytics-conversion-tracking/track-click-to-whatsapp-ads-campaign-ad-creative-without-website), Woztell (https://support.woztell.com/portal/en/kb/articles/wa-conversion-flow)
