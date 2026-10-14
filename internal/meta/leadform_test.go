package meta_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

func leadFormInput() meta.PublishInput {
	in := baseInput()
	in.Objective, in.LeadForm, in.FormPrivacyURL = "OUTCOME_LEADS", true, "https://meuanuncio.io/privacidade"
	in.Copies[0].CTA = "WHATSAPP_MESSAGE" // não vale para formulário: vira SIGN_UP
	return in
}

func (f *fakeGraph) leadFormBasics() {
	f.basics()
	f.on("GET /P1", `{"access_token":"PAGETOKEN"}`)
	f.on("GET /P1/leadgen_forms", `{"data":[]}`)
	f.on("POST /P1/leadgen_forms", `{"id":"FORM1"}`)
}

func TestPublishLeadForm(t *testing.T) {
	f, c := newFake(t)
	f.leadFormBasics()
	res, err := c.Publish(context.Background(), tok, leadFormInput())
	if err != nil {
		t.Fatal(err)
	}
	if res.CampaignID != "CAMP" || len(res.AdIDs) != 1 {
		t.Fatalf("resultado = %+v", res)
	}
	form := f.one("POST /P1/leadgen_forms").Form
	if !strings.Contains(form.Get("questions"), "FULL_NAME") || !strings.Contains(form.Get("questions"), "PHONE") ||
		!strings.Contains(form.Get("privacy_policy"), "meuanuncio.io/privacidade") || form.Get("name") == "" {
		t.Fatalf("formulário = %v", form)
	}
	if f.one("POST /act_9/campaigns").Form.Get("objective") != "OUTCOME_LEADS" {
		t.Fatal("objetivo da campanha")
	}
	set := f.one("POST /act_9/adsets").Form
	if set.Get("optimization_goal") != "LEAD_GENERATION" || set.Get("destination_type") != "ON_AD" {
		t.Fatalf("conjunto = %v", set)
	}
	if po := decode(t, set.Get("promoted_object")); po["page_id"] != "P1" || po["whatsapp_phone_number"] != nil {
		t.Fatalf("promoted_object = %v", po)
	}
	ld := decode(t, f.one("POST /act_9/adcreatives").Form.Get("object_story_spec"))["link_data"].(map[string]any)
	cta := ld["call_to_action"].(map[string]any)
	if cta["type"] != "SIGN_UP" || cta["value"].(map[string]any)["lead_gen_form_id"] != "FORM1" {
		t.Fatalf("cta = %v", cta)
	}
}

func TestPublishLeadFormReusesExisting(t *testing.T) {
	f, c := newFake(t)
	f.leadFormBasics()
	f.on("GET /P1/leadgen_forms", `{"data":[{"id":"OLD","name":"Pizzaria - formulário","status":"ACTIVE"}]}`)
	if _, err := c.Publish(context.Background(), tok, leadFormInput()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.calls("POST /P1/leadgen_forms")); n != 0 {
		t.Fatalf("criou formulário de novo (%d)", n)
	}
	ld := decode(t, f.one("POST /act_9/adcreatives").Form.Get("object_story_spec"))["link_data"].(map[string]any)
	if ld["call_to_action"].(map[string]any)["value"].(map[string]any)["lead_gen_form_id"] != "OLD" {
		t.Fatalf("criativo = %v", ld)
	}
}

func TestPublishLeadFormTermsNotAccepted(t *testing.T) {
	f, c := newFake(t)
	f.leadFormBasics()
	f.onCode("POST /P1/leadgen_forms", 400, `{"error":{"message":"You must accept the Lead Ads Terms of Service","code":100,"error_subcode":1692035}}`)
	_, err := c.Publish(context.Background(), tok, leadFormInput())
	var pe *meta.PublishError
	if err == nil || !errors.As(err, &pe) || !strings.Contains(pe.UserMessage, "Termos de Anúncios de Cadastro") {
		t.Fatalf("erro = %v", err)
	}
	if len(f.calls("POST /act_9/campaigns")) != 0 {
		t.Fatal("campanha criada sem formulário")
	}
}
