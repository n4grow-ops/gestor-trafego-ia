// Package notify manda avisos para apps de automação por webhook: Pushcut (iPhone), MacroDroid
// (Android) e um webhook genérico. Os eventos ficam numa lista central (Events): para criar um evento
// novo, acrescente-o aqui e chame Store.EmitNotify onde ele acontece.
package notify

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Event descreve um evento que dá para assinar. Vars são as variáveis do modelo ({nome}...).
type Event struct {
	Key          string            `json:"key"`
	Label        string            `json:"label"`
	Description  string            `json:"description"`
	Vars         []string          `json:"vars"`
	Sample       map[string]string `json:"sample"`
	DefaultTitle string            `json:"default_title"`
	DefaultText  string            `json:"default_text"`
	Subscribable bool              `json:"subscribable"` // false = só pelo botão (teste)
}

const (
	EventSale       = "venda.nova"
	EventNewClient  = "cliente.novo"
	EventSignup     = "cadastro.novo"
	EventMessage    = "cliente.mensagem"
	EventTest       = "teste"
	maxTitleRunes   = 120
	maxTextRunes    = 1000
	maxMessageRunes = 300
)

// CommonVars valem em qualquer evento.
var CommonVars = []string{"evento", "quando"}

// VarHelp explica cada variável na tela.
var VarHelp = map[string]string{
	"evento":   "nome do evento",
	"quando":   "data e hora (Brasília)",
	"nome":     "nome do cliente",
	"empresa":  "nome do negócio",
	"whatsapp": "WhatsApp do cliente",
	"valor":    "valor da venda",
	"produto":  "plano ou produto",
	"origem":   "de onde veio",
	"mensagem": "texto da mensagem",
}

// Events é a lista central dos eventos.
var Events = []Event{
	{
		Key: EventSale, Label: "Venda aprovada", Subscribable: true,
		Description:  "Pagamento confirmado no Asaas, venda lançada à mão no Faturamento ou venda de fora pelo POST /api/hooks/venda.",
		Vars:         []string{"nome", "empresa", "whatsapp", "valor", "produto", "origem"},
		Sample:       map[string]string{"nome": "Ana Souza", "empresa": "Pizzaria Boa", "whatsapp": "5511988887777", "valor": "R$ 97,00", "produto": "Plano Pro", "origem": "asaas"},
		DefaultTitle: "💰 Venda aprovada: {valor}", DefaultText: "{nome} ({empresa}) comprou {produto}. Origem: {origem}.",
	},
	{
		Key: EventNewClient, Label: "Cliente chamou", Subscribable: true,
		Description:  "Um número novo mandou a primeira mensagem para a Mia no WhatsApp.",
		Vars:         []string{"nome", "whatsapp", "mensagem", "origem"},
		Sample:       map[string]string{"nome": "Cliente 7777-0042", "whatsapp": "5511988887777", "mensagem": "Oi, quero anunciar minha pizzaria", "origem": "whatsapp"},
		DefaultTitle: "📲 Cliente novo chamou", DefaultText: "{whatsapp}: {mensagem}",
	},
	{
		Key: EventSignup, Label: "Cadastro concluído", Subscribable: true,
		Description:  "O cliente terminou o cadastro (página ou formulário do WhatsApp).",
		Vars:         []string{"nome", "empresa", "whatsapp", "produto", "origem"},
		Sample:       map[string]string{"nome": "Ana Souza", "empresa": "Pizzaria Boa", "whatsapp": "5511988887777", "produto": "Plano Pro", "origem": "cadastro"},
		DefaultTitle: "📝 Cadastro novo", DefaultText: "{nome} ({empresa}) se cadastrou no {produto}.",
	},
	{
		Key: EventMessage, Label: "Mensagem do cliente", Subscribable: true,
		Description:  "Qualquer mensagem que um cliente manda para a Mia. Avisa muito: use com cuidado.",
		Vars:         []string{"nome", "empresa", "whatsapp", "mensagem", "origem"},
		Sample:       map[string]string{"nome": "Ana Souza", "empresa": "Pizzaria Boa", "whatsapp": "5511988887777", "mensagem": "Consegue subir a campanha hoje?", "origem": "whatsapp"},
		DefaultTitle: "💬 {nome}", DefaultText: "{mensagem}",
	},
	{
		Key: EventTest, Label: "Teste", Subscribable: false,
		Description:  "O botão “Enviar teste” do painel, com dados de exemplo.",
		Vars:         []string{"nome", "empresa", "whatsapp", "valor", "produto", "origem", "mensagem"},
		Sample:       map[string]string{"nome": "Ana Souza", "empresa": "Pizzaria Boa", "whatsapp": "5511988887777", "valor": "R$ 97,00", "produto": "Plano Pro", "origem": "teste", "mensagem": "Mensagem de exemplo"},
		DefaultTitle: "🔔 Teste do meuanuncio", DefaultText: "Se você está lendo isto, a notificação chegou.",
	},
}

// EventByKey acha o evento; false se não existe.
func EventByKey(key string) (Event, bool) {
	for _, e := range Events {
		if e.Key == key {
			return e, true
		}
	}
	return Event{}, false
}

// Subscribable diz se o evento pode ser assinado por um destino.
func Subscribable(key string) bool {
	e, ok := EventByKey(key)
	return ok && e.Subscribable
}

var varRE = regexp.MustCompile(`\{([a-z_]+)\}`)

// Render troca {variavel} pelo valor. Variável desconhecida fica como está.
func Render(tpl string, vars map[string]string) string {
	return varRE.ReplaceAllStringFunc(tpl, func(m string) string {
		if v, ok := vars[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

var brt = func() *time.Location {
	if l, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return l
	}
	return time.FixedZone("BRT", -3*3600)
}()

// Vars junta os dados do evento com as variáveis comuns.
func Vars(ev Event, data map[string]string, at time.Time) map[string]string {
	out := make(map[string]string, len(data)+2)
	for k, v := range data {
		out[k] = v
	}
	out["evento"] = ev.Label
	out["quando"] = at.In(brt).Format("02/01 15:04")
	return out
}

// Message monta título e texto: o modelo do destino ou, vazio, o padrão do evento.
func Message(ev Event, titleTpl, textTpl string, vars map[string]string) (title, text string) {
	if strings.TrimSpace(titleTpl) == "" {
		titleTpl = ev.DefaultTitle
	}
	if strings.TrimSpace(textTpl) == "" {
		textTpl = ev.DefaultText
	}
	return Clip(Render(titleTpl, vars), maxTitleRunes), Clip(Render(textTpl, vars), maxTextRunes)
}

// Clip corta em n caracteres (não em bytes), com reticências.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// MessageSnippet é o texto da mensagem do cliente que vai para a notificação.
func MessageSnippet(s string) string { return Clip(s, maxMessageRunes) }
