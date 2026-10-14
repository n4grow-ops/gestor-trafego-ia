// Package report monta o relatório do dono em linguagem simples (texto e voz) e guarda as regras de
// entrega: preferência de áudio, silêncio da noite e janela de 24 h. Não faz I/O; só recebe números.
package report

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxWords é o teto do relatório: cerca de 45 s falado.
const MaxWords = 110

// Campaign é o resultado de uma campanha (anúncio) no dia, em centavos.
type Campaign struct {
	Name          string
	SpendCents    int64
	Conversations int64
}

// Totals é a soma de um dia (usada para o dia anterior).
type Totals struct {
	SpendCents    int64
	Conversations int64
}

// Input traz só números do banco. Prev nil = sem dia anterior para comparar.
type Input struct {
	Campaigns []Campaign
	Prev      *Totals
}

// NoData é o texto quando não há número nenhum para contar.
const NoData = "Bom dia! Ainda não tenho dados dos seus anúncios de ontem. Assim que chegarem, eu te conto por aqui."

// OwnerText é o relatório do dia em linguagem de dono: gasto, conversas, anúncio mais barato,
// uma observação honesta e, no máximo, uma sugestão. Nunca inventa número.
func OwnerText(in Input) string {
	var spend, convs int64
	for _, c := range in.Campaigns {
		spend += c.SpendCents
		convs += c.Conversations
	}
	if spend == 0 && convs == 0 {
		return NoData
	}

	head := "Bom dia! Ontem você investiu " + BRL(spend) + " nos anúncios"
	switch convs {
	case 0:
		head += ", mas ainda não chegou nenhuma conversa."
	case 1:
		head += " e recebeu 1 conversa de cliente, a " + BRL(spend) + " por conversa."
	default:
		head += fmt.Sprintf(" e recebeu %d conversas de clientes, a %s por conversa.", convs, BRL(spend/convs))
	}

	var best string
	if name, cost, ok := cheapest(in.Campaigns); ok {
		best = fmt.Sprintf("O anúncio que saiu mais barato foi o %s, a %s por conversa.", shortName(name), BRL(cost))
	}
	obs, sugg := observe(convs, in.Prev)

	parts := []string{head, best, obs, sugg}
	// Estourou o tamanho: a sugestão sai primeiro, depois o anúncio mais barato.
	for _, drop := range []int{3, 1} {
		if words(join(parts)) <= MaxWords {
			break
		}
		parts[drop] = ""
	}
	return join(parts)
}

func join(parts []string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

func words(s string) int { return len(strings.Fields(s)) }

// observe devolve a observação honesta e a sugestão (opcional) para o dia.
func observe(convs int64, prev *Totals) (obs, sugg string) {
	const revisar = "Se quiser, me chame aqui e a gente revisa o anúncio."
	hasPrev := prev != nil && prev.SpendCents > 0
	switch {
	case hasPrev && prev.Conversations >= 3 && convs*10 < prev.Conversations*7:
		return fmt.Sprintf("Vieram menos conversas que no dia anterior: %d, contra %d.", convs, prev.Conversations), revisar
	case convs == 0:
		return "Nos primeiros dias é comum não vir conversa ainda; vamos acompanhar.", ""
	case hasPrev && prev.Conversations >= 3 && convs*10 > prev.Conversations*13:
		return fmt.Sprintf("Foi melhor que o dia anterior: %d conversas, contra %d.", convs, prev.Conversations), ""
	case convs < 5 || !hasPrev:
		return "Ainda é pouco dado para tirar conclusões; vale esperar mais alguns dias.", ""
	}
	return "", ""
}

// cheapest acha o anúncio com menor custo por conversa; só vale com pelo menos dois com conversa.
func cheapest(cs []Campaign) (name string, cost int64, ok bool) {
	n := 0
	var bestConvs int64
	for _, c := range cs {
		if c.Conversations <= 0 {
			continue
		}
		n++
		cc := c.SpendCents / c.Conversations
		if n == 1 || cc < cost || (cc == cost && c.Conversations > bestConvs) {
			name, cost, bestConvs = c.Name, cc, c.Conversations
		}
	}
	return name, cost, n >= 2
}

func shortName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "da campanha"
	}
	if utf8.RuneCountInString(s) > 40 {
		s = string([]rune(s)[:39]) + "…"
	}
	return "*" + s + "*"
}

