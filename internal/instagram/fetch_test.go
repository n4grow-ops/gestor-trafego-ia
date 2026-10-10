package instagram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/media/mediatest"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

var ctx = context.Background()

// testFetcher aceita o servidor de teste (127.0.0.1, http) além dos hosts reais.
func testFetcher(base string) *Fetcher {
	f := New(nil)
	f.EmbedBase = base
	f.AllowHTTP = true
	f.AllowHost = func(h string) bool { return h == "127.0.0.1" || AllowedHost(h) }
	return f
}

func TestParseEmbedImageTagFallback(t *testing.T) {
	page := `<div><img alt="x" src="https://scontent.cdninstagram.com/v/a.jpg?x=1&amp;y=2" class="EmbeddedMediaImage" /></div>
<div class="Caption"><a class="CaptionUsername">loja</a>Só legenda</div>`
	videos, images, caption := parseEmbed(page)
	if len(videos) != 0 || len(images) != 1 || images[0] != "https://scontent.cdninstagram.com/v/a.jpg?x=1&y=2" || caption != "Só legenda" {
		t.Fatalf("videos = %v images = %v caption = %q", videos, images, caption)
	}
}

func TestAllowedHost(t *testing.T) {
	for host, want := range map[string]bool{
		"instagram.com":                     true,
		"www.instagram.com":                 true,
		"scontent-gru2-1.cdninstagram.com":  true,
		"instagram.fgru5-1.fna.fbcdn.net":   true,
		"video.xx.fbcdn.net.":               true,
		"cdninstagram.com.evil.com":         false,
		"evilcdninstagram.com":              false,
		"fbcdn.net.attacker.io":             false,
		"127.0.0.1":                         false,
		"169.254.169.254":                   false,
		"localhost":                         false,
		"instagram.com@evil.com":            false,
		"metadata.google.internal":          false,
		"www.instagram.com.attacker-io.net": false,
	} {
		if got := AllowedHost(host); got != want {
			t.Errorf("AllowedHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestCheckRejectsUnsafeURLs(t *testing.T) {
	f := New(nil)
	for _, u := range []string{
		"http://scontent.cdninstagram.com/a.jpg",     // só https
		"https://evil.com/a.jpg",                     // fora da lista
		"https://user:pw@scontent.cdninstagram.com/", // credencial no endereço
		"https://scontent.cdninstagram.com:8080/a",   // porta estranha
		"file:///etc/passwd",
		"https://10.0.0.1/a.jpg",
	} {
		if err := f.check(u); !errors.Is(err, ErrHostNotAllowed) {
			t.Errorf("check(%q) = %v", u, err)
		}
	}
	if err := f.check("https://scontent-gru2-1.cdninstagram.com/v/t51/abc.jpg?stp=1&_nc_ht=x"); err != nil {
		t.Errorf("CDN recusada: %v", err)
	}
}

// embedPage imita a página /embed/captioned/: JSON escapado dentro de string JS e a legenda em HTML.
func embedPage(base string, video bool) string {
	esc := func(s string) string { return strings.ReplaceAll(s, "/", `\\\/`) }
	data := fmt.Sprintf(`{\"shortcode_media\":{\"display_url\":\"%s\",\"edge_sidecar_to_children\":{\"edges\":[`+
		`{\"node\":{\"display_url\":\"%s\"}},{\"node\":{\"display_url\":\"%s\"}}]}}}`,
		esc(base+"/cdn/1.jpg?sig=a\\u0026x=1"), esc(base+"/cdn/1.jpg?sig=b"), esc(base+"/cdn/2.jpg"))
	if video {
		data = fmt.Sprintf(`{\"shortcode_media\":{\"display_url\":\"%s\",\"video_url\":\"%s\"}}`,
			esc(base+"/cdn/thumb.jpg"), esc(base+"/cdn/v.mp4"))
	}
	return `<html><body><div class="Embed">
<img class="EmbeddedMediaImage" alt="" src="` + base + `/cdn/fallback.jpg?a=1&amp;b=2" />
<div class="Caption"><a class="CaptionUsername" href="#">pizzariaboa</a><br />Pizza de calabresa &amp; queijo<br />Peça já! 🍕<div class="CaptionComments">ver comentários</div></div>
</div><script>window.__additionalDataLoaded('extra',{"contextJSON":"` + data + `"});</script></body></html>`
}

type cdn struct {
	srv   *httptest.Server
	hits  atomic.Int32
	pages map[string]string
}

func newCDN(t *testing.T, video bool) *cdn {
	c := &cdn{}
	mux := http.NewServeMux()
	mux.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" || !strings.Contains(r.Header.Get("User-Agent"), "Mozilla") {
			t.Errorf("sem user-agent de navegador")
		}
		if !strings.HasSuffix(r.URL.Path, "/embed/captioned/") {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, "PRIVADO") {
			fmt.Fprint(w, `<html><body>Faça login para ver este post</body></html>`)
			return
		}
		fmt.Fprint(w, embedPage(c.srv.URL, video))
	})
	mux.HandleFunc("/cdn/", func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		switch r.URL.Path {
		case "/cdn/v.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			w.Write(mediatest.MP4(10, 1080, 1920, false))
		case "/cdn/html.jpg":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>login</html>"))
		default:
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(mediatest.JPEG(1080, 1080))
		}
	})
	c.srv = httptest.NewServer(mux)
	t.Cleanup(c.srv.Close)
	return c
}

