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
		label = "contatos por formulário (a pessoa preenche nome, telefone e e-mail na própria Meta)"
	}
	fmt.Fprintf(&b, "Objetivo: %s\n", label)
	area := fmt.Sprintf("raio de %d km", p.Audience.RadiusKM)
	if p.Audience.RadiusKM == 0 {
		area = "somente a cidade (sem raio)"
	}
	fmt.Fprintf(&b, "Público: %d a %d anos, %s, %s\n", p.Audience.AgeMin, p.Audience.AgeMax, genders(p.Audience.Genders), area)
	if len(p.Audience.Interests) > 0 {
		fmt.Fprintf(&b, "Interesses: %s\n", strings.Join(p.Audience.Interests, ", "))
	}
	fmt.Fprintf(&b, "Onde aparece: %s\n", PlacementsText(p.Placements))
	fmt.Fprintf(&b, "Investimento: %s por dia, por %d dias (total de até %s)\n",
		BRL(p.DailyBudgetBRL), p.DurationDays, BRL(p.DailyBudgetBRL*float64(p.DurationDays)))
	for i, c := range p.Copies {
		label := ""
		if a := copylab.AngleLabel[c.Angle]; a != "" && len(p.Copies) > 1 {
			label = " (foco em " + a + ")"
		}
		fmt.Fprintf(&b, "\nAnúncio %d%s: *%s*\n%s\n", i+1, label, c.Headline, c.Text)
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