// BRL formata centavos como "R$ 1.234,56".
func BRL(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	reais := fmt.Sprint(cents / 100)
	var grouped []string
	for len(reais) > 3 {
		grouped = append([]string{reais[len(reais)-3:]}, grouped...)
		reais = reais[:len(reais)-3]
	}
	grouped = append([]string{reais}, grouped...)
	out := fmt.Sprintf("R$ %s,%02d", strings.Join(grouped, "."), cents%100)
	if neg {
		return "-" + out
	}
	return out
}

var money = regexp.MustCompile(`R\$\s?(\d{1,3}(?:\.\d{3})*),(\d{2})`)

// Spoken deixa o texto pronto para a voz: sem marcação do WhatsApp e com o dinheiro por extenso
// ("R$ 7,50" vira "7 reais e 50 centavos").
func Spoken(text string) string {
	text = money.ReplaceAllStringFunc(text, func(m string) string {
		g := money.FindStringSubmatch(m)
		reais, cents := g[1], g[2]
		unit := "reais"
		if reais == "1" {
			unit = "real"
		}
		if cents == "00" {
			return reais + " " + unit
		}
		return reais + " " + unit + " e " + strings.TrimLeft(cents, "0") + " centavos"
	})
	return strings.NewReplacer("*", "", "_", "").Replace(text)
}

// --- entrega ---

// BRT não tem horário de verão desde 2019.
var BRT = time.FixedZone("BRT", -3*3600)

const (
	quietFrom = 21 // a partir daqui o sistema não incomoda o cliente
	quietTo   = 8
)

// Quiet: entre 21h e 8h de Brasília não se manda relatório.
func Quiet(t time.Time) bool {
	h := t.In(BRT).Hour()
	return h >= quietFrom || h < quietTo
}

// Delivery junta o que decide se o áudio sai junto do texto.
type Delivery struct {
	AudioPref      bool // o cliente aceitou o áudio
	VoiceAvailable bool // há TTS configurado e a voz está ligada no painel
	Quiet          bool
	NeedsTemplates bool // WhatsApp oficial: janela de 24 h
	OutsideWindow  bool // cliente calado há mais de 23 h
}

// canSpeak: o canal aceita áudio agora (fora da janela de 24 h só entra modelo aprovado, sem voz).
func (d Delivery) canSpeak() bool {
	return d.VoiceAvailable && !d.Quiet && !(d.NeedsTemplates && d.OutsideWindow)
}

// Audio diz se o áudio vai junto do texto.
func (d Delivery) Audio() bool { return d.AudioPref && d.canSpeak() }

// OfferAudio diz se o texto leva o convite para o áudio (uma vez só: offered = já convidou).
// O convite só vai em texto livre, por isso exige a janela aberta.
func (d Delivery) OfferAudio(offered bool) bool {
	return !offered && !d.AudioPref && d.canSpeak()
}

// Offer é o convite para o áudio, no fim do relatório.
const Offer = "Quer receber esse resumo também em áudio? Responda *áudio*. Para voltar, *só texto*."

// PrefCommand é o pedido do cliente sobre o formato do relatório.
type PrefCommand int

const (
	PrefNone PrefCommand = iota
	PrefAudio
	PrefText
)

var (
	askAudio = regexp.MustCompile(`(?i)^\W*(sim\W+)?(quero\W+)?(o\W+)?(resumo\W+)?(em\W+)?[aá]udio\W*$`)
	askText  = regexp.MustCompile(`(?i)^\W*(quero\W+)?(o\W+)?(resumo\W+)?(s[oó]|apenas|somente)\W+(em\W+)?texto\W*$`)
)

// ParsePref lê "áudio" / "só texto" (mensagem curta e exata, para não confundir com conversa).
func ParsePref(text string) PrefCommand {
	if len(strings.Fields(text)) > 5 {
		return PrefNone
	}
	switch {
	case askText.MatchString(text):
		return PrefText
	case askAudio.MatchString(text):
		return PrefAudio
	}
	return PrefNone
}
