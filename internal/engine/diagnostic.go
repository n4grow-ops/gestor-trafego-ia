package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Diagnóstico grátis da conta: depois de conectar a Meta, a Mia entrega um resumo dos últimos 30 dias em
// linguagem simples. Só leitura (não altera campanha nenhuma) e sem inventar número: tudo vem da Insights API
// e, com pouco dado, o texto diz isso.

const (
	// JobAccountDiagnostic lê a conta e manda o resumo ao cliente.
	JobAccountDiagnostic = "account.diagnostic"

	diagnosticDays  = 30
	diagnosticDelay = 3 * time.Minute // depois da confirmação da conexão e do que ela dispara

	// Limiares das observações (centavos e vezes). Abaixo deles o texto não afirma nada.
	diagMinAdSpendCents   = 2000  // R$ 20: anúncio com menos gasto não entra em melhor/pior
	diagWasteSpendCents   = 3000  // R$ 30 sem nenhuma conversa já é desperdício a apontar
	diagLowSpendCents     = 15000 // R$ 150: abaixo disso o resumo leva a ressalva de pouco dado
	diagLowConversations  = 10
	diagHighFrequency     = 3.5
	diagAdHighFrequency   = 4.0
	diagWorstVsBestFactor = 2.0
	diagMaxObservations   = 3
)

// DiagnosticJob pede o diagnóstico de uma organização. Force: a pedido do cliente (repete); sem Force, só uma vez
// por conexão.
type DiagnosticJob struct {
	OrgID          int64 `json:"org_id"`
	ConversationID int64 `json:"conversation_id"`
	Force          bool  `json:"force,omitempty"`
}

func (e *Engine) diagnosticOn() bool {
	_, ok := e.Publisher.(meta.AccountReader)
	return ok && e.MetaTokenKey != nil
}

// scheduleDiagnostic agenda o diagnóstico depois da conexão da conta. Falha só vai pro log: é cortesia.
func (e *Engine) scheduleDiagnostic(ctx context.Context, convID int64) {
	if !e.diagnosticOn() {
		return
	}
	conv, err := e.Store.GetConversation(ctx, convID)
	if err != nil || conv.State == store.StateNeedsHuman {
		return
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return
	}
	if err := e.Store.Enqueue(ctx, store.JobSpec{Kind: JobAccountDiagnostic, Delay: diagnosticDelay,
		Payload: DiagnosticJob{OrgID: ident.OrgID, ConversationID: convID}}); err != nil {
		e.Log.Warn("diagnóstico da conta não agendado", "org_id", ident.OrgID, "erro", err)
	}
}

var diagnosticAsks = map[string]bool{
	"diagnostico": true, "diagnostico da conta": true, "diagnostico gratis": true, "diagnostico da minha conta": true,
	"quero um diagnostico": true, "me da um diagnostico": true, "analisar minha conta": true, "analise minha conta": true,
	"analisa minha conta": true, "analisar a conta": true,
}

// diagnosticCommand trata o pedido "DIAGNÓSTICO" do cliente (refaz o resumo). handled=false segue o fluxo normal.
func (e *Engine) diagnosticCommand(ctx context.Context, ident store.Identity, conv store.Conversation, text string) (bool, error) {
	if !diagnosticAsks[strings.TrimRight(normalizeName(strings.ReplaceAll(text, "*", "")), " .!?")] {
		return false, nil
	}
	if conv.State != store.StateIdle && conv.State != store.StateCollecting {
		return false, nil
	}
	_, connected, err := e.Store.ActiveMeta(ctx, ident.OrgID)
	if err != nil {
		return true, err
	}
	if !connected {
		if e.MetaConnectBaseURL == "" {
			return false, nil
		}
		return true, e.sendMetaLink(ctx, ident, conv, msgDiagnosticNeedsMeta)
	}
	if !e.diagnosticOn() {
		return false, nil
	}
	if !ident.CanApprove() {
		return true, e.send(ctx, conv, msgCampaignNotAllowed)
	}
	noteAction(ctx, "diagnóstico da conta (sem IA)")
	pending, err := e.Store.HasPendingJob(ctx, JobAccountDiagnostic, "org_id", ident.OrgID)
	if err != nil {
		return true, err
	}
	if pending {
		return true, e.send(ctx, conv, msgDiagnosticPending)
	}
	if err := e.Store.Enqueue(ctx, store.JobSpec{Kind: JobAccountDiagnostic,
		Payload: DiagnosticJob{OrgID: ident.OrgID, ConversationID: conv.ID, Force: true}}); err != nil {
		return true, err
	}
	return true, e.send(ctx, conv, msgDiagnosticStarted)
}

