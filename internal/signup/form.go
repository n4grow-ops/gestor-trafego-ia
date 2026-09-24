// Package signup tem as regras do cadastro do cliente: documentos, formulário, token do link e os
// clientes externos (BrasilAPI para CNPJ, Supabase Auth para o e-mail verificado).
package signup

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Option é uma escolha de lista fechada: o código vai para o banco, o rótulo para a tela.
type Option struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// Listas do formulário. A página monta os campos a partir delas (GET do cadastro), então há uma fonte só.
var (
	Segments = []Option{
		{"restaurante", "Restaurante e alimentação"}, {"varejo", "Loja e varejo"}, {"servicos", "Serviços"},
		{"saude", "Saúde"}, {"beleza", "Beleza e estética"}, {"educacao", "Educação e cursos"},
		{"imobiliario", "Imóveis"}, {"automotivo", "Automotivo"}, {"outro", "Outro"},
	}
	Objectives = []Option{
		{"mensagens", "Receber mensagens no WhatsApp"}, {"vendas", "Vender no site"},
		{"leads", "Conseguir cadastros (leads)"}, {"visitas", "Levar pessoas à loja"},
		{"engajamento", "Ganhar seguidores e engajamento"},
	}
	MonthlyBudgets = []Option{
		{"ate_500", "Até R$ 500 por mês"}, {"500_1500", "De R$ 500 a R$ 1.500"},
		{"1500_5000", "De R$ 1.500 a R$ 5.000"}, {"acima_5000", "Acima de R$ 5.000"},
	}
	AdsExperiences = []Option{
		{"nunca", "Nunca anunciei"}, {"menos_6m", "Anuncio há menos de 6 meses"}, {"mais_6m", "Anuncio há mais de 6 meses"},
	}
	MetaAdAccounts = []Option{{"sim", "Sim"}, {"nao", "Não"}, {"nao_sei", "Não sei"}}
	Approvers      = []Option{{"self", "Eu mesmo"}, {"other", "Outra pessoa"}}
	UFs            = strings.Fields("AC AL AP AM BA CE DF ES GO MA MT MS MG PA PB PR PE PI RJ RN RS RO RR SC SP SE TO")
)

// Form é o que a página envia. E-mail não está aqui: vem do Supabase Auth.
type Form struct {
	FullName       string `json:"full_name"`
	CPF            string `json:"cpf"`
	BusinessName   string `json:"business_name"`
	CNPJ           string `json:"cnpj"`
	LegalName      string `json:"legal_name"`
	Segment        string `json:"segment"`
	SegmentOther   string `json:"segment_other"`
	City           string `json:"city"`
	UF             string `json:"uf"`
	Website        string `json:"website"`
	Instagram      string `json:"instagram"`
	Objective      string `json:"objective"`
	MonthlyBudget  string `json:"monthly_budget"`
	AdsExperience  string `json:"ads_experience"`
	MetaAdAccount  string `json:"meta_ad_account"`
	Approver       string `json:"approver"`
	ApproverName   string `json:"approver_name"`
	ReferralSource string `json:"referral_source"`
	AcceptTerms    bool   `json:"accept_terms"`
}

// ValidationError lista o problema de cada campo, para a página marcar.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+": "+v)
	}
	return "cadastro inválido: " + strings.Join(parts, "; ")
}

const (
	shortMax = 120
	longMax  = 200
)

var spaces = regexp.MustCompile(`\s+`)

// clean tira espaços repetidos e corta no tamanho máximo.
func clean(s string, max int) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

