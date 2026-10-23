package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// JobProjectPropose pede proposta nova para um projeto a pedido da equipe (painel).
const JobProjectPropose = "project.propose"

// ProjectProposeJob é o pedido da equipe.
type ProjectProposeJob struct {
	ProjectID int64  `json:"project_id"`
	By        string `json:"by"`
}

// projectPropose põe a conversa do dono no projeto (cancelando a proposta aberta) e pede a proposta ao
// especialista; o cliente recebe o resumo normal com o código de aprovação.
func (e *Engine) projectPropose(ctx context.Context, raw []byte) error {
	var j ProjectProposeJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("engine: payload de proposta da equipe inválido: %w", err)
	}
	p, err := e.Store.ProjectByID(ctx, j.ProjectID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.Stage == store.StageArchived) {
		return nil
	}
	if err != nil {
		return err
	}
	conv, err := e.Store.OwnerConversation(ctx, p.OrgID)
	if err != nil {
		return err
	}
	unlock, err := e.Store.LockConversation(ctx, conv.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if conv, err = e.Store.GetConversation(ctx, conv.ID); err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	if conv.State == store.StateAwaitingApproval {
		if err := e.closeOpenDraft(ctx, ident, conv, "cancelled"); err != nil {
			return err
		}
	}
	fields := withDefaults(ident.Profile, p.Fields)
	if conv.ActiveProjectID == nil || *conv.ActiveProjectID != p.ID {
		if err := e.leaveProject(ctx, ident, conv); err != nil {
			return err
		}
		if err := e.Store.ActivateProject(ctx, conv.ID, p.ID, store.StateCollecting, fields); err != nil {
			return err
		}
		conv.ActiveProjectID, conv.State, conv.Fields = &p.ID, store.StateCollecting, fields
	}
	// A equipe decide pelo painel: valem só os dados básicos (as perguntas da conversa ficam com o padrão).
	if m := fields.Missing(); len(m) > 0 {
		e.Log.Warn("proposta da equipe sem dados completos", "project_id", p.ID, "faltam", m)
		return nil // a api já confere; dado apagado no meio do caminho: nada a pedir
	}
	e.audit(ctx, p.OrgID, "system", "proposal_requested_by_team", map[string]any{"project_id": p.ID, "by": j.By})
	return e.requestProposal(ctx, ident, conv, fields, "", fmt.Sprintf(msgTeamProposal, p.Name), true)
}
