// Package attendant é a IA 1: conversa com o cliente e coleta os dados da campanha.
// Não tem ferramentas; devolve um contract.AttendantTurn validado.
package attendant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
)

var ErrInvalidOutput = errors.New("attendant: resposta inválida do modelo")

type Turn struct {
	FromClient bool
	Text       string
}

type Input struct {
	OrgName string
	Niche   string
	State   string
	Fields  contract.Fields
	Missing []string
	// Memória do negócio, projeto ativo e nomes dos projetos do cliente.
	Profile     contract.Profile
	ProjectName string
	Projects    []string
	History     []Turn // mais antigas primeiro, sem a mensagem atual
	Message     string
	Voice       bool // a resposta vai virar mensagem de voz
	// Meta: estado da conta de anúncios ("não conectada" ou "conectada: …"); vazio = conexão desligada.
	Meta string
}

type Agent struct {
	LLM   llm.Provider
	Model string
}

func (a *Agent) Reply(ctx context.Context, in Input) (contract.AttendantTurn, llm.Usage, error) {
	msgs := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt(in)},
	}
	var usage llm.Usage
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := a.LLM.Complete(ctx, llm.Request{Model: a.Model, Messages: msgs, JSON: true, MaxTokens: 800})
		if err != nil {
			return contract.AttendantTurn{}, usage, err
		}
		usage = usage.Add(resp.Usage)
		var turn contract.AttendantTurn
		err = json.Unmarshal([]byte(llm.StripFences(resp.Content)), &turn)
		if err == nil {
			err = turn.Validate()
		}
		if err == nil {
			return turn, usage, nil
		}
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: resp.Content},
			llm.Message{Role: "user", Content: "Sua resposta anterior era inválida (" + err.Error() + "). Responda de novo somente com o JSON no formato pedido."},
		)
	}
	return contract.AttendantTurn{}, usage, ErrInvalidOutput
}

var clientTag = regexp.MustCompile(`(?i)<(/?)(cliente)>`)

// quote envolve o texto do cliente em <cliente> e neutraliza tags que tentem fechar o bloco.
func quote(s string) string {
	return "<cliente>" + clientTag.ReplaceAllString(s, "($1$2)") + "</cliente>"
}

func userPrompt(in Input) string {
	var b strings.Builder
	fields, _ := json.Marshal(in.Fields)
	missing := "nenhum"
	if len(in.Missing) > 0 {
		missing = strings.Join(in.Missing, ", ")
	}
	profile, _ := json.Marshal(in.Profile)
	active, projects := "nenhum", "nenhum"
	if in.ProjectName != "" {
		active = in.ProjectName
	}
	if len(in.Projects) > 0 {
		projects = strings.Join(in.Projects, ", ")
	}
	business := "nenhum"
	if m := in.Profile.Missing(); len(m) > 0 {
		business = strings.Join(m, "; ")
	}
	fmt.Fprintf(&b, "Empresa: %s (nicho: %s)\nMemória do negócio: %s\nNegócio faltando: %s\nProjeto ativo: %s\nProjetos do cliente: %s\n",
		in.OrgName, in.Niche, profile, business, active, projects)
	fmt.Fprintf(&b, "Estado da conversa: %s\nDados já coletados: %s\nDados faltando: %s\n", in.State, fields, missing)
	if in.Meta != "" {
		fmt.Fprintf(&b, "Conta Meta: %s\n", in.Meta)
	}
	if in.Voice {
		b.WriteString("Formato da resposta: áudio (fale como numa mensagem de voz: sem listas, sem emoji, sem asterisco, uma parte só)\n")
	} else {
		b.WriteString("Formato da resposta: texto\n")
	}
	if len(in.History) > 0 {
		b.WriteString("Histórico recente:\n")
		for _, t := range in.History {
			if t.FromClient {
				b.WriteString("Cliente: " + quote(t.Text) + "\n")
			} else {
				b.WriteString("Atendente: " + t.Text + "\n")
			}
		}
	}
	b.WriteString("Mensagem atual do cliente:\n" + quote(in.Message))
	return b.String()
}
