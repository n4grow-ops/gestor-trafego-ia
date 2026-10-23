package meta_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// Caso real (Método v3): "anúncio de seguidores" virava conversa no WhatsApp. Agora é tráfego para o
// perfil do Instagram, otimizado para visitas ao perfil e só no Instagram.
func TestPublishFollowersGoesToInstagramProfile(t *testing.T) {
	f, c := newFake(t)
	f.basics()
	in := baseInput()
	in.Objective, in.Followers, in.InstagramActorID, in.InstagramUser = "OUTCOME_TRAFFIC", true, "IG1", "@metodov3"
	if _, err := c.Publish(context.Background(), tok, in); err != nil {
		t.Fatal(err)
	}
	set := f.one("POST /act_9/adsets").Form
	if set.Get("destination_type") != "INSTAGRAM_PROFILE" || set.Get("optimization_goal") != "VISIT_INSTAGRAM_PROFILE" {
		t.Fatalf("conjunto = %v", set)
	}
	tg := decode(t, set.Get("targeting"))
	if pp, _ := tg["publisher_platforms"].([]any); len(pp) != 1 || pp[0] != "instagram" || tg["facebook_positions"] != nil {
		t.Fatalf("targeting = %v", tg)
	}
	ld := decode(t, f.one("POST /act_9/adcreatives").Form.Get("object_story_spec"))["link_data"].(map[string]any)
	cta := ld["call_to_action"].(map[string]any)
	if ld["link"] != "https://www.instagram.com/metodov3/" || cta["type"] != "VIEW_INSTAGRAM_PROFILE" {
		t.Fatalf("link_data = %v", ld)
	}
}

func TestPublishFollowersWithoutInstagramIsClientSide(t *testing.T) {
	_, c := newFake(t)
	in := baseInput()
	in.Objective, in.Followers = "OUTCOME_TRAFFIC", true
	_, err := c.Publish(context.Background(), tok, in)
	var pe *meta.PublishError
	if !errors.As(err, &pe) || !pe.ClientSide || !strings.Contains(pe.UserMessage, "Instagram") {
		t.Fatalf("erro = %+v", err)
	}
}
