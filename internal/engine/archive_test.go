package engine_test

import (
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// limitedHarness: plano free (3 projetos) já cheio com A, B e C.
func limitedHarness(t *testing.T) *harness {
	h := newHarness(t, "owner")
	org := h.ident().OrgID
	_, err := h.pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_code, status) VALUES ($1, 'free', 'active')`, org)
	h.must(err)
	for _, n := range []string{"A", "B", "C"} {
		_, _, err := h.st.CreateOrReopenProject(ctx, org, n, contract.Fields{})
		h.must(err)
	}
	return h
}

const chaleTurn = `{"intent":"new_project","project":"Chalé","fields":{"offer":"venda de chalé","audience":"homens de 40 a 56 anos","location":"Maravilha","daily_budget_brl":20,"placements":["feed","stories"]},"reply":"Vamos lá!"}`

// Caso real (conversa 51): a Mia oferecia arquivar um projeto, o cliente respondia "Pode arquivar um" e
// "Arquivar", e voltava a mesma mensagem do limite em loop. Agora ela lista os projetos, o cliente escolhe,
// ela arquiva e segue com o pedido novo sem perguntar tudo de novo.
func TestProjectLimitArchiveThenContinues(t *testing.T) {
	h := limitedHarness(t)
	h.att.Responses = []string{chaleTurn}
	h.mgr.Responses = []string{validProposal}
	h.say("Quero subir um anúncio pra venda de um chalé em Maravilha, homens de 40 a 56, feed e stories, 20 por dia")
	ask := h.wa.last().Text
	for _, want := range []string{"até 3 projetos", "1. ", "2. ", "3. ", "número"} {
		if !strings.Contains(ask, want) {
			t.Fatalf("pedido de escolha sem %q: %q", want, ask)
		}
	}

	h.say("Pode arquivar um")
	again := h.wa.last().Text
	if again == ask || !strings.Contains(again, "Qual") {
		t.Fatalf("resposta ambígua devia perguntar qual (sem repetir o limite): %q", again)
	}
	if h.att.Calls() != 1 {
		t.Fatalf("escolha do arquivamento não deve chamar a IA (chamadas = %d)", h.att.Calls())
	}

	// a lista vem do mais recente para o mais antigo: C, B, A
	h.say("2")
	var archived string
	h.must(h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE stage = 'archived'`).Scan(&archived))
	if archived != "B" {
		t.Fatalf("arquivou %q, devia ser B", archived)
	}
	msgs := strings.Join(h.wa.to(chatID), "\n")
	if !strings.Contains(msgs, "Arquivei o projeto *B*") {
		t.Fatalf("sem confirmação do arquivamento: %q", h.wa.last().Text)
	}
	c := h.conv()
	if c.State != store.StateAwaitingProposal || c.Fields.Offer == nil || *c.Fields.Offer != "venda de chalé" {
		t.Fatalf("pedido do chalé não seguiu: estado=%s campos=%+v", c.State, c.Fields)
	}
	if h.pendingJobs(engine.JobPropose) != 1 {
		t.Fatal("proposta do chalé não foi pedida")
	}
}

// Escolha pelo nome também vale; assunto diferente sai da escolha e segue a conversa normal.
func TestProjectLimitArchiveByNameAndEscape(t *testing.T) {
	h := limitedHarness(t)
	h.att.Responses = []string{chaleTurn}
	h.say("quero anunciar um chalé")
	h.say("arquiva o projeto A")
	var archived string
	h.must(h.pool.QueryRow(ctx, `SELECT name FROM projects WHERE stage = 'archived'`).Scan(&archived))
	if archived != "A" {
		t.Fatalf("arquivou %q", archived)
	}

	h2 := limitedHarness(t)
	h2.att.Responses = []string{chaleTurn, `{"intent":"question","reply":"Funciona assim..."}`}
	h2.say("quero anunciar um chalé")
	h2.say("como funciona o pagamento dos anúncios na meta, cobra quando?")
	if h2.att.Calls() != 2 || h2.wa.last().Text != "Funciona assim..." {
		t.Fatalf("assunto novo devia ir para a IA: %q", h2.wa.last().Text)
	}
}
