// Package shield é o escudo anti-bloqueio e anti-reprovação: confere a conta de anúncios do cliente antes
// de publicar, traduz o que a Meta diz para português simples e guia a recuperação. Só lê da Meta; quem
// decide (publicar, avisar, bloquear) é o chamador. Nada aqui promete que a Meta vai reverter uma decisão.
package shield

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// Level é a gravidade de um item da checagem.
type Level string

const (
	OK        Level = "ok"
	Attention Level = "atencao"  // dá para seguir, mas pode dar problema
	Blocks    Level = "bloqueia" // a Meta recusaria de qualquer forma
)

// Chaves dos itens da checagem.
const (
	KeyToken     = "token"
	KeyAccount   = "conta"
	KeyPayment   = "pagamento"
	KeyCap       = "limite"
	KeyCurrency  = "moeda"
	KeyPage      = "pagina"
	KeyInstagram = "instagram"
	KeyBusiness  = "business"
	KeyRejected  = "reprovado"
)

// Item é um ponto da checagem: o que está, em português simples, e o passo a passo curto para resolver.
type Item struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	Level   Level    `json:"level"`
	Message string   `json:"message"`
	Steps   []string `json:"steps,omitempty"`
}

// Report é o resultado de uma checagem.
type Report struct {
	At    time.Time `json:"at"`
	Items []Item    `json:"items"`
}

// Worst é a pior gravidade do relatório.
func (r Report) Worst() Level {
	w := OK
	for _, it := range r.Items {
		switch {
		case it.Level == Blocks:
			return Blocks
		case it.Level == Attention:
			w = Attention
		}
	}
	return w
}

// Blockers são os itens que a Meta recusaria.
func (r Report) Blockers() []Item { return r.filter(Blocks) }

// Attention são os itens que pedem cuidado, mas não impedem.
func (r Report) Attention() []Item { return r.filter(Attention) }

// Problems são os itens que não estão ok (bloqueios primeiro).
func (r Report) Problems() []Item { return append(r.filter(Blocks), r.filter(Attention)...) }

func (r Report) filter(l Level) []Item {
	var out []Item
	for _, it := range r.Items {
		if it.Level == l {
			out = append(out, it)
		}
	}
	return out
}

// Without devolve o relatório sem os itens das chaves dadas (quem publica já trata a moeda, por exemplo).
func (r Report) Without(keys ...string) Report {
	out := Report{At: r.At}
	for _, it := range r.Items {
		skip := false
		for _, k := range keys {
			skip = skip || it.Key == k
		}
		if !skip {
			out.Items = append(out.Items, it)
		}
	}
	return out
}

var countWords = map[int]string{1: "um ponto", 2: "dois pontos", 3: "três pontos"}

// Summary é o resumo curto para a Mia mandar: "tudo certo" ou "dois pontos para resolver: ...".
func (r Report) Summary() string {
	ps := r.Problems()
	if len(ps) == 0 {
		return "✅ Conferi a sua conta de anúncios na Meta e está tudo certo para publicar."
	}
	n := countWords[len(ps)]
	if n == "" {
		n = fmt.Sprintf("%d pontos", len(ps))
	}
	var b strings.Builder
	icon := "⚠️"
	fmt.Fprintf(&b, "%s Conferi a sua conta de anúncios na Meta e tem %s para resolver:", icon, n)
	for _, it := range ps {
		fmt.Fprintf(&b, "\n• *%s*: %s", it.Title, it.Message)
		if len(it.Steps) > 0 {
			fmt.Fprintf(&b, " Como resolver: %s", it.Steps[0])
		}
	}
	return b.String()
}

// Graph são as leituras da Meta de que a checagem precisa. *meta.Client implementa.
type Graph interface {
	AccountHealth(ctx context.Context, token, adAccountID string) (meta.AccountHealth, error)
	Funding(ctx context.Context, token, adAccountID string) (meta.Funding, error)
	PageAccess(ctx context.Context, token, pageID string) (meta.PageAccess, error)
	TokenInfo(ctx context.Context, token string) (meta.TokenInfo, error)
	BusinessVerification(ctx context.Context, token, businessID string) (string, error)
}

