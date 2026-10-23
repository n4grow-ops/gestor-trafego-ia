// Package contract define as mensagens trocadas entre o Atendente, o Gestor e o domínio.
// As duas IAs só se falam por estes tipos.
package contract

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
	// IntentChangeLive: mudança numa campanha que já está na Meta (pausada ou no ar).
	IntentChangeLive Intent = "change_live"
)

func (i Intent) Valid() bool {
	switch i {
	case IntentCreateCampaign, IntentProvideInfo, IntentApprove, IntentReject, IntentQuestion, IntentHuman, IntentOther,
		IntentNewProject, IntentSwitchProject, IntentListProjects, IntentProjectStatus, IntentConnectMeta, IntentBilling, IntentChangeLive:
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
	// LinkURL: link do site para onde o anúncio leva (campanha para o site, ou destino do formulário).
	LinkURL *string  `json:"link_url,omitempty"`
	Media          []string `json:"media,omitempty"`
	// Placements: onde o anúncio aparece, só quando o cliente pediu ("feed", "stories", "reels").
	// Vazio = automático (a Meta distribui).
	Placements []string `json:"placements,omitempty"`
	// LeadForm: formulário instantâneo que o cliente montou (perguntas, abertura, tela final). Nil = não falou.
	LeadForm *LeadForm `json:"lead_form,omitempty"`
	// Copies: textos do anúncio que o cliente escreveu ou escolheu na conversa com a Mia. Valem como vieram.
	Copies []Copy `json:"copies,omitempty"`
	// Interests: interesses da Meta que o cliente aceitou ("Empreendedorismo, Pequenas empresas") ou
	// "nenhum" quando ele não quer interesses. Nil = ainda não respondeu.
	Interests *string `json:"interests,omitempty"`
	// DurationDays: quantos dias o anúncio fica ativo, como o cliente disse.
	DurationDays *float64 `json:"duration_days,omitempty"`
	// AdsCount: quantos anúncios (variações de texto) o cliente quer, de 1 a 5.
	AdsCount *float64 `json:"ads_count,omitempty"`
}

// PlacementAuto marca em Fields.Placements que o cliente escolheu "automático" (a Meta distribui).
const PlacementAuto = "automatico"

// NoInterests diz se a resposta sobre interesses é "não quero interesses".
func NoInterests(s string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(s), ".!")) {
	case "", "nenhum", "nenhuma", "não", "nao", "sem", "sem interesses", "sem interesse", "não quero", "nao quero":
		return true
	}
	return false
}

var interestSep = regexp.MustCompile(`\s*(?:[,;
]|\s+e\s+)\s*`)