func TestPublicCarouselImages(t *testing.T) {
	c := newCDN(t, false)
	f := testFetcher(c.srv.URL)
	l, _ := ParseLink("https://www.instagram.com/p/CAROSSEL1/")
	p, err := f.Public(ctx, l, 0, Sink{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 2 || p.Items[0].Kind != "image" || p.Items[0].Mime != "image/jpeg" || p.Total != 2 {
		t.Fatalf("itens = %d total = %d %+v", len(p.Items), p.Total, p.Items)
	}
	if p.Caption != "Pizza de calabresa & queijo\nPeça já! 🍕" {
		t.Fatalf("legenda = %q", p.Caption)
	}
	if p.MediaID != "" || p.Permalink != "https://www.instagram.com/p/CAROSSEL1/" {
		t.Fatalf("post = %+v", p)
	}
	// respeita o máximo pedido
	p, err = f.Public(ctx, l, 1, Sink{})
	if err != nil || len(p.Items) != 1 || p.Total != 2 {
		t.Fatalf("max 1: %v %d", err, len(p.Items))
	}
}

func TestPublicVideo(t *testing.T) {
	c := newCDN(t, true)
	f := testFetcher(c.srv.URL)
	p, err := f.Public(ctx, Link{Shortcode: "REEL1", Kind: KindReel, URL: "https://www.instagram.com/reel/REEL1/"}, 5, Sink{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 || p.Items[0].Kind != "video" || p.Items[0].Mime != "video/mp4" {
		t.Fatalf("itens = %+v", p.Items)
	}
}

func TestPublicLoginWallIsUnavailable(t *testing.T) {
	c := newCDN(t, false)
	f := testFetcher(c.srv.URL)
	_, err := f.Public(ctx, Link{Shortcode: "PRIVADO", Kind: KindPost}, 5, Sink{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("erro = %v", err)
	}
	_, err = f.Public(ctx, Link{Shortcode: "X"}, 5, Sink{})
	if err != nil {
		t.Fatalf("página normal: %v", err)
	}
	// página fora do ar
	f.EmbedBase = "http://127.0.0.1:1"
	if _, err := f.Public(ctx, Link{Shortcode: "X"}, 5, Sink{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("fora do ar: %v", err)
	}
}

func TestPublicIgnoresMediaOutsideAllowList(t *testing.T) {
	var evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(mediatest.JPEG(1080, 1080))
	}))
	t.Cleanup(evil.Close)
	evilURL := strings.Replace(evil.URL, "127.0.0.1", "localhost", 1) // localhost não está na lista
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<script>{"display_url":"%s/x.jpg","video_url":"http://169.254.169.254/latest/meta-data"}</script>`, evilURL)
	}))
	t.Cleanup(page.Close)
	f := testFetcher(page.URL)
	_, err := f.Public(ctx, Link{Shortcode: "X"}, 5, Sink{})
	if !errors.Is(err, ErrUnavailable) || evilHits.Load() != 0 {
		t.Fatalf("erro = %v acessos ao host proibido = %d", err, evilHits.Load())
	}
}

func TestDownloadBlocksRedirectOutsideAllowList(t *testing.T) {
	var evilHits atomic.Int32
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { evilHits.Add(1) }))
	t.Cleanup(evil.Close)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(evil.URL, "127.0.0.1", "localhost", 1)+"/x.jpg", http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	f := testFetcher(redir.URL)
	if _, err := f.download(ctx, redir.URL+"/a.jpg", "image"); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("erro = %v", err)
	}
	if evilHits.Load() != 0 {
		t.Fatal("seguiu redirecionamento para host fora da lista")
	}
}

func TestDownloadLimitsAndType(t *testing.T) {
	big := make([]byte, 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/declarado.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(big)
		case "/sem-tamanho.jpg": // chunked: sem Content-Length, o corte é na leitura
			w.Header().Set("Content-Type", "image/jpeg")
			for range 5 {
				w.Write(big[:1000])
				w.(http.Flusher).Flush()
			}
		case "/pagina.jpg":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>login</html>"))
		case "/sem-tipo":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(mediatest.PNG(700, 700))
		}
	}))
	t.Cleanup(srv.Close)
	f := testFetcher(srv.URL)
	f.ImageLimit = 2000
	for _, p := range []string{"/declarado.jpg", "/sem-tamanho.jpg"} {
		if _, err := f.download(ctx, srv.URL+p, "image"); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: erro = %v", p, err)
		}
	}
	if _, err := f.download(ctx, srv.URL+"/pagina.jpg", "image"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("html aceito como imagem: %v", err)
	}
	f.ImageLimit = 0
	it, err := f.download(ctx, srv.URL+"/sem-tipo", "image")
	if err != nil || it.Mime != "image/png" {
		t.Errorf("tipo pelos bytes: %v %q", err, it.Mime)
	}
}

type fakeGraph struct {
	media meta.IGMedia
	err   error
	calls int
}

func (g *fakeGraph) FindInstagramMedia(_ context.Context, token, igUserID, shortcode string) (meta.IGMedia, error) {
	g.calls++
	if token != "tok" || igUserID != "IG1" {
		return meta.IGMedia{}, errors.New("token ou conta errados")
	}
	if g.err != nil {
		return meta.IGMedia{}, g.err
	}
	if shortcode != g.media.Shortcode {
		return meta.IGMedia{}, meta.ErrIGMediaNotFound
	}
	return g.media, nil
}

func TestOwnCarouselFromGraph(t *testing.T) {
	c := newCDN(t, false)
	g := &fakeGraph{media: meta.IGMedia{ID: "1789", Shortcode: "MEU1", Permalink: "https://www.instagram.com/p/MEU1/", MediaType: "CAROUSEL_ALBUM",
		Caption: "Promo de sexta", Children: []meta.IGChild{
			{MediaType: "IMAGE", MediaURL: c.srv.URL + "/cdn/a.jpg"},
			{MediaType: "VIDEO", MediaURL: c.srv.URL + "/cdn/v.mp4", ThumbnailURL: c.srv.URL + "/cdn/t.jpg"},
			{MediaType: "IMAGE", MediaURL: "https://evil.com/x.jpg"}, // fora da lista: fica de fora
		}}}
	f := testFetcher(c.srv.URL)
	f.Graph = g
	p, err := f.Own(ctx, "tok", "IG1", Link{Shortcode: "MEU1", Kind: KindPost}, 5, Sink{})
	if err != nil {
		t.Fatal(err)
	}
	if p.MediaID != "1789" || p.Caption != "Promo de sexta" || len(p.Items) != 2 || p.Items[1].Kind != "video" || p.Total != 3 {
		t.Fatalf("post = %+v itens = %d", p, len(p.Items))
	}
}

func TestOwnErrors(t *testing.T) {
	g := &fakeGraph{media: meta.IGMedia{Shortcode: "MEU1"}}
	f := testFetcher("")
	if _, err := f.Own(ctx, "tok", "IG1", Link{Shortcode: "X"}, 5, Sink{}); !errors.Is(err, ErrNoGraph) {
		t.Fatalf("sem graph: %v", err)
	}
	f.Graph = g
	if _, err := f.Own(ctx, "tok", "IG1", Link{Shortcode: "DE_OUTRO"}, 5, Sink{}); !errors.Is(err, ErrNotOwn) {
		t.Fatalf("post de outra conta: %v", err)
	}
	// o post existe mas o item não baixa
	if _, err := f.Own(ctx, "tok", "IG1", Link{Shortcode: "MEU1"}, 5, Sink{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("sem itens: %v", err)
	}
}

func TestOwnPermissionError(t *testing.T) {
	// erro de permissão de verdade vem do meta.Client (código 10/200); aqui pelo servidor de teste
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"(#10) Application does not have permission","code":10}}`)
	}))
	t.Cleanup(srv.Close)
	mc := meta.New("app", "segredo", "cfg", "https://x/cb")
	mc.GraphURL = srv.URL
	f := testFetcher("")
	f.Graph = mc
	if _, err := f.Own(ctx, "tok", "IG1", Link{Shortcode: "A"}, 5, Sink{}); !errors.Is(err, ErrPermission) {
		t.Fatalf("erro = %v", err)
	}
}