var _ Graph = (*meta.Client)(nil)

// Rejected é um anúncio reprovado recentemente (vem do banco, não da Meta).
type Rejected struct {
	At     time.Time
	Reason string
}

// Input é o que a checagem precisa saber da conexão e da campanha.
type Input struct {
	Token       string
	AdAccountID string
	PageID      string
	InstagramID string // Instagram escolhido na conexão; vazio = nenhum
	// NeedsInstagram: a campanha aparece no Instagram, então o vínculo faz diferença.
	NeedsInstagram bool
	Currency       string  // moeda guardada na conexão (usada se a leitura nova falhar)
	DailyBudgetBRL float64 // orçamento por dia da campanha; 0 = sem campanha ainda
	Rejected       []Rejected
	Now            time.Time
}

// ExpiringSoon: avisar quando o acesso vence em menos que isso.
const ExpiringSoon = 7 * 24 * time.Hour

// RecentRejection: reprovação dentro dessa janela ainda conta como recente.
const RecentRejection = 14 * 24 * time.Hour

// Check lê a conta na Meta e monta o relatório. As leituras rodam juntas; leitura que falha vira "atenção"
// (não conseguimos conferir), nunca bloqueio, exceto token inválido, que a Meta recusaria de qualquer forma.
func Check(ctx context.Context, g Graph, in Input) Report {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	rep := Report{At: now}

	// O token primeiro: sem ele nenhuma outra leitura vale.
	tokItem, tok := checkToken(ctx, g, in, now)
	rep.Items = append(rep.Items, tokItem...)
	if tokItem != nil && worstOf(tokItem) == Blocks && (!tok.Valid) {
		return rep
	}

	var (
		wg      sync.WaitGroup
		acct    meta.AccountHealth
		acctErr error
		fund    meta.Funding
		fundErr error
		page    meta.PageAccess
		pageErr error
	)
	wg.Add(3)
	go func() { defer wg.Done(); acct, acctErr = g.AccountHealth(ctx, in.Token, in.AdAccountID) }()
	go func() { defer wg.Done(); fund, fundErr = g.Funding(ctx, in.Token, in.AdAccountID) }()
	go func() { defer wg.Done(); page, pageErr = g.PageAccess(ctx, in.Token, in.PageID) }()
	wg.Wait()

	if meta.IsAuthError(acctErr) || meta.IsAuthError(fundErr) || meta.IsAuthError(pageErr) {
		rep.Items = []Item{tokenExpiredItem()} // a conexão caiu no meio: o "válida" de antes não vale mais
		return rep
	}
	rep.Items = append(rep.Items, accountItems(acct, acctErr, in)...)
	rep.Items = append(rep.Items, paymentItems(fund, fundErr, in)...)
	rep.Items = append(rep.Items, pageItems(page, pageErr, in)...)
	if acctErr == nil && acct.BusinessID != "" {
		if v, err := g.BusinessVerification(ctx, in.Token, acct.BusinessID); err == nil && v != "" {
			rep.Items = append(rep.Items, businessItem(v))
		}
	}
	if it, ok := rejectedItem(in.Rejected, now); ok {
		rep.Items = append(rep.Items, it)
	}
	return rep
}

func worstOf(items []Item) Level { return Report{Items: items}.Worst() }

// ---------- itens ----------

func tokenExpiredItem() Item {
	return Item{Key: KeyToken, Title: "Conexão com a Meta", Level: Blocks,
		Message: "a conexão com a sua conta da Meta venceu ou foi desfeita.",
		Steps: []string{"Conecte de novo pelo link que a Mia manda (leva 1 minuto).",
			"Deixe marcadas todas as permissões, de contas de anúncios e de Página."}}
}

