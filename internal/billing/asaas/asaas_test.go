package asaas

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type call struct {
	Method, Path, Query string
	Body                map[string]any
}

func fakeAsaas(t *testing.T, routes map[string]string) (*Client, *[]call) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("access_token") != "chave" || r.Header.Get("User-Agent") != "meuanuncio.io" ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("cabeçalhos: %v", r.Header)
		}
		c := call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery}
		json.NewDecoder(r.Body).Decode(&c.Body)
		calls = append(calls, c)
		resp, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"errors":[{"code":"not_found","description":"rota inexistente"}]}`))
			return
		}
		if strings.HasPrefix(resp, "!") {
			w.WriteHeader(http.StatusBadRequest)
			resp = resp[1:]
		}
		w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	c := New("chave", "sandbox")
	c.BaseURL = srv.URL
	return c, &calls
}

func TestNewPicksEnvironment(t *testing.T) {
	if New("k", "").BaseURL != SandboxURL || New("k", "sandbox").BaseURL != SandboxURL {
		t.Fatal("padrão deve ser o sandbox")
	}
	if New("k", "producao").BaseURL != ProductionURL {
		t.Fatal("producao deve usar a produção")
	}
}

func TestFindCustomer(t *testing.T) {
	c, calls := fakeAsaas(t, map[string]string{"GET /customers": `{"data":[{"id":"cus_1","deleted":true},{"id":"cus_2"}]}`})
	id, err := c.FindCustomer(context.Background(), "org-7")
	if err != nil || id != "cus_2" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	if (*calls)[0].Query != "externalReference=org-7" {
		t.Fatalf("consulta = %q", (*calls)[0].Query)
	}
	c2, _ := fakeAsaas(t, map[string]string{"GET /customers": `{"data":[]}`})
	if id, err := c2.FindCustomer(context.Background(), "org-8"); err != nil || id != "" {
		t.Fatalf("sem cliente: id = %q, err = %v", id, err)
	}
}

func TestCreateCustomer(t *testing.T) {
	c, calls := fakeAsaas(t, map[string]string{"POST /customers": `{"id":"cus_9"}`})
	id, err := c.CreateCustomer(context.Background(), Customer{Name: "Pizzaria", CpfCnpj: "11222333000181", Email: "a@b.io",
		MobilePhone: "11988887777", ExternalReference: "org-7"})
	if err != nil || id != "cus_9" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	b := (*calls)[0].Body
	if b["name"] != "Pizzaria" || b["cpfCnpj"] != "11222333000181" || b["externalReference"] != "org-7" || b["notificationDisabled"] != true {
		t.Fatalf("corpo = %v", b)
	}
}

func TestCreateSubscription(t *testing.T) {
	c, calls := fakeAsaas(t, map[string]string{"POST /subscriptions": `{"id":"sub_1","status":"ACTIVE"}`})
	id, err := c.CreateSubscription(context.Background(), Subscription{Customer: "cus_9", BillingType: BillingPix, Value: 397,
		NextDueDate: "2026-10-31", Description: "meuanuncio.io - Intermediário", ExternalReference: "org-7"})
	if err != nil || id != "sub_1" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	b := (*calls)[0].Body
	if b["cycle"] != "MONTHLY" || b["billingType"] != "PIX" || b["value"] != 397.0 || b["nextDueDate"] != "2026-10-31" {
		t.Fatalf("corpo = %v", b)
	}
}

func TestCreateSubscriptionErrorKeepsDescriptionNotKey(t *testing.T) {
	c, _ := fakeAsaas(t, map[string]string{"POST /subscriptions": `!{"errors":[{"code":"invalid_customer","description":"Cliente inválido"}]}`})
	_, err := c.CreateSubscription(context.Background(), Subscription{Customer: "x", BillingType: BillingCard, Value: 1, NextDueDate: "2026-10-31"})
	if err == nil || !strings.Contains(err.Error(), "Cliente inválido") || strings.Contains(err.Error(), "chave") {
		t.Fatalf("erro = %v", err)
	}
}

func TestFirstPaymentURL(t *testing.T) {
	c, calls := fakeAsaas(t, map[string]string{"GET /subscriptions/sub_1/payments": `{"data":[{"id":"pay_1","invoiceUrl":"https://sandbox.asaas.com/i/abc"}]}`})
	link, err := c.FirstPaymentURL(context.Background(), "sub_1")
	if err != nil || link != "https://sandbox.asaas.com/i/abc" {
		t.Fatalf("link = %q, err = %v", link, err)
	}
	if (*calls)[0].Method != http.MethodGet {
		t.Fatal("método errado")
	}
	c2, _ := fakeAsaas(t, map[string]string{"GET /subscriptions/sub_2/payments": `{"data":[]}`})
	if _, err := c2.FirstPaymentURL(context.Background(), "sub_2"); !errors.Is(err, ErrNoPayment) {
		t.Fatalf("sem cobrança: err = %v", err)
	}
}

func TestDeleteSubscription(t *testing.T) {
	c, calls := fakeAsaas(t, map[string]string{"DELETE /subscriptions/sub_1": `{"deleted":true,"id":"sub_1"}`})
	if err := c.DeleteSubscription(context.Background(), "sub_1"); err != nil {
		t.Fatal(err)
	}
	if (*calls)[0].Method != http.MethodDelete || (*calls)[0].Path != "/subscriptions/sub_1" {
		t.Fatalf("chamada = %+v", (*calls)[0])
	}
	// Já removida no Asaas (404): segue, para o cliente poder gerar outra.
	if err := c.DeleteSubscription(context.Background(), "sub_x"); err != nil {
		t.Fatalf("404 = %v", err)
	}
	c2, _ := fakeAsaas(t, map[string]string{"DELETE /subscriptions/sub_2": `!{"errors":[{"code":"x","description":"não pode"}]}`})
	if err := c2.DeleteSubscription(context.Background(), "sub_2"); err == nil {
		t.Fatal("400 deveria dar erro")
	}
}
