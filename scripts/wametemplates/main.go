// Comando wametemplates mostra e envia para aprovação da Meta os modelos que o engine usa fora
// da janela de 24 h (instância oficial da Wame). Sem -apply só lista o que faria e o estado atual.
//
//	WAME_KEY=... WAME_TOKEN=... go run ./scripts/wametemplates [-apply]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
)

func main() {
	apply := flag.Bool("apply", false, "envia os modelos que ainda não existem")
	flag.Parse()
	base := strings.TrimRight(envOr("WAME_BASE_URL", "https://server.wame.api.br"), "/")
	key, token := os.Getenv("WAME_KEY"), os.Getenv("WAME_TOKEN")
	if key == "" {
		fail("WAME_KEY vazio")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	call := func(method, path string, body any) (int, []byte) {
		var r io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			r = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, base+"/"+key+path, r)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := client.Do(req)
		if err != nil {
			fail(err.Error())
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		return res.StatusCode, out
	}

	status, raw := call(http.MethodGet, "/templates", nil)
	if status != http.StatusOK {
		fail(fmt.Sprintf("listar modelos: status %d: %s", status, raw))
	}
	existing := map[string]string{}
	for _, name := range []string{engine.TplSignupReminder, engine.TplDailyReport, engine.TplTeamAlert} {
		if i := bytes.Index(raw, []byte(`"`+name+`"`)); i >= 0 {
			existing[name] = statusNear(raw[i:])
		}
	}
	for _, d := range engine.TemplateDefinitions {
		if st, ok := existing[d.Name]; ok {
			fmt.Printf("%-20s já existe (%s)\n", d.Name, st)
			continue
		}
		fmt.Printf("%-20s novo: %s\n", d.Name, d.Body)
		if !*apply {
			continue
		}
		body := map[string]any{
			"name": d.Name, "language": "pt_BR", "category": "UTILITY",
			"components": []map[string]any{{
				"type": "BODY", "text": d.Body,
				"example": map[string]any{"body_text": [][]string{d.Example}},
			}},
		}
		code, out := call(http.MethodPost, "/templates", body)
		fmt.Printf("%-20s enviado: status %d %s\n", d.Name, code, strings.TrimSpace(string(out)))
	}
	if !*apply {
		fmt.Println("\n(rode com -apply para enviar os novos para aprovação)")
	}
}

// statusNear pega o "status" mais próximo depois do nome, sem depender do formato exato da lista.
func statusNear(b []byte) string {
	i := bytes.Index(b, []byte(`"status":"`))
	if i < 0 || i > 2000 {
		return "status desconhecido"
	}
	rest := b[i+len(`"status":"`):]
	if j := bytes.IndexByte(rest, '"'); j > 0 {
		return string(rest[:j])
	}
	return "status desconhecido"
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "wametemplates:", msg)
	os.Exit(1)
}