func checkToken(ctx context.Context, g Graph, in Input, now time.Time) ([]Item, meta.TokenInfo) {
	t, err := g.TokenInfo(ctx, in.Token)
	switch {
	case meta.IsAuthError(err):
		return []Item{tokenExpiredItem()}, meta.TokenInfo{}
	case err != nil:
		return []Item{{Key: KeyToken, Title: "Conexão com a Meta", Level: Attention,
			Message: "não consegui conferir a validade da conexão agora; sigo com as outras checagens."}}, meta.TokenInfo{Valid: true}
	case !t.Valid:
		return []Item{tokenExpiredItem()}, t
	}
	if !t.ExpiresAt.IsZero() && !t.ExpiresAt.After(now) {
		return []Item{tokenExpiredItem()}, meta.TokenInfo{}
	}
	var items []Item
	if len(t.Scopes) > 0 {
		if !t.HasScope("ads_management") {
			items = append(items, Item{Key: KeyToken, Title: "Permissões", Level: Blocks,
				Message: "faltou a permissão para criar anúncios na sua conta.",
				Steps: []string{"Conecte a conta de novo e deixe marcada a permissão de gerenciar anúncios.",
					"Se a tela da Meta não mostrar essa opção, avise a equipe."}})
		} else if miss := missingScopes(t, "ads_read", "pages_show_list", "pages_read_engagement"); len(miss) > 0 {
			items = append(items, Item{Key: KeyToken, Title: "Permissões", Level: Attention,
				Message: "algumas permissões da conexão não foram dadas (" + strings.Join(miss, ", ") + "); pode faltar leitura de resultados ou da Página.",
				Steps:   []string{"Conecte de novo e deixe todas as permissões marcadas."}})
		}
	}
	if !t.ExpiresAt.IsZero() && t.ExpiresAt.Sub(now) < ExpiringSoon {
		items = append(items, Item{Key: KeyToken, Title: "Conexão com a Meta", Level: Attention,
			Message: "a conexão vence em " + t.ExpiresAt.In(brt).Format("02/01") + "; depois disso eu não consigo mais publicar nem pausar.",
			Steps:   []string{"Reconecte pelo link que a Mia manda; a conta continua a mesma."}})
	}
	if len(items) == 0 {
		items = []Item{{Key: KeyToken, Title: "Conexão com a Meta", Level: OK, Message: "a conexão com a sua conta está válida."}}
	}
	return items, t
}

func missingScopes(t meta.TokenInfo, want ...string) []string {
	var out []string
	for _, s := range want {
		if !t.HasScope(s) {
			out = append(out, s)
		}
	}
	return out
}

func accountItems(a meta.AccountHealth, err error, in Input) []Item {
	if err != nil {
		return []Item{{Key: KeyAccount, Title: "Conta de anúncios", Level: Attention,
			Message: "não consegui ler a situação da conta na Meta agora."}}
	}
	var items []Item
	st := Classify(a)
	switch st {
	case StateOK:
		items = append(items, Item{Key: KeyAccount, Title: "Conta de anúncios", Level: OK, Message: "a conta de anúncios está ativa."})
	case StateRestricted:
		items = append(items, Item{Key: KeyAccount, Title: "Conta de anúncios", Level: Attention,
			Message: "a conta está em análise de risco ou com restrição na Meta; os anúncios podem não rodar até a análise terminar.",
			Steps:   GuideFor(ScenarioRestricted).Steps})
	case StateDisabled, StateClosed:
		items = append(items, Item{Key: KeyAccount, Title: "Conta de anúncios", Level: Blocks,
			Message: StateMessage(st, a.DisableReason),
			Steps:   GuideFor(ScenarioFor(st, a.DisableReason)).Steps})
	case StatePayment, StateGrace:
		items = append(items, Item{Key: KeyAccount, Title: "Conta de anúncios", Level: Attention,
			Message: StateMessage(st, a.DisableReason),
			Steps:   GuideFor(ScenarioPayment).Steps})
	}
	cur := a.Currency
	if cur == "" {
		cur = in.Currency
	}
	if cur != "BRL" {
		msg := "a conta de anúncios está em " + cur + " e eu só publico em contas em reais (R$)."
		if cur == "" {
			msg = "não consegui saber a moeda da conta de anúncios, e eu só publico em contas em reais (R$)."
		}
		items = append(items, Item{Key: KeyCurrency, Title: "Moeda da conta", Level: Blocks, Message: msg,
			Steps: []string{"Conecte outra conta de anúncios que esteja em reais (mande *conectar meta*)."}})
	} else {
		items = append(items, Item{Key: KeyCurrency, Title: "Moeda da conta", Level: OK, Message: "a conta está em reais (R$)."})
	}
	return items
}

