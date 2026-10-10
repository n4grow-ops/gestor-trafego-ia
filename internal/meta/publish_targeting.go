package meta

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Estados pela sigla: a busca da Meta devolve o nome por extenso em "region".
var ufNames = map[string]string{
	"AC": "Acre", "AL": "Alagoas", "AP": "Amapá", "AM": "Amazonas", "BA": "Bahia", "CE": "Ceará", "DF": "Distrito Federal",
	"ES": "Espírito Santo", "GO": "Goiás", "MA": "Maranhão", "MT": "Mato Grosso", "MS": "Mato Grosso do Sul",
	"MG": "Minas Gerais", "PA": "Pará", "PB": "Paraíba", "PR": "Paraná", "PE": "Pernambuco", "PI": "Piauí",
	"RJ": "Rio de Janeiro", "RN": "Rio Grande do Norte", "RS": "Rio Grande do Sul", "RO": "Rondônia", "RR": "Roraima",
	"SC": "Santa Catarina", "SP": "São Paulo", "SE": "Sergipe", "TO": "Tocantins",
}

type geoResult struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	CountryCode string `json:"country_code"`
	Region      string `json:"region"`
}

var placeAccents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "ê", "e", "è", "e", "ë", "e",
	"í", "i", "î", "i", "ì", "i", "ï", "i",
	"ó", "o", "ô", "o", "õ", "o", "ò", "o", "ö", "o",
	"ú", "u", "û", "u", "ù", "u", "ü", "u", "ç", "c",
)

// normPlace compara nomes de lugar: sem acento, sem caixa e sem o " (state)" que a Meta põe no estado
// homônimo da capital ("São Paulo (state)", "Rio de Janeiro (state)").
func normPlace(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSpace(strings.TrimSuffix(s, "(state)"))
	s = strings.TrimSpace(strings.TrimSuffix(s, "(estado)"))
	return strings.Join(strings.Fields(placeAccents.Replace(s)), " ")
}

// stateName devolve o nome do estado pela sigla ou pelo nome (com ou sem acento).
func stateName(s string) (string, bool) {
	if name, ok := ufNames[strings.ToUpper(strings.TrimSpace(s))]; ok {
		return name, true
	}
	n := normPlace(s)
	for _, name := range ufNames {
		if normPlace(name) == n {
			return name, true
		}
	}
	return "", false
}

// splitPlace separa "Chapecó, SC" (ou "Chapecó - SC", "Chapecó/SC") em cidade e estado.
func splitPlace(q string) (place, state string) {
	q = strings.TrimSpace(q)
	for _, sep := range []string{",", " - ", "/"} {
		if i := strings.LastIndex(q, sep); i > 0 {
			place, state = strings.TrimSpace(q[:i]), strings.TrimSpace(q[i+len(sep):])
			if name, ok := stateName(state); ok {
				return place, name
			}
			return place, "" // depois do separador não é estado ("Chapecó, Brasil")
		}
	}
	if name, ok := ufNames[strings.ToUpper(q)]; ok {
		return "", name
	}
	return q, ""
}

// wholeCountry: o cliente pediu o Brasil todo.
func wholeCountry(q string) bool {
	switch normPlace(q) {
	case "brasil", "brazil", "br", "todo o brasil", "brasil todo", "brasil inteiro", "o brasil inteiro", "nacional", "todo brasil":
		return true
	}
	return false
}

// cityNotFound pede ao cliente a cidade com o estado; nada é criado na conta.
const cityNotFound = "Não achei a cidade \"%s\" na Meta pra mirar os anúncios. Me manda a cidade e o estado (ex.: Chapecó, SC) que eu publico na sequência."

func (c *Client) searchGeo(ctx context.Context, token, q, kind string) ([]geoResult, error) {
	var r struct {
		Data []geoResult `json:"data"`
	}
	err := c.call(ctx, http.MethodGet, "/search", token, url.Values{"type": {"adgeolocation"}, "q": {q},
		"location_types": {`["` + kind + `"]`}, "country_code": {"BR"}, "limit": {"10"}}, &r)
	var out []geoResult
	for _, g := range r.Data {
		if g.Type == kind && g.Key != "" && (g.CountryCode == "" || g.CountryCode == "BR") {
			out = append(out, g)
		}
	}
	return out, err
}

