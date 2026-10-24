package meta

import (
	"fmt"
	"strings"
)

// goalCodes é a sigla do tipo de campanha no nome padrão ("CAMP ENG", "CAMP SEG"...).
var goalCodes = map[string]string{
	"whatsapp": "WPP", "direct": "DIRECT", "engajamento": "ENG", "seguidores": "SEG",
	"formulario": "FORM", "site": "SITE", "vendas": "VENDAS", "alcance": "ALC",
}

// nameConnectors ficam de fora da sigla da cidade ("Rio de Janeiro" = RJ).
var nameConnectors = map[string]bool{"de": true, "da": true, "do": true, "das": true, "dos": true, "e": true}

// ConventionName monta o nome padrão da campanha, em maiúsculas:
// "[MA] - CAMP ENG - FEMININO - 35+ - MARAVILHA +40KM - STORIES E FEED".
// city é a cidade do negócio, usada na sigla quando o local é só pino.
func ConventionName(in PublishInput, city string) string {
	places := ParsePlaces(in.LocationQuery)
	return strings.Join([]string{
		"[" + cityCode(places, city) + "]",
		"CAMP " + goalCode(in),
		genderLabel(in.Genders),
		ageLabel(in.AgeMin, in.AgeMax),
		placesLabel(places, in.RadiusKM),
		placementsLabel(in.Placements),
	}, " - ")
}

func goalCode(in PublishInput) string {
	goal := in.Goal
	switch {
	case goal != "":
	case in.Followers:
		goal = "seguidores"
	case in.LeadForm:
		goal = "formulario"
	case in.Objective == "OUTCOME_TRAFFIC":
		goal = "site"
	case in.Objective == "OUTCOME_AWARENESS":
		goal = "alcance"
	default:
		goal = "whatsapp"
	}
	if c := goalCodes[goal]; c != "" {
		return c
	}
	return strings.ToUpper(goal)
}

func genderLabel(genders []string) string {
	var male, female bool
	for _, g := range genders {
		male = male || g == "male"
		female = female || g == "female"
	}
	switch {
	case male && !female:
		return "MASCULINO"
	case female && !male:
		return "FEMININO"
	}
	return "MASCULINO E FEMININO"
}

// ageLabel: "35+" quando vai até o fim (65, o máximo da Meta), senão "25-45".
func ageLabel(lo, hi int) string {
	if lo <= 0 {
		lo = 18
	}
	if hi <= 0 || hi >= 65 {
		return fmt.Sprintf("%d+", lo)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}

func placesLabel(places []Place, defaultRadius int) string {
	if len(places) == 0 {
		return "BRASIL"
	}
	parts := make([]string, 0, len(places))
	for _, p := range places {
		switch {
		case p.Pin:
			parts = append(parts, fmt.Sprintf("PINO %dKM", pinRadius(p.RadiusKM)))
		case wholeCountry(p.Query):
			parts = append(parts, "BRASIL")
		default:
			name := strings.ToUpper(strings.TrimSpace(strings.SplitN(p.Query, ",", 2)[0]))
			if r := cityRadius(p.RadiusKM, defaultRadius); r > 0 {
				name += fmt.Sprintf(" +%dKM", r)
			}
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, " / ")
}

func placementsLabel(placements []string) string {
	labels := map[string]string{"feed": "FEED", "stories": "STORIES", "reels": "REELS"}
	var out []string
	for _, p := range placements {
		if l := labels[p]; l != "" {
			out = append(out, l)
		}
	}
	switch len(out) {
	case 0:
		return "AUTOMÁTICO"
	case 1:
		return out[0]
	}
	return strings.Join(out[:len(out)-1], ", ") + " E " + out[len(out)-1]
}

// cityCode é a sigla da cidade do primeiro lugar: uma palavra = as 2 primeiras letras (Maravilha = MA),
// várias = as iniciais (São Paulo = SP). Estado vira a UF; Brasil, BR. Só pino: a cidade do negócio.
func cityCode(places []Place, city string) string {
	name := ""
	for _, p := range places {
		if !p.Pin {
			name = p.Query
			break
		}
	}
	if name == "" {
		name = city
	}
	name = strings.TrimSpace(strings.SplitN(name, ",", 2)[0])
	switch {
	case name == "":
		return "PINO"
	case wholeCountry(name):
		return "BR"
	}
	if full, ok := stateName(name); ok {
		for uf, n := range ufNames {
			if n == full {
				return uf
			}
		}
	}
	var words []string
	for _, w := range strings.Fields(normPlace(name)) {
		if !nameConnectors[w] {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return "PINO"
	}
	if len(words) == 1 {
		r := []rune(words[0])
		return strings.ToUpper(string(r[:min(2, len(r))]))
	}
	var b strings.Builder
	for _, w := range words[:min(3, len(words))] {
		b.WriteRune([]rune(w)[0])
	}
	return strings.ToUpper(b.String())
}
