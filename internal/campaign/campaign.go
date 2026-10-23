// Package campaign cuida do que é determinístico na proposta: hash, código de aprovação e o
// resumo enviado ao cliente (gerado em Go para valores e código saírem exatos).
package campaign

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/copylab"
)

// ProposalHash identifica exatamente a versão da proposta que o cliente aprova.
func ProposalHash(p contract.CampaignProposal) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("campaign: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func NewApprovalCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("campaign: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// HashCode é o que fica no banco; o código em si só vai para o cliente.
func HashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

var approvalRe = regexp.MustCompile(`(?i)^\s*sim\s*[:\-]?\s*(\d{6})\s*[.!]?\s*$`)

// ParseApproval reconhece "SIM 123456" e variações próximas; qualquer outra coisa não é aprovação.
func ParseApproval(text string) (string, bool) {
	m := approvalRe.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func BRL(v float64) string {
	return "R$ " + strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1)
}

var objectiveLabels = map[string]string{
	"OUTCOME_ENGAGEMENT": "conversas no WhatsApp",
	"OUTCOME_LEADS":      "geração de contatos",
	"OUTCOME_TRAFFIC":    "visitas",
	"OUTCOME_SALES":      "vendas",
}

// SummaryOptions dá ao resumo o contexto do laboratório de copy.
type SummaryOptions struct {
	Segment  string // segmento/nicho do cliente (texto livre); ativa regras de conselhos profissionais
	Adjusted []int  // variações (base 0) que o copylab trocou por versão segura
}

func Summary(p contract.CampaignProposal, code string) string {
	return SummaryWith(p, code, SummaryOptions{})
}

// maxCopyNotes limita a seção "Atenção no texto" para o resumo continuar curto.
const maxCopyNotes = 3

// copyNotes monta a seção "Atenção no texto". Vazia quando não há nada a dizer. Só informa: quem
// decide é o cliente, e nada aqui impede a aprovação ou a publicação.
func copyNotes(p contract.CampaignProposal, o SummaryOptions) string {
	var lines []string
	for _, i := range o.Adjusted {
		lines = append(lines, fmt.Sprintf("- Anúncio %d: troquei trechos que a Meta costuma reprovar (promessa de resultado). Se preferir o seu texto original, é só pedir.", i+1))
	}
	total := 0
	for i, c := range p.Copies {
		for _, f := range copylab.Check(c.Headline, c.Text, o.Segment) {
			total++
			if len(lines) >= maxCopyNotes {
				continue
			}
			l := fmt.Sprintf("- Anúncio %d, \"%s\": %s", i+1, f.Excerpt, f.Reason)
			if f.Alternative != "" {
				l += " Sugestão: " + f.Alternative
			}
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if extra := total + len(o.Adjusted) - len(lines); extra > 0 {
		lines = append(lines, fmt.Sprintf("- Mais %d ponto(s) parecido(s) nos anúncios.", extra))
	}
	return "\n*Atenção no texto*\n" + strings.Join(lines, "\n") + "\nA decisão é sua: pode aprovar assim ou pedir o ajuste que quiser.\n"
}

// SummaryWith é o Summary com o aviso do laboratório de copy.
func SummaryWith(p contract.CampaignProposal, code string, o SummaryOptions) string {
	var b strings.Builder
	b.WriteString("*Proposta da sua campanha*\n\n")
	label := objectiveLabels[p.Objective]
	if label == "" {
		label = p.Objective
	}
	if p.LeadForm {
		label = "contatos por formulário (a pessoa preenche um formulário na própria Meta)"
	}
	if p.Followers {
		label = "seguidores no Instagram (o anúncio leva a pessoa ao seu perfil)"
	}
	if l := contract.GoalLabel[p.Goal]; l != "" {
		label = l
	}
	fmt.Fprintf(&b, "Objetivo: %s\n", label)
	if p.Location != "" {
		fmt.Fprintf(&b, "Onde: %s\n", p.Location)
		fmt.Fprintf(&b, "Público: %d a %d anos, %s\n", p.Audience.AgeMin, p.Audience.AgeMax, genders(p.Audience.Genders))
	} else {
		area := fmt.Sprintf("raio de %d km", p.Audience.RadiusKM)
		if p.Audience.RadiusKM == 0 {
			area = "somente a cidade (sem raio)"
		}
		fmt.Fprintf(&b, "Público: %d a %d anos, %s, %s\n", p.Audience.AgeMin, p.Audience.AgeMax, genders(p.Audience.Genders), area)
	}
	if len(p.Audience.Interests) > 0 {
		fmt.Fprintf(&b, "Interesses da Meta: %s\n", strings.Join(p.Audience.Interests, ", "))
	}
	where := PlacementsText(p.Placements)
	if p.Followers || p.Goal == contract.GoalDirect {
		where = "só no Instagram (o perfil é no Instagram)"
	}
	fmt.Fprintf(&b, "Onde aparece: %s\n", where)
	fmt.Fprintf(&b, "Investimento: %s por dia, por %d dias (total de até %s)\n",
		BRL(p.DailyBudgetBRL), p.DurationDays, BRL(p.DailyBudgetBRL*float64(p.DurationDays)))
	for i, c := range p.Copies {
		label := ""
		if a := copylab.AngleLabel[c.Angle]; a != "" && len(p.Copies) > 1 {
			label = " (foco em " + a + ")"
		}
		fmt.Fprintf(&b, "\nAnúncio %d%s: *%s*\n%s\n", i+1, label, c.Headline, c.Text)
	}
	if p.LeadForm {
		b.WriteString(LeadFormText(p.Form))
	}
	b.WriteString(copyNotes(p, o))
	fmt.Fprintf(&b, "\nPor quê: %s\n", p.Rationale)
	fmt.Fprintf(&b, "\nPara aprovar, responda: *SIM %s*\n(código válido por 30 minutos)\nPara mudar algo, é só me dizer o quê.", code)
	return b.String()
}

// PlacementsText diz em português onde o anúncio aparece. Vazio = automático.
func PlacementsText(pl []string) string {
	pl = contract.NormalizePlacements(pl)
	if len(pl) == 0 {
		return "automático (a Meta distribui entre Feed, Stories, Reels e outros lugares)"
	}
	names := map[string]string{contract.PlacementFeed: "no Feed", contract.PlacementStories: "nos Stories", contract.PlacementReels: "nos Reels"}
	parts := make([]string, len(pl))
	for i, p := range pl {
		parts[i] = names[p]
	}
	list := parts[0]
	if n := len(parts); n > 1 {
		list = strings.Join(parts[:n-1], ", ") + " e " + parts[n-1]
	}
	return "só " + list + " (Facebook e Instagram)"
}

func genders(g []string) string {
	if len(g) == 1 {
		switch g[0] {
		case "female":
			return "mulheres"
		case "male":
			return "homens"
		}
	}
	return "todos os gêneros"
}

// LeadFormText descreve o formulário no resumo da proposta, para o cliente conferir antes de aprovar.
func LeadFormText(f *contract.LeadForm) string {
	n := contract.LeadForm{}.Normalize()
	if f != nil {
		n = f.Normalize()
	}
	var b strings.Builder
	b.WriteString("\n*Formulário*\n")
	if n.Kind == "qualified" {
		b.WriteString("Tipo: mais qualificado (a pessoa confere os dados antes de enviar)\n")
	} else {
		b.WriteString("Tipo: mais cadastros (preenchimento rápido)\n")
	}
	if n.HasIntro() {
		intro := n.Intro
		if len(n.Bullets) > 0 {
			intro = strings.Join(n.Bullets, " • ")
		}
		b.WriteString("Abertura:")
		if n.Headline != "" {
			fmt.Fprintf(&b, " *%s*", n.Headline)
		}
		if intro != "" && intro != n.Headline {
			b.WriteString(" " + intro)
		}
		if n.IntroButton != "" {
			fmt.Fprintf(&b, " [%s]", n.IntroButton)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("Abertura: sem tela de abertura\n")
	}
	if n.QuestionsHeadline != "" {
		fmt.Fprintf(&b, "Título das perguntas: %s\n", n.QuestionsHeadline)
	}
	b.WriteString("Perguntas:\n")
	for i, q := range n.Questions {
		fmt.Fprintf(&b, "%d. %s\n", i+1, contract.LeadQuestionText(q))
	}
	if n.HasThanks() {
		title := n.ThanksTitle
		if title == "" {
			title = "Obrigado!"
		}
		fmt.Fprintf(&b, "Tela final: *%s*", title)
		if n.ThanksText != "" {
			b.WriteString(" " + n.ThanksText)
		}
		b.WriteString("\n")
		switch n.ButtonAction {
		case "none":
			b.WriteString("Botão: sem botão\n")
		case "whatsapp":
			fmt.Fprintf(&b, "Botão: abre o WhatsApp %s\n", n.ButtonPhone)
		case "call":
			fmt.Fprintf(&b, "Botão: liga para %s\n", n.ButtonPhone)
		default:
			dest := n.ButtonURL
			if dest == "" {
				dest = "o destino do anúncio"
			}
			fmt.Fprintf(&b, "Botão: abre %s\n", dest)
		}
	} else {
		b.WriteString("Tela final: padrão da Meta (agradecimento)\n")
	}
	if n.PrivacyURL != "" {
		fmt.Fprintf(&b, "Política de privacidade: %s\n", n.PrivacyURL)
	}
	b.WriteString("Pode mudar qualquer parte: perguntas, opções, textos e botão.\n")
	return b.String()
}
