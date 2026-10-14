package engine

import (
	"context"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// Modelos aprovados pela Meta para mensagens que o sistema inicia (Cloud API). Fora da janela
// de 24 h desde a última mensagem do cliente, texto livre é recusado; dentro dela vai o texto normal.
const (
	TplSignupReminder = "lembrete_cadastro"
	TplDailyReport    = "relatorio_diario"
	TplTeamAlert      = "aviso_equipe"

	// Margem antes das 24 h: o job pode sair alguns minutos depois de decidido.
	serviceWindow = 23 * time.Hour
)

// TemplateDefinition é o modelo como é enviado para aprovação (categoria UTILITY, pt_BR).
type TemplateDefinition struct {
	Name    string
	Body    string
	Example []string
}

var TemplateDefinitions = []TemplateDefinition{
	{
		Name:    TplMetaExpiring,
		Body:    "Oi! A conexão do meu anúncio com a sua conta de anúncios da Meta vence em {{1}}. Pra suas campanhas continuarem funcionando, reconecte pelo link {{2}}. Leva 1 minuto.",
		Example: []string{"12/12", "https://cadastro.meuanuncio.io/m/AbC123xYz789"},
	},
	{
		Name:    TplSignupReminder,
		Body:    "Oi! Seu cadastro no meu anúncio ainda está aberto. Para continuar e começar seus anúncios, acesse {{1}} e conclua em cerca de 2 minutos. Qualquer dúvida, é só responder esta mensagem.",
		Example: []string{"https://cadastro.meuanuncio.io/c/AbC123xYz789"},
	},
	{
		Name: TplDailyReport,
		Body: "Resumo das últimas 24 horas do meu anúncio: contatos novos {{1}}, cadastros concluídos {{2}}, " +
			"cadastros pendentes {{3}}, mensagens recebidas {{4}}, propostas geradas {{5}}, propostas aprovadas {{6}}, " +
			"anúncios recebidos {{7}} e custo de IA de US$ {{8}}. Os detalhes completos estão no painel da equipe.",
		Example: []string{"12", "8", "3", "240", "5", "4", "9", "1,35"},
	},
	{
		Name:    TplTeamAlert,
		Body:    "Um cliente pediu atendimento humano no meu anúncio. Cliente: {{1}} ({{2}}), telefone {{3}}. Motivo: {{4}}. Responda o cliente pelo painel da equipe.",
		Example: []string{"Ana", "Pizzaria Boa", "5511988887777", "quer falar com uma pessoa"},
	},
}

// maxTemplateParam é o tamanho de cada parâmetro do aviso à equipe (a Meta recusa parâmetro longo,
// com quebra de linha ou tabulação).
const maxTemplateParam = 200

// TeamAlertTemplate é o aviso de atendimento humano para a equipe.
func TeamAlertTemplate(name, org, phone, reason string) *whatsapp.Template {
	return &whatsapp.Template{Name: TplTeamAlert, Params: []string{templateParam(name), templateParam(org), templateParam(phone), templateParam(reason)}}
}

// templateParam deixa o parâmetro numa linha só, sem espaços repetidos e cortado em maxTemplateParam runas.
func templateParam(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "-"
	}
	if r := []rune(s); len(r) > maxTemplateParam {
		s = string(r[:maxTemplateParam-1]) + "…"
	}
	return s
}

// systemSendJob é um envio iniciado pelo sistema: leva o modelo para o caso de cair fora da janela.
func systemSendJob(convID int64, chatID, text string, tpl *whatsapp.Template) store.JobSpec {
	return store.JobSpec{Kind: JobSend, Payload: SendJob{ConversationID: convID, ChatID: chatID, Text: text, Template: tpl}, MaxAttempts: sendAttempts}
}

// templateSender devolve o provedor quando ele está sujeito à janela de 24 h.
func (e *Engine) templateSender() (whatsapp.TemplateSender, bool) {
	ts, ok := e.WA.(whatsapp.TemplateSender)
	if !ok || !ts.NeedsTemplates() {
		return nil, false
	}
	return ts, true
}

// outsideWindow: aviso fora de conversa (equipe, relatório) e cliente calado há mais de 23 h.
func (e *Engine) outsideWindow(ctx context.Context, convID int64) (bool, error) {
	if convID == 0 {
		return true, nil
	}
	at, ok, err := e.Store.LastInboundAt(ctx, convID)
	if err != nil || !ok {
		return true, err
	}
	return e.now().Sub(at) > serviceWindow, nil
}

// chatOutsideWindow mede a janela de 24 h pelo chat do destino: avisos fora de conversa (equipe,
// relatório) vão em texto completo quando aquele número escreveu ao bot há menos de 23 h.
func (e *Engine) chatOutsideWindow(ctx context.Context, chatID string) (bool, error) {
	at, ok, err := e.Store.LastInboundByChat(ctx, chatID)
	if err != nil || !ok {
		return true, err
	}
	return e.now().Sub(at) > serviceWindow, nil
}

// deliver manda o texto, ou o modelo aprovado quando a Cloud API exige.
func (e *Engine) deliver(ctx context.Context, s SendJob) error {
	if s.Template != nil {
		if ts, ok := e.templateSender(); ok {
			out, err := e.outsideWindow(ctx, s.ConversationID)
			if s.ConversationID == 0 {
				out, err = e.chatOutsideWindow(ctx, s.ChatID)
			}
			if err != nil {
				return err
			}
			if out {
				return ts.SendTemplate(ctx, s.ChatID, *s.Template)
			}
		}
	}
	return e.WA.SendText(ctx, s.ChatID, s.Text)
}