// Memória: cada item vai para o Save assim que baixa e é solto antes do próximo download.
func TestItemsStreamOneAtATime(t *testing.T) {
	c := newCDN(t, false)
	f := testFetcher(c.srv.URL)
	var got []int
	save := func(p Post, pos int, it Item) error {
		if int(c.hits.Load()) != pos+1 {
			t.Fatalf("item %d: %d downloads antes de guardar (baixou o próximo antes)", pos, c.hits.Load())
		}
		if p.Permalink == "" || p.Total != 2 || len(it.Data) == 0 {
			t.Fatalf("post = %+v item = %d bytes", p, len(it.Data))
		}
		got = append(got, pos)
		return nil
	}
	p, err := f.Public(ctx, Link{Shortcode: "CAR", Kind: KindPost, URL: "https://www.instagram.com/p/CAR/"}, 5, Sink{Save: save})
	if err != nil || p.Got != 2 || len(p.Items) != 0 || len(got) != 2 {
		t.Fatalf("público: err = %v got = %d itens guardados no post = %d", err, p.Got, len(p.Items))
	}

	c2 := newCDN(t, false)
	g := &fakeGraph{media: meta.IGMedia{ID: "9", Shortcode: "MEU1", Children: []meta.IGChild{
		{MediaType: "IMAGE", MediaURL: c2.srv.URL + "/cdn/a.jpg"}, {MediaType: "IMAGE", MediaURL: c2.srv.URL + "/cdn/b.jpg"}}}}
	f2 := testFetcher(c2.srv.URL)
	f2.Graph = g
	c = c2
	got = nil
	p, err = f2.Own(ctx, "tok", "IG1", Link{Shortcode: "MEU1", URL: "https://www.instagram.com/p/MEU1/"}, 5, Sink{Save: save})
	if err != nil || p.Got != 2 || len(p.Items) != 0 {
		t.Fatalf("conta: err = %v got = %d itens = %d", err, p.Got, len(p.Items))
	}
}

