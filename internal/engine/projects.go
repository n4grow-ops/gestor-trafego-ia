package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/campaign"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/mia"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// O limite diário conta pelo dia de São Paulo (sem horário de verão desde 2019).
var saoPaulo = time.FixedZone("BRT", -3*60*60)

func startOfDay(t time.Time) time.Time {
	t = t.In(saoPaulo)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, saoPaulo)
}

var fieldLabels = map[string]string{
	"offer":            "o que anunciar",
	"audience":         "o público",
	"location":         "a cidade ou região",
	"daily_budget_brl": "o orçamento por dia",
	"link_url":         "o link do site",
	"objective_hint":   "o objetivo",
	"interests":        "os interesses",
	"duration_days":    "quantos dias fica ativo",
	"placements":       "onde aparece",
	"ads_count":        "quantos anúncios",
}

var stageLabels = map[string]string{
	store.StageCollecting:        "coletando dados",
	store.StageAwaitingProposal:  "proposta em preparo",
	store.StageAwaitingApproval:  "aguardando sua aprovação",
	store.StageApproved:          "aprovado",
	store.StageAwaitingCreatives: "aguardando os anúncios (imagens ou vídeos)",
	store.StageReady:             "pronto para publicar na Meta",
	store.StageArchived:          "arquivado",
}

// missingText lista o que falta para a proposta, com os campos obrigatórios configurados na Mia.
func missingText(cfg mia.Config, f contract.Fields) string {
	var parts []string
	for _, m := range cfg.CampaignMissing(f) {
		l, ok := fieldLabels[m]
		if !ok {
			l = cfg.Label(m)
		}
		parts = append(parts, l)
	}
	return strings.Join(parts, ", ")
}

// withDefaults completa os campos com a memória do negócio, sem sobrescrever o que já existe. O público da
// ficha só vale para o produto principal: campanha de outro produto (ex.: imóvel numa consultoria) pede o
// público dela.
func withDefaults(p contract.Profile, f contract.Fields) contract.Fields {
	// A região e o público não vêm da ficha: cada campanha confirma os seus na conversa (a Mia pergunta,
	// e pode sugerir o cliente ideal da ficha).
	d := contract.Fields{Offer: p.Offer}
	return d.Merge(f)
}

// campaignFields junta o que o cliente disse nesta mensagem aos campos da campanha. Se ele trocou para
// outro produto sem dizer o público, o público herdado da ficha sai.
func campaignFields(p contract.Profile, base, turn contract.Fields) contract.Fields {
	if turn.Offer != nil && !mainOffer(p, *turn.Offer) && turn.Audience == nil &&
		base.Audience != nil && p.Audience != nil && *base.Audience == *p.Audience {
		base.Audience = nil
	}
	return withDefaults(p, base.Merge(turn))
}

// mainOffer diz se a oferta da campanha é o que a empresa vende (a da ficha). Sem oferta na ficha, vale.
func mainOffer(p contract.Profile, offer string) bool {
	if p.Offer == nil || strings.TrimSpace(*p.Offer) == "" || strings.TrimSpace(offer) == "" {
		return true
	}
	a, b := strings.ToLower(strings.TrimSpace(*p.Offer)), strings.ToLower(strings.TrimSpace(offer))
	return a == b || strings.Contains(a, b) || strings.Contains(b, a)
}

// campaignBusiness é a ficha que vai ao especialista. Para outro produto, só a identidade da empresa: o
// público, a idade, os diferenciais e o ticket da ficha são do produto principal.
func campaignBusiness(p contract.Profile, offer string) contract.Profile {
	if mainOffer(p, offer) {
		return p
	}
	return contract.Profile{BusinessName: p.BusinessName, City: p.City, SalesChannel: p.SalesChannel}
}

