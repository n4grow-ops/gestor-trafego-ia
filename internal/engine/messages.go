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

	msgSignupLink           = "Oi! Pra gente começar, faz seu cadastro rapidinho por aqui:\n\n%s\n\nLeva uns 2 minutos. Assim que terminar, a gente continua por aqui."
	msgWelcome              = "Cadastro concluído, %s! Você está no plano Free (e-mail %s; se não foi você, é só avisar aqui).\n\nPra completar seu cadastro, preciso conhecer bem o seu negócio. São umas perguntas rápidas e é isso que deixa seus anúncios certeiros.\n\nPra começar: o que a sua empresa faz? Me conta os principais produtos ou serviços."
	msgApprovedAskCreatives = "Campanha aprovada! ✅\n\nPróxima etapa: os anúncios. Agora eu preciso das imagens ou vídeos que vão aparecer pras pessoas no Instagram e no Facebook. É isso que eu vou subir na Meta junto com a sua campanha.\n\nMe manda aqui mesmo, até 5: foto do produto, do espaço, da equipe ou um vídeo curto. Quando terminar, é só escrever PRONTO."
	msgCreativesAck         = "Recebi! Até agora: %d de %d anúncios. Pode mandar mais ou escrever PRONTO."
	msgCreativesWaiting     = "Tô esperando as imagens ou vídeos dos seus anúncios 🙂 Pode mandar aqui mesmo. Quando terminar, escreve PRONTO."
	msgCreativesNeedOne     = "Preciso de pelo menos uma imagem ou vídeo pra montar o anúncio na Meta. Pode mandar aqui?"
	msgCreativeFailed       = "Não consegui salvar um dos arquivos. Manda de novo, por favor?"
	msgCreativesDone        = "Perfeito, recebi %d anúncio(s)! Etapa dos anúncios concluída: seu projeto está pronto pra publicar na Meta. Assim que a conexão com o Facebook estiver ativa, a campanha é publicada pausada pra uma última conferência."

	msgSignupReminder1 = "Oi! Faltou pouquinho pro seu cadastro no meuanuncio.io 🙂 Leva uns 2 minutos e aí a gente já começa a montar seus anúncios:\n\n%s"
	msgSignupReminder2 = "Passando pra lembrar: seu cadastro ainda está aberto. Se quiser continuar, é só tocar aqui:\n\n%s\n\nQualquer dúvida, me chama por aqui mesmo."

	msgDailyReport = "📊 meuanuncio.io — resumo das últimas 24 h\n\n👤 Contatos novos: %d\n✅ Cadastros concluídos: %d\n⏳ Cadastros pendentes (com link): %d\n💬 Mensagens recebidas: %d\n📝 Propostas geradas: %d\n👍 Propostas aprovadas: %d\n🖼️ Anúncios recebidos: %d\n💸 Custo de IA: US$ %s"

	msgSignupOwner = "Oi! Antes de começar, o cadastro da conta precisa ser feito pelo responsável. Pede pra ele mandar uma mensagem aqui, que eu envio o link."
)
