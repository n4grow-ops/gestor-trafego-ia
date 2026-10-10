package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Tipos de aviso para a equipe.
const (
	TeamHuman   = "human"
	TeamTester  = "tester"
	TeamPublish = "publish"
	TeamAlert   = "alert"
)

// JobTeamGroup manda o aviso ao grupo da equipe pela instância não oficial.
const JobTeamGroup = "team.group"

// maxMotive é o tamanho do motivo (última fala do cliente) no aviso de atendimento humano.
const maxMotive = 200

// TeamNotice é um aviso para a equipe (privado dos números da equipe e, se ligado, o grupo).
type TeamNotice struct {
	Kind   string
	Ident  store.Identity
	Reason string // motivo curto (vai no template fora da janela de 24 h)
	Text   string // texto completo; vazio = monta a partir de Reason
}

// TeamGroupSender manda texto ao grupo da equipe. É uma instância Wame não oficial (QR):
// o número oficial (Cloud API) não manda para grupo.
type TeamGroupSender interface {
	SendText(ctx context.Context, chatID, text string) error
}

// TeamGroupJob é o aviso a mandar no grupo da equipe.
type TeamGroupJob struct {
	Text string `json:"text"`
}

// teamPhones são os números da equipe; sem TEAM_PHONES vale o OPERATOR_PHONE.
func (e *Engine) teamPhones() []string {
	if len(e.TeamPhones) > 0 {
		return e.TeamPhones
	}
	if e.OperatorPhone != "" {
		return []string{e.OperatorPhone}
	}
	return nil
}

// teamGroupOn: grupo da equipe configurado.
func (e *Engine) teamGroupOn() bool { return e.TeamGroup != nil && e.TeamGroupJID != "" }

// teamJobs monta os envios do aviso para a equipe: um no privado de cada número (com o modelo
// aprovado para fora da janela de 24 h) e, com o grupo ligado, um no grupo. Cada envio é um job
// próprio: falha no grupo tenta de novo sem segurar o cliente nem os outros avisos.
func (e *Engine) teamJobs(ctx context.Context, n TeamNotice) []store.JobSpec {
	text := n.Text
	if text == "" {
		text = fmt.Sprintf(msgOperator, n.Ident.MemberName, n.Ident.OrgName, n.Ident.Phone, n.Reason)
	}
	var jobs []store.JobSpec
	for _, p := range e.teamPhones() {
		jobs = append(jobs, systemSendJob(0, e.WA.ChatIDForPhone(p), text,
			TeamAlertTemplate(n.Ident.MemberName, n.Ident.OrgName, n.Ident.Phone, n.Reason)))
	}
	if e.teamGroupOn() {
		jobs = append(jobs, store.JobSpec{Kind: JobTeamGroup, Payload: TeamGroupJob{Text: text}, MaxAttempts: sendAttempts})
	}
	return jobs
}

// sendTeamGroup entrega o aviso no grupo. Sem grupo configurado (ex.: variável removida com
// job na fila), o aviso é descartado: os números da equipe já receberam no privado.
func (e *Engine) sendTeamGroup(ctx context.Context, raw json.RawMessage) error {
	var g TeamGroupJob
	if err := json.Unmarshal(raw, &g); err != nil {
		return fmt.Errorf("engine: payload do grupo da equipe inválido: %w", err)
	}
	if !e.teamGroupOn() {
		e.Log.Warn("aviso ao grupo da equipe descartado: grupo não configurado")
		return nil
	}
	return e.TeamGroup.SendText(ctx, e.TeamGroupJID, g.Text)
}

// humanNotice é o aviso de que o cliente pediu atendimento humano, com a fala dele como motivo.
func (e *Engine) humanNotice(ident store.Identity, clientText string) TeamNotice {
	motive := clientMotive(clientText)
	text := fmt.Sprintf(msgTeamHuman, ident.MemberName, ident.OrgName, ident.Phone, motive)
	if e.AdminURL != "" {
		text += " Atender: " + e.AdminURL
	}
	return TeamNotice{Kind: TeamHuman, Ident: ident, Reason: motive, Text: text}
}

// clientMotive limpa a fala do cliente para o aviso: uma linha só, sem tokens de link nem
// segredos, cortada em maxMotive runas.
func clientMotive(text string) string {
	s := strings.Join(strings.Fields(store.RedactSecrets(redactLinks(text))), " ")
	if s == "" {
		return "(sem texto)"
	}
	if r := []rune(s); len(r) > maxMotive {
		s = string(r[:maxMotive-1]) + "…"
	}
	return s
}
