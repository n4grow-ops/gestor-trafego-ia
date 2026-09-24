package contract

import (
	"strings"
	"unicode/utf8"
)

// Profile é a memória do negócio do cliente, reaproveitada em todos os projetos.
type Profile struct {
	BusinessName   *string `json:"business_name,omitempty"`
	City           *string `json:"city,omitempty"`
	Offer          *string `json:"offer,omitempty"`           // o que a empresa faz: principais produtos ou serviços
	Audience       *string `json:"audience,omitempty"`        // persona: quem compra, características e interesses
	AudienceAge    *string `json:"audience_age,omitempty"`    // faixa de idade do cliente (ex.: 25 a 45)
	AudienceGender *string `json:"audience_gender,omitempty"` // mulheres, homens ou todos
	ServiceArea    *string `json:"service_area,omitempty"`    // bairros, cidade, raio de entrega ou online
	Differentials  *string `json:"differentials,omitempty"`   // por que comprar ali
	AverageTicket  *string `json:"average_ticket,omitempty"`  // quanto o cliente gasta em média
	SalesChannel   *string `json:"sales_channel,omitempty"`   // onde vende: WhatsApp, loja, site, iFood...
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
	pick(&out.Offer, o.Offer)
	pick(&out.Audience, o.Audience)
	pick(&out.AudienceAge, o.AudienceAge)
	pick(&out.AudienceGender, o.AudienceGender)
	pick(&out.ServiceArea, o.ServiceArea)
	pick(&out.Differentials, o.Differentials)
	pick(&out.AverageTicket, o.AverageTicket)
	pick(&out.SalesChannel, o.SalesChannel)
	return out
}

// Missing lista, na ordem da conversa, o que ainda falta saber do negócio para montar bons anúncios.
func (p Profile) Missing() []string {
	var out []string
	for _, f := range p.fields() {
		if blank(f.v) {
			out = append(out, f.label)
		}
	}
	return out
}

type profileField struct {
	v     *string
	label string
}

func (p Profile) fields() []profileField {
	return []profileField{
		{p.BusinessName, "nome da empresa"},
		{p.City, "cidade"},
		{p.Offer, "o que a empresa faz (principais produtos ou serviços)"},
		{p.Audience, "persona: quem compra, como é e do que gosta"},
		{p.AudienceAge, "faixa de idade do cliente"},
		{p.AudienceGender, "gênero predominante do cliente (mulheres, homens ou todos)"},
		{p.ServiceArea, "região que atende (bairros, cidade, raio de entrega ou online)"},
		{p.Differentials, "diferencial (por que comprar com você)"},
		{p.AverageTicket, "ticket médio (quanto o cliente gasta em média)"},
		{p.SalesChannel, "onde vende (WhatsApp, loja física, site, iFood...)"},
	}
}

// Complete diz se já conhecemos o negócio o bastante para falar de anúncios.
func (p Profile) Complete() bool { return len(p.Missing()) == 0 }

func (p Profile) HasData() bool {
	for _, f := range p.fields() {
		if !blank(f.v) {
			return true
		}
	}
	return false
}