// Item já guardado (nova tentativa do job) não é baixado de novo nem conta no máximo.
func TestSkipAvoidsDownload(t *testing.T) {
	c := newCDN(t, false)
	f := testFetcher(c.srv.URL)
	var saved []int
	p, err := f.Public(ctx, Link{Shortcode: "CAR", Kind: KindPost}, 1, Sink{
		Skip: func(_ Post, pos int) bool { return pos == 0 },
		Save: func(_ Post, pos int, _ Item) error { saved = append(saved, pos); return nil },
	})
	if err != nil || p.Got != 1 || p.Skipped != 1 || len(saved) != 1 || saved[0] != 1 || c.hits.Load() != 1 {
		t.Fatalf("err = %v got = %d skipped = %d saved = %v hits = %d", err, p.Got, p.Skipped, saved, c.hits.Load())
	}
	// tudo já guardado: não é erro
	p, err = f.Public(ctx, Link{Shortcode: "CAR", Kind: KindPost}, 5, Sink{Skip: func(Post, int) bool { return true }})
	if err != nil || p.Got != 0 || p.Skipped != 2 {
		t.Fatalf("tudo guardado: err = %v %+v", err, p)
	}
	// ErrStop do Save encerra sem erro
	p, err = f.Public(ctx, Link{Shortcode: "CAR", Kind: KindPost}, 5, Sink{Save: func(Post, int, Item) error { return ErrStop }})
	if err != nil || p.Got != 0 {
		t.Fatalf("ErrStop: err = %v %+v", err, p)
	}
	// erro do Save volta para quem chamou
	boom := errors.New("banco fora")
	if _, err = f.Public(ctx, Link{Shortcode: "CAR", Kind: KindPost}, 5, Sink{Save: func(Post, int, Item) error { return boom }}); !errors.Is(err, boom) {
		t.Fatalf("erro do Save: %v", err)
	}
}

