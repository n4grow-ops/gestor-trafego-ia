package instagram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

const (
	// Limites de cada arquivo baixado: lidos em memória com corte, nunca mais que isso.
	DefaultImageLimit = 15 << 20
	DefaultVideoLimit = 300 << 20
	pageLimit         = 4 << 20

	defaultPageTimeout  = 15 * time.Second
	defaultImageTimeout = 30 * time.Second
	defaultVideoTimeout = 3 * time.Minute
	maxRedirects        = 3
	maxCaption          = 2000

	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0 Safari/537.36"
)

var (
	// ErrNotOwn: o post não está na conta do Instagram conectada.
	ErrNotOwn = errors.New("instagram: post não é da conta conectada")
	// ErrPermission: o token não tem instagram_basic (ou pages_read_engagement).
	ErrPermission = errors.New("instagram: sem permissão para ler os posts da conta")
	// ErrUnavailable: post privado, login exigido, removido ou página em outro formato.
	ErrUnavailable = errors.New("instagram: post indisponível")
	// ErrTooLarge: arquivo acima do limite.
	ErrTooLarge = errors.New("instagram: arquivo grande demais")
	// ErrHostNotAllowed: endereço fora do Instagram e das CDNs da Meta.
	ErrHostNotAllowed = errors.New("instagram: endereço fora do Instagram")
	// ErrNoGraph: sem cliente da Graph API (publicação na Meta desligada no worker).
	ErrNoGraph = errors.New("instagram: Graph API desligada")
)

// Item é uma imagem ou vídeo baixado do post.
type Item struct {
	Kind string // image | video
	Mime string
	Data []byte
}

// Post é o que foi baixado. MediaID só vem da conta do cliente (Graph API).
type Post struct {
	MediaID   string
	Permalink string
	Caption   string
	Items     []Item
	Total     int // itens que o post tem (carrossel pode ter mais do que os baixados)
}

// Graph é a parte da Graph API usada aqui. *meta.Client implementa.
type Graph interface {
	FindInstagramMedia(ctx context.Context, token, igUserID, shortcode string) (meta.IGMedia, error)
}

// Fetcher baixa os posts. Os campos zerados usam os padrões; AllowHost e AllowHTTP só mudam nos testes.
type Fetcher struct {
	Graph       Graph
	HTTP        *http.Client // transporte; redirecionamento e prazos são do Fetcher
	EmbedBase   string       // https://www.instagram.com
	AllowHost   func(host string) bool
	AllowHTTP   bool
	PageTimeout time.Duration
	ImageLimit  int64
	VideoLimit  int64
}

// New devolve o Fetcher com os padrões. graph nil = só o caminho público.
func New(graph Graph) *Fetcher {
	f := &Fetcher{}
	if graph != nil {
		f.Graph = graph
	}
	return f
}