func (e *Engine) projectNames(ctx context.Context, orgID int64) ([]store.Project, []string, error) {
	projects, err := e.Store.ListProjects(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	return projects, names, nil
}

func nextProjectName(projects []store.Project) string {
	taken := map[string]bool{}
	for _, p := range projects {
		taken[strings.ToLower(p.Name)] = true
	}
	for i := len(projects) + 1; ; i++ {
		name := fmt.Sprintf("Campanha %d", i)
		if !taken[strings.ToLower(name)] {
			return name
		}
	}
}

// ensureProject garante um projeto ativo antes de coletar dados de campanha.
func (e *Engine) ensureProject(ctx context.Context, ident store.Identity, conv *store.Conversation, name string, projects []store.Project) error {
	if conv.ActiveProjectID != nil {
		return nil
	}
	if strings.TrimSpace(name) == "" {
		name = nextProjectName(projects)
	}
	p, _, err := e.Store.CreateOrReopenProject(ctx, ident.OrgID, name, contract.Fields{})
	if err != nil {
		return err
	}
	if err := e.Store.ActivateProject(ctx, conv.ID, p.ID, conv.State, conv.Fields); err != nil {
		return err
	}
	conv.ActiveProjectID = &p.ID
	return nil
}

// leaveProject prepara a troca: proposta aguardando aprovação é cancelada e o projeto volta a coletar.
func (e *Engine) leaveProject(ctx context.Context, ident store.Identity, conv store.Conversation) error {
	if conv.State != store.StateAwaitingApproval || conv.ActiveProjectID == nil {
		return nil
	}
	if err := e.closeOpenDraft(ctx, ident, conv, "cancelled"); err != nil {
		return err
	}
	return e.Store.SetProjectStage(ctx, *conv.ActiveProjectID, store.StageCollecting)
}

// startProject cria (ou reabre) um projeto e passa a trabalhar nele.
func (e *Engine) startProject(ctx context.Context, ident store.Identity, conv store.Conversation, turn contract.AttendantTurn, projects []store.Project) error {
	if err := e.leaveProject(ctx, ident, conv); err != nil {
		return err
	}
	name := strings.TrimSpace(turn.Project)
	if name == "" {
		name = nextProjectName(projects)
	}
	p, created, err := e.Store.CreateOrReopenProject(ctx, ident.OrgID, name, contract.Fields{})
	if max, ok := projectLimit(err); ok {
		return e.askArchive(ctx, ident, conv, max, strings.TrimSpace(turn.Project), turn.Fields)
	}
	if err != nil {
		return err
	}
	fields := campaignFields(ident.Profile, p.Fields, turn.Fields)
	if err := e.Store.ActivateProject(ctx, conv.ID, p.ID, store.StateCollecting, fields); err != nil {
		return err
	}
	conv.ActiveProjectID, conv.State, conv.Fields = &p.ID, store.StateCollecting, fields
	noteAction(ctx, "abriu o projeto "+p.Name)
	e.audit(ctx, ident.OrgID, member(ident), "project_started", map[string]any{"project_id": p.ID, "created": created})
	if len(e.miaConfig(ctx).CampaignMissing(fields)) == 0 {
		return e.requestProposal(ctx, ident, conv, fields, "", fmt.Sprintf(msgProjectReady, p.Name), true)
	}
	if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateCollecting, fields); err != nil {
		return err
	}
	verb := "Retomei"
	if created {
		verb = "Criei"
	}
	return e.send(ctx, conv, fmt.Sprintf(msgProjectStarted, verb, p.Name, missingText(e.miaConfig(ctx), fields)))
}

// switchProject retoma um projeto existente escolhido pelo nome.
func (e *Engine) switchProject(ctx context.Context, ident store.Identity, conv store.Conversation, turn contract.AttendantTurn, projects []store.Project) error {
	found, err := e.Store.FindProjects(ctx, ident.OrgID, turn.Project)
	if err != nil {
		return err
	}
	switch len(found) {
	case 0:
		return e.send(ctx, conv, fmt.Sprintf(msgProjectNotFound, turn.Project)+"\n\n"+listText(e.miaConfig(ctx), projects))
	case 1:
	default:
		var names []string
		for _, p := range found {
			names = append(names, "*"+p.Name+"*")
		}
		return e.send(ctx, conv, fmt.Sprintf(msgProjectAmbiguous, strings.Join(names, ", ")))
	}
	target := found[0]
	if conv.ActiveProjectID != nil && *conv.ActiveProjectID == target.ID && conv.State != store.StateIdle {
		return e.send(ctx, conv, fmt.Sprintf(msgProjectAlreadyActive, target.Name))
	}
	if err := e.leaveProject(ctx, ident, conv); err != nil {
		return err
	}
	fields := campaignFields(ident.Profile, target.Fields, turn.Fields)
	if err := e.Store.ActivateProject(ctx, conv.ID, target.ID, store.StateCollecting, fields); err != nil {
		return err
	}
	if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateCollecting, fields); err != nil {
		return err
	}
	conv.ActiveProjectID, conv.State, conv.Fields = &target.ID, store.StateCollecting, fields
	e.audit(ctx, ident.OrgID, member(ident), "project_resumed", map[string]any{"project_id": target.ID})
	if len(e.miaConfig(ctx).CampaignMissing(fields)) == 0 {
		return e.send(ctx, conv, fmt.Sprintf(msgProjectResumedComplete, target.Name))
	}
	return e.send(ctx, conv, fmt.Sprintf(msgProjectStarted, "Retomei", target.Name, missingText(e.miaConfig(ctx), fields)))
}

