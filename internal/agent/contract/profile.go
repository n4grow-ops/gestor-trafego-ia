package contract

import (
	"strings"
	"unicode/utf8"
)

// Profile é a memória do negócio do cliente, reaproveitada em todos os projetos.
type Profile struct {
	BusinessName *string `json:"business_name,omitempty"`
	City         *string `json:"city,omitempty"`
	Audience     *string `json:"audience,omitempty"`
	Offer        *string `json:"offer,omitempty"`
}

const profileFieldMax = 120

// Merge devolve p com os campos informados em o por cima. Vazio não apaga; texto longo é cortado.
func (p Profile) Merge(o Profile) Profile {
	out := p
	pick := func(dst **string, v *string) {
		if blank(v) {
			return
		}
		s := strings.TrimSpace(*v)
		if utf8.RuneCountInString(s) > profileFieldMax {
			s = string([]rune(s)[:profileFieldMax])
		}
		*dst = &s
	}
	pick(&out.BusinessName, o.BusinessName)
	pick(&out.City, o.City)
	pick(&out.Audience, o.Audience)
	pick(&out.Offer, o.Offer)
	return out
}

// Complete diz se o onboarding já tem o mínimo: nome do negócio e cidade.
func (p Profile) Complete() bool { return !blank(p.BusinessName) && !blank(p.City) }

func (p Profile) HasData() bool {
	return !blank(p.BusinessName) || !blank(p.City) || !blank(p.Audience) || !blank(p.Offer)
}
