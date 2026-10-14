package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

var (
	archiveNumberRe = regexp.MustCompile(`^\D{0,30}?\b(\d{1,2})\b\D*$`)
	archivePlansRe  = regexp.MustCompile(`(?i)\b(planos?|starter|pro|aumentar o limite)\b`)
	archiveCancelRe = regexp.MustCompile(`(?i)^\s*(n[aã]o|nao|cancela|cancelar|deixa|esquece|nenhum)\b`)
	archiveVagueRe  = regexp.MustCompile(`(?i)\b(arquiv\w*|sim|pode|ok|beleza|blz|qualquer)\b`)
)

// askArchive: o plano chegou ao limite de projetos. Guarda o pedido novo e mostra os projetos numerados para
// o cliente escolher qual arquivar; depois da escolha o pedido segue sem perguntar tudo de novo.
func (e *Engine) askArchive(ctx context.Context, ident store.Identity, conv store.Conversation, max int, name string, f contract.Fields) error {
	projects, err := e.Store.ListProjects(ctx, ident.OrgID)
	if err != nil {
		return err
	}
	pend := &store.PendingProject{Name: strings.TrimSpace(name), Fields: f}
	for _, p := range projects {
		pend.Options = append(pend.Options, p.ID)
	}
	if err := e.Store.SetPendingProject(ctx, conv.ID, pend); err != nil {
		return err
	}
	label := "o projeto novo"
	if pend.Name != "" {
		label = "o *" + pend.Name + "*"
	}
	return e.send(ctx, conv, fmt.Sprintf(msgArchiveAsk, max, label, numberedProjects(projects)))
}

func numberedProjects(projects []store.Project) string {
	lines := make([]string, len(projects))
	for i, p := range projects {
		lines[i] = fmt.Sprintf("%d. *%s* · %s", i+1, p.Name, stageLabels[p.Stage])
	}
	return strings.Join(lines, "\n")
}

// archiveChoiceTurn trata a resposta à lista do limite: número ou nome arquiva e segue com o pedido;
// "planos" chama a equipe; "não" desiste; resposta vaga pergunta qual; outro assunto sai da escolha.
func (e *Engine) archiveChoiceTurn(ctx context.Context, ident store.Identity, conv store.Conversation, text string) (bool, error) {
	pend, ok, err := e.Store.GetPendingProject(ctx, conv.ID)
	if err != nil || !ok {
		return false, err
	}
	var options []store.Project
	for _, id := range pend.Options {
		if p, err := e.Store.GetProject(ctx, ident.OrgID, id); err == nil && p.Stage != store.StageArchived {
			options = append(options, p)
		}
	}
	words := len(strings.Fields(text))
	var chosen *store.Project
	if m := archiveNumberRe.FindStringSubmatch(text); m != nil && words <= 8 {
		if n, _ := strconv.Atoi(m[1]); n >= 1 && n <= len(pend.Options) {
			for i := range options {
				if options[i].ID == pend.Options[n-1] {
					chosen = &options[i]
				}
			}
		}
	}
	if chosen == nil && words <= 10 {
		for i := range options {
			if mentionsName(text, options[i].Name) {
				chosen = &options[i]
				break
			}
		}
	}
	switch {
	case chosen != nil:
		return true, e.archiveAndContinue(ctx, ident, conv, pend, *chosen)
	case archivePlansRe.MatchString(text) && words <= 10:
		if err := e.Store.SetPendingProject(ctx, conv.ID, nil); err != nil {
			return true, err
		}
		e.alertTeam(ctx, ident, "cliente chegou ao limite de projetos do plano e quer conhecer os planos Starter e Pro")
		return true, e.send(ctx, conv, msgArchivePlans)
	case archiveCancelRe.MatchString(text) && words <= 6:
		if err := e.Store.SetPendingProject(ctx, conv.ID, nil); err != nil {
			return true, err
		}
		return true, e.send(ctx, conv, msgArchiveCancelled)
	case archiveVagueRe.MatchString(text) && words <= 6:
		return true, e.send(ctx, conv, fmt.Sprintf(msgArchiveWhich, numberedProjects(options)))
	}
	// Outro assunto: a escolha fica para trás e a conversa segue normal.
	return false, e.Store.SetPendingProject(ctx, conv.ID, nil)
}

// archiveAndContinue arquiva o projeto escolhido e retoma o pedido que parou no limite.
func (e *Engine) archiveAndContinue(ctx context.Context, ident store.Identity, conv store.Conversation, pend store.PendingProject, p store.Project) error {
	if err := e.Store.ArchiveProject(ctx, ident.OrgID, p.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err := e.Store.SetPendingProject(ctx, conv.ID, nil); err != nil {
		return err
	}
	e.audit(ctx, ident.OrgID, member(ident), "project_archived", map[string]any{"project_id": p.ID, "reason": "limite do plano"})
	if err := e.send(ctx, conv, fmt.Sprintf(msgArchived, p.Name)); err != nil {
		return err
	}
	conv, err := e.Store.GetConversation(ctx, conv.ID) // o arquivamento pode ter tirado a conversa do projeto
	if err != nil {
		return err
	}
	projects, err := e.Store.ListProjects(ctx, ident.OrgID)
	if err != nil {
		return err
	}
	turn := contract.AttendantTurn{Intent: contract.IntentNewProject, Project: pend.Name, Fields: pend.Fields}
	return e.startProject(ctx, ident, conv, turn, projects)
}

// mentionsName diz se o texto cita o nome do projeto como palavra inteira ("A" não casa com "arquivar").
func mentionsName(text, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	re, err := regexp.Compile(`(?i)(^|[^\p{L}\p{N}])` + regexp.QuoteMeta(name) + `([^\p{L}\p{N}]|$)`)
	return err == nil && re.MatchString(text)
}
