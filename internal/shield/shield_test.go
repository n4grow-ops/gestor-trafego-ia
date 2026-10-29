package shield_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/shield"
)

// graph é uma Graph API simulada: cada caminho devolve o JSON cadastrado; conta as chamadas.
type graph struct {
	mu    sync.Mutex
	resp  map[string]string // caminho → corpo
	code  map[string]int    // caminho → status de erro (0 = 200)
	calls map[string]int
}

func newGraph(t *testing.T) (*graph, *meta.Client) {
	g := &graph{resp: map[string]string{}, code: map[string]int{}, calls: map[string]int{}}
	g.ok()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	c := meta.New("123", "segredo", "", "")
	c.GraphURL = srv.URL
	return g, c
}

func (g *graph) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/v23.0")
	g.mu.Lock()
	g.calls[p]++
	body, code := g.resp[p], g.code[p]
	g.mu.Unlock()
	if code != 0 {
		w.WriteHeader(code)
	} else if body == "" {
		w.WriteHeader(404)
		body = `{"error":{"message":"sem rota","code":100}}`
	}
	fmt.Fprint(w, body)
}

func (g *graph) set(path, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resp[path] = body
	delete(g.code, path)
}

func (g *graph) fail(path string, status int, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resp[path], g.code[path] = body, status
}

func (g *graph) count(path string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[path]
}

const card = `"funding_source":"FS1","funding_source_details":{"id":"FS1","display_string":"Visa *1234","type":1},`

// account monta o corpo de /act_9 (a mesma rota serve a saúde e o pagamento).
func account(status, reason int, currency, funding string) string {
	return fmt.Sprintf(`{"id":"act_9","name":"Loja","currency":%q,"account_status":%d,"disable_reason":%d,
		"business":{"id":"B1","name":"Loja LTDA"},"is_prepay_account":false,%s"spend_cap":"0","amount_spent":"0"}`,
		currency, status, reason, funding)
}

// ok deixa tudo saudável.
func (g *graph) ok() {
	g.set("/debug_token", `{"data":{"is_valid":true,"scopes":["ads_management","ads_read","pages_show_list","pages_read_engagement"],"expires_at":0,"data_access_expires_at":0}}`)
	g.set("/act_9", account(1, 0, "BRL", card))
	g.set("/me/accounts", `{"data":[{"id":"P1","tasks":["ADVERTISE"],"is_published":true,"instagram_business_account":{"id":"IG1"}}]}`)
	g.set("/B1", `{"verification_status":"verified"}`)
}

var now = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

func input() shield.Input {
	return shield.Input{Token: "TOK", AdAccountID: "act_9", PageID: "P1", InstagramID: "IG1", NeedsInstagram: true,
		Currency: "BRL", DailyBudgetBRL: 30, Now: now}
}

func item(t *testing.T, r shield.Report, key string) shield.Item {
	t.Helper()
	for _, it := range r.Items {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("sem item %q em %+v", key, r.Items)
	return shield.Item{}
}

func TestEverythingOK(t *testing.T) {
	_, c := newGraph(t)
	r := shield.Check(context.Background(), c, input())
	if r.Worst() != shield.OK || len(r.Problems()) != 0 {
		t.Fatalf("%+v", r)
	}
	if s := r.Summary(); !strings.Contains(s, "tudo certo") {
		t.Fatalf("resumo = %q", s)
	}
	for _, k := range []string{shield.KeyToken, shield.KeyAccount, shield.KeyPayment, shield.KeyPage, shield.KeyInstagram, shield.KeyBusiness, shield.KeyCurrency} {
		item(t, r, k)
	}
}

func TestAccountStates(t *testing.T) {
	cases := []struct {
		name           string
		status, reason int
		level          shield.Level
		contains       string
	}{
		{"desativada por política", 2, 1, shield.Blocks, "desativou"},
		{"encerrada", 101, 0, shield.Blocks, "encerrad"},
		{"encerrando", 100, 0, shield.Blocks, "encerrad"},
		{"análise de risco", 7, 0, shield.Attention, "análise de risco"},
		{"pagamento pendente", 3, 0, shield.Attention, "pagamento"},
		{"carência", 9, 0, shield.Attention, "pagamento"},
		{"acerto pendente", 8, 0, shield.Attention, "pagamento"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, c := newGraph(t)
			g.set("/act_9", account(tc.status, tc.reason, "BRL", card))
			r := shield.Check(context.Background(), c, input())
			it := item(t, r, shield.KeyAccount)
			if it.Level != tc.level || !strings.Contains(strings.ToLower(it.Message), tc.contains) || len(it.Steps) == 0 {
				t.Fatalf("item = %+v", it)
			}
			if tc.level == shield.Blocks && r.Worst() != shield.Blocks {
				t.Fatal("conta desativada deve bloquear")
			}
		})
	}
}

