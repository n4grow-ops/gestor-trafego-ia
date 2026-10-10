// Package instagram baixa a mídia de um post do Instagram a partir do link que o cliente manda:
// da própria conta do cliente pela Graph API (token da conexão Meta) ou, sem isso, da página
// pública de incorporação do post (melhor esforço; post privado ou com login exigido falha).
package instagram

import (
	neturl "net/url"
	"regexp"
	"strings"
)

// Tipos de link de post.
const (
	KindPost = "p"
	KindReel = "reel"
	KindTV   = "tv"
)

// Link é o post apontado pelo cliente. URL é a forma canônica (www.instagram.com, sem parâmetros).
type Link struct {
	Shortcode string
	Kind      string
	URL       string
}

// reCandidate acha trechos com cara de link do Instagram dentro do texto.
// O grupo 1 é o link; o que vem antes garante que o domínio não é pedaço de outro ("notinstagram.com").
var reCandidate = regexp.MustCompile(`(?i)(?:^|[^a-z0-9.-])((?:https?://)?(?:[a-z0-9-]+\.)*(?:instagram\.com|instagr\.am)/[^\s<>"'()\[\]{}]*)`)

var reShortcode = regexp.MustCompile(`^[A-Za-z0-9_-]{2,64}$`)

// ParseLink devolve o primeiro link de post (/p/, /reel/, /reels/, /tv/) do texto.
func ParseLink(text string) (Link, bool) {
	for _, m := range candidates(text) {
		if l, ok := parseURL(m); ok {
			return l, true
		}
	}
	return Link{}, false
}

// ParseLinks devolve os links de post do texto, sem repetir o mesmo post.
func ParseLinks(text string) []Link {
	var out []Link
	seen := map[string]bool{}
	for _, m := range candidates(text) {
		l, ok := parseURL(m)
		if !ok || seen[l.Shortcode] {
			continue
		}
		seen[l.Shortcode] = true
		out = append(out, l)
	}
	return out
}

func candidates(text string) []string {
	var out []string
	for _, m := range reCandidate.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

func parseURL(raw string) (Link, bool) {
	raw = strings.TrimRight(raw, ".,;:!?")
	if !strings.Contains(strings.ToLower(raw), "://") {
		raw = "https://" + raw
	}
	u, err := neturl.Parse(raw)
	if err != nil || (strings.ToLower(u.Scheme) != "https" && strings.ToLower(u.Scheme) != "http") {
		return Link{}, false
	}
	host := strings.ToLower(u.Hostname())
	if !isInstagramHost(host) {
		return Link{}, false
	}
	segs := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	// /<usuario>/p/<code>/ também aponta o post
	if len(segs) >= 3 && !isKind(segs[0]) && isKind(segs[1]) {
		segs = segs[1:]
	}
	if len(segs) < 2 || !isKind(segs[0]) || !reShortcode.MatchString(segs[1]) {
		return Link{}, false
	}
	kind := strings.ToLower(segs[0])
	if kind == "reels" {
		kind = KindReel
	}
	return Link{Shortcode: segs[1], Kind: kind, URL: "https://www.instagram.com/" + kind + "/" + segs[1] + "/"}, true
}

func isKind(s string) bool {
	switch strings.ToLower(s) {
	case "p", "reel", "reels", "tv":
		return true
	}
	return false
}

func isInstagramHost(host string) bool {
	return host == "instagram.com" || strings.HasSuffix(host, ".instagram.com") || host == "instagr.am" || host == "www.instagr.am"
}
