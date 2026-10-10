package meta_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

func TestParseProfileLink(t *testing.T) {
	for _, c := range []struct {
		text, kind, value, url string
	}{
		{"https://www.facebook.com/profile.php?id=100012345678901", "id", "100012345678901", "https://www.facebook.com/profile.php?id=100012345678901"},
		{"meu perfil: facebook.com/profile.php?id=100012345678901&mibextid=ZbWKwL obrigado", "id", "100012345678901", "https://www.facebook.com/profile.php?id=100012345678901"},
		{"https://m.facebook.com/profile.php?id=987654321&ref=bookmarks#x", "id", "987654321", "https://www.facebook.com/profile.php?id=987654321"},
		{"http://web.facebook.com/profile.php?id=42", "id", "42", "https://www.facebook.com/profile.php?id=42"},
		{"https://www.facebook.com/people/Maria-Silva/100087654321098/", "id", "100087654321098", "https://www.facebook.com/people/Maria-Silva/100087654321098"},
		{"https://facebook.com/100012345678901", "id", "100012345678901", "https://www.facebook.com/profile.php?id=100012345678901"},
		{"https://www.facebook.com/maria.silva.5?mibextid=LQQJ4d", "username", "maria.silva.5", "https://www.facebook.com/maria.silva.5"},
		{"Segue: fb.com/Joao.Souza/.", "username", "joao.souza", "https://www.facebook.com/joao.souza"},
		{"(https://m.facebook.com/doceria.mel)", "username", "doceria.mel", "https://www.facebook.com/doceria.mel"},
		{"https://www.facebook.com/joaosouza/", "username", "joaosouza", "https://www.facebook.com/joaosouza"},
		{"https://www.facebook.com/share/1AbCdEfGh/?mibextid=wwXIfr", "share", "https://www.facebook.com/share/1AbCdEfGh", "https://www.facebook.com/share/1AbCdEfGh"},
		{"olha fb.me/2XyZ123 aí", "share", "https://fb.me/2XyZ123", "https://fb.me/2XyZ123"},
		// O primeiro link que não é perfil não impede achar o segundo.
		{"https://www.facebook.com/groups/123 e o meu é facebook.com/ana.paula.77", "username", "ana.paula.77", "https://www.facebook.com/ana.paula.77"},
	} {
		got, ok := meta.ParseProfileLink(c.text)
		if !ok || got.Kind != c.kind || got.Value != c.value || got.URL != c.url {
			t.Errorf("%q = %+v %v; quer %s %s %s", c.text, got, ok, c.kind, c.value, c.url)
		}
	}
}

func TestParseProfileLinkRejects(t *testing.T) {
	for _, text := range []string{
		"",
		"meu facebook é maria silva",
		"https://www.facebook.com/",
		"https://www.facebook.com/groups/123456",
		"https://www.facebook.com/pages/Doceria/123",
		"https://www.facebook.com/events/123",
		"https://www.facebook.com/watch/?v=123",
		"https://www.facebook.com/reel/123",
		"https://www.facebook.com/photo/?fbid=123",
		"https://www.facebook.com/photo.php?fbid=123",
		"https://www.facebook.com/story.php?story_fbid=1&id=2",
		"https://www.facebook.com/permalink.php?story_fbid=1&id=2",
		"https://www.facebook.com/login/?next=x",
		"https://www.facebook.com/help/123",
		"https://www.facebook.com/marketplace/item/1",
		"https://www.facebook.com/gaming/x",
		"https://www.facebook.com/profile.php",
		"https://www.facebook.com/profile.php?id=abc",
		"https://developers.facebook.com/apps/1",
		"https://notfacebook.com/maria.silva",
		"https://www.instagram.com/maria.silva",
		"https://www.facebook.com/share",
		"https://fb.me/",
		// Página de empresa com caminho a mais não é perfil.
		"https://www.facebook.com/joaosouza/about",
		"facebook.com/pizzariaboa/posts/123456",
		"https://www.facebook.com/pizzariaboa/photos/a.1/2",
		"https://www.facebook.com/pizzariaboa/videos/1",
		"https://www.facebook.com/100012345678901/posts/1",
		// Rotas da plataforma.
		"https://www.facebook.com/v18.0/dialog/oauth?client_id=1",
		"https://www.facebook.com/v18.0",
		"https://www.facebook.com/v2",
		"https://www.facebook.com/commerce/products/1",
		"https://www.facebook.com/commerce",
		"https://www.facebook.com/shops/x",
		"https://www.facebook.com/shops",
		"https://www.facebook.com/profile.php?sk=about",
		"https://www.facebook.com/sharer.php?u=x",
	} {
		if got, ok := meta.ParseProfileLink(text); ok {
			t.Errorf("%q aceito: %+v", text, got)
		}
	}
}

// fbTransport manda toda requisição para o servidor de teste, como se fosse o host real.
type fbTransport struct {
	target *url.URL
	hosts  []string
}