func paymentItems(f meta.Funding, err error, in Input) []Item {
	if err != nil {
		return []Item{{Key: KeyPayment, Title: "Pagamento", Level: Attention,
			Message: "não consegui ler a forma de pagamento da conta agora."}}
	}
	daily := int64(in.DailyBudgetBRL * 100)
	var items []Item
	switch {
	case f.Status == 3 || f.Status == 8 || f.Status == 9:
		items = append(items, Item{Key: KeyPayment, Title: "Pagamento", Level: Attention,
			Message: "a Meta está com um pagamento pendente ou recusado nessa conta; o anúncio não roda até acertar.",
			Steps:   GuideFor(ScenarioPayment).Steps})
	case !f.HasSource:
		items = append(items, Item{Key: KeyPayment, Title: "Pagamento", Level: Attention,
			Message: "a conta não tem forma de pagamento; a campanha é criada, mas só veicula depois de cadastrar cartão ou saldo.",
			Steps:   []string{"Abra a Cobrança da conta no Gerenciador de Anúncios.", "Cadastre um cartão (ou coloque saldo por Pix ou boleto).", "Avise a Mia com um *paguei*."}})
	case f.Prepaid && f.AvailableCents >= 0 && daily > 0 && f.AvailableCents < daily:
		items = append(items, Item{Key: KeyPayment, Title: "Pagamento", Level: Attention,
			Message: "o saldo pré-pago não cobre nem um dia da campanha.",
			Steps:   []string{"Coloque mais saldo pela Cobrança da conta (Pix ou boleto).", "Avise a Mia com um *paguei*."}})
	default:
		items = append(items, Item{Key: KeyPayment, Title: "Pagamento", Level: OK, Message: "a forma de pagamento da conta está cadastrada."})
	}
	if f.SpendCapCents > 0 {
		left := max(f.SpendCapCents-f.SpentCents, 0)
		if daily > 0 && left < daily {
			items = append(items, Item{Key: KeyCap, Title: "Limite de gastos", Level: Attention,
				Message: "o limite de gastos da conta quase acabou e não cobre um dia da campanha; ela para quando o limite chegar.",
				Steps:   []string{"Abra Configurações de cobrança da conta no Gerenciador de Anúncios.", "Aumente (ou remova) o limite de gastos da conta."}})
		} else {
			items = append(items, Item{Key: KeyCap, Title: "Limite de gastos", Level: OK, Message: "o limite de gastos da conta ainda tem folga."})
		}
	}
	return items
}

