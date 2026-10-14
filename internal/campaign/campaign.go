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

func Summary(p contract.CampaignProposal, code string) string {
	var b strings.Builder
	b.WriteString("*Proposta da sua campanha*\n\n")
	label := objectiveLabels[p.Objective]
	if label == "" {
		label = p.Objective
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
		fmt.Fprintf(&b, "\nAnúncio %d: *%s*\n%s\n", i+1, c.Headline, c.Text)
	}
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
