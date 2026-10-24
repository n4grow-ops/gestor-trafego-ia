package meta

import "testing"

// Nome padrão pedido pelo dono: "[MA] - CAMP ENG - GENERO - IDADE - LOCALIZAÇÃO - STORIES E FEED".
func TestConventionName(t *testing.T) {
	cases := []struct {
		in   PublishInput
		city string
		want string
	}{
		{PublishInput{Goal: "engajamento", Genders: []string{"female"}, AgeMin: 35, AgeMax: 65, LocationQuery: "Maravilha, SC", RadiusKM: 40,
			Placements: []string{"stories", "feed"}}, "", "[MA] - CAMP ENG - FEMININO - 35+ - MARAVILHA +40KM - STORIES E FEED"},
		{PublishInput{Followers: true, Genders: []string{"male"}, AgeMin: 18, AgeMax: 50, LocationQuery: "São Paulo"},
			"", "[SP] - CAMP SEG - MASCULINO - 18-50 - SÃO PAULO - AUTOMÁTICO"},
		// só pino: a sigla vem da cidade do negócio
		{PublishInput{Goal: "whatsapp", Genders: []string{"male", "female"}, AgeMin: 25, LocationQuery: "pino -26.778833,-53.179998 raio 10 km",
			Placements: []string{"feed", "stories", "reels"}}, "Maravilha", "[MA] - CAMP WPP - MASCULINO E FEMININO - 25+ - PINO 10KM - FEED, STORIES E REELS"},
		{PublishInput{LeadForm: true, LocationQuery: "Brasil"}, "", "[BR] - CAMP FORM - MASCULINO E FEMININO - 18+ - BRASIL - AUTOMÁTICO"},
		{PublishInput{Goal: "alcance", LocationQuery: "Santa Catarina", Placements: []string{"reels"}}, "", "[SC] - CAMP ALC - MASCULINO E FEMININO - 18+ - SANTA CATARINA - REELS"},
		{PublishInput{Goal: "site", LocationQuery: "Rio de Janeiro + 10km"}, "", "[RJ] - CAMP SITE - MASCULINO E FEMININO - 18+ - RIO DE JANEIRO +17KM - AUTOMÁTICO"}, // mínimo da Meta em volta de cidade,
	}
	for _, c := range cases {
		if got := ConventionName(c.in, c.city); got != c.want {
			t.Errorf("nome = %q\n quer %q", got, c.want)
		}
	}
}
