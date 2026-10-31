// Package asaas fala com a API v3 do Asaas (asaas.com): cliente, assinatura mensal e link da cobrança.
package asaas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	SandboxURL    = "https://api-sandbox.asaas.com/v3"
	ProductionURL = "https://api.asaas.com/v3"
)

// Formas de cobrança aceitas (decisão do usuário: só Pix e cartão).
const (
	BillingPix  = "PIX"
	BillingCard = "CREDIT_CARD"
)

// ErrNoPayment: a assinatura ainda não tem cobrança gerada (o Asaas cria em seguida).
var ErrNoPayment = errors.New("asaas: assinatura sem cobrança")

// Client chama o Asaas com a chave da conta. A chave vai só no cabeçalho access_token, nunca no erro.
type Client struct {
	Key     string
	BaseURL string
	HTTP    *http.Client
}

// New monta o cliente; env "producao" usa a produção, qualquer outro valor o sandbox.
func New(key, env string) *Client {
	base := SandboxURL
	if strings.EqualFold(strings.TrimSpace(env), "producao") {
		base = ProductionURL
	}
	return &Client{Key: key, BaseURL: base, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Customer é o cliente no Asaas. ExternalReference = "org-<id>".
type Customer struct {
	Name                 string `json:"name"`
	CpfCnpj              string `json:"cpfCnpj,omitempty"`
	Email                string `json:"email,omitempty"`
	MobilePhone          string `json:"mobilePhone,omitempty"`
	ExternalReference    string `json:"externalReference"`
	NotificationDisabled bool   `json:"notificationDisabled"`
}

// Subscription é a assinatura mensal. Value em reais; NextDueDate AAAA-MM-DD.
type Subscription struct {
	Customer          string  `json:"customer"`
	BillingType       string  `json:"billingType"`
	Value             float64 `json:"value"`
	NextDueDate       string  `json:"nextDueDate"`
	Cycle             string  `json:"cycle"`
	Description       string  `json:"description"`
	ExternalReference string  `json:"externalReference"`
}

// FindCustomer procura o cliente pela referência; "" quando não há.
func (c *Client) FindCustomer(ctx context.Context, externalRef string) (string, error) {
	var out struct {
		Data []struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/customers?externalReference="+url.QueryEscape(externalRef), nil, &out); err != nil {
		return "", err
	}
	for _, d := range out.Data {
		if !d.Deleted && d.ID != "" {
			return d.ID, nil
		}
	}
	return "", nil
}

// CreateCustomer cria o cliente sem as notificações do Asaas (quem avisa é a Mia, pelo WhatsApp).
func (c *Client) CreateCustomer(ctx context.Context, cu Customer) (string, error) {
	cu.NotificationDisabled = true
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers", cu, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("asaas: cliente criado sem id")
	}
	return out.ID, nil
}

// CreateSubscription cria a assinatura mensal e devolve o id (sub_...).
func (c *Client) CreateSubscription(ctx context.Context, s Subscription) (string, error) {
	if s.Cycle == "" {
		s.Cycle = "MONTHLY"
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/subscriptions", s, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("asaas: assinatura criada sem id")
	}
	return out.ID, nil
}

// FirstPaymentURL é o link (invoiceUrl) da primeira cobrança da assinatura.
func (c *Client) FirstPaymentURL(ctx context.Context, subID string) (string, error) {
	var out struct {
		Data []struct {
			InvoiceURL string `json:"invoiceUrl"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/subscriptions/"+url.PathEscape(subID)+"/payments", nil, &out); err != nil {
		return "", err
	}
	if len(out.Data) == 0 || out.Data[0].InvoiceURL == "" {
		return "", ErrNoPayment
	}
	return out.Data[0].InvoiceURL, nil
}

// DeleteSubscription remove a assinatura (troca de plano ou de forma antes de pagar). Já removida (404)
// não é erro: senão o cliente ficaria preso sem conseguir gerar outra.
func (c *Client) DeleteSubscription(ctx context.Context, subID string) error {
	err := c.do(ctx, http.MethodDelete, "/subscriptions/"+url.PathEscape(subID), nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// APIError é a resposta de erro do Asaas (status e descrição, sem a chave).
type APIError struct {
	Method, Path string
	Status       int
	Message      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("asaas: %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("access_token", c.Key)
	req.Header.Set("User-Agent", "meuanuncio.io")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("asaas: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return &APIError{Method: method, Path: strings.SplitN(path, "?", 2)[0], Status: res.StatusCode, Message: apiError(raw)}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("asaas: resposta ilegível: %w", err)
	}
	return nil
}

// apiError tira a descrição do corpo de erro do Asaas ({"errors":[{"code","description"}]}).
func apiError(raw []byte) string {
	var e struct {
		Errors []struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &e) == nil && len(e.Errors) > 0 {
		parts := make([]string, 0, len(e.Errors))
		for _, x := range e.Errors {
			parts = append(parts, strings.TrimSpace(x.Code+" "+x.Description))
		}
		return strings.Join(parts, "; ")
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
