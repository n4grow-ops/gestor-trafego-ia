package meta_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

func TestParsePlaces(t *testing.T) {
	cases := map[string][]meta.Place{
		"Maravilha + 15km": {{Query: "Maravilha", RadiusKM: 15}},
		"Chapecó, SC":      {{Query: "Chapecó, SC"}},
		"Maravilha, Chapecó, SC 20 km e Pinhalzinho":                                 {{Query: "Maravilha"}, {Query: "Chapecó, SC", RadiusKM: 20}, {Query: "Pinhalzinho"}},
		"Joinville raio de 30 km; Santa Catarina":                                    {{Query: "Joinville", RadiusKM: 30}, {Query: "Santa Catarina"}},
		"pino -26.761234,-53.173456 raio 3 km":                                       {{Pin: true, Lat: -26.761234, Lng: -53.173456, RadiusKM: 3}},
		"[localização enviada pelo WhatsApp: pino -26.761234,-53.173456 (Loja)] 2km": {{Pin: true, Lat: -26.761234, Lng: -53.173456, RadiusKM: 2}},
		"pino na Rua XV de Novembro 100, Chapecó 4 km":                               {{Pin: true, Address: "Rua XV de Novembro 100, Chapecó", RadiusKM: 4}},
		"Maravilha e região":                                                         {{Query: "Maravilha"}},
		// cidade com o nome do estado: uma cidade só, não cidade + estado (a Meta recusa o conflito)
		"São Paulo, SP":              {{Query: "São Paulo, SP"}},
		"Rio de Janeiro, RJ + 20 km": {{Query: "Rio de Janeiro, RJ", RadiusKM: 20}},
		"Santa Catarina, Paraná":     {{Query: "Santa Catarina"}, {Query: "Paraná"}},
		"Sao Paulo, São Paulo":       {{Query: "Sao Paulo, São Paulo"}},
	}
	for in, want := range cases {
		if got := meta.ParsePlaces(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParsePlaces(%q)\n got %+v\nwant %+v", in, got, want)
		}
	}
}

func TestPublishManyPlacesAndPin(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	in := baseInput()
	in.LocationQuery, in.RadiusKM = "Chapecó, SC 20 km; Chapecó, SC; pino -26.761234,-53.173456 raio 3 km", 0
	if _, err := c.Publish(context.Background(), tok, in); err != nil {
		t.Fatal(err)
	}
	tg := decode(t, f.one("POST /act_9/adsets").Form.Get("targeting"))
	geo := tg["geo_locations"].(map[string]any)
	cities, _ := geo["cities"].([]any)
	pins, _ := geo["custom_locations"].([]any)
	if len(cities) != 1 || cities[0].(map[string]any)["radius"] != float64(20) || len(pins) != 1 {
		t.Fatalf("geo = %v", geo)
	}
	pin := pins[0].(map[string]any)
	if pin["latitude"] != -26.761234 || pin["radius"] != float64(3) || pin["distance_unit"] != "kilometer" {
		t.Fatalf("pino = %v", pin)
	}
}

func TestPublishGoals(t *testing.T) {
	cases := []struct {
		goal, objective, optimization, destination, cta string
	}{
		{"engajamento", "OUTCOME_ENGAGEMENT", "POST_ENGAGEMENT", "ON_POST", "LEARN_MORE"},
		{"site", "OUTCOME_TRAFFIC", "LINK_CLICKS", "WEBSITE", "LEARN_MORE"},
		{"alcance", "OUTCOME_AWARENESS", "REACH", "", "LEARN_MORE"},
		{"vendas", "OUTCOME_SALES", "CONVERSATIONS", "WHATSAPP", "WHATSAPP_MESSAGE"},
		{"direct", "OUTCOME_ENGAGEMENT", "CONVERSATIONS", "INSTAGRAM_DIRECT", "INSTAGRAM_MESSAGE"},
		{"whatsapp", "OUTCOME_ENGAGEMENT", "CONVERSATIONS", "WHATSAPP", "WHATSAPP_MESSAGE"},
	}
	for _, tc := range cases {
		t.Run(tc.goal, func(t *testing.T) {
			f, c := newFake(t)
			f.basics()
			in := baseInput()
			in.Goal, in.LinkURL, in.InstagramActorID, in.InstagramUser, in.PageWhatsApp = tc.goal, "https://loja.com.br", "IG1", "loja", true
			in.Copies[0].CTA = "LEARN_MORE"
			if tc.goal == "vendas" || tc.goal == "whatsapp" {
				in.Copies[0].CTA = "WHATSAPP_MESSAGE"
			}
			if _, err := c.Publish(context.Background(), tok, in); err != nil {
				t.Fatal(err)
			}
			camp := f.one("POST /act_9/campaigns").Form
			set := f.one("POST /act_9/adsets").Form
			if camp.Get("objective") != tc.objective || set.Get("optimization_goal") != tc.optimization || set.Get("destination_type") != tc.destination {
				t.Fatalf("campanha=%v conjunto=%v", camp.Get("objective"), set)
			}
			spec := f.one("POST /act_9/adcreatives").Form.Get("object_story_spec")
			if !strings.Contains(spec, `"type":"`+tc.cta+`"`) {
				t.Fatalf("criativo = %s", spec)
			}
		})
	}
}

func TestPublishSiteWithoutLinkIsClientSide(t *testing.T) {
	_, c := newFake(t)
	in := baseInput()
	in.Goal = "site"
	if _, err := c.Publish(context.Background(), tok, in); err == nil || !strings.Contains(err.Error(), "sem link") {
		t.Fatalf("err = %v", err)
	}
}

// Caso real (04/11): "São Paulo, SP" virava a cidade e o estado de SP juntos e a Meta recusava o conjunto
// com "Algumas localizações estão em conflito".
func TestPublishCityNamedLikeItsState(t *testing.T) {
	for in, want := range map[string]string{"São Paulo, SP": "cities", "São Paulo; SP": "regions"} {
		f, c := newFake(t)
		f.basics()
		bi := baseInput()
		bi.LocationQuery, bi.RadiusKM = in, 0
		if _, err := c.Publish(context.Background(), tok, bi); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		geo := decode(t, f.one("POST /act_9/adsets").Form.Get("targeting"))["geo_locations"].(map[string]any)
		if len(geo) != 1 || len(geo[want].([]any)) != 1 {
			t.Fatalf("%q: geo = %v (esperava só %s)", in, geo, want)
		}
	}
}
