package shield

import (
	"fmt"
	"strings"
)

// Scenario é um dos quatro problemas mais comuns de conta de anúncios.
type Scenario string

const (
	ScenarioRestricted Scenario = "conta_restrita"
	ScenarioIdentity   Scenario = "verificacao_identidade"
	ScenarioPayment    Scenario = "pagamento_recusado"
	ScenarioRejected   Scenario = "anuncio_reprovado"
)

// Scenarios na ordem em que o painel mostra.
var Scenarios = []Scenario{ScenarioRestricted, ScenarioIdentity, ScenarioPayment, ScenarioRejected}

// Guide é o guia de recuperação de um cenário. O texto não promete reversão nem prazo: quem decide é a Meta.
type Guide struct {
	Scenario Scenario `json:"scenario"`
	Title    string   `json:"title"`
	What     string   `json:"what"`  // o que costuma ter acontecido
	Steps    []string `json:"steps"` // passo a passo curto
	Note     string   `json:"note"`  // o que não dá para prometer
}

// StepsText numera os passos para o WhatsApp.
func (g Guide) StepsText() string {
	lines := make([]string, len(g.Steps))
	for i, s := range g.Steps {
		lines[i] = fmt.Sprintf("%d. %s", i+1, s)
	}
	return strings.Join(lines, "\n")
}

const noPromise = "Eu não consigo reverter nada pela Meta e ninguém garante o resultado ou o prazo: a decisão é só dela. Se o pedido for negado, a Meta mostra o que fazer a seguir."

var guides = map[Scenario]Guide{
	ScenarioRestricted: {
		Scenario: ScenarioRestricted,
		Title:    "Conta restrita ou desativada",
		What:     "A Meta limitou ou desativou a conta de anúncios, em geral por uma revisão de risco ou por anúncios que ela entendeu que quebram as políticas.",
		Steps: []string{
			"Abra o Gerenciador de Anúncios e entre em Qualidade da Conta (Account Quality) para ver o motivo que a Meta mostra.",
			"Se houver o botão *Solicitar análise* (ou *Contestar*), peça a revisão uma única vez e explique de forma curta e objetiva.",
			"Pare de criar anúncios novos nessa conta enquanto a análise não termina; insistir ou criar outra conta no mesmo perfil pode piorar.",
			"Tire do ar o que a Meta apontou (texto, imagem ou página de destino) antes de anunciar de novo.",
		},
		Note: noPromise,
	},
	ScenarioIdentity: {
		Scenario: ScenarioIdentity,
		Title:    "Verificação de identidade ou do Business",
		What:     "A Meta pode pedir que você confirme quem é (documento com foto) ou que a empresa seja verificada (CNPJ, endereço e site) antes de liberar ou voltar a veicular.",
		Steps: []string{
			"Abra o Gerenciador de Anúncios (ou a Central de Segurança do Business) e procure o aviso *Confirmar identidade* ou *Verificação do Business*.",
			"Envie um documento oficial legível e que bata com o nome do perfil e da empresa; para o Business, use o CNPJ e o comprovante de endereço com o mesmo nome.",
			"Acompanhe pelo mesmo lugar: a Meta responde ali e por e-mail. Confira a caixa de spam.",
		},
		Note: "A análise é da Meta e o prazo varia (de horas a alguns dias). Não dá para prometer aprovação nem acelerar por aqui.",
	},
	ScenarioPayment: {
		Scenario: ScenarioPayment,
		Title:    "Pagamento recusado",
		What:     "A Meta não conseguiu cobrar o cartão (limite, cartão vencido, bloqueio do banco) ou o saldo pré-pago acabou. Enquanto isso os anúncios param.",
		Steps: []string{
			"Abra a Cobrança da conta no Gerenciador de Anúncios e veja qual forma de pagamento falhou.",
			"Troque ou cadastre outro cartão, ou coloque saldo por Pix ou boleto. Se o cartão foi bloqueado ou negado, fale com o banco.",
			"Pague o valor em aberto pelo botão *Pagar agora*.",
			"Me avise com um *paguei* que eu confiro na Meta e te digo se liberou.",
		},
		Note: "Depois de pagar, a Meta pode levar de alguns minutos a algumas horas para voltar a veicular. Cobranças recusadas muitas vezes podem levar a Meta a restringir a conta.",
	},
	ScenarioRejected: {
		Scenario: ScenarioRejected,
		Title:    "Anúncio reprovado",
		What:     "A Meta revisou o anúncio e entendeu que ele quebra alguma política de publicidade (texto, imagem, promessa ou página de destino).",
		Steps: []string{
			"Leia o motivo que a Meta informou (eu traduzo para você) e ache no texto ou na imagem o trecho que causou isso.",
			"Mude o trecho em vez de reenviar o mesmo anúncio: reenvio igual costuma ser reprovado de novo.",
			"Não use promessa de resultado, antes e depois, nem fale de características pessoais da pessoa (idade, saúde, dinheiro).",
			"Mande a imagem ou o texto novo para a Mia, que refaz o anúncio; se achar que a reprovação foi engano, peça nova análise pelo Gerenciador de Anúncios.",
		},
		Note: "A Meta pode manter a reprovação mesmo depois do pedido de nova análise. Reprovações repetidas pesam contra a conta.",
	},
}

// GuideFor devolve o guia de recuperação do cenário.
func GuideFor(s Scenario) Guide {
	if g, ok := guides[s]; ok {
		return g
	}
	return guides[ScenarioRestricted]
}

// AllGuides são os quatro guias, na ordem de Scenarios.
func AllGuides() []Guide {
	out := make([]Guide, len(Scenarios))
	for i, s := range Scenarios {
		out[i] = guides[s]
	}
	return out
}

// ScenarioFor escolhe o guia de um estado de conta ruim.
func ScenarioFor(s State, disableReason int) Scenario {
	switch s {
	case StatePayment, StateGrace:
		return ScenarioPayment
	case StateDisabled, StateRestricted, StateClosed:
		if disableReason == 5 || disableReason == 15 {
			return ScenarioIdentity // revisão de segurança: a Meta costuma pedir confirmação de identidade
		}
		return ScenarioRestricted
	}
	return ScenarioRestricted
}
