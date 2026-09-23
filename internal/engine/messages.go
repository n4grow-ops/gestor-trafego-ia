package engine

// Mensagens fixas ao cliente. Valores e códigos nunca vêm da IA.
const (
	msgUnknown        = "Olá! Este número ainda não está liberado para o seu contato. Fale com o responsável pelo serviço para cadastrar seu telefone."
	msgBusy           = "Estou montando sua proposta com o especialista. Em instantes ela chega por aqui."
	msgRetryLater     = "Tive um problema técnico agora. Pode me mandar a mensagem de novo daqui a pouco?"
	msgHuman          = "Certo! Vou chamar uma pessoa da nossa equipe para continuar com você."
	msgOverBudget     = "O limite de investimento diário da sua conta é %s. Pode me dizer um valor até esse limite?"
	msgUnsupported    = "[mensagem de um tipo que ainda não entendo]"
	msgApproved       = "Campanha aprovada! Registrei sua aprovação. Assim que a conexão com o Facebook estiver ativa, ela é publicada pausada para uma última conferência."
	msgCodeInvalid    = "Esse código não confere. Responda SIM seguido do código de 6 dígitos que está na proposta."
	msgCodeExpired    = "Esse código expirou. Vou pedir uma proposta atualizada ao especialista, com um código novo."
	msgCodeLocked     = "Foram muitas tentativas com código errado. Vou gerar uma proposta nova, com outro código."
	msgNotAllowed     = "Seu contato não tem permissão para aprovar gastos desta conta. Peça ao responsável para aprovar."
	msgNoDraft        = "Não encontrei nenhuma proposta esperando aprovação. Quer criar uma campanha?"
	msgRejected       = "Tudo bem, descartei essa proposta. Quando quiser, é só me contar o que anunciar."
	msgProposalFailed = "Não consegui montar uma proposta segura com esses dados. Vou chamar uma pessoa da nossa equipe para te ajudar."
	msgOperator       = "Atendimento humano: %s (%s), telefone %s. Motivo: %s"
)
