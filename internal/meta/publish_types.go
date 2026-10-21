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
	// InstagramMediaID: post da conta do Instagram (InstagramActorID) usado como anúncio, sem upload.
	// O anúncio leva a legenda, as curtidas e os comentários do post; Data e os textos ficam de fora.
	InstagramMediaID string
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
	InstagramActorID string        // opcional
	WhatsAppNumber   string        // número do WhatsApp da Página, só dígitos; opcional (vazio = fica de fora)
	PageWhatsApp     bool          // a Página tem WhatsApp vinculado (mesmo sem o número conhecido)
	LinkURL          string        // opcional; vazio = página do Facebook
	Name             string        // terminando em "#p<id>", a campanha de tentativa anterior é procurada e apagada antes
	Objective        string        // OUTCOME_ENGAGEMENT | OUTCOME_LEADS | OUTCOME_TRAFFIC
	LeadForm         bool          // com OUTCOME_LEADS: formulário instantâneo em vez de WhatsApp
	FormPrivacyURL   string        // política de privacidade do formulário (a Meta exige)
	Form             *LeadFormSpec // como o cliente montou o formulário; nil = nome, telefone e e-mail
	DailyBudgetCents int64         // na moeda da conta
	StartTime        time.Time
	EndTime          time.Time // zero = sem fim
	LocationQuery    string
	RadiusKM         int
	AgeMin, AgeMax   int
	Genders          []string // "male" | "female"; vazio = todos
	Interests        []string // nomes; os não encontrados são ignorados
	Placements       []string // "feed" | "stories" | "reels"; vazio = posicionamento automático
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
	// ClientSide: só quem anuncia resolve (termos, pagamento, conta, Página); a mensagem traz erro e solução.
	ClientSide bool
	// Orphan é a campanha que ficou na conta porque o rollback falhou (a equipe apaga ou a próxima
	// tentativa acha pela marca do nome).
	Orphan string
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

// LeadFormSpec é o formulário instantâneo já nos termos da Meta. Campo vazio = padrão da Meta.
type LeadFormSpec struct {
	Qualified         bool // "mais qualificado": a pessoa revisa os dados antes de enviar
	Headline          string
	Intro             string
	Bullets           []string
	IntroButton       string
	QuestionsHeadline string
	Questions         []LeadFormQuestion // vazio = nome, telefone e e-mail
	ThanksTitle       string
	ThanksText        string
	ButtonAction      string // "site" | "whatsapp" | "call" | "none"
	ButtonText        string
	ButtonURL         string // site; vazio = destino do anúncio
	ButtonPhone       string // só dígitos com DDI (whatsapp e call)
	PrivacyURL        string // política do cliente; vazio = FormPrivacyURL
}

// LeadFormQuestion é uma pergunta: Type é o tipo da Meta (FULL_NAME, PHONE, CUSTOM...).
// CUSTOM com Options vira múltipla escolha; sem Options, resposta livre.
type LeadFormQuestion struct {
	Type    string
	Label   string
	Options []string
}
