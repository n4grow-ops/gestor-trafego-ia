package engine

import (
	"context"
	"fmt"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Tipos de aviso para a equipe.
const (
	TeamHuman   = "human"
	TeamTester  = "tester"
	TeamPublish = "publish"
	TeamAlert   = "alert"
)

// TeamNotice é um aviso para a equipe (privado dos números da equipe e, se ligado, o grupo).
type TeamNotice struct {
	Kind   string
	Ident  store.Identity
	Reason string // motivo curto (vai no template fora da janela de 24 h)
	Text   string // texto completo; vazio = monta a partir de Reason
}

// teamJobs monta os envios do aviso para a equipe.
func (e *Engine) teamJobs(ctx context.Context, n TeamNotice) []store.JobSpec {
	if e.OperatorPhone == "" {
		return nil
	}
	text := n.Text
	if text == "" {
		text = fmt.Sprintf(msgOperator, n.Ident.MemberName, n.Ident.OrgName, n.Ident.Phone, n.Reason)
	}
	return []store.JobSpec{systemSendJob(0, e.WA.ChatIDForPhone(e.OperatorPhone), text,
		TeamAlertTemplate(n.Ident.MemberName, n.Ident.OrgName, n.Ident.Phone, n.Reason))}
}
