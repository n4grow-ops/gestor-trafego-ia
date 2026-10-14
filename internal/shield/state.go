package shield

import (
	"fmt"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// State é a situação da conta de anúncios, em poucos estados que o cliente entende.
type State string

const (
	StateOK         State = "ok"
	StateRestricted State = "restrita"   // em análise de risco
	StateDisabled   State = "desativada" // a Meta desativou a conta
	StateClosed     State = "encerrada"  // encerrando ou encerrada
	StatePayment    State = "pagamento"  // pagamento pendente ou recusado
	StateGrace      State = "carencia"   // pagamento atrasado, ainda rodando em carência
)

// Classify traduz o account_status e o disable_reason da Meta.
func Classify(a meta.AccountHealth) State {
	switch a.Status {
	case 0, 1, 201:
		// 0 = a Meta não devolveu (leitura incompleta): não acusa problema que não viu.
		return StateOK
	case 2:
		if a.DisableReason == 3 {
			return StatePayment // RISK_PAYMENT
		}
		if a.DisableReason == 7 {
			return StateClosed
		}
		return StateDisabled
	case 3, 8:
		return StatePayment
	case 7:
		return StateRestricted
	case 9:
		return StateGrace
	case 100, 101, 202:
		return StateClosed
	}
	return StateOK
}

// Bad: o estado merece aviso ao cliente.
func (s State) Bad() bool { return s != StateOK && s != "" }

// DisableReasonText diz o motivo da desativação em português simples ("" quando não há).
func DisableReasonText(reason int) string {
	switch reason {
	case 0:
		return ""
	case 1:
		return "a Meta entendeu que algum anúncio quebrou as políticas de publicidade"
	case 2:
		return "a conta caiu numa revisão de propriedade intelectual (uso de marca, imagem ou música de terceiros)"
	case 3:
		return "a Meta viu risco no pagamento da conta"
	case 4:
		return "a Meta fechou a conta por atividade que considerou suspeita"
	case 5:
		return "a conta está numa revisão de segurança da Meta"
	case 6:
		return "o Business ligado à conta caiu numa revisão de integridade"
	case 7:
		return "a conta foi encerrada em definitivo"
	case 8, 9:
		return "a conta foi encerrada por falta de uso"
	case 11:
		return "o Business Manager ligado à conta caiu numa revisão de políticas"
	case 12:
		return "a Meta entendeu que a conta representava algo diferente do que é de verdade"
	case 15:
		return "a Meta suspeita que alguém sem autorização acessou a conta"
	}
	return "a Meta não detalhou o motivo"
}

// StateMessage é a frase do estado, em português simples, sem prometer reversão.
func StateMessage(s State, disableReason int) string {
	why := DisableReasonText(disableReason)
	if why != "" {
		why = " Motivo informado: " + why + "."
	}
	switch s {
	case StateRestricted:
		return "a sua conta de anúncios entrou em análise de risco na Meta; os anúncios podem parar até a análise terminar." + why
	case StateDisabled:
		return "a Meta desativou a sua conta de anúncios; nenhum anúncio roda e eu não consigo criar campanhas nela." + why
	case StateClosed:
		return "a sua conta de anúncios está encerrada ou encerrando na Meta; nenhum anúncio roda nela." + why
	case StatePayment:
		return "a Meta está com um pagamento pendente ou recusado na sua conta de anúncios; os anúncios param até acertar." + why
	case StateGrace:
		return "a sua conta de anúncios tem pagamento atrasado e está na carência da Meta; se não acertar, ela para de veicular." + why
	}
	return "a sua conta de anúncios está ativa."
}

// brt é o horário de Brasília (sem horário de verão desde 2019).
var brt = time.FixedZone("BRT", -3*3600)

// Janela de silêncio: o cliente não recebe aviso do escudo das 21h às 8h de Brasília.
const (
	QuietFrom = 21
	QuietTo   = 8
)

// Quiet diz se o horário cai na janela de silêncio (21h às 8h BRT).
func Quiet(t time.Time) bool {
	h := t.In(brt).Hour()
	return h >= QuietFrom || h < QuietTo
}

// NextOpen é o primeiro instante fora da janela de silêncio: o próprio t se já está fora, senão as 8h de Brasília.
func NextOpen(t time.Time) time.Time {
	if !Quiet(t) {
		return t
	}
	b := t.In(brt)
	y, m, d := b.Date()
	at := time.Date(y, m, d, QuietTo, 0, 0, 0, brt)
	if !at.After(b) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}

// Alert é o aviso ao cliente pela mudança de estado.
type Alert struct {
	State    State
	Message  string // texto do WhatsApp
	Scenario Scenario
}

// AlertFor monta o aviso de um estado ruim: o que houve, o que fazer e a honestidade sobre o prazo.
func AlertFor(s State, disableReason int, accountName string) Alert {
	sc := ScenarioFor(s, disableReason)
	g := GuideFor(sc)
	name := ""
	if accountName != "" {
		name = fmt.Sprintf(" (%s)", accountName)
	}
	msg := fmt.Sprintf("⚠️ Aviso importante: %s%s\n\n%s\n\n*O que fazer agora:*\n%s\n\n%s",
		StateMessage(s, disableReason), name, g.What, g.StepsText(), g.Note)
	return Alert{State: s, Message: msg, Scenario: sc}
}