// accountDiagnostic lê os últimos 30 dias da conta e manda o resumo. Idempotente: sem Force, a conexão só
// recebe o diagnóstico uma vez; falha na leitura da Meta desfaz a reivindicação e o outbox tenta de novo.
func (e *Engine) accountDiagnostic(ctx context.Context, raw []byte) error {
	var j DiagnosticJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("engine: payload do diagnóstico inválido: %w", err)
	}
	ar, ok := e.Publisher.(meta.AccountReader)
	if !ok || e.MetaTokenKey == nil {
		return nil
	}
	conn, ok, err := e.Store.ActiveMeta(ctx, j.OrgID)
	if err != nil || !ok {
		return err
	}
	conv, err := e.Store.GetConversation(ctx, j.ConversationID)
	if errors.Is(err, store.ErrNotFound) {
		conv, err = e.Store.OwnerConversation(ctx, j.OrgID)
	}
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if conv.State == store.StateNeedsHuman {
		return nil // atendimento humano: a equipe conduz
	}
	claimed, err := e.Store.ClaimDiagnostic(ctx, j.OrgID, conn.ID)
	if err != nil {
		return err
	}
	if !claimed && !j.Force {
		return nil // essa conexão já recebeu o diagnóstico
	}
	release := func() {
		if claimed {
			if rerr := e.Store.ReleaseDiagnostic(context.WithoutCancel(ctx), conn.ID); rerr != nil {
				e.Log.Warn("diagnóstico: reivindicação não desfeita", "connection_id", conn.ID, "erro", rerr)
			}
		}
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, j.OrgID)
	if err != nil {
		release()
		return nil // sem conexão: o aviso de conexão cuida disso
	}
	tok, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		release()
		return nil
	}
	token := string(tok)

	until := brtDay(e.now()).AddDate(0, 0, -1) // dias completos: até ontem
	since := until.AddDate(0, 0, -(diagnosticDays - 1))
	total, err := ar.AccountInsights(ctx, token, conn.AdAccountID, since, until)
	if err != nil {
		release()
		return err
	}
	ads, err := ar.AdInsights(ctx, token, conn.AdAccountID, since, until)
	if err != nil {
		release()
		return err
	}
	active, aerr := ar.ActiveAds(ctx, token, conn.AdAccountID)
	if aerr != nil {
		e.Log.Warn("diagnóstico: anúncios ligados não lidos; a parte dos anúncios parados fica de fora", "org_id", j.OrgID, "erro", aerr)
	}
	data := DiagnosticData{AccountName: conn.AdAccountName, Currency: conn.Currency, Since: since, Until: until,
		Total: total, Ads: ads, Active: active, ActiveKnown: aerr == nil}
	text, summary := buildDiagnostic(data)
	if err := e.Store.SaveDiagnostic(ctx, conn.ID, summary); err != nil {
		e.Log.Warn("diagnóstico: resumo não guardado", "connection_id", conn.ID, "erro", err)
	}
	e.audit(ctx, j.OrgID, "system", "account_diagnostic_sent", map[string]any{"connection_id": conn.ID, "spend_cents": total.SpendCents,
		"conversations": total.Conversations, "ads": len(ads), "forced": j.Force})
	return e.send(ctx, conv, text)
}

// DiagnosticData é tudo o que o texto usa, vindo da Meta.
type DiagnosticData struct {
	AccountName string
	Currency    string
	Since       time.Time
	Until       time.Time
	Total       meta.AdPerformance
	Ads         []meta.AdPerformance
	Active      []meta.ActiveAd
	ActiveKnown bool // false: a lista de anúncios ligados não pôde ser lida
}

// DiagnosticSummary é o que fica guardado do diagnóstico entregue.
type DiagnosticSummary struct {
	SpendCents    int64    `json:"spend_cents"`
	Conversations int64    `json:"conversations"`
	Frequency     float64  `json:"frequency"`
	AdsWithData   int      `json:"ads_with_data"`
	StalledAds    int      `json:"stalled_ads"`
	Observations  []string `json:"observations"`
	NoData        bool     `json:"no_data"`
	LowData       bool     `json:"low_data"`
}

func moneyIn(cents int64, currency string) string {
	if currency == "" || currency == "BRL" {
		return brlCents(cents)
	}
	return fmt.Sprintf("%.2f %s", float64(cents)/100, currency)
}

func adLabel(a meta.AdPerformance) string {
	if n := strings.TrimSpace(a.AdName); n != "" {
		return "*" + n + "*"
	}
	return "um anúncio sem nome"
}