// geo resolve o texto do local: cidade (com raio) ou estado. Vazio ou "Brasil" = país inteiro.
// Cidade pedida que a Meta não acha não vira estado nem país: volta erro pedindo a cidade ao cliente.
func (c *Client) geo(ctx context.Context, token, query string, radiusKM int) (map[string]any, []string, error) {
	brasil := map[string]any{"countries": []string{"BR"}}
	if strings.TrimSpace(query) == "" {
		return brasil, []string{"sem local definido: anúncio para o Brasil inteiro"}, nil
	}
	if wholeCountry(query) {
		return brasil, nil, nil
	}
	place, state := splitPlace(query)
	if place != "" {
		cities, err := c.searchGeo(ctx, token, place, "city")
		if err != nil {
			return nil, nil, err // busca fora: o outbox tenta de novo, sem publicar com local errado
		}
		for _, g := range cities {
			if state != "" && normPlace(g.Region) != normPlace(state) {
				continue
			}
			var warnings []string
			city := map[string]any{"key": g.Key}
			if radiusKM > 0 {
				r := min(max(radiusKM, minCityRadiusKM), maxCityRadiusKM)
				if r != radiusKM {
					warnings = append(warnings, fmt.Sprintf("raio ajustado de %d km para %d km (limite da Meta)", radiusKM, r))
				}
				city["radius"], city["distance_unit"] = r, "kilometer"
			}
			return map[string]any{"cities": []any{city}}, warnings, nil
		}
		name, isState := stateName(place)
		if state != "" || !isState {
			return nil, nil, &PublishError{Step: "targeting", Message: fmt.Sprintf("cidade %q não encontrada na Meta", query),
				UserMessage: fmt.Sprintf(cityNotFound, place)}
		}
		state = name // só o nome de um estado ("Santa Catarina")
	}
	regions, err := c.searchGeo(ctx, token, state, "region")
	if err != nil {
		return nil, nil, err
	}
	for _, g := range regions {
		if normPlace(g.Name) == normPlace(state) {
			return map[string]any{"regions": []any{map[string]any{"key": g.Key}}}, nil, nil
		}
	}
	return nil, nil, &PublishError{Step: "targeting", Message: fmt.Sprintf("estado %q não encontrado na Meta", state),
		UserMessage: fmt.Sprintf(cityNotFound, query)}
}

// targeting monta o público: local, idade, gênero e interesses. O Advantage+ de público fica desligado
// (respeita o público dado); posicionamentos ficam automáticos (sem publisher_platforms).
func (c *Client) targeting(ctx context.Context, token string, in PublishInput) (map[string]any, []string, error) {
	geo, warnings, err := c.geo(ctx, token, in.LocationQuery, in.RadiusKM)
	if err != nil {
		return nil, nil, publishError("targeting", err)
	}
	ageMin, ageMax := in.AgeMin, in.AgeMax
	if ageMin < 18 {
		ageMin = 18
	}
	if ageMin > 65 {
		ageMin = 65
	}
	if ageMax == 0 || ageMax > 65 {
		ageMax = 65
	}
	if ageMax < ageMin {
		ageMax = ageMin
	}
	t := map[string]any{"geo_locations": geo, "age_min": ageMin, "age_max": ageMax,
		"targeting_automation": map[string]any{"advantage_audience": 0}}

	var genders []int
	seen := map[int]bool{}
	for _, g := range in.Genders {
		code := map[string]int{"male": 1, "female": 2}[strings.ToLower(strings.TrimSpace(g))]
		if code != 0 && !seen[code] {
			seen[code] = true
			genders = append(genders, code)
		}
	}
	if len(genders) == 1 { // os dois gêneros = todos
		t["genders"] = genders
	}

	var interests []map[string]string
	ids := map[string]bool{}
	for i, name := range in.Interests {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if i >= maxInterests {
			warnings = append(warnings, fmt.Sprintf("só os %d primeiros interesses foram usados", maxInterests))
			break
		}
		var r struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		err := c.call(ctx, http.MethodGet, "/search", token, url.Values{"type": {"adinterest"}, "q": {name}, "limit": {"1"}}, &r)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, publishError("targeting", err)
		}
		if err != nil || len(r.Data) == 0 || r.Data[0].ID == "" {
			warnings = append(warnings, "interesse não encontrado: "+name)
			continue
		}
		if !ids[r.Data[0].ID] {
			ids[r.Data[0].ID] = true
			interests = append(interests, map[string]string{"id": r.Data[0].ID, "name": r.Data[0].Name})
		}
	}
	if len(interests) > 0 {
		t["flexible_spec"] = []any{map[string]any{"interests": interests}}
	}
	return t, warnings, nil
}
