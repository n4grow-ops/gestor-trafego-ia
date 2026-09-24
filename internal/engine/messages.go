package engine

// Mensagens fixas ao cliente. Valores e códigos nunca vêm da IA.
const (
	msgUnknown        = "Oi! Este número ainda não está liberado para o seu contato. Fala com o responsável pelo serviço pra cadastrar seu telefone, tá?"
	msgBusy           = "Tô montando sua proposta com o especialista, já já ela chega aqui."
	msgRetryLater     = "Opa, tive um problema técnico aqui. Me manda de novo daqui a pouquinho?"
	msgHuman          = "Beleza! Vou chamar uma pessoa da nossa equipe pra continuar com você."
	msgOverBudget     = "O limite de investimento por dia da sua conta é %s. Qual valor até esse limite fica bom pra você?"
	msgUnsupported    = "[mensagem de um tipo que ainda não entendo]"
	msgApproved       = "Campanha aprovada! Anotei sua aprovação. Assim que a conexão com o Facebook estiver ativa, ela é publicada pausada pra uma última conferência."
	msgCodeInvalid    = "Hmm, esse código não confere. Responde SIM seguido do código de 6 dígitos que está na proposta."
	msgCodeExpired    = "Esse código expirou. Vou pedir uma proposta atualizada pro especialista, com código novo."
	msgCodeLocked     = "Foram muitas tentativas com código errado. Vou gerar uma proposta nova, com outro código."
	msgNotAllowed     = "Seu contato não tem permissão pra aprovar gastos desta conta. Pede pro responsável aprovar, tá?"
	msgNoDraft        = "Não achei nenhuma proposta esperando aprovação. Quer criar uma campanha?"
	msgRejected       = "Tranquilo, descartei essa proposta. Quando quiser, é só me contar o que anunciar."
	msgProposalFailed = "Não consegui montar uma proposta segura com esses dados. Vou chamar uma pessoa da nossa equipe pra te ajudar."
	msgOperator       = "Atendimento humano: %s (%s), telefone %s. Motivo: %s"
	msgAudioFailed    = "Não consegui ouvir seu áudio agora. Consegue me mandar por escrito?"

	msgDailyLimit             = "Você chegou no limite de mensagens de hoje. Amanhã a gente continua de onde parou!"
	msgProjectStarted         = "%s o projeto *%s*. Para montar a proposta, falta: %s."
	msgProjectReady           = "Projeto *%s* com todos os dados. Vou pedir a proposta ao especialista; ela chega em instantes."
	msgProjectResumedComplete = "Retomei o projeto *%s*. Já tenho todos os dados. Quer que eu gere a proposta?"
	msgProjectAlreadyActive   = "Já estamos trabalhando no projeto *%s*."
	msgProjectNotFound        = "Não encontrei o projeto \"%s\"."
	msgProjectAmbiguous       = "Achei mais de um projeto parecido: %s. Qual deles?"
	msgNoProjects             = "Você ainda não tem projetos. É só me contar o que quer anunciar."

	msgSignupLink = "Oi! Pra gente começar, faz seu cadastro rapidinho por aqui:\n\n%s\n\nLeva uns 2 minutos. Assim que terminar, a gente continua por aqui."
	msgWelcome    = "Cadastro concluído, %s! Você está no plano Free.\n\nMe conta: o que você quer anunciar primeiro?"
)
