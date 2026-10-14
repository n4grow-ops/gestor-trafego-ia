// Package contract define as mensagens trocadas entre o Atendente, o Gestor e o domínio.
// As duas IAs só se falam por estes tipos.
package contract

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

type Intent string

const (
	IntentCreateCampaign Intent = "create_campaign"
	IntentProvideInfo    Intent = "provide_info"
	IntentApprove        Intent = "approve"
	IntentReject         Intent = "reject"
	IntentQuestion       Intent = "question"
	IntentHuman          Intent = "human"
	IntentOther          Intent = "other"
	IntentNewProject     Intent = "new_project"
	IntentSwitchProject  Intent = "switch_project"
	IntentListProjects   Intent = "list_projects"
	IntentProjectStatus  Intent = "project_status"
	IntentConnectMeta    Intent = "connect_meta"
	IntentBilling        Intent = "billing"
)

func (i Intent) Valid() bool {
	switch i {
	case IntentCreateCampaign, IntentProvideInfo, IntentApprove, IntentReject, IntentQuestion, IntentHuman, IntentOther,
		IntentNewProject, IntentSwitchProject, IntentListProjects, IntentProjectStatus, IntentConnectMeta, IntentBilling:
		return true
	}
	return false
}

// Fields são os dados da campanha coletados na conversa. Ponteiro nil = não informado.
type Fields struct {
	Offer          *string  `json:"offer,omitempty"`
	Audience       *string  `json:"audience,omitempty"`
	Location       *string  `json:"location,omitempty"`
	DailyBudgetBRL *float64 `json:"daily_budget_brl,omitempty"`
	ObjectiveHint  *string  `json:"objective_hint,omitempty"`
	Media          []string `json:"media,omitempty"`
	// Placements: onde o anúncio aparece, só quando o cliente pediu ("feed", "stories", "reels").
	// Vazio = automático (a Meta distribui).
	Placements []string `json:"placements,omitempty"`
}

// Posicionamentos que o cliente pode escolher.
const (
	PlacementFeed    = "feed"
	PlacementStories = "stories"
	PlacementReels   = "reels"
)

var placementOrder = []string{PlacementFeed, PlacementStories, PlacementReels}

// NormalizePlacements deixa só os posicionamentos conhecidos, sem repetição e em ordem fixa.
// Aceita singular e caixa diferente ("Story", "reel"). Nenhum conhecido = nil (automático).
func NormalizePlacements(in []string) []string {
	seen := map[string]bool{}
	for _, p := range in {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "feed":
			seen[PlacementFeed] = true
		case "stories", "story":
			seen[PlacementStories] = true
		case "reels", "reel":
			seen[PlacementReels] = true
		}
	}
	var out []string
	for _, p := range placementOrder {
		if seen[p] {
			out = append(out, p)
		}
	}
	return out
}

func Str(s string) *string   { return &s }
func Num(v float64) *float64 { return &v }

func blank(s *string) bool { return s == nil || strings.TrimSpace(*s) == "" }

func positive(v *float64) bool { return v != nil && *v > 0 }

// Merge devolve f com os campos informados em o por cima.
// Texto em branco e orçamento <= 0 não apagam o que já foi coletado.
func (f Fields) Merge(o Fields) Fields {
	out := f
	if !blank(o.Offer) {
		out.Offer = o.Offer
	}
	if !blank(o.Audience) {
		out.Audience = o.Audience
	}
	if !blank(o.Location) {
		out.Location = o.Location
	}
	if !blank(o.ObjectiveHint) {
		out.ObjectiveHint = o.ObjectiveHint
	}
	if positive(o.DailyBudgetBRL) {
		out.DailyBudgetBRL = o.DailyBudgetBRL
	}
	if len(o.Media) > 0 {
		out.Media = append(slices.Clone(f.Media), o.Media...)
	}
	if pl := NormalizePlacements(o.Placements); len(pl) > 0 {
		out.Placements = pl
	}
	return out
}

// HasData diz se algum campo foi de fato informado.
func (f Fields) HasData() bool {
	return !blank(f.Offer) || !blank(f.Audience) || !blank(f.Location) || !blank(f.ObjectiveHint) ||
		positive(f.DailyBudgetBRL) || len(f.Media) > 0 || len(NormalizePlacements(f.Placements)) > 0
}

