package meta_test

import (
	"context"
	"encoding/json"
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

func TestPublishLeadFormCustom(t *testing.T) {
	f, c := newFake(t)
	f.leadFormBasics()
	in := leadFormInput()
	in.Form = &meta.LeadFormSpec{
		Qualified: true, Headline: "Ganhe 10% na primeira compra", Bullets: []string{"Sem custo", "Resposta em 1 dia"},
		IntroButton: "Quero", QuestionsHeadline: "Conte um pouco",
		Questions: []meta.LeadFormQuestion{{Type: "FULL_NAME"}, {Type: "WHATSAPP_NUMBER"},
			{Type: "CUSTOM", Label: "Melhor horário?", Options: []string{"Manhã", "Tarde"}}, {Type: "CUSTOM", Label: "Seu caso"}},
		ThanksTitle: "Recebemos!", ThanksText: "Te chamamos hoje.", ButtonAction: "whatsapp", ButtonPhone: "11999990000",
		PrivacyURL: "https://cliente.com/privacidade",
	}
	if _, err := c.Publish(context.Background(), tok, in); err != nil {
		t.Fatal(err)
	}
	form := f.one("POST /P1/leadgen_forms").Form
	if form.Get("is_optimized_for_quality") != "true" || form.Get("question_page_custom_headline") != "Conte um pouco" ||
		!strings.Contains(form.Get("privacy_policy"), "cliente.com/privacidade") || !strings.Contains(form.Get("name"), "formulário ") {
		t.Fatalf("formulário = %v", form)
	}
	var qs []map[string]any
	if err := json.Unmarshal([]byte(form.Get("questions")), &qs); err != nil {
		t.Fatal(err)
	}
	if len(qs) != 4 || qs[1]["type"] != "WHATSAPP_NUMBER" || qs[2]["label"] != "Melhor horário?" || qs[2]["key"] != "pergunta_1" ||
		len(qs[2]["options"].([]any)) != 2 || qs[3]["options"] != nil || qs[0]["label"] != nil {
		t.Fatalf("perguntas = %v", qs)
	}
	card := decode(t, form.Get("context_card"))
	if card["title"] != "Ganhe 10% na primeira compra" || card["style"] != "LIST_STYLE" || card["button_text"] != "Quero" {
		t.Fatalf("abertura = %v", card)
	}
	ty := decode(t, form.Get("thank_you_page"))
	if ty["title"] != "Recebemos!" || ty["body"] != "Te chamamos hoje." || ty["button_type"] != "VIEW_WEBSITE" ||
		ty["website_url"] != "https://wa.me/5511999990000" {
		t.Fatalf("tela final = %v", ty)
	}
}

func TestPublishLeadFormCallWithoutPhoneFallsBackToSite(t *testing.T) {
	f, c := newFake(t)
	f.leadFormBasics()
	in := leadFormInput()
	in.Form = &meta.LeadFormSpec{ThanksTitle: "Valeu", ButtonAction: "call"}
	res, err := c.Publish(context.Background(), tok, in)
	if err != nil {
		t.Fatal(err)
	}
	form := f.one("POST /P1/leadgen_forms").Form
	if form.Get("context_card") != "" {
		t.Fatal("abertura sem pedido")
	}
	ty := decode(t, form.Get("thank_you_page"))
	if ty["button_type"] != "VIEW_WEBSITE" || ty["website_url"] != "https://www.facebook.com/P1" || len(res.Warnings) == 0 {
		t.Fatalf("tela final = %v avisos = %v", ty, res.Warnings)
	}
	if !strings.Contains(form.Get("questions"), "FULL_NAME") {
		t.Fatal("sem perguntas deve usar o padrão")
	}
}

// Termos de cadastro recusados no conjunto (caso real 1815089): erro do cliente, com o erro da Meta, a solução
// e o link de aceite já na Página dele.
func TestPublishLeadTermsOnAdSetIsClientSide(t *testing.T) {
	f, c := newFake(t)
	f.leadFormBasics()
	f.onCode("POST /act_9/adsets", 400, `{"error":{"message":"Invalid parameter","code":100,"error_subcode":1815089,"error_user_title":"Os Termos de Serviço não foram aceitos","error_user_msg":"Você não pode veicular anúncios de cadastros até sua Página do Facebook aceitar os Termos de Serviço de Geração de Cadastros do Facebook."}}`)
	in := leadFormInput()
	_, err := c.Publish(context.Background(), tok, in)
	var pe *meta.PublishError
	if !errors.As(err, &pe) || !pe.ClientSide {
		t.Fatalf("erro = %+v", err)
	}
	for _, want := range []string{"*O que aconteceu:*", "*Erro da Meta:* Você não pode veicular", "*Como resolver:*",
		"https://www.facebook.com/ads/leadgen/tos?page_id=" + in.PageID} {
		if !strings.Contains(pe.UserMessage, want) {
			t.Errorf("mensagem sem %q: %s", want, pe.UserMessage)
		}
	}
	if strings.Contains(pe.UserMessage, "equipe já foi avisada") {
		t.Errorf("erro do cliente com texto genérico: %s", pe.UserMessage)
	}
}

// Casos reais 1815316 e 1856030: no anúncio de formulário o link do criativo é externo (nunca a Página) e o
// botão não é CONTACT_US.
func TestPublishLeadFormCreativeLinkAndCTA(t *testing.T) {
	for _, site := range []string{"", "https://www.facebook.com/minhapagina", "https://instagram.com/loja"} {
		f, c := newFake(t)
		f.leadFormBasics()
		in := leadFormInput()
		in.LinkURL = site
		in.Copies[0].CTA = "CONTACT_US"
		if _, err := c.Publish(context.Background(), tok, in); err != nil {
			t.Fatal(err)
		}
		spec := decode(t, f.one("POST /act_9/adcreatives").Form.Get("object_story_spec"))
		ld := spec["link_data"].(map[string]any)
		cta := ld["call_to_action"].(map[string]any)
		if ld["link"] != "https://meuanuncio.io/privacidade" || cta["type"] != "SIGN_UP" ||
			cta["value"].(map[string]any)["link"] != "https://meuanuncio.io/privacidade" {
			t.Errorf("site %q: link_data = %v", site, ld)
		}
	}
	f, c := newFake(t)
	f.leadFormBasics()
	in := leadFormInput()
	in.LinkURL = "https://metodov3.com.br"
	if _, err := c.Publish(context.Background(), tok, in); err != nil {
		t.Fatal(err)
	}
	ld := decode(t, f.one("POST /act_9/adcreatives").Form.Get("object_story_spec"))["link_data"].(map[string]any)
	if ld["link"] != "https://metodov3.com.br" {
		t.Errorf("site do cliente: link = %v", ld["link"])
	}
}