// AllowedHost: Instagram e CDNs da Meta (cdninstagram.com, fbcdn.net). Qualquer outro host,
// IP ou porta interna fica de fora: o endereço vem da página ou da API e não pode virar SSRF.
func AllowedHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, d := range []string{"instagram.com", "cdninstagram.com", "fbcdn.net"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Own procura o post na conta do Instagram do cliente e baixa até max itens.
func (f *Fetcher) Own(ctx context.Context, token, igUserID string, l Link, max int) (Post, error) {
	if f.Graph == nil {
		return Post{}, ErrNoGraph
	}
	m, err := f.Graph.FindInstagramMedia(ctx, token, igUserID, l.Shortcode)
	switch {
	case errors.Is(err, meta.ErrIGMediaNotFound):
		return Post{}, ErrNotOwn
	case meta.IsPermissionError(err):
		return Post{}, fmt.Errorf("%w: %v", ErrPermission, err)
	case err != nil:
		return Post{}, err
	}
	type src struct{ kind, url string }
	var srcs []src
	add := func(mediaType, mediaURL string) {
		kind := "image"
		if strings.EqualFold(mediaType, "VIDEO") {
			kind = "video"
		}
		if mediaURL != "" {
			srcs = append(srcs, src{kind, mediaURL})
		}
	}
	if len(m.Children) > 0 {
		for _, c := range m.Children {
			add(c.MediaType, c.MediaURL)
		}
	} else {
		add(m.MediaType, m.MediaURL)
	}
	p := Post{MediaID: m.ID, Permalink: firstNonEmpty(m.Permalink, l.URL), Caption: clip(m.Caption), Total: len(srcs)}
	for _, s := range srcs {
		if max > 0 && len(p.Items) >= max {
			break
		}
		it, err := f.download(ctx, s.url, s.kind)
		if err != nil {
			if errors.Is(err, ErrTooLarge) && len(srcs) == 1 {
				return Post{}, err
			}
			continue // item que não baixa fica de fora; os outros seguem
		}
		p.Items = append(p.Items, it)
	}
	if len(p.Items) == 0 {
		return Post{}, fmt.Errorf("%w: nenhum item do post baixou", ErrUnavailable)
	}
	return p, nil
}

// Public baixa o post pela página pública de incorporação (melhor esforço, sem login).
func (f *Fetcher) Public(ctx context.Context, l Link, max int) (Post, error) {
	pctx, cancel := context.WithTimeout(ctx, f.pageTimeout())
	defer cancel()
	page, err := f.fetch(pctx, f.embedBase()+"/p/"+neturl.PathEscape(l.Shortcode)+"/embed/captioned/", pageLimit, "")
	if err != nil {
		return Post{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	videos, images, caption := parseEmbed(string(page.data))
	if len(videos) == 0 && (l.Kind == KindReel || l.Kind == KindTV) {
		// reel sem o endereço do vídeo na página: a imagem seria só a capa, não o anúncio que o cliente mandou
		return Post{}, fmt.Errorf("%w: reel sem vídeo na página pública", ErrUnavailable)
	}
	type src struct{ kind, url string }
	var srcs []src
	for _, v := range videos {
		srcs = append(srcs, src{"video", v})
	}
	if len(videos) == 0 {
		for _, i := range images {
			srcs = append(srcs, src{"image", i})
		}
	}
	if len(srcs) == 0 {
		return Post{}, fmt.Errorf("%w: página sem mídia (privado ou login exigido)", ErrUnavailable)
	}
	p := Post{Permalink: l.URL, Caption: caption, Total: len(srcs)}
	var firstErr error
	for _, s := range srcs {
		if max > 0 && len(p.Items) >= max {
			break
		}
		it, err := f.download(ctx, s.url, s.kind)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		p.Items = append(p.Items, it)
	}
	if len(p.Items) == 0 {
		if errors.Is(firstErr, ErrTooLarge) {
			return Post{}, firstErr
		}
		return Post{}, fmt.Errorf("%w: %v", ErrUnavailable, firstErr)
	}
	return p, nil
}

// ---------- página pública ----------

// unescapeJSON desfaz o escape do JSON embutido na página (às vezes duas vezes, dentro de string JS).
var unescapeJSON = strings.NewReplacer(`\\\/`, `/`, `\\/`, `/`, `\/`, `/`, `\\\"`, `"`, `\\"`, `"`, `\"`, `"`,
	`\\u0026`, `&`, `&`, `&`, `\\u003d`, `=`, `=`, `=`, `&amp;`, `&`)

var (
	reVideoURL   = regexp.MustCompile(`"video_url"\s*:\s*"(https?://[^"\s]+)"`)
	reDisplayURL = regexp.MustCompile(`"display_url"\s*:\s*"(https?://[^"\s]+)"`)
	reEmbedImg   = regexp.MustCompile(`<img[^>]*class="[^"]*EmbeddedMediaImage[^"]*"[^>]*>`)
	reImgSrc     = regexp.MustCompile(`\ssrc="([^"]+)"`)
	reCaption    = regexp.MustCompile(`(?s)<div class="Caption">(.*?)<div class="CaptionComments"`)
	reCaptionAlt = regexp.MustCompile(`(?s)<div class="Caption">(.*?)</div>`)
	reCapUser    = regexp.MustCompile(`(?s)<a[^>]*class="CaptionUsername"[^>]*>.*?</a>`)
	reTags       = regexp.MustCompile(`(?s)<[^>]+>`)
)

// parseEmbed tira da página de incorporação os vídeos, as imagens (na ordem, sem repetir) e a legenda.
func parseEmbed(page string) (videos, images []string, caption string) {
	norm := unescapeJSON.Replace(page)
	videos = uniqueURLs(allGroups(reVideoURL, norm))
	images = uniqueURLs(allGroups(reDisplayURL, norm))
	if len(images) == 0 {
		var srcs []string
		for _, tag := range reEmbedImg.FindAllString(page, -1) {
			if m := reImgSrc.FindStringSubmatch(tag); m != nil {
				srcs = append(srcs, html.UnescapeString(m[1]))
			}
		}
		images = uniqueURLs(srcs)
	}
	m := reCaption.FindStringSubmatch(page)
	if m == nil {
		m = reCaptionAlt.FindStringSubmatch(page)
	}
	if m != nil {
		c := reCapUser.ReplaceAllString(m[1], "")
		c = strings.ReplaceAll(c, "<br />", "\n")
		c = strings.ReplaceAll(c, "<br/>", "\n")
		c = strings.ReplaceAll(c, "<br>", "\n")
		c = html.UnescapeString(reTags.ReplaceAllString(c, ""))
		caption = clip(strings.TrimSpace(c))
	}
	return videos, images, caption
}

func allGroups(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// uniqueURLs tira repetidos pelo caminho do arquivo (a mesma imagem vem com assinaturas diferentes).
func uniqueURLs(list []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		u, err := neturl.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		key := u.Host + u.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, raw)
	}
	return out
}

// ---------- download com limite ----------

type fetched struct {
	data []byte
	mime string
}

func (f *Fetcher) download(ctx context.Context, rawURL, kind string) (Item, error) {
	limit, timeout := f.imageLimit(), defaultImageTimeout
	if kind == "video" {
		limit, timeout = f.videoLimit(), defaultVideoTimeout
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := f.fetch(dctx, rawURL, limit, kind)
	if err != nil {
		return Item{}, err
	}
	return Item{Kind: kind, Mime: r.mime, Data: r.data}, nil
}

// fetch baixa o endereço (só hosts permitidos, também nos redirecionamentos) lendo no máximo limit bytes.
// kind "" = página HTML; "image"/"video" = o tipo do arquivo precisa bater.
func (f *Fetcher) fetch(ctx context.Context, rawURL string, limit int64, kind string) (fetched, error) {
	if err := f.check(rawURL); err != nil {
		return fetched{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fetched{}, fmt.Errorf("instagram: endereço inválido")
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9,en;q=0.8")
	resp, err := f.client().Do(req)
	if err != nil {
		if errors.Is(err, ErrHostNotAllowed) {
			return fetched{}, ErrHostNotAllowed
		}
		if ctx.Err() != nil {
			return fetched{}, ctx.Err()
		}
		return fetched{}, fmt.Errorf("instagram: falha de rede")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fetched{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return fetched{}, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return fetched{}, ctx.Err()
		}
		return fetched{}, fmt.Errorf("instagram: resposta incompleta")
	}
	if int64(len(data)) > limit {
		return fetched{}, ErrTooLarge
	}
	if kind == "" {
		return fetched{data: data}, nil
	}
	mt := mediaType(resp.Header.Get("Content-Type"), data)
	if !strings.HasPrefix(mt, kind+"/") {
		return fetched{}, fmt.Errorf("%w: arquivo %q não é %s", ErrUnavailable, mt, kind)
	}
	return fetched{data: data, mime: mt}, nil
}

// mediaType usa o tipo do cabeçalho e, se não for imagem nem vídeo, o que os primeiros bytes dizem.
func mediaType(header string, data []byte) string {
	mt, _, _ := mime.ParseMediaType(header)
	if strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "video/") {
		return mt
	}
	mt, _, _ = mime.ParseMediaType(http.DetectContentType(data))
	return mt
}

func (f *Fetcher) check(rawURL string) error {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ErrHostNotAllowed
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(f.AllowHTTP && scheme == "http") {
		return ErrHostNotAllowed
	}
	allow := f.AllowHost
	if allow == nil {
		allow = AllowedHost
	}
	if u.User != nil || !allow(u.Hostname()) {
		return ErrHostNotAllowed
	}
	if p := u.Port(); p != "" && !f.AllowHTTP {
		if n, err := strconv.Atoi(p); err != nil || n != 443 {
			return ErrHostNotAllowed
		}
	}
	return nil
}

func (f *Fetcher) client() *http.Client {
	var tr http.RoundTripper
	if f.HTTP != nil {
		tr = f.HTTP.Transport
	}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return ErrUnavailable
		}
		return f.check(req.URL.String())
	}}
}

func (f *Fetcher) embedBase() string {
	if f.EmbedBase != "" {
		return strings.TrimRight(f.EmbedBase, "/")
	}
	return "https://www.instagram.com"
}

func (f *Fetcher) pageTimeout() time.Duration {
	if f.PageTimeout > 0 {
		return f.PageTimeout
	}
	return defaultPageTimeout
}

func (f *Fetcher) imageLimit() int64 {
	if f.ImageLimit > 0 {
		return f.ImageLimit
	}
	return DefaultImageLimit
}

func (f *Fetcher) videoLimit() int64 {
	if f.VideoLimit > 0 {
		return f.VideoLimit
	}
	return DefaultVideoLimit
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// clip corta a legenda em maxCaption caracteres.
func clip(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxCaption {
		return s
	}
	return string([]rune(s)[:maxCaption])
}