func (f *fbTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.hosts = append(f.hosts, r.URL.Host)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = f.target.Scheme
	r2.URL.Host = f.target.Host
	r2.Host = r.URL.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func shareServer(t *testing.T) (*http.Client, *fbTransport) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.UserAgent(), "Mozilla/5.0") {
			t.Errorf("sem user-agent de navegador: %q", r.UserAgent())
		}
		switch r.Host + r.URL.Path {
		case "www.facebook.com/share/ok":
			http.Redirect(w, r, "https://www.facebook.com/profile.php?id=100012345678901&mibextid=x", http.StatusFound)
		case "fb.me/curto":
			http.Redirect(w, r, "https://www.facebook.com/share/ok", http.StatusMovedPermanently)
		case "www.facebook.com/share/login":
			http.Redirect(w, r, "https://www.facebook.com/login/?next="+url.QueryEscape("https://www.facebook.com/maria.silva.5?x=1"), http.StatusFound)
		case "www.facebook.com/share/html":
			w.Write([]byte(`<html><head><meta property="og:url" content="https://www.facebook.com/people/Ana-Lima/100055555555555/" /></head></html>`))
		case "www.facebook.com/share/grupo":
			http.Redirect(w, r, "https://www.facebook.com/groups/123", http.StatusFound)
		case "www.facebook.com/share/loop":
			http.Redirect(w, r, "https://www.facebook.com/share/loop", http.StatusFound)
		case "www.facebook.com/share/fora":
			http.Redirect(w, r, "http://169.254.169.254/latest", http.StatusFound)
		case "www.facebook.com/share/lento":
			time.Sleep(300 * time.Millisecond)
			http.Redirect(w, r, "https://www.facebook.com/maria.silva.5", http.StatusFound)
		case "www.facebook.com/login/":
			w.Write([]byte(`<html>login</html>`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	tr := &fbTransport{target: u}
	return &http.Client{Transport: tr}, tr
}

func TestResolveShareLink(t *testing.T) {
	hc, _ := shareServer(t)
	ctx := context.Background()
	for _, c := range []struct{ in, want string }{
		{"https://www.facebook.com/share/ok", "https://www.facebook.com/profile.php?id=100012345678901"},
		{"https://fb.me/curto", "https://www.facebook.com/profile.php?id=100012345678901"},
		{"https://www.facebook.com/share/login", "https://www.facebook.com/maria.silva.5"},
		{"https://www.facebook.com/share/html", "https://www.facebook.com/people/Ana-Lima/100055555555555"},
	} {
		got, err := meta.ResolveShareLink(ctx, hc, c.in)
		if err != nil || got != c.want {
			t.Errorf("%s = %q %v; quer %q", c.in, got, err, c.want)
		}
		if ref, ok := meta.ParseProfileLink(got); !ok || (ref.Kind != "id" && ref.Kind != "username") {
			t.Errorf("%s: resultado não é perfil: %+v", c.in, ref)
		}
	}
	for _, in := range []string{
		"https://www.facebook.com/share/grupo",
		"https://www.facebook.com/share/loop",
		"https://www.facebook.com/share/inexistente",
		"https://example.com/share/ok",
		"ftp://www.facebook.com/share/ok",
	} {
		if got, err := meta.ResolveShareLink(ctx, hc, in); err == nil {
			t.Errorf("%s resolveu para %q", in, got)
		}
	}
}

func TestResolveShareLinkStaysOnFacebook(t *testing.T) {
	hc, tr := shareServer(t)
	if got, err := meta.ResolveShareLink(context.Background(), hc, "https://www.facebook.com/share/fora"); err == nil {
		t.Fatalf("seguiu redirect para fora: %q", got)
	}
	for _, h := range tr.hosts {
		if h != "www.facebook.com" {
			t.Fatalf("requisição para %s", h)
		}
	}
}

func TestResolveShareLinkTimeout(t *testing.T) {
	hc, _ := shareServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := meta.ResolveShareLink(ctx, hc, "https://www.facebook.com/share/lento"); err == nil {
		t.Fatal("ignorou o prazo")
	}
}

func TestAppTesters(t *testing.T) {
	appToken := "123|" + secret
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.URL.Path != "/v23.0/123/roles" || q.Get("access_token") != appToken || q.Get("appsecret_proof") != proof(appToken) {
			t.Errorf("chamada errada: %s token=%q proof=%q", r.URL.Path, q.Get("access_token"), q.Get("appsecret_proof"))
		}
		switch q.Get("after") {
		case "":
			w.Write([]byte(`{"data":[{"app_id":"123","user":"900","role":"administrators"},{"app_id":"123","user":"901","role":"testers"},
				{"app_id":"123","user":"902","role":"developers"}],"paging":{"cursors":{"before":"a","after":"P2"},"next":"https://graph.facebook.com/v23.0/123/roles?after=P2"}}`))
		case "P2":
			w.Write([]byte(`{"data":[{"app_id":"123","user":{"id":"903"},"role":"testers"},{"app_id":"123","user":"901","role":"testers"}],
				"paging":{"cursors":{"before":"b","after":"P3"}}}`))
		default:
			t.Errorf("página a mais: %s", q.Get("after"))
		}
	}))
	defer srv.Close()
	c := meta.New("123", secret, "cfg", "https://x.test/cb")
	c.GraphURL = srv.URL
	ids, err := c.AppTesters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "901,903" || calls.Load() != 2 {
		t.Fatalf("testadores = %v em %d chamadas", ids, calls.Load())
	}
}

func TestAppTestersError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"Invalid OAuth access token","code":190}}`))
	}))
	defer srv.Close()
	c := meta.New("123", secret, "cfg", "https://x.test/cb")
	c.GraphURL = srv.URL
	_, err := c.AppTesters(context.Background())
	if err == nil || !strings.Contains(err.Error(), "190") || strings.Contains(err.Error(), secret) {
		t.Fatalf("erro = %v", err)
	}
	if _, err := meta.New("", "", "", "").AppTesters(context.Background()); err == nil {
		t.Fatal("sem app configurado não deu erro")
	}
}
