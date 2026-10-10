# Publicação efetiva na Meta (2026-10-10)

Objetivo: depois que o cliente aprova a proposta, manda as fotos/vídeos e (opcional) referências de
headline/texto, o sistema publica a campanha de verdade na conta de anúncios dele, PAUSADA, e ativa
quando o cliente responder ATIVAR.

## Fluxo no WhatsApp

1. Proposta aprovada (já existe) -> estado `awaiting_creatives` (já existe) pede imagens/vídeos.
2. NOVO: nessa mesma etapa, a Mia também pede referências: "Se tiver headlines, textos ou referências
   de anúncio que você gosta, manda aqui também (ou escreve PRONTO)". Texto do cliente que não for
   comando vira referência de copy, guardada no rascunho (`campaign_drafts.client_copy`, texto livre,
   até 2000 caracteres, acumulando). Na publicação, o Gestor (LLM) reescreve as copies da proposta
   usando as referências (mantendo as regras de `contract.CampaignProposal.Validate` para copies);
   se o LLM falhar, usa as copies da proposta.
3. PRONTO -> prévia (já existe) -> se a Meta NÃO está conectada, pede a conexão (já existe
   `askMetaAfterPreview`). Se ESTÁ conectada, enfileira `meta.publish`.
4. Quando a conexão Meta é concluída (`store.CompleteMeta`) e existe rascunho pronto
   (projeto em `StageReady`, sem publicação), enfileira `meta.publish`.
5. `meta.publish` (worker): monta `meta.PublishInput`, chama `Publisher.Publish`, grava em
   `meta_publications`, avisa: "Campanha criada na sua conta, PAUSADA. Confere no Gerenciador
   e responde ATIVAR pra colocar no ar." Erro da Meta -> mensagem humana (`meta.PublishError.UserMessage`)
   + alerta da equipe; status `failed` na publicação.
6. Cliente responde ATIVAR (com publicação `paused`) -> `Publisher.SetStatus(ACTIVE)` -> "No ar ✅".
   PAUSAR -> `SetStatus(PAUSED)`.

## Contrato entre as partes (fixo; os dois lados implementam exatamente isto)

### Pacote `internal/meta` (Agente A)

```go
// Objetivos aceitos (iguais aos de internal/campaign): OUTCOME_ENGAGEMENT (conversas no WhatsApp),
// OUTCOME_LEADS, OUTCOME_TRAFFIC.
type PublishCreative struct {
	Kind      string // "image" | "video"
	Mime      string
	Data      []byte
	Placement string // "feed" | "stories" (media.Feed / media.Stories)
}

type PublishCopy struct {
	Headline string
	Text     string
	CTA      string // WHATSAPP_MESSAGE | LEARN_MORE | CONTACT_US
}

type PublishInput struct {
	AdAccountID      string // com ou sem "act_"
	PageID           string
	InstagramActorID string // opcional
	WhatsAppNumber   string // E.164 sem +, opcional (destino do clique p/ WhatsApp)
	LinkURL          string // opcional (TRAFFIC/LEARN_MORE); vazio = página do Facebook
	Name             string // nome da campanha
	Objective        string
	DailyBudgetCents int64  // na moeda da conta
	StartTime        time.Time
	EndTime          time.Time // zero = sem fim
	LocationQuery    string    // texto livre da cidade/região ("Chapecó, SC")
	RadiusKM         int
	AgeMin, AgeMax   int
	Genders          []string // "male" | "female"; vazio = todos
	Interests        []string // nomes; os não encontrados são ignorados
	Creatives        []PublishCreative
	Copies           []PublishCopy
}

type PublishResult struct {
	CampaignID string
	AdSetID    string
	AdIDs      []string
	Warnings   []string // ex.: interesse não encontrado, local aproximado
}

type PublishError struct {
	Step        string // "campaign" | "adset" | "creative" | "ad" | "upload" | "targeting"
	Code        int    // código da Graph API
	Subcode     int
	Message     string // técnico
	UserMessage string // pt-BR, para o cliente (ex.: "Sua conta de anúncios está sem forma de pagamento...")
}
func (e *PublishError) Error() string

type Publisher interface {
	Publish(ctx context.Context, token string, in PublishInput) (PublishResult, error)
	SetStatus(ctx context.Context, token, campaignID, status string) error // "ACTIVE" | "PAUSED"
}
// *meta.Client implementa Publisher.
```

Tudo é criado com `status=PAUSED`. Em falha no meio, o que já foi criado é apagado (melhor esforço)
para não deixar lixo na conta. Graph API: a mesma `version` de `internal/meta/meta.go` (v23.0).

### Banco e store (Agente A)

Migração `0023_meta_publicacoes.sql` (com RLS como as outras, `IF NOT EXISTS` em índices):

- `ALTER TABLE campaign_drafts ADD COLUMN IF NOT EXISTS client_copy text NOT NULL DEFAULT ''`
- tabela `meta_publications`: id bigserial, organization_id, draft_id (único), project_id null,
  meta_connection_id, status (`publishing|paused|active|failed`), campaign_id, adset_id, ad_ids text[],
  error text, warnings text[], created_at, updated_at, activated_at.

Store (`internal/store/publication.go`):

```go
type Publication struct { ID, OrgID, DraftID int64; ProjectID *int64; ConnectionID int64; Status, CampaignID, AdSetID string; AdIDs, Warnings []string; Error string; CreatedAt, UpdatedAt time.Time; ActivatedAt *time.Time }
func (s *Store) MetaTokenSealed(ctx context.Context, orgID int64) (connID int64, sealed []byte, err error) // ErrNotFound sem conexão ativa
func (s *Store) StartPublication(ctx context.Context, orgID, draftID int64, projectID *int64, connID int64) (Publication, error) // idempotente: já existe e não está failed -> devolve a existente com ErrAlreadyPublished
func (s *Store) FinishPublication(ctx context.Context, id int64, r PublicationResult) error // status paused, ids, warnings
func (s *Store) FailPublication(ctx context.Context, id int64, msg string) error
func (s *Store) SetPublicationStatus(ctx context.Context, id int64, status string) error // active grava activated_at
func (s *Store) LatestPublication(ctx context.Context, orgID int64) (Publication, error)
func (s *Store) AppendClientCopy(ctx context.Context, draftID int64, text string) error // acumula, corta em 2000 runas
func (s *Store) PublishData(ctx context.Context, draftID int64) (PublishData, error) // proposta (JSON do draft), fields (offer/location...), client_copy, project name, org name
type PublicationResult struct { CampaignID, AdSetID string; AdIDs, Warnings []string }
var ErrAlreadyPublished = errors.New("store: rascunho já publicado")
```

### Engine e worker (Agente B)

- `Engine.Publisher meta.Publisher` e `Engine.MetaTokenKey` (para abrir o token com `internal/secretbox`).
- Job `JobMetaPublish = "meta.publish"`, payload `PublishJob{ConversationID, DraftID int64}`.
- Comandos ATIVAR / PAUSAR (com publicação existente) e referências de copy na etapa de criativos.
- Worker: env `META_TOKEN_KEY` (já existe na api) + `META_APP_ID/SECRET` ligam o Publisher.
