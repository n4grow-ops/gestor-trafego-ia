package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"
)

// ProfileRef é o perfil do Facebook que o cliente mandou: ID numérico ou nome de usuário.
type ProfileRef struct {
	Kind  string // "id" | "username" | "share" (link de compartilhamento ainda não resolvido)
	Value string // ID, nome de usuário ou a URL de compartilhamento
	URL   string // link original, normalizado
}

// fbLink acha candidatos a link do Facebook no meio do texto (com ou sem https://).
var fbLink = regexp.MustCompile(`(?i)(?:https?://)?(?:[a-z0-9-]+\.)*(?:facebook\.com|fb\.com|fb\.me)(?:/[^\s<>"'«»]*)?`)

var (
	numericID = regexp.MustCompile(`^[0-9]{1,25}$`)
	username  = regexp.MustCompile(`^[a-z0-9][a-z0-9.]{2,49}$`)
	// Versão da Graph API no caminho (/v18.0/dialog/oauth): rota da plataforma, não perfil.
	apiVersion = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)?$`)
)

// Hosts aceitos do Facebook (subdomínios como developers. ou business. ficam de fora).
var fbHosts = map[string]bool{
	"facebook.com": true, "www.facebook.com": true, "m.facebook.com": true, "web.facebook.com": true,
	"mobile.facebook.com": true, "touch.facebook.com": true, "mbasic.facebook.com": true,
	"fb.com": true, "www.fb.com": true, "m.fb.com": true,
}

var shortHosts = map[string]bool{"fb.me": true, "www.fb.me": true}

// Primeiros trechos de caminho que não são perfil.
var reservedPaths = map[string]bool{
	"share": true, "sharer": true, "groups": true, "pages": true, "pg": true, "events": true, "watch": true,
	"reel": true, "reels": true, "photo": true, "photos": true, "media": true, "video": true, "videos": true,
	"live": true, "story": true, "stories": true, "posts": true, "permalink": true, "login": true, "logout": true,
	"recover": true, "checkpoint": true, "help": true, "marketplace": true, "gaming": true, "settings": true,
	"privacy": true, "policies": true, "policy": true, "legal": true, "terms": true, "business": true, "ads": true,
	"adsmanager": true, "hashtag": true, "search": true, "messages": true, "messenger": true,
	"notifications": true, "friends": true, "find-friends": true, "people": true, "dialog": true, "plugins": true,
	"bookmarks": true, "saved": true, "memories": true, "fundraisers": true, "donate": true, "jobs": true,
	"careers": true, "about": true, "directory": true, "places": true, "home": true, "me": true, "reg": true,
	"r": true, "l": true, "tr": true, "badges": true, "lite": true, "developers": true, "apps": true,
	"notes": true, "offers": true, "community": true, "communities": true, "feeds": true, "latest": true,
	"profile": true, "public": true, "campaign": true, "games": true, "pay": true, "facebookpay": true,
	"commerce": true, "shops": true, "shop": true, "store": true, "page": true,
	"accountscenter": true, "security": true, "oauth": true, "connect": true, "v": true,
}

// ParseProfileLink acha um link de perfil do Facebook no texto.
func ParseProfileLink(text string) (ProfileRef, bool) {
	for _, loc := range fbLink.FindAllStringIndex(text, -1) {
		// "notfacebook.com" ou "a@facebook.com" não são link do Facebook.
		if loc[0] > 0 && strings.ContainsAny(text[loc[0]-1:loc[0]], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-@_") {
			continue
		}
		raw := strings.TrimRight(text[loc[0]:loc[1]], ".,;:!?)]}>'\"»")
		if ref, ok := parseFBURL(raw); ok {
			return ref, true
		}
	}
	return ProfileRef{}, false
}

// parseFBURL lê um único link (com ou sem esquema) do Facebook.
func parseFBURL(raw string) (ProfileRef, bool) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := neturl.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ProfileRef{}, false
	}
	host := strings.ToLower(u.Hostname())
	segs := pathSegments(u.Path)
	if shortHosts[host] {
		if len(segs) == 0 {
			return ProfileRef{}, false
		}
		s := "https://fb.me/" + escapeSegments(segs)
		return ProfileRef{Kind: "share", Value: s, URL: s}, true
	}
	if !fbHosts[host] || len(segs) == 0 {
		return ProfileRef{}, false
	}
	first := strings.ToLower(segs[0])
	switch {
	case first == "profile.php":
		id := u.Query().Get("id")
		if !numericID.MatchString(id) {
			return ProfileRef{}, false
		}
		return idRef(id), true
	case first == "people":
		if len(segs) < 3 || !numericID.MatchString(segs[2]) {
			return ProfileRef{}, false
		}
		return ProfileRef{Kind: "id", Value: segs[2],
			URL: "https://www.facebook.com/people/" + neturl.PathEscape(segs[1]) + "/" + segs[2]}, true
	case first == "share":
		if len(segs) < 2 {
			return ProfileRef{}, false
		}
		s := "https://www.facebook.com/" + escapeSegments(segs)
		return ProfileRef{Kind: "share", Value: s, URL: s}, true
	case len(segs) > 1:
		// Perfil é só /{id} ou /{usuário}: /pizzariaboa/posts/1, /nome/about etc. são página ou post.
		return ProfileRef{}, false
	case numericID.MatchString(first):
		return idRef(first), true
	case reservedPaths[first] || strings.Contains(first, ".php") || apiVersion.MatchString(first):
		return ProfileRef{}, false
	case username.MatchString(first) && strings.ContainsAny(first, "abcdefghijklmnopqrstuvwxyz") && !strings.HasSuffix(first, "."):
		return ProfileRef{Kind: "username", Value: first, URL: "https://www.facebook.com/" + first}, true
	}
	return ProfileRef{}, false
}

func idRef(id string) ProfileRef {
	return ProfileRef{Kind: "id", Value: id, URL: "https://www.facebook.com/profile.php?id=" + id}
}

func pathSegments(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func escapeSegments(segs []string) string {
	esc := make([]string, len(segs))
	for i, s := range segs {
		esc[i] = neturl.PathEscape(s)
	}
	return strings.Join(esc, "/")
}

// profileFromURL diz se a URL (ou o next= da tela de login) já é um perfil (ID ou usuário).
func profileFromURL(u *neturl.URL) (string, bool) {
	if ref, ok := parseFBURL(u.String()); ok && ref.Kind != "share" {
		return ref.URL, true
	}
	if next := u.Query().Get("next"); next != "" {
		if ref, ok := parseFBURL(next); ok && ref.Kind != "share" {
			return ref.URL, true
		}
	}
	return "", false
}

const (
	browserUA       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	maxShareHops    = 5
	shareTimeout    = 10 * time.Second
	maxShareHTMLLen = 512 << 10
)

var (
	errShareInvalid  = errors.New("meta: link de compartilhamento inválido")
	errShareNoProfil = errors.New("meta: link de compartilhamento não leva a um perfil")
	errShareLeft     = errors.New("meta: link de compartilhamento saiu do Facebook")
	errShareHops     = errors.New("meta: link de compartilhamento com redirects demais")
	ogURL            = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:url["'][^>]*content=["']([^"']+)["']`)
	canonicalURL     = regexp.MustCompile(`(?i)<link[^>]+rel=["']canonical["'][^>]*href=["']([^"']+)["']`)
)

