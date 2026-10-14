package meta

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Funding é como a conta de anúncios paga: forma de pagamento, saldo pré-pago e limite de gastos.
// Valores em centavos da moeda da conta.
type Funding struct {
	Status         int    // account_status (1 ativa; 3, 8 e 9 = pagamento pendente)
	Prepaid        bool   // conta pré-paga (saldo por Pix/boleto)
	HasSource      bool   // tem forma de pagamento; sem ela a Meta cria o anúncio mas não veicula
	SourceType     int    // funding_source_details.type (1 cartão, 20 saldo pré-pago…)
	Source         string // como a Meta mostra a forma de pagamento ("Visa *1234", "Saldo disponível (R$…)")
	AvailableCents int64  // saldo pré-pago lido do texto da Meta; -1 = desconhecido
	SpendCapCents  int64  // limite de gastos da conta; 0 = sem limite
	SpentCents     int64  // gasto contado contra o limite
}

// FundingReader lê como a conta paga. A Meta não tem campo de saldo pré-pago: o valor sai do texto de
// exibição da forma de pagamento, quando ele traz.
type FundingReader interface {
	Funding(ctx context.Context, token, adAccountID string) (Funding, error)
}

func (c *Client) Funding(ctx context.Context, token, adAccountID string) (Funding, error) {
	acct := strings.TrimPrefix(strings.TrimSpace(adAccountID), "act_")
	var r struct {
		AccountStatus  int    `json:"account_status"`
		IsPrepay       bool   `json:"is_prepay_account"`
		FundingSource  string `json:"funding_source"`
		FundingDetails *struct {
			ID            string `json:"id"`
			DisplayString string `json:"display_string"`
			Type          int    `json:"type"`
		} `json:"funding_source_details"`
		SpendCap    string `json:"spend_cap"`
		AmountSpent string `json:"amount_spent"`
	}
	q := url.Values{"fields": {"account_status,is_prepay_account,funding_source,funding_source_details,spend_cap,amount_spent"}}
	if err := c.call(ctx, http.MethodGet, "/act_"+acct, token, q, &r); err != nil {
		return Funding{}, err
	}
	f := Funding{Status: r.AccountStatus, Prepaid: r.IsPrepay, HasSource: r.FundingSource != "", AvailableCents: -1}
	if d := r.FundingDetails; d != nil {
		f.HasSource = f.HasSource || d.ID != ""
		f.SourceType, f.Source = d.Type, strings.TrimSpace(d.DisplayString)
		if d.Type == 20 {
			f.Prepaid = true
		}
		if f.Prepaid {
			f.AvailableCents = ParseBRL(f.Source)
		}
	}
	f.SpendCapCents, _ = strconv.ParseInt(r.SpendCap, 10, 64)
	f.SpentCents, _ = strconv.ParseInt(r.AmountSpent, 10, 64)
	return f, nil
}

var brlRe = regexp.MustCompile(`R\$\s*(\d[\d.,]*)`)

// ParseBRL lê o primeiro valor em reais do texto ("R$1.234,56", "R$50.00", "R$ 10") em centavos; -1 se não há.
func ParseBRL(s string) int64 {
	m := brlRe.FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	num := strings.TrimRight(m[1], ".,")
	intPart, frac := num, ""
	// O separador decimal é o último , ou . seguido de 1 ou 2 dígitos no fim.
	if i := strings.LastIndexAny(num, ".,"); i >= 0 && len(num)-i-1 <= 2 && len(num)-i-1 > 0 {
		intPart, frac = num[:i], num[i+1:]
	}
	intPart = strings.NewReplacer(".", "", ",", "").Replace(intPart)
	for len(frac) < 2 {
		frac += "0"
	}
	v, err := strconv.ParseInt(intPart+frac, 10, 64)
	if err != nil {
		return -1
	}
	return v
}

// O cliente da Meta lê o pagamento da conta (a publicação usa quando o Publisher é este cliente).
var _ FundingReader = (*Client)(nil)
