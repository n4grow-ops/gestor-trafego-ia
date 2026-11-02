package asaas

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Payment é uma cobrança do Asaas (pay_...). Value em reais; DueDate AAAA-MM-DD.
type Payment struct {
	ID          string  `json:"id"`
	Status      string  `json:"status"`
	BillingType string  `json:"billingType"`
	Value       float64 `json:"value"`
	DueDate     string  `json:"dueDate"`
	InvoiceURL  string  `json:"invoiceUrl"`
}

// FirstPayment é a primeira cobrança da assinatura (a que o checkout cobra).
func (c *Client) FirstPayment(ctx context.Context, subID string) (Payment, error) {
	var out struct {
		Data []Payment `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(subID)+"/payments", nil, &out); err != nil {
		return Payment{}, err
	}
	if len(out.Data) == 0 || out.Data[0].ID == "" {
		return Payment{}, ErrNoPayment
	}
	return out.Data[0], nil
}

// GetPayment lê uma cobrança.
func (c *Client) GetPayment(ctx context.Context, paymentID string) (Payment, error) {
	var p Payment
	err := c.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(paymentID), nil, &p)
	return p, err
}

// PixQRCode é o QR dinâmico da cobrança: imagem PNG em base64, copia-e-cola e validade.
type PixQRCode struct {
	EncodedImage   string `json:"encodedImage"`
	Payload        string `json:"payload"`
	ExpirationDate string `json:"expirationDate"`
}

// PixQRCode devolve o QR da cobrança. Cobrança criada como cartão não aceita Pix: o checkout troca a forma
// da cobrança (SetPaymentBillingType) antes de pedir de novo.
func (c *Client) PixQRCode(ctx context.Context, paymentID string) (PixQRCode, error) {
	var q PixQRCode
	if err := c.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(paymentID)+"/pixQrCode", nil, &q); err != nil {
		return q, err
	}
	if q.Payload == "" || q.EncodedImage == "" {
		return q, errors.New("asaas: QR Code do Pix vazio")
	}
	return q, nil
}

// SetPaymentBillingType troca a forma de uma cobrança pendente (só ela; a assinatura fica como está).
func (c *Client) SetPaymentBillingType(ctx context.Context, paymentID, billingType string) error {
	return c.do(ctx, http.MethodPut, "/payments/"+url.PathEscape(paymentID), map[string]string{"billingType": billingType}, nil)
}

// CreditCard são os dados do cartão. Só passam em memória para o Asaas: nunca gravar nem logar.
type CreditCard struct {
	HolderName  string `json:"holderName"`
	Number      string `json:"number"`
	ExpiryMonth string `json:"expiryMonth"` // 2 dígitos
	ExpiryYear  string `json:"expiryYear"`  // 4 dígitos
	CCV         string `json:"ccv"`
}

// CardHolder é o titular que o Asaas exige na cobrança por cartão.
type CardHolder struct {
	Name          string `json:"name"`
	Email         string `json:"email"`
	CpfCnpj       string `json:"cpfCnpj"`
	PostalCode    string `json:"postalCode"`
	AddressNumber string `json:"addressNumber"`
	Phone         string `json:"phone"`
}

// CardResult é o que interessa da cobrança paga com cartão. ChallengeURL vem quando o banco pede 3DS.
type CardResult struct {
	ID           string  // cobrança que o Asaas diz ter cobrado
	Value        float64 // valor cobrado, em reais
	Status       string
	Last4        string
	Brand        string
	CardToken    string // token do cartão no Asaas: serve para a recorrência, não é o número
	ChallengeURL string
}

// PayWithCard cobra agora a cobrança existente com o cartão. O Asaas não trata a operação como idempotente:
// em timeout, ler a cobrança (GetPayment) antes de tentar de novo. remoteIp é o IP do cliente, nunca o nosso.
func (c *Client) PayWithCard(ctx context.Context, paymentID string, card CreditCard, holder CardHolder, remoteIP string) (CardResult, error) {
	body := struct {
		CreditCard           CreditCard `json:"creditCard"`
		CreditCardHolderInfo CardHolder `json:"creditCardHolderInfo"`
		RemoteIP             string     `json:"remoteIp"`
	}{card, holder, remoteIP}
	var out struct {
		ID         string  `json:"id"`
		Value      float64 `json:"value"`
		Status     string  `json:"status"`
		CreditCard struct {
			Number string `json:"creditCardNumber"`
			Brand  string `json:"creditCardBrand"`
			Token  string `json:"creditCardToken"`
		} `json:"creditCard"`
		Challenge string `json:"threeDSecureChallengeUrl"`
	}
	if err := c.do(ctx, http.MethodPost, "/payments/"+url.PathEscape(paymentID)+"/payWithCreditCard", body, &out); err != nil {
		return CardResult{}, err
	}
	return CardResult{ID: out.ID, Value: out.Value, Status: out.Status, Last4: out.CreditCard.Number, Brand: out.CreditCard.Brand,
		CardToken: out.CreditCard.Token, ChallengeURL: out.Challenge}, nil
}

// SetSubscriptionCard põe o cartão (pelo token) na assinatura, sem cobrar agora: os meses seguintes saem
// no cartão sozinhos (o Asaas troca a forma da assinatura para CREDIT_CARD).
func (c *Client) SetSubscriptionCard(ctx context.Context, subID, cardToken, remoteIP string) error {
	return c.do(ctx, http.MethodPut, "/subscriptions/"+url.PathEscape(subID)+"/creditCard",
		map[string]string{"creditCardToken": cardToken, "remoteIp": remoteIP}, nil)
}

// SetSubscriptionBillingType troca a forma da assinatura (os próximos meses). Para cartão, use
// SetSubscriptionCard, que já leva o cartão.
func (c *Client) SetSubscriptionBillingType(ctx context.Context, subID, billingType string) error {
	return c.do(ctx, http.MethodPut, "/subscriptions/"+url.PathEscape(subID), map[string]string{"billingType": billingType}, nil)
}

// Paid diz se o status do Asaas é de cobrança paga.
func Paid(status string) bool {
	switch status {
	case "CONFIRMED", "RECEIVED", "RECEIVED_IN_CASH":
		return true
	}
	return false
}