func listText(cfg mia.Config, projects []store.Project) string {
	if len(projects) == 0 {
		return msgNoProjects
	}
	var b strings.Builder
	b.WriteString("*Seus projetos*")
	for i, p := range projects {
		fmt.Fprintf(&b, "\n%d. *%s* — %s", i+1, p.Name, stageLabels[p.Stage])
		if p.Stage == store.StageCollecting {
			if m := missingText(cfg, p.Fields); m != "" {
				fmt.Fprintf(&b, " (falta: %s)", m)
			}
		}
	}
	return b.String()
}

// projectStatus monta o resumo do projeto a partir do banco, sem IA.
func (e *Engine) projectStatus(ctx context.Context, ident store.Identity, conv store.Conversation, name string, projects []store.Project) error {
	var target *store.Project
	if strings.TrimSpace(name) != "" {
		found, err := e.Store.FindProjects(ctx, ident.OrgID, name)
		if err != nil {
			return err
		}
		if len(found) == 1 {
			target = &found[0]
		} else if len(found) > 1 {
			var names []string
			for _, p := range found {
				names = append(names, "*"+p.Name+"*")
			}
			return e.send(ctx, conv, fmt.Sprintf(msgProjectAmbiguous, strings.Join(names, ", ")))
		}
	} else if conv.ActiveProjectID != nil {
		p, err := e.Store.GetProject(ctx, ident.OrgID, *conv.ActiveProjectID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err == nil {
			target = &p
		}
	}
	if target == nil {
		return e.send(ctx, conv, fmt.Sprintf(msgProjectNotFound, name)+"\n\n"+listText(e.miaConfig(ctx), projects))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*%s*\nSituação: %s", target.Name, stageLabels[target.Stage])
	f := target.Fields
	if f.Offer != nil {
		fmt.Fprintf(&b, "\nOferta: %s", *f.Offer)
	}
	if f.Audience != nil {
		fmt.Fprintf(&b, "\nPúblico: %s", *f.Audience)
	}
	if f.Location != nil {
		fmt.Fprintf(&b, "\nLocal: %s", *f.Location)
	}
	if f.DailyBudgetBRL != nil {
		fmt.Fprintf(&b, "\nOrçamento: %s por dia", campaign.BRL(*f.DailyBudgetBRL))
	}
	if m := missingText(e.miaConfig(ctx), f); m != "" && target.Stage == store.StageCollecting {
		fmt.Fprintf(&b, "\nFalta: %s", m)
	}
	d, ok, err := e.Store.LatestProjectDraft(ctx, target.ID)
	if err != nil {
		return err
	}
	if ok {
		fmt.Fprintf(&b, "\nÚltima proposta: %s por dia, %d dias — %s",
			campaign.BRL(d.Proposal.DailyBudgetBRL), d.Proposal.DurationDays, draftLabels[d.Status])
		if d.ApprovedAt != nil {
			fmt.Fprintf(&b, " em %s", d.ApprovedAt.In(saoPaulo).Format("02/01"))
		}
	}
	return e.send(ctx, conv, b.String())
}

var draftLabels = map[string]string{
	"proposed":  "aguardando aprovação",
	"approved":  "aprovada",
	"rejected":  "recusada",
	"expired":   "expirada",
	"cancelled": "cancelada",
}

// projectLimit diz se o erro é o limite de projetos do plano e qual é o máximo.
func projectLimit(err error) (int, bool) {
	var pl *store.ProjectLimitError
	if !errors.As(err, &pl) {
		return 0, false
	}
	return pl.Max, true
}

// WithProfileDefaults completa os dados da campanha com a ficha do negócio, como a conversa faz (painel).
func WithProfileDefaults(p contract.Profile, f contract.Fields) contract.Fields {
	return withDefaults(p, f)
}

// knownPlaces: a cidade e a região da ficha valem como local só quando o cliente confirma uma sugestão da
// Mia ("sim", "isso", "pode ser") ou cita o próprio lugar ("na minha cidade"). Fora disso o local precisa
// estar na mensagem dele.
func knownPlaces(message string, p contract.Profile) []string {
	if !shortYes(message) && !ownPlace(message) {
		return nil
	}
	return []string{deref(p.City), deref(p.ServiceArea)}
}

func ownPlace(message string) bool {
	m := normalizeName(message)
	for _, w := range []string{"minha cidade", "nossa cidade", "mesma cidade", "minha regiao", "nossa regiao", "aqui na cidade", "onde atendo", "onde atendemos"} {
		if strings.Contains(m, w) {
			return true
		}
	}
	return false
}

func shortYes(message string) bool {
	m := normalizeName(message)
	if len(strings.Fields(m)) > 4 {
		return false
	}
	for _, w := range []string{"sim", "isso", "pode", "mesmo", "essa", "esse", "ok", "exato", "confirmo", "beleza", "certo", "claro"} {
		if containsWord(m, w) {
			return true
		}
	}
	return false
}
