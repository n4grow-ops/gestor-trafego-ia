package shield

import "time"

// Decision é o que o job de saúde faz com a mudança de estado de uma conta.
type Decision string

const (
	// DecideNone: nada mudou desde o último aviso.
	DecideNone Decision = "nada"
	// DecideNotify: o estado ruim é novo e ainda não foi avisado; avisa agora.
	DecideNotify Decision = "avisar"
	// DecideDefer: o estado ruim é novo, mas é a janela de silêncio (21h às 8h BRT); fica para depois, sem marcar como avisado.
	DecideDefer Decision = "adiar"
	// DecideReset: a conta voltou ao normal; zera o último aviso em silêncio, para um novo problema avisar de novo.
	DecideReset Decision = "zerar"
)

// Decide compara o estado de agora com o último de que o cliente foi avisado. Uma mudança gera no máximo
// um aviso: com o mesmo estado ruim em checagens seguintes, não repete.
func Decide(current, notified State, now time.Time) Decision {
	if notified == "" {
		notified = StateOK
	}
	switch {
	case current == notified:
		return DecideNone
	case !current.Bad():
		return DecideReset
	case Quiet(now):
		return DecideDefer
	}
	return DecideNotify
}
