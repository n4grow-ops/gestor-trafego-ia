// Comando diagwa: diagnóstico só de leitura do WhatsApp da Página conectada de uma organização.
// Mostra o que a Meta devolve (números mascarados) por cada caminho possível.
//
//	DATABASE_URL=... META_APP_SECRET=... META_TOKEN_KEY=... go run ./scripts/diagwa 13
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/secretbox"
)

var digits = regexp.MustCompile(`\+?\d[\d \-()]{7,}\d`)

func mask(s string) string {
	return digits.ReplaceAllStringFunc(s, func(m string) string {
		if len(m) <= 4 {
			return m
		}
		return "…" + m[len(m)-4:]
	})
}

func main() {
	http.DefaultClient.Timeout = 20 * time.Second
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	if _, err := conn.Exec(ctx, "SET default_transaction_read_only = on; SET search_path = app"); err != nil {
		panic(err)
	}
	var sealed []byte
	var pageID, act string
	if err := conn.QueryRow(ctx, `SELECT token_ciphertext, coalesce(page_id,''), coalesce(ad_account_id,'') FROM meta_connections
		WHERE organization_id = $1 AND status = 'active'`, os.Args[1]).Scan(&sealed, &pageID, &act); err != nil {
		panic(err)
	}
	box, err := secretbox.New(os.Getenv("META_TOKEN_KEY"))
	if err != nil {
		panic(err)
	}
	tok, err := box.Open(sealed)
	if err != nil {
		panic(err)
	}
	secret := os.Getenv("META_APP_SECRET")
	get := func(label, path, token string, q url.Values) {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(token))
		q.Set("access_token", token)
		q.Set("appsecret_proof", hex.EncodeToString(m.Sum(nil)))
		resp, err := http.Get("https://graph.facebook.com/v23.0" + path + "?" + q.Encode())
		if err != nil {
			fmt.Println(label, "ERRO rede")
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 6000))
		fmt.Printf("== %s [%d]\n%s\n\n", label, resp.StatusCode, mask(string(b)))
	}
	user := string(tok)
	fmt.Println("page:", pageID, "act:", act)
	get("permissões", "/me/permissions", user, url.Values{})
	get("páginas", "/me/accounts", user, url.Values{"fields": {"id,name"}})
	// token da Página
	var pageTok string
	{
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(user))
		q := url.Values{"fields": {"access_token"}, "access_token": {user}, "appsecret_proof": {hex.EncodeToString(m.Sum(nil))}}
		resp, err := http.Get("https://graph.facebook.com/v23.0/" + pageID + "?" + q.Encode())
		if err == nil {
			var out struct {
				AccessToken string `json:"access_token"`
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			_ = jsonUnmarshal(b, &out)
			pageTok = out.AccessToken
			fmt.Println("token da página obtido:", pageTok != "")
		}
	}
	f := url.Values{"fields": {"whatsapp_number,has_whatsapp_number,has_whatsapp_business_number"}}
	if pageTok != "" {
		get("página (token da página)", "/"+pageID, pageTok, url.Values{"fields": f["fields"]})
	}
	get("página (token do usuário)", "/"+pageID, user, url.Values{"fields": f["fields"]})
	for _, fl := range []string{"whatsapp_number", "has_whatsapp_number", "has_whatsapp_business_number", "phone", "whatsapp_business_account", "connected_whatsapp_business_account", "whatsapp_numbers", "business{id,name}"} {
		if pageTok != "" {
			get("página campo "+fl, "/"+pageID, pageTok, url.Values{"fields": {fl}})
		}
	}
	for _, ed := range []string{"/whatsapp_business_accounts", "/whatsapp_numbers", "/phone_numbers"} {
		if pageTok != "" {
			get("página aresta "+ed, "/"+pageID+ed, pageTok, url.Values{})
		}
	}
	get("negócios", "/me/businesses", user, url.Values{"fields": {"id,name"}})
	var biz string
	_ = conn.QueryRow(ctx, `SELECT coalesce(business_id,'') FROM meta_connections WHERE organization_id = $1 AND status = 'active'`, os.Args[1]).Scan(&biz)
	os.Args = append(os.Args[:2], biz)
	if biz != "" {
		get("WABA do negócio", "/"+os.Args[2]+"/owned_whatsapp_business_accounts", user, url.Values{})
		get("WABA cliente", "/"+os.Args[2]+"/client_whatsapp_business_accounts", user, url.Values{})
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
