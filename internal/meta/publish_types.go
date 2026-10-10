package meta

import (
	"context"
	"fmt"
	"time"
)

// PublishCreative é uma imagem ou vídeo que vira anúncio.
type PublishCreative struct {
	Kind      string // "image" | "video"
	Mime      string
	Data      []byte
	Placement string // "feed" | "stories"
}

// PublishCopy é um texto de anúncio.
type PublishCopy struct {
	Headline string
	Text     string
	CTA      string // WHATSAPP_MESSAGE | LEARN_MORE | CONTACT_US
}

// PublishInput é tudo o que a publicação precisa; a campanha nasce PAUSADA.
type PublishInput struct {
	AdAccountID      string // com ou sem "act_"
	PageID           string
	InstagramActorID string // opcional
	WhatsAppNumber   string // E.164 sem +, opcional
	LinkURL          string // opcional; vazio = página do Facebook
	Name             string
	Objective        string // OUTCOME_ENGAGEMENT | OUTCOME_LEADS | OUTCOME_TRAFFIC
	DailyBudgetCents int64  // na moeda da conta
	StartTime        time.Time
	EndTime          time.Time // zero = sem fim
	LocationQuery    string
	RadiusKM         int
	AgeMin, AgeMax   int
	Genders          []string // "male" | "female"; vazio = todos
	Interests        []string // nomes; os não encontrados são ignorados
	Creatives        []PublishCreative
	Copies           []PublishCopy
}

// PublishResult são os IDs criados na conta de anúncios.
type PublishResult struct {
	CampaignID string
	AdSetID    string
	AdIDs      []string
	Warnings   []string
}

// PublishError é uma falha da Graph API numa etapa da publicação.
type PublishError struct {
	Step        string // "campaign" | "adset" | "creative" | "ad" | "upload" | "targeting"
	Code        int
	Subcode     int
	Message     string // técnico
	UserMessage string // pt-BR, para o cliente
}

func (e *PublishError) Error() string {
	return fmt.Sprintf("meta: publicação (%s): %d/%d %s", e.Step, e.Code, e.Subcode, e.Message)
}

// Publisher cria a campanha na conta do cliente e liga ou pausa. *Client implementa.
type Publisher interface {
	Publish(ctx context.Context, token string, in PublishInput) (PublishResult, error)
	SetStatus(ctx context.Context, token, campaignID, status string) error // "ACTIVE" | "PAUSED"
}

// FirstActivation são os IDs gravados na publicação e o fim do período, contado da ativação.
type FirstActivation struct {
	CampaignID string
	AdSetID    string
	AdIDs      []string
	EndTime    time.Time // zero = sem fim
}

// Activator faz a primeira ativação: liga o conjunto e os anúncios criados na publicação e a campanha.
// Opcional ao Publisher; *Client implementa.
type Activator interface {
	ActivateFirst(ctx context.Context, token string, a FirstActivation) error
}
