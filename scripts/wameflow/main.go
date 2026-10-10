// Comando wameflow cria (ou atualiza), envia o JSON e publica o formulário de cadastro do WhatsApp
// pela Wame. Imprime o flowId para SIGNUP_FLOW_ID.
//
//	WAME_KEY=... WAME_TOKEN=... go run ./scripts/wameflow [-id FLOW_ID] [-publish]
//
// Sem -id cria um formulário novo (rascunho). Com -publish publica depois de enviar o JSON.
// Formulário publicado não muda: para alterar, crie outro (sem -id) e troque SIGNUP_FLOW_ID.
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

	"github.com/n4grow-ops/gestor-trafego-ia/internal/signup"
)

func main() {
	id := flag.String("id", "", "formulário existente (rascunho) para atualizar")
	publish := flag.Bool("publish", false, "publica depois de enviar o JSON")
	site := flag.String("site", "https://cadastro.meuanuncio.io", "endereço público dos termos e da privacidade")
	flag.Parse()
	base := strings.TrimRight(envOr("WAME_BASE_URL", "https://server.wame.api.br"), "/")
	key, token := os.Getenv("WAME_KEY"), os.Getenv("WAME_TOKEN")
	if key == "" {
		fail("WAME_KEY vazio")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	call := func(method, path string, body []byte) (int, []byte) {
		req, _ := http.NewRequest(method, base+"/"+key+path, bytes.NewReader(body))
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

	flowJSON, err := signup.FlowJSON(*site+"/termos", *site+"/privacidade")
	if err != nil {
		fail(err.Error())
	}
	if *id == "" {
		b, _ := json.Marshal(map[string]any{"name": "Cadastro meuanuncio.io " + time.Now().Format("2006-01-02 15h04"), "categories": []string{"SIGN_UP"}})
		code, out := call(http.MethodPost, "/flows", b)
		fmt.Printf("criar: status %d %s\n", code, strings.TrimSpace(string(out)))
		var r map[string]any
		json.Unmarshal(out, &r)
		*id = findID(r)
		if *id == "" {
			fail("não achei o id do formulário na resposta")
		}
	}
	wrapped, _ := json.Marshal(map[string]json.RawMessage{"flowJson": flowJSON})
	code, out := call(http.MethodPut, "/flows/"+*id+"/json", wrapped)
	fmt.Printf("json: status %d %s\n", code, strings.TrimSpace(string(out)))
	if bytes.Contains(out, []byte(`"validation_errors":[{`)) {
		fail("a Meta apontou erros de validação no formulário (acima)")
	}
	if *publish {
		code, out = call(http.MethodPost, "/flows/"+*id+"/publish", nil)
		fmt.Printf("publicar: status %d %s\n", code, strings.TrimSpace(string(out)))
	}
	code, out = call(http.MethodGet, "/flows/"+*id+"/preview", nil)
	fmt.Printf("prévia: status %d %s\n", code, strings.TrimSpace(string(out)))
	fmt.Printf("\nSIGNUP_FLOW_ID=%s\n", *id)
}

// findID procura o id em {id}, {flow:{id}} ou {data:{id}}.
func findID(r map[string]any) string {
	if v, ok := r["id"].(string); ok && v != "" {
		return v
	}
	for _, k := range []string{"flow", "data"} {
		if m, ok := r[k].(map[string]any); ok {
			if v, ok := m["id"].(string); ok {
				return v
			}
		}
	}
	return ""
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "wameflow:", msg)
	os.Exit(1)
}