func TestDisabledAccountReasonIsTranslated(t *testing.T) {
	g, c := newGraph(t)
	g.set("/act_9", account(2, 1, "BRL", card))
	it := item(t, shield.Check(context.Background(), c, input()), shield.KeyAccount)
	if !strings.Contains(it.Message, "políticas de publicidade") {
		t.Fatalf("motivo não traduzido: %q", it.Message)
	}
}

func TestCurrencyNotBRLBlocks(t *testing.T) {
	g, c := newGraph(t)
	g.set("/act_9", account(1, 0, "USD", card))
	r := shield.Check(context.Background(), c, input())
	if it := item(t, r, shield.KeyCurrency); it.Level != shield.Blocks || !strings.Contains(it.Message, "USD") {
		t.Fatalf("%+v", it)
	}
	// Quem publica trata a moeda sozinho: sem ela, o relatório volta a ficar sem bloqueio.
	if len(r.Without(shield.KeyCurrency).Blockers()) != 0 {
		t.Fatal("Without(moeda) ainda bloqueia")
	}
}

func TestPaymentStates(t *testing.T) {
	t.Run("sem forma de pagamento", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/act_9", account(1, 0, "BRL", ""))
		it := item(t, shield.Check(context.Background(), c, input()), shield.KeyPayment)
		if it.Level != shield.Attention || !strings.Contains(it.Message, "forma de pagamento") {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("saldo pré-pago menor que um dia", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/act_9", `{"id":"act_9","currency":"BRL","account_status":1,"is_prepay_account":true,"funding_source":"F",
			"funding_source_details":{"id":"F","display_string":"Saldo disponível (R$10,00 BRL)","type":20},"spend_cap":"0","amount_spent":"0"}`)
		it := item(t, shield.Check(context.Background(), c, input()), shield.KeyPayment)
		if it.Level != shield.Attention || !strings.Contains(it.Message, "saldo") {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("limite de gastos quase no fim", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/act_9", `{"id":"act_9","currency":"BRL","account_status":1,`+card+`"spend_cap":"100000","amount_spent":"99000"}`)
		it := item(t, shield.Check(context.Background(), c, input()), shield.KeyCap)
		if it.Level != shield.Attention || len(it.Steps) == 0 {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("limite com folga", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/act_9", `{"id":"act_9","currency":"BRL","account_status":1,`+card+`"spend_cap":"1000000","amount_spent":"1000"}`)
		if it := item(t, shield.Check(context.Background(), c, input()), shield.KeyCap); it.Level != shield.OK {
			t.Fatalf("%+v", it)
		}
	})
}

func TestPageAndInstagram(t *testing.T) {
	t.Run("página sumiu", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/me/accounts", `{"data":[{"id":"OUTRA","tasks":["ADVERTISE"]}]}`)
		r := shield.Check(context.Background(), c, input())
		if it := item(t, r, shield.KeyPage); it.Level != shield.Blocks || len(it.Steps) == 0 {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("sem papel de anunciante", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/me/accounts", `{"data":[{"id":"P1","tasks":["ANALYZE","CREATE_CONTENT"],"is_published":true,"instagram_business_account":{"id":"IG1"}}]}`)
		if it := item(t, shield.Check(context.Background(), c, input()), shield.KeyPage); it.Level != shield.Blocks || !strings.Contains(it.Message, "permissão") {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("página despublicada", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/me/accounts", `{"data":[{"id":"P1","tasks":["ADVERTISE"],"is_published":false,"instagram_business_account":{"id":"IG1"}}]}`)
		if it := item(t, shield.Check(context.Background(), c, input()), shield.KeyPage); it.Level != shield.Attention {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("instagram desvinculado", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/me/accounts", `{"data":[{"id":"P1","tasks":["ADVERTISE"],"is_published":true}]}`)
		if it := item(t, shield.Check(context.Background(), c, input()), shield.KeyInstagram); it.Level != shield.Attention || !strings.Contains(it.Message, "vinculado") {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("sem instagram e a campanha não usa", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/me/accounts", `{"data":[{"id":"P1","tasks":["ADVERTISE"],"is_published":true}]}`)
		in := input()
		in.InstagramID, in.NeedsInstagram = "", false
		for _, it := range shield.Check(context.Background(), c, in).Items {
			if it.Key == shield.KeyInstagram {
				t.Fatalf("não deveria comentar o Instagram: %+v", it)
			}
		}
	})
	t.Run("sem instagram e a campanha usa", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/me/accounts", `{"data":[{"id":"P1","tasks":["ADVERTISE"],"is_published":true}]}`)
		in := input()
		in.InstagramID = ""
		if it := item(t, shield.Check(context.Background(), c, in), shield.KeyInstagram); it.Level != shield.Attention {
			t.Fatalf("%+v", it)
		}
	})
}

func TestTokenProblems(t *testing.T) {
	t.Run("token inválido: só o item da conexão e nenhuma outra leitura", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/debug_token", `{"data":{"is_valid":false}}`)
		r := shield.Check(context.Background(), c, input())
		if len(r.Items) != 1 || r.Items[0].Level != shield.Blocks || r.Items[0].Key != shield.KeyToken {
			t.Fatalf("%+v", r.Items)
		}
		if g.count("/act_9")+g.count("/me/accounts") != 0 {
			t.Fatal("leu a conta com token inválido")
		}
	})
	t.Run("token vencido", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/debug_token", fmt.Sprintf(`{"data":{"is_valid":true,"expires_at":%d}}`, now.Add(-time.Hour).Unix()))
		if r := shield.Check(context.Background(), c, input()); r.Worst() != shield.Blocks {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("erro 190 numa leitura vira conexão vencida", func(t *testing.T) {
		g, c := newGraph(t)
		g.fail("/act_9", 400, `{"error":{"message":"Error validating access token","code":190}}`)
		r := shield.Check(context.Background(), c, input())
		if it := item(t, r, shield.KeyToken); it.Level != shield.Blocks {
			t.Fatalf("%+v", r.Items)
		}
	})
	t.Run("sem ads_management", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/debug_token", `{"data":{"is_valid":true,"scopes":["ads_read","pages_show_list"]}}`)
		it := item(t, shield.Check(context.Background(), c, input()), shield.KeyToken)
		if it.Level != shield.Blocks || !strings.Contains(it.Message, "permissão") {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("sem lista de permissões não acusa falta", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/debug_token", `{"data":{"is_valid":true}}`)
		if r := shield.Check(context.Background(), c, input()); r.Worst() != shield.OK {
			t.Fatalf("%+v", r.Items)
		}
	})
	t.Run("vence em poucos dias", func(t *testing.T) {
		g, c := newGraph(t)
		g.set("/debug_token", fmt.Sprintf(`{"data":{"is_valid":true,"expires_at":%d}}`, now.Add(48*time.Hour).Unix()))
		if it := item(t, shield.Check(context.Background(), c, input()), shield.KeyToken); it.Level != shield.Attention || !strings.Contains(it.Message, "vence em") {
			t.Fatalf("%+v", it)
		}
	})
}

func TestBusinessVerification(t *testing.T) {
	for status, level := range map[string]shield.Level{"verified": shield.OK, "not_verified": shield.Attention, "pending": shield.Attention, "rejected": shield.Attention} {
		g, c := newGraph(t)
		g.set("/B1", fmt.Sprintf(`{"verification_status":%q}`, status))
		if it := item(t, shield.Check(context.Background(), c, input()), shield.KeyBusiness); it.Level != level {
			t.Errorf("%s: %+v", status, it)
		}
	}
	// Sem permissão de business: não acusa nada (o item some).
	g, c := newGraph(t)
	g.fail("/B1", 403, `{"error":{"message":"perm","code":200}}`)
	for _, it := range shield.Check(context.Background(), c, input()).Items {
		if it.Key == shield.KeyBusiness {
			t.Fatalf("item de business sem leitura: %+v", it)
		}
	}
}

func TestRecentRejectionIsTranslatedAndOldOnesIgnored(t *testing.T) {
	_, c := newGraph(t)
	in := input()
	in.Rejected = []shield.Rejected{{At: now.Add(-48 * time.Hour), Reason: "Ad rejected: too much text in image"}}
	it := item(t, shield.Check(context.Background(), c, in), shield.KeyRejected)
	if it.Level != shield.Attention || !strings.Contains(it.Message, "texto demais") {
		t.Fatalf("%+v", it)
	}
	in.Rejected = []shield.Rejected{{At: now.Add(-30 * 24 * time.Hour), Reason: "x"}}
	for _, it := range shield.Check(context.Background(), c, in).Items {
		if it.Key == shield.KeyRejected {
			t.Fatal("reprovação de um mês atrás não é recente")
		}
	}
}

func TestSummaryCountsPoints(t *testing.T) {
	g, c := newGraph(t)
	g.set("/act_9", account(1, 0, "BRL", ""))
	g.set("/B1", `{"verification_status":"not_verified"}`)
	s := shield.Check(context.Background(), c, input()).Summary()
	// a verificação do Business não conta para o cliente (fica só no painel)
	if !strings.Contains(s, "um ponto para resolver") || !strings.Contains(s, "Pagamento") || !strings.Contains(s, "Como resolver") ||
		strings.Contains(s, "Business") {
		t.Fatalf("resumo = %q", s)
	}
}

func TestReadFailureIsAttentionNotBlock(t *testing.T) {
	g, c := newGraph(t)
	g.fail("/act_9", 500, `{"error":{"message":"fora do ar","code":2}}`)
	r := shield.Check(context.Background(), c, input())
	if r.Worst() == shield.Blocks {
		t.Fatalf("falha de leitura não pode bloquear: %+v", r.Items)
	}
	if it := item(t, r, shield.KeyAccount); it.Level != shield.Attention {
		t.Fatalf("%+v", it)
	}
}
