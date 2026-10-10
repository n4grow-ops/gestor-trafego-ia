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

// splitPlace separa "Chapecó, SC" (ou "Chapecó - SC", "Chapecó/SC") em cidade e estado.
func splitPlace(q string) (place, state string) {
	q = strings.TrimSpace(q)
	for _, sep := range []string{",", " - ", "/"} {
		if i := strings.LastIndex(q, sep); i > 0 {
			place, state = strings.TrimSpace(q[:i]), strings.TrimSpace(q[i+len(sep):])
			if name, ok := ufNames[strings.ToUpper(state)]; ok {
				return place, name
			}
			for _, name := range ufNames {
				if strings.EqualFold(name, state) {
					return place, name
				}
			}
			return place, "" // depois do separador não é estado ("Chapecó, Brasil")
		}
	}
	if name, ok := ufNames[strings.ToUpper(q)]; ok {
		return "", name
	}
	return q, ""
}

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

// geo resolve o texto do local: cidade (com raio), senão estado, senão Brasil inteiro.
func (c *Client) geo(ctx context.Context, token, query string, radiusKM int) (map[string]any, []string, error) {
	var warnings []string
	brasil := map[string]any{"countries": []string{"BR"}}
	if strings.TrimSpace(query) == "" {
		return brasil, []string{"sem local definido: anúncio para o Brasil inteiro"}, nil
	}
	place, state := splitPlace(query)
	// Erro de busca não derruba a publicação: cai para um local mais amplo com aviso.
	soft := func(err error) error {
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		warnings = append(warnings, "busca de local falhou: "+err.Error())
		return nil
	}
	if place != "" {
		cities, err := c.searchGeo(ctx, token, place, "city")
		if err := soft(err); err != nil {
			return nil, nil, err
		}
		for _, g := range cities {
			if state != "" && !strings.EqualFold(g.Region, state) {
				continue
			}
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
	}
	regionQ := state
	if regionQ == "" {
		regionQ = place
	}
	regions, err := c.searchGeo(ctx, token, regionQ, "region")
	if err := soft(err); err != nil {
		return nil, nil, err
	}
	if len(regions) > 0 {
		warnings = append(warnings, fmt.Sprintf("local aproximado: %q virou o estado %s", query, regions[0].Name))
		return map[string]any{"regions": []any{map[string]any{"key": regions[0].Key}}}, warnings, nil
	}
	warnings = append(warnings, fmt.Sprintf("local %q não encontrado: anúncio para o Brasil inteiro", query))
	return brasil, warnings, nil
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