// Missing lista os campos obrigatórios que faltam, em ordem fixa.
func (f Fields) Missing() []string {
	var m []string
	if blank(f.Offer) {
		m = append(m, "offer")
	}
	if blank(f.Audience) {
		m = append(m, "audience")
	}
	if blank(f.Location) {
		m = append(m, "location")
	}
	if !positive(f.DailyBudgetBRL) {
		m = append(m, "daily_budget_brl")
	}
	return m
}

// AttendantTurn é a saída do Atendente a cada mensagem do cliente.
type AttendantTurn struct {
	Intent  Intent  `json:"intent"`
	Fields  Fields  `json:"fields"`
	Profile Profile `json:"profile"`
	Project string  `json:"project,omitempty"` // nome do projeto citado
	Reply   string  `json:"reply"`
}

func (t AttendantTurn) Validate() error {
	if !t.Intent.Valid() {
		return fmt.Errorf("intent %q desconhecida", t.Intent)
	}
	if strings.TrimSpace(t.Reply) == "" {
		return errors.New("reply vazio")
	}
	if utf8.RuneCountInString(t.Reply) > 1000 {
		return errors.New("reply com mais de 1000 caracteres")
	}
	if utf8.RuneCountInString(t.Project) > 60 {
		return errors.New("project com mais de 60 caracteres")
	}
	return nil
}

type Constraints struct {
	MaxDailyBudgetBRL float64  `json:"max_daily_budget_brl"`
	AllowedObjectives []string `json:"allowed_objectives"`
	Niche             string   `json:"niche"`
}

// Briefing é o que o Gestor recebe: dados validados, nunca o texto cru da conversa.
type Briefing struct {
	OrganizationID int64       `json:"organization_id"`
	Offer          string      `json:"offer"`
	Audience       string      `json:"audience"`
	Location       string      `json:"location"`
	DailyBudgetBRL float64     `json:"daily_budget_brl"`
	ObjectiveHint  string      `json:"objective_hint,omitempty"`
	Media          []string    `json:"media,omitempty"`
	Placements     []string    `json:"placements,omitempty"` // pedido do cliente; vazio = automático
	Constraints    Constraints `json:"constraints"`
	Adjustment     string      `json:"adjustment,omitempty"`
	// CopyVariations: quantas variações de texto o cliente pediu. 0 = não pediu (o Gestor gera 1).
	CopyVariations int `json:"copy_variations,omitempty"`
	Business       Profile     `json:"business,omitempty"` // memória do negócio
}

func NewBriefing(orgID int64, f Fields, c Constraints) (Briefing, error) {
	if m := f.Missing(); len(m) > 0 {
		return Briefing{}, fmt.Errorf("briefing incompleto: faltam %s", strings.Join(m, ", "))
	}
	b := Briefing{
		OrganizationID: orgID,
		Offer:          *f.Offer,
		Audience:       *f.Audience,
		Location:       *f.Location,
		DailyBudgetBRL: *f.DailyBudgetBRL,
		Media:          f.Media,
		Placements:     NormalizePlacements(f.Placements),
		Constraints:    c,
	}
	if f.ObjectiveHint != nil {
		b.ObjectiveHint = *f.ObjectiveHint
	}
	return b, nil
}

type Audience struct {
	AgeMin    int      `json:"age_min"`
	AgeMax    int      `json:"age_max"`
	Genders   []string `json:"genders"`
	Interests []string `json:"interests"`
	RadiusKM  int      `json:"radius_km"`
}

type Copy struct {
	Headline string `json:"headline"`
	Text     string `json:"text"`
	CTA      string `json:"cta"`
	// Angle: ângulo da variação (preco, urgencia, prova_social, dor, autoridade, novidade). Opcional.
	Angle string `json:"angle,omitempty"`
}

