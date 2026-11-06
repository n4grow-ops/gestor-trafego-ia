package httpapi_test

import (
	"strings"
	"testing"
)

func TestVSLEndpoint(t *testing.T) {
	e := newSignupEnv(t)
	ok := `{"s":"00000000-0000-4000-8000-00000000000a","v":"11111111-0000-4000-8000-000000000001","e":"play","d":120.4,"t":30,"m":3,"src":"Instagram.com"}`
	if rec := e.do("POST", "/api/vsl", ok, ""); rec.Code != 204 {
		t.Fatalf("evento válido: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"sessão inválida":  `{"s":"x","v":"11111111-0000-4000-8000-000000000001","e":"play"}`,
		"evento estranho":  `{"s":"00000000-0000-4000-8000-00000000000a","v":"11111111-0000-4000-8000-000000000001","e":"pay"}`,
		"campo extra":      `{"s":"00000000-0000-4000-8000-00000000000a","v":"11111111-0000-4000-8000-000000000001","e":"play","video":"x"}`,
		"máscara grande":   `{"s":"00000000-0000-4000-8000-00000000000a","v":"11111111-0000-4000-8000-000000000001","e":"tick","m":2000000}`,
		"duração negativa": `{"s":"00000000-0000-4000-8000-00000000000a","v":"11111111-0000-4000-8000-000000000001","e":"tick","d":-1}`,
		"corpo enorme":     `{"s":"` + strings.Repeat("a", 2000) + `"}`,
	} {
		if rec := e.do("POST", "/api/vsl", body, ""); rec.Code != 400 {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestHomeCSPAllowsOnlyYouTube(t *testing.T) {
	e := newSignupEnv(t)
	rec := e.do("GET", "/", "", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self' https://www.youtube.com;", "frame-src https://www.youtube-nocookie.com https://www.youtube.com;", "connect-src 'self';", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP sem %q: %s", want, csp)
		}
	}
	body := rec.Body.String()
	if strings.Contains(body, "{{VSL_ID}}") || !strings.Contains(body, `data-vsl="B-mCESD6gtg"`) {
		t.Error("id do vídeo não entrou na página")
	}
}
