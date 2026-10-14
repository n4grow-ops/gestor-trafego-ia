package engine

// Textos do diagnóstico grátis da conta (a Mia olha os últimos 30 dias, só leitura).
const (
	msgDiagnosticHead = "Olhei os últimos 30 dias de %s (%s). Foi só leitura: não mudei nada na sua conta.\n\nSó uso o que a Meta me informa, sem chute."

	msgDiagnosticNoData        = "Olhei os últimos 30 dias de %s (%s) e não encontrei nenhum anúncio com entrega nesse período: nenhum gasto e nenhuma exibição. Então não tenho números pra te mostrar, e prefiro não inventar nenhum 🙂"
	msgDiagnosticNoDataStalled = "Vi %d anúncio(s) ligado(s) sem nenhuma exibição no período. Vale conferir no Gerenciador de Anúncios se há problema de pagamento, reprovação ou orçamento zerado."

	msgDiagnosticLowData = "⚠️ Ressalva: é pouco dado (%s gastos e %s). Números assim oscilam muito: trate como pista, não como veredito."
	msgDiagnosticClose   = "Se quiser, me conta o que você quer divulgar e eu monto uma campanha já pensando nisso. Pra rever esse resumo depois, é só me mandar *DIAGNÓSTICO*."

	msgDiagnosticStarted   = "Vou olhar os últimos 30 dias da sua conta de anúncios (só leitura, não mexo em nada). Já te mando o resumo."
	msgDiagnosticPending   = "Já estou olhando a sua conta, o resumo chega aqui em instantes."
	msgDiagnosticNeedsMeta = "Pra eu olhar a sua conta de anúncios, preciso que você conecte ela primeiro."
)