// CampaignProposal é a saída do Gestor.
type CampaignProposal struct {
	Objective      string   `json:"objective"`
	Audience       Audience `json:"audience"`
	DailyBudgetBRL float64  `json:"daily_budget_brl"`
	DurationDays   int      `json:"duration_days"`
	Copies         []Copy   `json:"copies"`
	Rationale      string   `json:"rationale"`
	Risks          []string `json:"risks"`
	// Placements vem do pedido do cliente (o sistema copia do briefing; o Gestor não escolhe). Vazio = automático.
	Placements []string `json:"placements,omitempty"`
}

// MinRadiusKM é o menor raio que a Meta aceita em volta de cidade. Raio 0 = só a cidade.
const MinRadiusKM = 17

// Normalize ajusta o que a Meta não aceita do jeito mais fiel ao pedido: raio menor que o mínimo vira
// só a cidade (um raio pequeno fica mais perto da cidade do que de 17 km em volta).
func (p *CampaignProposal) Normalize() {
	if p.Audience.RadiusKM > 0 && p.Audience.RadiusKM < MinRadiusKM {
		p.Audience.RadiusKM = 0
	}
}

// MaxCopies é o teto de variações de texto numa proposta (o cliente escolhe quantas, de 1 até aqui).
const MaxCopies = 5

var allowedCTAs = []string{"WHATSAPP_MESSAGE", "LEARN_MORE", "CONTACT_US"}

// Validate confere estrutura e regras de negócio contra o briefing. Devolve todos os erros juntos,
// para o Gestor corrigir tudo numa tentativa.
func (p CampaignProposal) Validate(b Briefing) error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if !slices.Contains(b.Constraints.AllowedObjectives, p.Objective) {
		add("objective %q não permitido (use um de: %s)", p.Objective, strings.Join(b.Constraints.AllowedObjectives, ", "))
	} else if h := strings.TrimSpace(b.ObjectiveHint); slices.Contains(b.Constraints.AllowedObjectives, h) && p.Objective != h {
		// O cliente já definiu o objetivo (valor exato da Meta): ele prevalece.
		add("objective deve ser %q, o objetivo que o cliente definiu", h)
	}
	switch {
	case p.DailyBudgetBRL <= 0:
		add("daily_budget_brl deve ser maior que zero")
	case p.DailyBudgetBRL > b.DailyBudgetBRL:
		add("daily_budget_brl %.2f acima do orçamento do cliente (%.2f)", p.DailyBudgetBRL, b.DailyBudgetBRL)
	case p.DailyBudgetBRL > b.Constraints.MaxDailyBudgetBRL:
		add("daily_budget_brl %.2f acima do limite da conta (%.2f)", p.DailyBudgetBRL, b.Constraints.MaxDailyBudgetBRL)
	}
	if p.DurationDays < 1 || p.DurationDays > 90 {
		add("duration_days deve estar entre 1 e 90")
	}
	a := p.Audience
	if a.AgeMin < 18 || a.AgeMax > 65 || a.AgeMin > a.AgeMax {
		add("idade deve estar entre 18 e 65, com age_min <= age_max")
	}
	for _, g := range a.Genders {
		if g != "male" && g != "female" {
			add("gender %q inválido (use male ou female)", g)
		}
	}
	if a.RadiusKM != 0 && (a.RadiusKM < MinRadiusKM || a.RadiusKM > 80) {
		add("radius_km deve ser 0 (só a cidade) ou entre %d e 80", MinRadiusKM)
	}
	if len(p.Copies) < 1 || len(p.Copies) > MaxCopies {
		add("copies deve ter de 1 a %d itens", MaxCopies)
	} else if n := b.CopyVariations; n > 0 && n <= MaxCopies && len(p.Copies) != n {
		add("copies deve ter exatamente %d itens, como o cliente pediu", n)
	}
	for i, c := range p.Copies {
		if n := utf8.RuneCountInString(strings.TrimSpace(c.Headline)); n < 1 || n > 40 {
			add("copies[%d].headline deve ter de 1 a 40 caracteres", i)
		}
		if n := utf8.RuneCountInString(strings.TrimSpace(c.Text)); n < 1 || n > 500 {
			add("copies[%d].text deve ter de 1 a 500 caracteres", i)
		}
		if !slices.Contains(allowedCTAs, c.CTA) {
			add("copies[%d].cta %q inválido (use um de: %s)", i, c.CTA, strings.Join(allowedCTAs, ", "))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}