// Normalize limpa e valida o formulário. Devolve *ValidationError com todos os campos com problema.
func (f Form) Normalize() (Form, error) {
	errs := map[string]string{}
	out := Form{
		FullName: clean(f.FullName, shortMax), BusinessName: clean(f.BusinessName, shortMax),
		LegalName: clean(f.LegalName, longMax), Segment: clean(f.Segment, 40), SegmentOther: clean(f.SegmentOther, shortMax),
		City: clean(f.City, shortMax), UF: strings.ToUpper(clean(f.UF, 2)),
		Website: clean(f.Website, longMax), Instagram: clean(f.Instagram, shortMax),
		Objective: clean(f.Objective, 40), MonthlyBudget: clean(f.MonthlyBudget, 40),
		AdsExperience: clean(f.AdsExperience, 40), MetaAdAccount: clean(f.MetaAdAccount, 40),
		Approver: clean(f.Approver, 40), ApproverName: clean(f.ApproverName, shortMax),
		ReferralSource: clean(f.ReferralSource, longMax), AcceptTerms: f.AcceptTerms,
	}
	required := map[string]string{"full_name": out.FullName, "business_name": out.BusinessName, "city": out.City}
	for field, v := range required {
		if v == "" {
			errs[field] = "obrigatório"
		}
	}
	oneOf := func(field, v string, opts []Option) {
		for _, o := range opts {
			if o.Code == v {
				return
			}
		}
		errs[field] = "escolha uma opção"
	}
	oneOf("segment", out.Segment, Segments)
	oneOf("objective", out.Objective, Objectives)
	oneOf("monthly_budget", out.MonthlyBudget, MonthlyBudgets)
	oneOf("ads_experience", out.AdsExperience, AdsExperiences)
	oneOf("meta_ad_account", out.MetaAdAccount, MetaAdAccounts)
	oneOf("approver", out.Approver, Approvers)
	if out.Segment == "outro" && out.SegmentOther == "" {
		errs["segment_other"] = "diga qual é o segmento"
	}
	if out.Approver == "other" && out.ApproverName == "" {
		errs["approver_name"] = "diga quem aprova"
	}
	if !validUF(out.UF) {
		errs["uf"] = "UF inválida"
	}
	if f.CPF != "" {
		if !ValidCPF(f.CPF) {
			errs["cpf"] = "CPF inválido"
		}
		out.CPF = OnlyDigits(f.CPF)
	}
	if f.CNPJ != "" {
		if !ValidCNPJ(f.CNPJ) {
			errs["cnpj"] = "CNPJ inválido"
		}
		out.CNPJ = OnlyDigits(f.CNPJ)
	}
	if !out.AcceptTerms {
		errs["accept_terms"] = "é preciso aceitar os Termos e a Política de Privacidade"
	}
	if len(errs) > 0 {
		return Form{}, &ValidationError{Fields: errs}
	}
	return out, nil
}

// SegmentLabel é o segmento para mostrar e guardar: o texto do cliente quando for "outro".
func (f Form) SegmentLabel() string {
	if f.Segment == "outro" && f.SegmentOther != "" {
		return f.SegmentOther
	}
	for _, o := range Segments {
		if o.Code == f.Segment {
			return o.Label
		}
	}
	return f.Segment
}

func validUF(uf string) bool { return slices.Contains(UFs, uf) }

// OnlyDigits deixa só os dígitos.
func OnlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ValidCPF confere os dígitos verificadores. Máscara é aceita; letras não.
func ValidCPF(s string) bool {
	d := OnlyDigits(s)
	if len(d) != 11 || strings.Trim(s, "0123456789.- ") != "" || allSame(d) {
		return false
	}
	return checkDigit(d[:9], []int{10, 9, 8, 7, 6, 5, 4, 3, 2}) == d[9] &&
		checkDigit(d[:10], []int{11, 10, 9, 8, 7, 6, 5, 4, 3, 2}) == d[10]
}

// ValidCNPJ confere os dígitos verificadores. Máscara é aceita; letras não.
func ValidCNPJ(s string) bool {
	d := OnlyDigits(s)
	if len(d) != 14 || strings.Trim(s, "0123456789./- ") != "" || allSame(d) {
		return false
	}
	return checkDigit(d[:12], []int{5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}) == d[12] &&
		checkDigit(d[:13], []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}) == d[13]
}

// checkDigit é o módulo 11 da Receita: resto < 2 vira 0.
func checkDigit(digits string, weights []int) byte {
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	r := sum % 11
	if r < 2 {
		return '0'
	}
	return byte('0' + 11 - r)
}

func allSame(d string) bool { return strings.Count(d, d[:1]) == len(d) }

// FormatCNPJ devolve o CNPJ com máscara (para mostrar).
func FormatCNPJ(d string) string {
	if len(d) != 14 {
		return d
	}
	return fmt.Sprintf("%s.%s.%s/%s-%s", d[:2], d[2:5], d[5:8], d[8:12], d[12:])
}
