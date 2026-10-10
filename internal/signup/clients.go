package signup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	ErrNotFound     = errors.New("signup: não encontrado")
	ErrInvalid      = errors.New("signup: documento inválido")
	ErrUnauthorized = errors.New("signup: login inválido ou e-mail não confirmado")
)

const (
	tokenAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	// 12 caracteres de 62 = ~71 bits: impossível de adivinhar com link de 24 h, uso único e limite de tentativas,
	// e curto o bastante para o link caber bonito no WhatsApp.
	tokenLength = 12
)

// NewToken gera o token do link de cadastro: raw vai na URL, hash vai para o banco.
func NewToken() (raw, hash string, err error) {
	out := make([]byte, 0, tokenLength)
	buf := make([]byte, 32)
	for len(out) < tokenLength {
		if _, err := rand.Read(buf); err != nil {
			return "", "", fmt.Errorf("signup: %w", err)
		}
		for _, b := range buf {
			// descarta 248..255 para não enviesar o alfabeto de 62 (248 = 4 × 62)
			if b < 248 && len(out) < tokenLength {
				out = append(out, tokenAlphabet[int(b)%len(tokenAlphabet)])
			}
		}
	}
	raw = string(out)
	return raw, HashToken(raw), nil
}

// HashToken é o SHA-256 em hex do token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Company são os dados públicos de um CNPJ.
type Company struct {
	LegalName string `json:"legal_name"`
	TradeName string `json:"trade_name"`
	City      string `json:"city"`
	UF        string `json:"uf"`
	Activity  string `json:"activity"`
	Status    string `json:"status"`
}

// BrasilAPI consulta CNPJ na BrasilAPI (pública e gratuita).
type BrasilAPI struct {
	BaseURL string
	HTTP    *http.Client
}

func NewBrasilAPI() *BrasilAPI {
	return &BrasilAPI{BaseURL: "https://brasilapi.com.br", HTTP: &http.Client{Timeout: 8 * time.Second}}
}

func (b *BrasilAPI) LookupCNPJ(ctx context.Context, cnpj string) (Company, error) {
	if !ValidCNPJ(cnpj) {
		return Company{}, ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.BaseURL+"/api/cnpj/v1/"+OnlyDigits(cnpj), nil)
	if err != nil {
		return Company{}, fmt.Errorf("signup: %w", err)
	}
	res, err := b.HTTP.Do(req)
	if err != nil {
		return Company{}, fmt.Errorf("signup: brasilapi: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return Company{}, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return Company{}, fmt.Errorf("signup: brasilapi status %d", res.StatusCode)
	}
	var body struct {
		RazaoSocial  string `json:"razao_social"`
		NomeFantasia string `json:"nome_fantasia"`
		Municipio    string `json:"municipio"`
		UF           string `json:"uf"`
		CNAE         string `json:"cnae_fiscal_descricao"`
		Situacao     string `json:"descricao_situacao_cadastral"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return Company{}, fmt.Errorf("signup: brasilapi: %w", err)
	}
	return Company{LegalName: body.RazaoSocial, TradeName: body.NomeFantasia, City: body.Municipio,
		UF: body.UF, Activity: body.CNAE, Status: body.Situacao}, nil
}

// User é o usuário autenticado no Supabase Auth, com e-mail confirmado.
type User struct {
	ID         string
	Email      string
	Name       string // nome da conta Google, quando houver
	VerifiedAt time.Time
}

// SupabaseAuth confere o token de acesso do Supabase perguntando ao próprio Supabase quem é o usuário.
type SupabaseAuth struct {
	URL  string
	Key  string // chave publishable
	HTTP *http.Client
}

func NewSupabaseAuth(url, key string) *SupabaseAuth {
	return &SupabaseAuth{URL: strings.TrimRight(url, "/"), Key: key, HTTP: &http.Client{Timeout: 8 * time.Second}}
}

func (a *SupabaseAuth) GetUser(ctx context.Context, accessToken string) (User, error) {
	if accessToken == "" {
		return User{}, ErrUnauthorized
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL+"/auth/v1/user", nil)
	if err != nil {
		return User{}, fmt.Errorf("signup: %w", err)
	}
	req.Header.Set("apikey", a.Key)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := a.HTTP.Do(req)
	if err != nil {
		return User{}, fmt.Errorf("signup: supabase auth: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return User{}, ErrUnauthorized
	}
	if res.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("signup: supabase auth status %d", res.StatusCode)
	}
	var body struct {
		ID          string     `json:"id"`
		Email       string     `json:"email"`
		ConfirmedAt *time.Time `json:"email_confirmed_at"`
		Meta        struct {
			FullName string `json:"full_name"`
			Name     string `json:"name"`
		} `json:"user_metadata"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return User{}, fmt.Errorf("signup: supabase auth: %w", err)
	}
	if body.ID == "" || body.Email == "" || body.ConfirmedAt == nil || body.ConfirmedAt.IsZero() {
		return User{}, ErrUnauthorized
	}
	name := body.Meta.FullName
	if name == "" {
		name = body.Meta.Name
	}
	return User{ID: body.ID, Email: strings.ToLower(strings.TrimSpace(body.Email)), Name: name, VerifiedAt: *body.ConfirmedAt}, nil
}

// SendOTP pede ao Supabase Auth o código de login por e-mail (painel da equipe). Quem chama já
// conferiu que o e-mail é da equipe; por isso o usuário pode ser criado no Auth no primeiro login.
func (a *SupabaseAuth) SendOTP(ctx context.Context, email string) error {
	res, err := a.post(ctx, "/auth/v1/otp", map[string]any{"email": email, "create_user": true})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("signup: supabase otp status %d", res.StatusCode)
	}
	return nil
}

// VerifyOTP confere o código digitado e devolve o usuário (e-mail confirmado pelo próprio código).
func (a *SupabaseAuth) VerifyOTP(ctx context.Context, email, code string) (User, error) {
	if email == "" || code == "" {
		return User{}, ErrUnauthorized
	}
	res, err := a.post(ctx, "/auth/v1/verify", map[string]any{"type": "email", "email": email, "token": code})
	if err != nil {
		return User{}, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity:
		return User{}, ErrUnauthorized
	default:
		return User{}, fmt.Errorf("signup: supabase verify status %d", res.StatusCode)
	}
	var body struct {
		User struct {
			ID          string     `json:"id"`
			Email       string     `json:"email"`
			ConfirmedAt *time.Time `json:"email_confirmed_at"`
		} `json:"user"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return User{}, fmt.Errorf("signup: supabase verify: %w", err)
	}
	u := body.User
	if u.ID == "" || u.Email == "" || u.ConfirmedAt == nil || u.ConfirmedAt.IsZero() {
		return User{}, ErrUnauthorized
	}
	return User{ID: u.ID, Email: strings.ToLower(strings.TrimSpace(u.Email)), VerifiedAt: *u.ConfirmedAt}, nil
}

func (a *SupabaseAuth) post(ctx context.Context, path string, body any) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("signup: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("signup: %w", err)
	}
	req.Header.Set("apikey", a.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("signup: supabase auth: %w", err)
	}
	return res, nil
}