// ResolveShareLink segue o redirect de um link de compartilhamento até o perfil.
// Só segue links do Facebook (no máximo 5 redirects); devolve o link normalizado do perfil.
func ResolveShareLink(ctx context.Context, hc *http.Client, url string) (string, error) {
	start, err := neturl.Parse(strings.TrimSpace(url))
	if err != nil || !onFacebook(start) {
		return "", errShareInvalid
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, shareTimeout)
		defer cancel()
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	var found string
	c := *hc
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !onFacebook(req.URL) {
			return errShareLeft
		}
		if p, ok := profileFromURL(req.URL); ok {
			found = p // chegou no perfil: não precisa abrir a página
			return http.ErrUseLastResponse
		}
		if len(via) >= maxShareHops {
			return errShareHops
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, start.String(), nil)
	if err != nil {
		return "", errShareInvalid
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9")
	resp, err := c.Do(req)
	if err != nil {
		var ue *neturl.Error
		if errors.As(err, &ue) && (errors.Is(ue.Err, errShareLeft) || errors.Is(ue.Err, errShareHops)) {
			return "", ue.Err
		}
		return "", fmt.Errorf("meta: link de compartilhamento: %w", errors.Unwrap(err))
	}
	defer resp.Body.Close()
	if found != "" {
		return found, nil
	}
	if p, ok := profileFromURL(resp.Request.URL); ok {
		return p, nil
	}
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxShareHTMLLen))
		for _, re := range []*regexp.Regexp{ogURL, canonicalURL} {
			if m := re.FindSubmatch(body); m != nil {
				if u, err := neturl.Parse(html.UnescapeString(string(m[1]))); err == nil && onFacebook(u) {
					if p, ok := profileFromURL(u); ok {
						return p, nil
					}
				}
			}
		}
	}
	return "", errShareNoProfil
}

func onFacebook(u *neturl.URL) bool {
	h := strings.ToLower(u.Hostname())
	return (u.Scheme == "https" || u.Scheme == "http") && (fbHosts[h] || shortHosts[h])
}

// AppTesters devolve os IDs (do app) dos testadores que já aceitaram o convite.
// Usa o token do app (APP_ID|APP_SECRET) e segue a paginação de /{app}/roles.
func (c *Client) AppTesters(ctx context.Context) ([]string, error) {
	if c.AppID == "" || c.AppSecret == "" {
		return nil, errors.New("meta: app sem ID ou segredo")
	}
	token := c.AppID + "|" + c.AppSecret
	path := "/" + neturl.PathEscape(c.AppID) + "/roles"
	seen := map[string]bool{}
	ids := []string{}
	after := ""
	for range 50 {
		q := c.auth(token, neturl.Values{"limit": {"100"}})
		if after != "" {
			q.Set("after", after)
		}
		var out struct {
			Data []struct {
				User json.RawMessage `json:"user"`
				Role string          `json:"role"`
			} `json:"data"`
			Paging struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := c.get(ctx, path, q, &out); err != nil {
			return nil, err
		}
		for _, r := range out.Data {
			if r.Role != "testers" {
				continue
			}
			if id := roleUserID(r.User); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		next := out.Paging.Cursors.After
		if out.Paging.Next == "" || next == "" || next == after {
			return ids, nil
		}
		after = next
	}
	return nil, fmt.Errorf("meta: %s: páginas demais", path)
}

// roleUserID lê o usuário do papel, que vem como "123" ou {"id":"123"}.
func roleUserID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var o struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.ID
	}
	return ""
}
