// simmia conversa com a Mia (modelo real, via OpenRouter) sem banco nem WhatsApp, para conferir o jeito
// dela antes de publicar. Cada linha de -msgs é uma mensagem do cliente; os campos se acumulam como no bot.
//
//	OPENROUTER_API_KEY=... go run ./scripts/simmia -msgs "oi|quero anunciar minha pizzaria|..."
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/attendant"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/agent/contract"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/llm"
)

func main() {
	model := flag.String("model", "openai/gpt-4o-mini", "modelo da Atendente")
	msgs := flag.String("msgs", "", "mensagens do cliente separadas por |")
	state := flag.String("state", "collecting", "estado da conversa")
	flag.Parse()
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" || *msgs == "" {
		fmt.Fprintln(os.Stderr, "precisa de OPENROUTER_API_KEY e -msgs")
		os.Exit(2)
	}
	a := attendant.Agent{LLM: llm.NewOpenRouter("https://openrouter.ai/api/v1", key), Model: *model}
	var fields contract.Fields
	var hist []attendant.Turn
	for _, m := range strings.Split(*msgs, "|") {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		turn, _, err := a.Reply(ctx, attendant.Input{OrgName: "Teste", State: *state, Fields: fields,
			Missing: fields.Missing(), History: hist, Message: m})
		cancel()
		if err != nil {
			fmt.Println("ERRO:", err)
			os.Exit(1)
		}
		fields = fields.Merge(turn.Fields)
		got, _ := json.Marshal(turn.Fields)
		fmt.Printf("\n> CLIENTE: %s\n< MIA [%s]: %s\n  fields: %s\n", m, turn.Intent, turn.Reply, got)
		hist = append(hist, attendant.Turn{FromClient: true, Text: m}, attendant.Turn{Text: turn.Reply})
	}
	all, _ := json.MarshalIndent(fields, "", "  ")
	fmt.Printf("\nCAMPOS FINAIS:\n%s\n", all)
}
