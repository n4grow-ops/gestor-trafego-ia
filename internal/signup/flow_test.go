package signup_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
)

const flowAnswer = `{"flow_token":"tok123","full_name":"Ana Souza","email":" Ana@Exemplo.com ","documento":"11.222.333/0001-81",
"business_name":"Pizzaria Boa","segment":"restaurante","segment_other":"","city":"Campinas","uf":"SP","website":"","instagram":"@boa",
"objectives":["mensagens","vendas"],"monthly_budget":"ate_500","ads_experience":"nunca","meta_ad_account":"sim",
"approver":"self","approver_name":"","referral_source":"Instagram","accept_terms":true}`

func TestParseFlowResponse(t *testing.T) {
	sub, err := signup.ParseFlowResponse([]byte(flowAnswer))
	if err != nil {
		t.Fatal(err)
	}
	if sub.Token != "tok123" || sub.Email != "ana@exemplo.com" {
		t.Fatalf("token/e-mail: %+v", sub)
	}
	f := sub.Form
	if f.FullName != "Ana Souza" || f.CNPJ != "11.222.333/0001-81" || f.CPF != "" || f.Segment != "restaurante" || f.UF != "SP" ||
		len(f.Objectives) != 2 || f.Approver != "self" || !f.AcceptTerms {
		t.Fatalf("form: %+v", f)
	}
	if _, err := f.Normalize(); err != nil {
		t.Fatalf("form do formulário deve passar na validação: %v", err)
	}
}

func TestParseFlowResponseDocumentIsCPF(t *testing.T) {
	raw := strings.Replace(flowAnswer, `"11.222.333/0001-81"`, `"529.982.247-25"`, 1)
	sub, err := signup.ParseFlowResponse([]byte(raw))
	if err != nil || sub.Form.CPF != "529.982.247-25" || sub.Form.CNPJ != "" {
		t.Fatalf("%+v %v", sub.Form, err)
	}
}

func TestParseFlowResponseAcceptsStringBools(t *testing.T) {
	raw := strings.Replace(flowAnswer, `"accept_terms":true`, `"accept_terms":"true"`, 1)
	sub, err := signup.ParseFlowResponse([]byte(raw))
	if err != nil || !sub.Form.AcceptTerms {
		t.Fatalf("%+v %v", sub.Form, err)
	}
}

func TestParseFlowResponseErrors(t *testing.T) {
	var ve *signup.ValidationError
	_, err := signup.ParseFlowResponse([]byte(strings.Replace(flowAnswer, `" Ana@Exemplo.com "`, `"ana"`, 1)))
	if !errors.As(err, &ve) || ve.Fields["email"] == "" {
		t.Fatalf("e-mail inválido: %v", err)
	}
	if _, err := signup.ParseFlowResponse([]byte(`{"full_name":"x"}`)); !errors.Is(err, signup.ErrFlowToken) {
		t.Fatalf("sem token: %v", err)
	}
	if _, err := signup.ParseFlowResponse([]byte(`{nope`)); err == nil {
		t.Fatal("json inválido")
	}
}

func TestFlowJSON(t *testing.T) {
	raw, err := signup.FlowJSON("https://x/termos", "https://x/privacidade")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version string `json:"version"`
		Screens []struct {
			ID       string `json:"id"`
			Terminal bool   `json:"terminal"`
		} `json:"screens"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version == "" || len(doc.Screens) != 4 || doc.Screens[0].ID != signup.FlowFirstScreen || !doc.Screens[3].Terminal {
		t.Fatalf("telas: %+v", doc)
	}
	s := string(raw)
	// Toda resposta que o servidor lê sai no payload do "complete".
	for _, field := range []string{"flow_token", "full_name", "email", "documento", "business_name", "segment", "segment_other", "city", "uf",
		"website", "instagram", "objectives", "monthly_budget", "ads_experience", "meta_ad_account", "approver", "approver_name",
		"referral_source", "accept_terms"} {
		if !strings.Contains(s, `"`+field+`":"${`) && field != "flow_token" {
			t.Errorf("campo %s fora do payload", field)
		}
	}
	if !strings.Contains(s, "https://x/termos") || !strings.Contains(s, "https://x/privacidade") {
		t.Error("links dos termos e da privacidade")
	}
	// A Meta recusa título de opção com mais de 30 caracteres.
	var titles struct {
		Screens []struct {
			Layout struct {
				Children []struct {
					DataSource []struct {
						Title string `json:"title"`
					} `json:"data-source"`
				} `json:"children"`
			} `json:"layout"`
		} `json:"screens"`
	}
	json.Unmarshal(raw, &titles)
	n := 0
	for _, sc := range titles.Screens {
		for _, c := range sc.Layout.Children {
			for _, o := range c.DataSource {
				n++
				if len([]rune(o.Title)) > 30 {
					t.Errorf("título longo: %q", o.Title)
				}
			}
		}
	}
	if n < len(signup.Segments)+len(signup.UFs) {
		t.Errorf("opções no formulário = %d", n)
	}
}