// Prazo total esgotado no meio: para de baixar e fica com o que já baixou.
func TestBudgetExhaustedKeepsDownloaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lento.jpg" {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(mediatest.JPEG(1080, 1080))
	}))
	t.Cleanup(srv.Close)
	g := &fakeGraph{media: meta.IGMedia{ID: "9", Shortcode: "MEU1", Children: []meta.IGChild{
		{MediaType: "IMAGE", MediaURL: srv.URL + "/a.jpg"}, {MediaType: "IMAGE", MediaURL: srv.URL + "/lento.jpg"},
		{MediaType: "IMAGE", MediaURL: srv.URL + "/c.jpg"}}}}
	f := testFetcher(srv.URL)
	f.Graph = g
	bctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	n := 0
	p, err := f.Own(bctx, "tok", "IG1", Link{Shortcode: "MEU1"}, 5, Sink{Save: func(Post, int, Item) error { n++; return nil }})
	if err != nil || p.Got != 1 || n != 1 {
		t.Fatalf("err = %v got = %d", err, p.Got)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("não parou no prazo: %v", time.Since(start))
	}
	// sem nada baixado no prazo: erro
	bctx2, cancel2 := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel2()
	g.media.Children = []meta.IGChild{{MediaType: "IMAGE", MediaURL: srv.URL + "/lento.jpg"}, {MediaType: "IMAGE", MediaURL: srv.URL + "/a.jpg"}}
	if _, err := f.Own(bctx2, "tok", "IG1", Link{Shortcode: "MEU1"}, 5, Sink{Save: func(Post, int, Item) error { return nil }}); err == nil {
		t.Fatal("prazo esgotado sem item deveria dar erro")
	}
}

func TestVideoLimitAndPreallocation(t *testing.T) {
	if DefaultVideoLimit != 100<<20 {
		t.Fatalf("limite de vídeo = %d MB", DefaultVideoLimit>>20)
	}
	// com Content-Length, o buffer nasce do tamanho certo (sem dobrar a memória ao crescer)
	data, err := readBody(strings.NewReader(strings.Repeat("x", 3000)), 10000, 3000)
	if err != nil || len(data) != 3000 || cap(data) != 3001 {
		t.Fatalf("err = %v len = %d cap = %d", err, len(data), cap(data))
	}
	// acima do limite: lê só limit+1
	data, err = readBody(strings.NewReader(strings.Repeat("x", 3000)), 1000, -1)
	if err != nil || len(data) != 1001 {
		t.Fatalf("corte: err = %v len = %d", err, len(data))
	}
}

// Reel/IGTV público sem video_url (só a capa) não vira anúncio de imagem estática.
func TestPublicReelWithoutVideoIsUnavailable(t *testing.T) {
	c := newCDN(t, false) // página só com imagens
	f := testFetcher(c.srv.URL)
	for _, kind := range []string{KindReel, KindTV} {
		_, err := f.Public(ctx, Link{Shortcode: "REEL2", Kind: kind, URL: "https://www.instagram.com/reel/REEL2/"}, 5, Sink{})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s sem vídeo: erro = %v", kind, err)
		}
	}
	if c.hits.Load() != 0 {
		t.Fatalf("baixou a capa do reel: %d acessos", c.hits.Load())
	}
}
