package engine

// Modelo aprovado pela Meta para o aviso fora da janela de 24 h (Cloud API); o texto é neutro para servir
// à pausa e ao alerta de "só avisar".
func init() {
	TemplateDefinitions = append(TemplateDefinitions, TemplateDefinition{
		Name:    TplGuardrail,
		Body:    "Aviso da sua campanha no meu anúncio: {{1}} Responda esta mensagem para ver as opções.",
		Example: []string{"pausei a campanha Pizzaria: ela passou do limite de R$ 15,00 por conversa que você escolheu."},
	})
}

// Textos das proteções escolhidas pelo cliente (limite de custo por conversa, teto de gasto, pausa automática).
const (
	// Oferta única, depois do primeiro ATIVAR. Sem resposta, nada é ligado.
	msgGuardrailOffer = "Uma coisa opcional: quer que eu cuide de um limite pra você? Dois jeitos, você escolhe (ou ignora, que eu não ligo nada sozinha):\n\n• *LIMITE 15*: se cada conversa passar de R$ 15, eu pauso a campanha e te aviso\n• *TETO 100*: se o gasto de um dia chegar a R$ 100, eu pauso e te aviso\n\nSó mexo se você pedir. Dá pra trocar o valor ou tirar a qualquer hora (*SEM LIMITE*)."

	msgGuardrailCostSet   = "Combinado ✅ Se a campanha passar de %s por conversa, eu %s.\n\nPra não agir por ruído, só confiro depois de %s gastos e %s no ar. Pra tirar o limite, é só responder *SEM LIMITE*."
	msgGuardrailCapSet    = "Combinado ✅ Se o gasto de um dia chegar a %s, eu %s.\n\nO teto vale pra cada campanha, e eu confiro de hora em hora. Pra tirar, é só responder *SEM LIMITE*."
	msgGuardrailActPause  = "pauso a campanha e te aviso na hora (pra ligar de novo é só responder *ATIVAR*)"
	msgGuardrailActNotify = "te aviso (você pediu só aviso; a campanha continua no ar até você mandar *PAUSAR*)"
	msgGuardrailCleared   = "Pronto, tirei os limites. Não vou mais pausar nada sozinha. Se quiser de novo, é só pedir (ex.: *LIMITE 15* ou *TETO 100*)."
	msgGuardrailNone      = "Você não tem nenhum limite ligado. Se quiser, responde *LIMITE 15* (custo máximo por conversa) ou *TETO 100* (gasto máximo por dia)."
	msgGuardrailAutoOn    = "Certo: se um limite for passado, eu pauso a campanha sozinha e te aviso."
	msgGuardrailAutoOff   = "Certo: se um limite for passado, eu só te aviso. A campanha continua no ar até você mandar *PAUSAR*."
	msgGuardrailNoLimit   = "Você ainda não tem limite definido. Primeiro responde *LIMITE 15* (custo máximo por conversa) ou *TETO 100* (gasto máximo por dia)."
	msgGuardrailBadValue  = "Esse valor não me parece certo. Pra custo por conversa, use entre R$ 1 e R$ 1.000 (ex.: *LIMITE 15*); pro teto diário, entre R$ 5 e R$ 100.000 (ex.: *TETO 100*)."

	// Aviso da pausa (ou do alerta) — %s são o nome da campanha e o motivo com os números.
	msgGuardrailPaused = "⏸️ Pausei a campanha *%s* porque passou do limite que você escolheu.\n\n%s\n\nSe quiser colocar no ar de novo, me responde *ATIVAR*. Pra mudar ou tirar o limite: *LIMITE 20* ou *SEM LIMITE*."
	msgGuardrailAlert  = "⚠️ A campanha *%s* passou do limite que você escolheu.\n\n%s\n\nComo você pediu só aviso, ela continua no ar. Se quiser parar, me responde *PAUSAR*."
)
