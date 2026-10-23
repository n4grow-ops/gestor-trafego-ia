package engine_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/instagram"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Pedido do cliente (23/10): a Mia pergunta objetivo, público, interesses, dias, onde aparece e quantos
// anúncios antes da proposta; sem resposta para tudo, a proposta não sai. E o que ele respondeu vale.
func TestProposalWaitsForAllAnswers(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Rewrite = nil // sem completar: o teste responde pergunta por pergunta
	h.att.Responses = []string{turnOffer, turnRest}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia")
	if c := h.conv(); c.State != store.StateCollecting || h.pendingJobs(engine.JobPropose) != 0 {
		t.Fatalf("proposta sem as perguntas novas: estado = %s", c.State)
	}
	prompt := h.att.Requests[len(h.att.Requests)-1].Messages[1].Content
	for _, want := range []string{"objective_hint", "interests", "duration_days", "placements", "ads_count"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("faltante %q não chegou à Mia", want)
		}
	}
	h.att.Responses = append(h.att.Responses,
		`{"intent":"provide_info","fields":{"objective_hint":"engajamento","interests":"Pizza, Comida italiana"},"reply":"Quantos dias?"}`,
		`{"intent":"provide_info","fields":{"duration_days":4,"placements":["feed","stories"],"ads_count":2},"reply":"Fechado."}`)
	h.say("engajamento, pode usar esses interesses")
	if h.pendingJobs(engine.JobPropose) != 0 {
		t.Fatal("proposta antes de dias, onde aparece e quantos anúncios")
	}
	two := strings.Replace(validProposal, `"copies":[`, `"copies":[{"headline":"Pizza de sexta","text":"Peça já","cta":"LEARN_MORE"},`, 1)
	h.mgr.Responses = []string{two}
	h.say("4 dias, feed e stories, 2 anúncios")
	if c := h.conv(); c.State != store.StateAwaitingProposal {
		t.Fatalf("respostas completas e a proposta não foi pedida: %s", c.State)
	}
	h.runJobs()
	var b contract.Briefing
	brief := h.mgr.Requests[0].Messages[len(h.mgr.Requests[0].Messages)-1].Content
	h.must(json.Unmarshal([]byte(brief[strings.Index(brief, "{"):]), &b))
	if b.DurationDays != 4 || b.CopyVariations != 2 || !slices.Equal(b.Interests, []string{"Pizza", "Comida italiana"}) {
		t.Fatalf("briefing = dias %d anúncios %d interesses %v", b.DurationDays, b.CopyVariations, b.Interests)
	}
	summary := h.wa.last().Text
	for _, want := range []string{"por 4 dias", "Interesses da Meta: Pizza, Comida italiana", "Objetivo: engajamento", "só no Feed e nos Stories"} {
		if !strings.Contains(summary, want) {
			t.Errorf("resumo sem %q:\n%s", want, summary)
		}
	}
}

// "Nenhum" interesse: a proposta sai sem interesses, mesmo que o Gestor sugira algum.
func TestNoInterestsMeansNone(t *testing.T) {
	h := newHarness(t, "owner")
	h.att.Rewrite = func(s string) string { return strings.Replace(completeCampaign(s), `"interests":"pizza"`, `"interests":"nenhum"`, 1) }
	h.att.Responses = []string{turnOffer, turnRest}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	h.say("famílias em Campinas, 50 por dia, sem interesse")
	h.runJobs()
	if strings.Contains(h.wa.last().Text, "Interesses da Meta") {
		t.Fatalf("cliente não quis interesses:\n%s", h.wa.last().Text)
	}
}

// Pedido do cliente (23/10): sem a conta de anúncios conectada a proposta não sai; o link vai junto e,
// conectando, a proposta sai sozinha.
func TestProposalWaitsForMetaConnection(t *testing.T) {
	h := newMetaHarness(t, "owner")
	h.eng.RequireMetaBeforeProposal = true
	h.att.Responses = []string{turnOffer, turnRest}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza")
	if !strings.Contains(h.wa.last().Text, metaBase) {
		t.Fatalf("primeira conversa de campanha sem o link de conexão: %q", h.wa.last().Text)
	}
	h.say("famílias em Campinas, 50 por dia")
	if c := h.conv(); c.State != store.StateCollecting || h.pendingJobs(engine.JobPropose) != 0 {
		t.Fatalf("proposta sem conta conectada: estado = %s", c.State)
	}
	if !strings.Contains(h.wa.last().Text, "Só falta a conta de anúncios") {
		t.Fatalf("resposta = %q", h.wa.last().Text)
	}
	h.connectMeta()
	c := h.conv()
	h.must(h.st.Enqueue(ctx, engine.MetaConnectedJob(c.ID, c.ChatID, engine.MetaSummary{AccountName: "Loja", Currency: "BRL", AccountStatus: 1, PageName: "Loja Page", PageWhatsApp: true})))
	h.drain()
	if m := codeRe.FindStringSubmatch(h.wa.last().Text); m == nil {
		t.Fatalf("conectou e a proposta não saiu: %q", h.wa.last().Text)
	}
}

// Link do Instagram mandado antes da aprovação fica guardado e é buscado quando a campanha é aprovada.
func TestEarlyInstagramLinkImportedOnApproval(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Permalink: "https://www.instagram.com/p/MEU1/", Items: []instagram.Item{igSquare}}
	h.att.Responses = []string{`{"intent":"create_campaign","fields":{"offer":"Pizza grande"},"reply":"Guardei o post! Pra quem?"}`, turnRest}
	h.mgr.Responses = []string{validProposal}
	h.say("quero anunciar pizza, usa esse post https://www.instagram.com/p/MEU1/")
	h.say("famílias em Campinas, 50 por dia")
	h.runJobs()
	m := codeRe.FindStringSubmatch(h.wa.last().Text)
	if m == nil {
		t.Fatalf("resumo sem código: %q", h.wa.last().Text)
	}
	h.say("SIM " + m[1])
	if !strings.Contains(h.lastClient(), "post do Instagram que você mandou") || h.pendingJobs(engine.JobInstagramFetch) != 1 {
		t.Fatalf("link de antes não agendado: %q", h.lastClient())
	}
	h.runKind(engine.JobInstagramFetch)
	if h.creatives() != 1 {
		t.Fatalf("criativos = %d", h.creatives())
	}
}
