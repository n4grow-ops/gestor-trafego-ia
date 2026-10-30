package whatsapp

import "context"

// Health é a situação do número na Meta (Cloud API): se dá para mandar mensagem que o bot inicia
// (avisos e modelos) e os problemas apontados. Blocked = a Meta barra esses envios sem erro na hora.
type Health struct {
	Blocked  bool
	Problems []HealthProblem
}

// HealthProblem é um erro da Meta no número, na conta do WhatsApp Business ou no app.
type HealthProblem struct {
	Entity      string // PHONE_NUMBER, WABA, BUSINESS, APP
	Code        int
	Description string
	Solution    string
}

// HealthChecker é opcional: provedores que leem a situação do número na Meta.
type HealthChecker interface {
	Health(ctx context.Context) (Health, bool, error) // ok=false: provedor sem essa leitura (ex.: QR)
}