func pageItems(p meta.PageAccess, err error, in Input) []Item {
	if err != nil {
		return []Item{{Key: KeyPage, Title: "Página e Instagram", Level: Attention,
			Message: "não consegui conferir a Página agora."}}
	}
	if !p.Found {
		return []Item{{Key: KeyPage, Title: "Página do Facebook", Level: Blocks,
			Message: "a Página escolhida não aparece mais para o seu login; sem ela a Meta não cria o anúncio.",
			Steps: []string{"Confirme que você ainda é administrador da Página.",
				"Conecte de novo pelo link que a Mia manda e escolha a Página."}}}
	}
	var items []Item
	switch {
	case !p.CanAdvertise():
		items = append(items, Item{Key: KeyPage, Title: "Página do Facebook", Level: Blocks,
			Message: "o seu usuário não tem permissão de anunciar por essa Página.",
			Steps: []string{"Peça a um administrador da Página para dar a você o papel de Anunciante (ou Administrador).",
				"Depois conecte de novo pelo link da Mia."}})
	case !p.Published:
		items = append(items, Item{Key: KeyPage, Title: "Página do Facebook", Level: Attention,
			Message: "a Página está despublicada; anúncio com Página despublicada costuma não rodar.",
			Steps:   []string{"Abra as configurações da Página e publique-a de novo."}})
	default:
		items = append(items, Item{Key: KeyPage, Title: "Página do Facebook", Level: OK, Message: "a Página está publicada e você tem permissão para anunciar."})
	}
	switch {
	case in.InstagramID != "" && p.InstagramID == "":
		items = append(items, Item{Key: KeyInstagram, Title: "Instagram", Level: Attention,
			Message: "o Instagram escolhido não está mais vinculado à Página; o anúncio pode sair só no Facebook ou ser recusado.",
			Steps:   []string{"Nas configurações da Página, vincule de novo a conta profissional do Instagram.", "Conecte de novo pelo link da Mia, se precisar."}})
	case in.InstagramID != "" && p.InstagramID != in.InstagramID:
		items = append(items, Item{Key: KeyInstagram, Title: "Instagram", Level: Attention,
			Message: "o Instagram vinculado à Página mudou desde a conexão.",
			Steps:   []string{"Conecte de novo pelo link da Mia para atualizar o Instagram."}})
	case in.InstagramID == "" && in.NeedsInstagram:
		items = append(items, Item{Key: KeyInstagram, Title: "Instagram", Level: Attention,
			Message: "a Página não tem Instagram profissional vinculado; o anúncio vai aparecer só no Facebook.",
			Steps:   []string{"Se quiser anunciar no Instagram, vincule a conta profissional à Página e conecte de novo."}})
	case in.InstagramID != "":
		items = append(items, Item{Key: KeyInstagram, Title: "Instagram", Level: OK, Message: "o Instagram está vinculado à Página."})
	}
	return items
}

func businessItem(v string) Item {
	switch v {
	case "verified":
		return Item{Key: KeyBusiness, Title: "Verificação do Business", Level: OK, Message: "o seu Business está verificado na Meta."}
	case "pending", "pending_submission", "pending_need_more_info":
		return Item{Key: KeyBusiness, Title: "Verificação do Business", Level: Attention,
			Message: "a verificação do seu Business na Meta está em andamento ou pedindo mais informações.",
			Steps:   GuideFor(ScenarioIdentity).Steps}
	case "rejected", "failed", "expired", "revoked":
		return Item{Key: KeyBusiness, Title: "Verificação do Business", Level: Attention,
			Message: "a verificação do seu Business na Meta não foi aprovada ou venceu; a Meta pode limitar a conta até refazer.",
			Steps:   GuideFor(ScenarioIdentity).Steps}
	}
	return Item{Key: KeyBusiness, Title: "Verificação do Business", Level: Attention,
		Message: "o seu Business ainda não foi verificado na Meta; ela pode pedir isso a qualquer momento e limitar a conta até concluir.",
		Steps:   GuideFor(ScenarioIdentity).Steps}
}

func rejectedItem(list []Rejected, now time.Time) (Item, bool) {
	var last *Rejected
	n := 0
	for i := range list {
		if now.Sub(list[i].At) > RecentRejection {
			continue
		}
		n++
		if last == nil || list[i].At.After(last.At) {
			last = &list[i]
		}
	}
	if n == 0 {
		return Item{}, false
	}
	why := ExplainRejection([]string{last.Reason})
	msg := "a Meta reprovou um anúncio seu nos últimos dias"
	if n > 1 {
		msg = fmt.Sprintf("a Meta reprovou %d anúncios seus nos últimos dias", n)
	}
	msg += ". Repetir o mesmo erro pesa contra a conta."
	if p := why.First(); p != "" {
		msg += " Motivo, em simples: " + p
	}
	return Item{Key: KeyRejected, Title: "Anúncio reprovado", Level: Attention, Message: msg, Steps: GuideFor(ScenarioRejected).Steps}, true
}