// InterestList separa os interesses aceitos pelo cliente; vazio quando ele não quer interesses.
func InterestList(s string) []string {
	if NoInterests(s) {
		return nil
	}
	var out []string
	for _, p := range regexp.MustCompile(`\s*(?:[,;\n]|\s+e\s+)\s*`).Split(s, -1) {
		if p = strings.Trim(strings.TrimSpace(p), ".-•*"); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// wantsAutoPlacement diz se a lista traz a escolha "automático".
func wantsAutoPlacement(pl []string) bool {
	for _, p := range pl {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case PlacementAuto, "automático", "auto", "todos", "tudo":
			return true
		}
	}
	return false
}

// Posicionamentos que o cliente pode escolher.
const (
	PlacementFeed    = "feed"
	PlacementStories = "stories"
	PlacementReels   = "reels"
)

var placementOrder = []string{PlacementFeed, PlacementStories, PlacementReels}

// WantsLeadForm diz se o cliente pediu campanha de formulário (formulário instantâneo da Meta).
func WantsLeadForm(hint string) bool {
	h := strings.ToLower(hint)
	for _, w := range []string{"formul", "lead form", "leadform", "cadastro"} {
		if strings.Contains(h, w) {
			return true
		}
	}
	return false
}

// WantsFollowers diz se o cliente pediu campanha para ganhar seguidores (visitas ao perfil do Instagram).
func WantsFollowers(hint string) bool {
	h := strings.ToLower(hint)
	for _, w := range []string{"seguidor", "seguir", "follower", "perfil do instagram", "perfil no instagram", "visitas ao perfil"} {
		if strings.Contains(h, w) {
			return true
		}
	}
	return false
}

// radiusRe acha o raio pedido no texto do local ("raio de 15 km", "+ 15km", "15 quilômetros").
var radiusRe = regexp.MustCompile(`(?i)(\d{1,3})\s*(km|quil[oô]metros?)\b`)

// RadiusFromLocation lê o raio em km escrito no local da campanha; 0 = não pediu raio.
func RadiusFromLocation(loc string) int {
	m := radiusRe.FindStringSubmatch(loc)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

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
	if !blank(o.LinkURL) {
		out.LinkURL = o.LinkURL
	}
	if positive(o.DailyBudgetBRL) {
		out.DailyBudgetBRL = o.DailyBudgetBRL
	}
	if len(o.Media) > 0 {
		out.Media = append(slices.Clone(f.Media), o.Media...)
	}
	if pl := NormalizePlacements(o.Placements); len(pl) > 0 {
		out.Placements = pl
	} else if wantsAutoPlacement(o.Placements) {
		out.Placements = []string{PlacementAuto}
	}
	if !blank(o.Interests) {
		out.Interests = o.Interests
	}
	if positive(o.DurationDays) {
		out.DurationDays = o.DurationDays
	}
	if positive(o.AdsCount) {
		out.AdsCount = o.AdsCount
	}
	out.LeadForm = MergeLeadForm(f.LeadForm, o.LeadForm)
	if cs := CleanCopies(o.Copies); len(cs) > 0 {
		out.Copies = cs // lista completa como deve ficar
	}
	return out
}

// HasData diz se algum campo foi de fato informado.
func (f Fields) HasData() bool {
	return !blank(f.Offer) || !blank(f.Audience) || !blank(f.Location) || !blank(f.ObjectiveHint) || !blank(f.LinkURL) ||
		positive(f.DailyBudgetBRL) || len(f.Media) > 0 || len(NormalizePlacements(f.Placements)) > 0 || f.LeadForm != nil ||
		len(CleanCopies(f.Copies)) > 0 || wantsAutoPlacement(f.Placements) || !blank(f.Interests) ||
		positive(f.DurationDays) || positive(f.AdsCount)
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
	return append(m, f.GoalMissing()...)
}

// GoalMissing é o que o tipo de campanha pede a mais: campanha para o site precisa do link.
func (f Fields) GoalMissing() []string {
	if f.ObjectiveHint != nil && GoalFromHint(*f.ObjectiveHint) == GoalSite && blank(f.LinkURL) {
		return []string{"link_url"}
	}
	return nil
}

// AttendantTurn é a saída do Atendente a cada mensagem do cliente.
type AttendantTurn struct {
	Intent  Intent  `json:"intent"`
	Fields  Fields  `json:"fields"`
	Profile Profile `json:"profile"`
	Project string  `json:"project,omitempty"` // nome do projeto citado
	Reply   string  `json:"reply"`
}

// MaxReply é o tamanho máximo da resposta da Atendente.
const MaxReply = 2500

func (t AttendantTurn) Validate() error {
	if !t.Intent.Valid() {
		return fmt.Errorf("intent %q desconhecida", t.Intent)
	}
	if strings.TrimSpace(t.Reply) == "" {
		return errors.New("reply vazio")
	}
	// Cabe até 3 opções de texto de anúncio inteiras (o WhatsApp aceita 4096).
	if utf8.RuneCountInString(t.Reply) > MaxReply {
		return fmt.Errorf("reply com mais de %d caracteres", MaxReply)
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
	LinkURL        string      `json:"link_url,omitempty"`
	Media          []string    `json:"media,omitempty"`
	Placements     []string    `json:"placements,omitempty"` // pedido do cliente; vazio = automático
	LeadForm       *LeadForm   `json:"lead_form,omitempty"`  // formulário que o cliente montou; nil = não falou
	// ClientCopies: textos que o cliente fechou na conversa; o Gestor usa exatamente estes.
	ClientCopies []Copy `json:"client_copies,omitempty"`
	Constraints    Constraints `json:"constraints"`
	Adjustment     string      `json:"adjustment,omitempty"`
	// CopyVariations: quantas variações de texto o cliente pediu. 0 = não pediu (o Gestor gera 1).
	CopyVariations int `json:"copy_variations,omitempty"`
	// DurationDays: dias que o cliente pediu. 0 = não disse (o Gestor usa 7).
	DurationDays int `json:"duration_days,omitempty"`
	// Interests: interesses que o cliente aceitou (o Gestor usa exatamente estes). NoInterests: ele não quer
	// interesses. Os dois vazios = não respondeu (o Gestor traduz o público).
	Interests   []string `json:"interests,omitempty"`
	NoInterests bool     `json:"no_interests,omitempty"`
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
	if f.LinkURL != nil {
		b.LinkURL = strings.TrimSpace(*f.LinkURL)
	}
	if f.LeadForm != nil {
		lf := f.LeadForm.Normalize()
		b.LeadForm = &lf
	}
	b.ClientCopies = CleanCopies(f.Copies)
	if f.DurationDays != nil && *f.DurationDays >= 1 {
		b.DurationDays = min(int(*f.DurationDays), 90)
	}
	if f.AdsCount != nil && *f.AdsCount >= 1 {
		b.CopyVariations = min(int(*f.AdsCount), MaxCopies)
	}
	if f.Interests != nil {
		b.Interests = InterestList(*f.Interests)
		b.NoInterests = len(b.Interests) == 0
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
	// LeadForm: o cliente pediu formulário (o sistema copia do briefing; objetivo vira OUTCOME_LEADS).
	LeadForm bool `json:"lead_form,omitempty"`
	// Followers: o cliente pediu seguidores (o sistema copia do briefing; objetivo vira OUTCOME_TRAFFIC
	// com destino no perfil do Instagram).
	Followers bool `json:"followers,omitempty"`
	// Goal: tipo de campanha que o cliente pediu (GoalWhatsApp, GoalSite...). Vazio = proposta antiga.
	Goal string `json:"goal,omitempty"`
	// Location: onde o anúncio aparece, já em português ("Maravilha + 17 km; Chapecó (só a cidade)").
	Location string `json:"location,omitempty"`
	// Form: como o formulário fica (copiado do briefing, já normalizado). Nil com LeadForm = padrão.
	Form *LeadForm `json:"form,omitempty"`
}

// MinRadiusKM é o menor raio que a Meta aceita em volta de cidade. Raio 0 = só a cidade.
const MinRadiusKM = 17

// Normalize ajusta o que a Meta não aceita do jeito mais fiel ao pedido: raio menor que o mínimo vira
// o mínimo (caso real: "Maravilha + 15 km" saía só a cidade, sem raio nenhum).
func (p *CampaignProposal) Normalize() {
	if p.Audience.RadiusKM > 0 && p.Audience.RadiusKM < MinRadiusKM {
		p.Audience.RadiusKM = MinRadiusKM
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