// buildDiagnostic monta o texto e o resumo. Pura: só usa os números recebidos.
func buildDiagnostic(d DiagnosticData) (string, DiagnosticSummary) {
	name := "sua conta de anúncios"
	if n := strings.TrimSpace(d.AccountName); n != "" {
		name = "sua conta *" + n + "*"
	}
	period := fmt.Sprintf("%s a %s", d.Since.Format("02/01"), d.Until.Format("02/01"))
	sum := DiagnosticSummary{SpendCents: d.Total.SpendCents, Conversations: d.Total.Conversations, Frequency: d.Total.Frequency}

	// Anúncios com entrega no período e anúncios ligados que não entregaram nada.
	delivered := map[string]bool{}
	var withData []meta.AdPerformance
	for _, a := range d.Ads {
		if a.Impressions > 0 || a.SpendCents > 0 {
			withData = append(withData, a)
			delivered[a.AdID] = true
		}
	}
	sum.AdsWithData = len(withData)
	var stalled []meta.ActiveAd
	if d.ActiveKnown {
		for _, a := range d.Active {
			if !delivered[a.ID] {
				stalled = append(stalled, a)
			}
		}
	}
	sum.StalledAds = len(stalled)

	if d.Total.SpendCents == 0 && d.Total.Impressions == 0 && len(withData) == 0 {
		sum.NoData = true
		parts := []string{fmt.Sprintf(msgDiagnosticNoData, name, period)}
		if len(stalled) > 0 {
			parts = append(parts, fmt.Sprintf(msgDiagnosticNoDataStalled, len(stalled)))
		}
		parts = append(parts, msgDiagnosticClose)
		return join(parts...), sum
	}

	cur := d.Currency
	lines := []string{fmt.Sprintf("💰 Gasto: %s", moneyIn(d.Total.SpendCents, cur))}
	switch {
	case d.Total.Conversations == 0:
		lines = append(lines, "💬 Conversas iniciadas: nenhuma registrada")
	default:
		lines = append(lines, fmt.Sprintf("💬 Conversas iniciadas: %d", d.Total.Conversations))
		if d.Total.SpendCents > 0 {
			lines = append(lines, fmt.Sprintf("🎯 Custo por conversa: %s", moneyIn(d.Total.SpendCents/d.Total.Conversations, cur)))
		}
	}

	// Melhor e pior anúncio por custo por conversa: só com gasto que sustente a comparação.
	var costed []meta.AdPerformance // com conversa e gasto relevante
	var wasted []meta.AdPerformance // gasto relevante e nenhuma conversa
	for _, a := range withData {
		switch {
		case a.Conversations > 0 && a.SpendCents >= diagMinAdSpendCents:
			costed = append(costed, a)
		case a.Conversations == 0 && a.SpendCents >= diagWasteSpendCents:
			wasted = append(wasted, a)
		}
	}
	perConv := func(a meta.AdPerformance) int64 { return a.SpendCents / a.Conversations }
	sort.SliceStable(costed, func(i, j int) bool { return perConv(costed[i]) < perConv(costed[j]) })
	sort.SliceStable(wasted, func(i, j int) bool { return wasted[i].SpendCents > wasted[j].SpendCents })

	var best, worst *meta.AdPerformance
	var ratio float64
	switch {
	case len(costed) >= 2:
		best, worst = &costed[0], &costed[len(costed)-1]
		if perConv(*best) > 0 {
			ratio = float64(perConv(*worst)) / float64(perConv(*best))
		}
		lines = append(lines, fmt.Sprintf("🏆 Melhor anúncio: %s, %s por conversa (%s)", adLabel(*best), moneyIn(perConv(*best), cur), conversationsText(best.Conversations)))
		if len(wasted) == 0 {
			lines = append(lines, fmt.Sprintf("⚠️ Pior anúncio: %s, %s por conversa (%s)", adLabel(*worst), moneyIn(perConv(*worst), cur), conversationsText(worst.Conversations)))
		}
	case len(costed) == 1:
		best = &costed[0]
		lines = append(lines, fmt.Sprintf("🏆 Só um anúncio gerou conversas com gasto relevante: %s, %s por conversa. Sem outro pra comparar.", adLabel(*best), moneyIn(perConv(*best), cur)))
	default:
		lines = append(lines, fmt.Sprintf("🏆 Melhor e pior anúncio: nenhum anúncio gastou o bastante (%s ou mais) pra eu comparar com segurança.", moneyIn(diagMinAdSpendCents, cur)))
	}
	if len(wasted) > 0 {
		w := wasted[0]
		lines = append(lines, fmt.Sprintf("⚠️ Pior anúncio: %s, gastou %s e não gerou nenhuma conversa", adLabel(w), moneyIn(w.SpendCents, cur)))
	}

	// Frequência.
	if d.Total.Frequency > 0 {
		f := fmt.Sprintf("🔁 Frequência: cada pessoa viu seus anúncios %.1f vezes em média", d.Total.Frequency)
		if d.Total.Frequency >= diagHighFrequency {
			f += " (alta)"
		}
		lines = append(lines, f)
	}
	var tired []meta.AdPerformance
	for _, a := range withData {
		if a.Frequency >= diagAdHighFrequency && a.SpendCents >= diagMinAdSpendCents {
			tired = append(tired, a)
		}
	}

	// Anúncios parados (ligados e sem nenhuma exibição).
	switch {
	case !d.ActiveKnown:
	case len(stalled) > 0:
		lines = append(lines, fmt.Sprintf("😴 Anúncios ligados sem nenhuma exibição: %d (%s)", len(stalled), stalledNames(stalled)))
	case len(d.Active) > 0:
		lines = append(lines, fmt.Sprintf("✅ Anúncios ligados: %d, todos com exibição", len(d.Active)))
	}

	// Até 3 observações, só do que os números sustentam, em ordem de importância.
	var obs []string
	if d.Total.SpendCents > 0 && d.Total.Conversations == 0 {
		obs = append(obs, "Você gastou e não tem nenhuma conversa registrada. Eu só conto conversa de campanha de mensagem (WhatsApp, Direct, Messenger). Se as suas têm outro objetivo, isso é esperado; se eram pra gerar mensagem, vale conferir o destino e o objetivo delas.")
	}
	if len(wasted) > 0 {
		obs = append(obs, fmt.Sprintf("O anúncio %s gastou %s sem gerar conversa. Vale pausar ou trocar a imagem e o texto dele.", adLabel(wasted[0]), moneyIn(wasted[0].SpendCents, cur)))
	}
	if best != nil && worst != nil && ratio >= diagWorstVsBestFactor {
		obs = append(obs, fmt.Sprintf("O anúncio %s custa cerca de %.1fx mais por conversa que o %s. Vale concentrar verba no melhor e testar variações dele.", adLabel(*worst), ratio, adLabel(*best)))
	}
	if d.Total.Frequency >= diagHighFrequency {
		obs = append(obs, fmt.Sprintf("A frequência está alta (%.1f): as mesmas pessoas estão vendo o anúncio muitas vezes, o que costuma encarecer o resultado. Vale renovar os criativos ou ampliar o público.", d.Total.Frequency))
	} else if len(tired) > 0 {
		obs = append(obs, fmt.Sprintf("O anúncio %s está com frequência %.1f, bem acima do resto. Vale trocar o criativo dele.", adLabel(tired[0]), tired[0].Frequency))
	}
	if len(stalled) > 0 {
		lead := fmt.Sprintf("%d anúncios ligados não tiveram nenhuma exibição.", len(stalled))
		if len(stalled) == 1 {
			lead = "1 anúncio ligado não teve nenhuma exibição."
		}
		obs = append(obs, lead+" Pode ser reprovação, orçamento zerado ou conjunto parado: vale conferir no Gerenciador de Anúncios.")
	}
	if len(obs) > diagMaxObservations {
		obs = obs[:diagMaxObservations]
	}
	if len(obs) == 0 {
		obs = append(obs, "Não achei nada fora do normal nos números que eu consigo ver.")
	}
	sum.Observations = obs

	parts := []string{fmt.Sprintf(msgDiagnosticHead, name, period), strings.Join(lines, "\n")}
	var ol []string
	for i, o := range obs {
		ol = append(ol, fmt.Sprintf("%d. %s", i+1, o))
	}
	parts = append(parts, "O que eu notei:\n"+strings.Join(ol, "\n"))
	if d.Total.SpendCents < diagLowSpendCents || d.Total.Conversations < diagLowConversations {
		sum.LowData = true
		parts = append(parts, fmt.Sprintf(msgDiagnosticLowData, moneyIn(d.Total.SpendCents, cur), conversationsText(d.Total.Conversations)))
	}
	parts = append(parts, msgDiagnosticClose)
	return join(parts...), sum
}

func stalledNames(list []meta.ActiveAd) string {
	var names []string
	for _, a := range list {
		if n := strings.TrimSpace(a.Name); n != "" {
			names = append(names, n)
		}
		if len(names) == 3 {
			break
		}
	}
	if len(names) == 0 {
		return "sem nome"
	}
	s := strings.Join(names, ", ")
	if len(list) > len(names) {
		s += fmt.Sprintf(" e mais %d", len(list)-len(names))
	}
	return s
}
