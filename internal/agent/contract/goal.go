package contract

import "strings"

// Tipos de campanha que o cliente pode pedir. Cada um vira um objetivo e um destino na Meta.
const (
	GoalWhatsApp   = "whatsapp"    // conversas no WhatsApp
	GoalDirect     = "direct"      // conversas no Direct do Instagram
	GoalEngagement = "engajamento" // curtidas, comentários e compartilhamentos no anúncio
	GoalFollowers  = "seguidores"  // visitas ao perfil do Instagram
	GoalLeadForm   = "formulario"  // formulário instantâneo da Meta
	GoalSite       = "site"        // cliques para o site
	GoalSales      = "vendas"      // vendas (conversa no WhatsApp com objetivo de vendas)
	GoalReach      = "alcance"     // mostrar para o maior número de pessoas da região
)

// AllObjectives são os objetivos da Meta que os tipos de campanha usam.
var AllObjectives = []string{"OUTCOME_ENGAGEMENT", "OUTCOME_LEADS", "OUTCOME_TRAFFIC", "OUTCOME_SALES", "OUTCOME_AWARENESS"}

var goalObjective = map[string]string{
	GoalWhatsApp: "OUTCOME_ENGAGEMENT", GoalDirect: "OUTCOME_ENGAGEMENT", GoalEngagement: "OUTCOME_ENGAGEMENT",
	GoalFollowers: "OUTCOME_TRAFFIC", GoalLeadForm: "OUTCOME_LEADS", GoalSite: "OUTCOME_TRAFFIC",
	GoalSales: "OUTCOME_SALES", GoalReach: "OUTCOME_AWARENESS",
}

// GoalObjective é o objetivo da Meta do tipo de campanha ("" = tipo desconhecido).
func GoalObjective(goal string) string { return goalObjective[goal] }

// goalWords: a ordem importa (formulário e seguidores antes de "mensagens", "direct" antes de "instagram").
var goalWords = []struct {
	goal  string
	words []string
}{
	{GoalLeadForm, []string{"formul", "lead form", "leadform", "cadastro"}},
	{GoalFollowers, []string{"seguidor", "seguir", "follower", "perfil do instagram", "perfil no instagram", "visitas ao perfil"}},
	{GoalDirect, []string{"direct", "dm do instagram", "mensagem no instagram", "mensagens no instagram"}},
	{GoalEngagement, []string{"engajamento", "curtida", "comentário", "comentario", "interaç", "interac", "engagement"}},
	{GoalSales, []string{"venda", "vender", "compra", "sales"}},
	{GoalSite, []string{"site", "landing", "página de vendas", "pagina de vendas", "loja virtual", "link", "tráfego", "trafego"}},
	{GoalReach, []string{"alcance", "reconhecimento", "visibilidade", "divulgar a marca", "divulgação da marca", "awareness", "reach"}},
	{GoalWhatsApp, []string{"whatsapp", "zap", "mensage", "conversa", "messages"}},
}

// GoalFromHint lê o tipo de campanha do pedido do cliente. "" = não disse (o padrão é conversa no WhatsApp).
func GoalFromHint(hint string) string {
	h := strings.ToLower(strings.TrimSpace(hint))
	if h == "" {
		return ""
	}
	for _, g := range goalWords {
		for _, w := range g.words {
			if strings.Contains(h, w) {
				return g.goal
			}
		}
	}
	// valor exato da Meta vindo de cadastro antigo
	switch strings.ToUpper(h) {
	case "OUTCOME_LEADS":
		return GoalWhatsApp // leads sem formulário = conversa
	case "OUTCOME_TRAFFIC":
		return GoalSite
	case "OUTCOME_SALES":
		return GoalSales
	case "OUTCOME_AWARENESS":
		return GoalReach
	case "OUTCOME_ENGAGEMENT":
		return GoalWhatsApp
	}
	return ""
}

// GoalLabel diz em português o que a campanha busca.
var GoalLabel = map[string]string{
	GoalWhatsApp:   "conversas no WhatsApp",
	GoalDirect:     "conversas no Direct do Instagram",
	GoalEngagement: "engajamento (curtidas, comentários e compartilhamentos)",
	GoalFollowers:  "seguidores no Instagram (o anúncio leva a pessoa ao seu perfil)",
	GoalLeadForm:   "contatos por formulário (a pessoa preenche um formulário na própria Meta)",
	GoalSite:       "visitas ao seu site",
	GoalSales:      "vendas (a pessoa chama no WhatsApp para comprar)",
	GoalReach:      "alcance (mostrar o anúncio para o máximo de pessoas da região)",
}
