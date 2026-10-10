package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

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

// teamDirectoryTTL: por quanto tempo a lista do painel vale antes de ler o banco de novo.
const teamDirectoryTTL = 30 * time.Second

// teamDirCache guarda a última leitura dos números marcados no painel.
type teamDirCache struct {
	mu     sync.Mutex
	phones []string
	at     time.Time
	loaded bool
}

// directoryPhones lê os números marcados no painel, com cache de 30 s. Se o banco falhar, segue
// com a última leitura boa (ou nada) e só tenta de novo depois do TTL, para não martelar o banco.
func (e *Engine) directoryPhones(ctx context.Context) []string {
	if e.TeamDirectory == nil {
		return nil
	}
	c := &e.teamDir
	c.mu.Lock()
	defer c.mu.Unlock()
	now := e.now()
	if c.loaded && now.Sub(c.at) < teamDirectoryTTL {
		return c.phones
	}
	phones, err := e.TeamDirectory(ctx)
	c.at, c.loaded = now, true
	if err != nil {
		e.Log.Error("falha ao ler os números da equipe no banco; usando a última lista", "erro", err)
		return c.phones
	}
	c.phones = phones
	return phones
}

// InvalidateTeamDirectory força a próxima leitura dos números da equipe no banco.
func (e *Engine) InvalidateTeamDirectory() {
	e.teamDir.mu.Lock()
	e.teamDir.loaded = false
	e.teamDir.mu.Unlock()
}

// teamPhones são os números que recebem os avisos. Ordem: os marcados no painel (se houver
// algum), depois TEAM_PHONES e por fim OPERATOR_PHONE.
func (e *Engine) teamPhones(ctx context.Context) []string {
	if p := e.directoryPhones(ctx); len(p) > 0 {
		return p
	}
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
	for _, p := range e.teamPhones(ctx) {
		jobs = append(jobs, systemSendJob(0, e.WA.ChatIDForPhone(p), text,
			TeamAlertTemplate(n.Ident.MemberName, n.Ident.OrgName, n.Ident.Phone, n.Reason)))
	}
	if e.teamGroupOn() {
		jobs = append(jobs, store.JobSpec{Kind: JobTeamGroup, Payload: TeamGroupJob{Text: text}, MaxAttempts: sendAttempts})
	}
	return jobs
}

// MsgTeamTest é o aviso de teste mandado pelo painel (Equipe → "Enviar teste").
const MsgTeamTest = "✅ Teste: você vai receber os avisos da equipe do meu anúncio.io aqui."

// TeamTestJob é o envio de teste para o número de alguém da equipe. Vai pelo mesmo caminho dos
// avisos de verdade: texto livre dentro da janela de 24 h e, fora dela (o normal, porque o número
// da equipe quase nunca conversa com o bot), o modelo aprovado aviso_equipe com motivo de teste.
func TeamTestJob(chatID, phone string) store.JobSpec {
	return systemSendJob(0, chatID, MsgTeamTest,
		TeamAlertTemplate("Teste", "meu anúncio.io", phone, "teste dos avisos da equipe pelo painel, nenhum cliente pediu nada"))
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
