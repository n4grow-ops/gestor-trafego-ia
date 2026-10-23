package meta

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Place é um lugar onde o anúncio aparece: cidade ou estado (Query), ou um pino (Lat/Lng ou endereço).
type Place struct {
	Query    string // "Chapecó, SC", "Santa Catarina", "Brasil"; vazio no pino
	RadiusKM int    // escrito pelo cliente; 0 = não disse
	Pin      bool
	Lat, Lng float64 // pino por coordenada (localização mandada no WhatsApp)
	Address  string  // pino por endereço ("Rua XV de Novembro, 100, Chapecó")
}

// Limites da Meta para pino (custom_locations): de 1 a 80 km.
const (
	minPinRadiusKM     = 1
	maxPinRadiusKM     = 80
	defaultPinRadiusKM = 5
)

var (
	coordRe  = regexp.MustCompile(`(-?\d{1,2}\.\d+)\s*,\s*(-?\d{1,3}\.\d+)`)
	kmRe     = regexp.MustCompile(`(?i)(?:\+|com)?\s*(?:(?:um\s+)?raio\s*(?:de)?\s*)?(\d{1,3})\s*(?:km|quil[oô]metros?)\b(?:\s*(?:de\s+)?raio)?`)
	pinRe    = regexp.MustCompile(`(?i)^\s*(?:um\s+)?(?:pino|ponto|endere[çc]o|localiza[çc][ãa]o)\b\s*(?:em|no|na|:)?\s*`)
	splitRe  = regexp.MustCompile(`(?i)\s*(?:;|\n|\s+e\s+|\s+mais\s+|\s+também\s+)\s*`)
	noiseRe  = regexp.MustCompile(`(?i)^(?:e\s+)?(?:a\s+)?(?:regi[ãa]o|arredores|redondezas|cidades vizinhas|vizinhan[çc]a)$`)
	edgeTrim = " +-,.:"
)

// ParsePlaces lê o texto do local da campanha, com quantos lugares o cliente quiser:
// "Maravilha + 15km e Chapecó, SC 20 km; pino -26.76,-53.17 raio 3 km".
func ParsePlaces(text string) []Place {
	// a vírgula da coordenada não separa lugares
	text = coordRe.ReplaceAllString(text, "$1|$2")
	var out []Place
	for _, seg := range splitRe.Split(text, -1) {
		for _, piece := range splitCommas(seg) {
			if p, ok := parsePlace(piece); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// splitCommas separa "Maravilha, Chapecó, SC" em lugares, juntando o estado à cidade que vem antes.
func splitCommas(seg string) []string {
	if pinRe.MatchString(seg) {
		return []string{seg} // endereço do pino tem vírgula ("Rua XV, 100, Chapecó")
	}
	var out []string
	for _, part := range strings.Split(seg, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// "SC", "Santa Catarina" ou "SC 20 km" logo depois de uma cidade é o estado dela
		bare := strings.Trim(kmRe.ReplaceAllString(part, ""), edgeTrim)
		if _, isState := stateName(bare); isState && len(out) > 0 && !strings.Contains(out[len(out)-1], ",") && !isPinText(out[len(out)-1]) {
			if _, prevState := stateName(strings.Trim(kmRe.ReplaceAllString(out[len(out)-1], ""), edgeTrim)); !prevState {
				out[len(out)-1] += ", " + part
				continue
			}
		}
		out = append(out, part)
	}
	return out
}

func isPinText(s string) bool { return pinRe.MatchString(s) || strings.Contains(s, "|") }

func parsePlace(piece string) (Place, bool) {
	var p Place
	if m := kmRe.FindStringSubmatch(piece); m != nil {
		p.RadiusKM, _ = strconv.Atoi(m[1])
		piece = kmRe.ReplaceAllString(piece, " ")
	}
	piece = strings.Join(strings.Fields(piece), " ")
	if m := coordRe.FindStringSubmatch(strings.ReplaceAll(piece, "|", ",")); m != nil {
		p.Pin = true
		p.Lat, _ = strconv.ParseFloat(m[1], 64)
		p.Lng, _ = strconv.ParseFloat(m[2], 64)
		return p, p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
	}
	if pinRe.MatchString(piece) {
		p.Pin = true
		p.Address = strings.Trim(pinRe.ReplaceAllString(piece, ""), edgeTrim)
		return p, p.Address != ""
	}
	p.Query = strings.Trim(piece, edgeTrim)
	if p.Query == "" || noiseRe.MatchString(p.Query) {
		return Place{}, false
	}
	return p, true
}

// PlacesText descreve os lugares para o cliente, com o raio que vai valer na Meta.
func PlacesText(places []Place, defaultRadius int) string {
	parts := make([]string, 0, len(places))
	for _, p := range places {
		switch {
		case p.Pin:
			where := p.Address
			if where == "" {
				where = fmt.Sprintf("localização enviada (%.5f, %.5f)", p.Lat, p.Lng)
			}
			parts = append(parts, fmt.Sprintf("pino em %s, raio de %d km", where, pinRadius(p.RadiusKM)))
		case wholeCountry(p.Query):
			parts = append(parts, "Brasil inteiro")
		default:
			if r := cityRadius(p.RadiusKM, defaultRadius); r > 0 {
				parts = append(parts, fmt.Sprintf("%s + %d km em volta", p.Query, r))
			} else {
				parts = append(parts, p.Query+" (só a cidade)")
			}
		}
	}
	return strings.Join(parts, "; ")
}

// cityRadius é o raio em volta da cidade: o do lugar, senão o geral; dentro dos limites da Meta (0 = só a cidade).
func cityRadius(own, def int) int {
	r := own
	if r == 0 {
		r = def
	}
	if r <= 0 {
		return 0
	}
	return min(max(r, minCityRadiusKM), maxCityRadiusKM)
}

// pinRadius é o raio do pino: o que o cliente disse para ele (o raio geral é de cidade, grande demais para um ponto).
func pinRadius(own int) int {
	r := own
	if r <= 0 {
		r = defaultPinRadiusKM
	}
	return min(max(r, minPinRadiusKM), maxPinRadiusKM)
}
