package meta

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Posts que o cliente já publicou, para virar anúncio como estão: os mais recentes do Instagram vinculado
// e da Página do Facebook. Ler a Página pede pages_read_engagement (token da Página).

const (
	recentIGFields   = igFields + ",timestamp"
	pagePostFields   = "id,message,permalink_url,created_time,full_picture,attachments{media_type,media{image{src},source},subattachments{media_type,media{image{src},source}}}"
	recentPostsLimit = 25
)

// PagePost é um post da Página do Facebook. Items: fotos e vídeos do post na ordem (álbum traz vários).
type PagePost struct {
	ID        string // <página>_<post>, o object_story_id do anúncio
	Message   string
	Permalink string
	Created   time.Time
	Items     []PagePostItem
}

// PagePostItem é uma foto ou um vídeo do post. URL do vídeo é o arquivo; da foto, a imagem maior.
type PagePostItem struct {
	Kind string // image | video
	URL  string
}

// RecentInstagramMedia devolve os n posts mais recentes da conta do Instagram (n até 25).
func (c *Client) RecentInstagramMedia(ctx context.Context, token, igUserID string, n int) ([]IGMedia, error) {
	if strings.TrimSpace(igUserID) == "" {
		return nil, errors.New("meta: conta do Instagram vazia")
	}
	var page struct {
		Data []struct {
			ID           string `json:"id"`
			Permalink    string `json:"permalink"`
			Shortcode    string `json:"shortcode"`
			MediaType    string `json:"media_type"`
			MediaURL     string `json:"media_url"`
			ThumbnailURL string `json:"thumbnail_url"`
			Caption      string `json:"caption"`
			Timestamp    string `json:"timestamp"`
		} `json:"data"`
	}
	q := url.Values{"fields": {recentIGFields}, "limit": {strconv.Itoa(clampPosts(n))}}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(igUserID)+"/media", token, q, &page); err != nil {
		return nil, err
	}
	out := make([]IGMedia, 0, len(page.Data))
	for _, m := range page.Data {
		if m.Permalink == "" {
			continue // sem link não dá pra buscar depois
		}
		out = append(out, IGMedia{ID: m.ID, Permalink: m.Permalink, Shortcode: m.Shortcode, MediaType: m.MediaType,
			MediaURL: m.MediaURL, ThumbnailURL: m.ThumbnailURL, Caption: m.Caption, Created: graphTime(m.Timestamp)})
	}
	return out, nil
}

// RecentPagePosts devolve os n posts mais recentes publicados pela Página (n até 25).
func (c *Client) RecentPagePosts(ctx context.Context, token, pageID string, n int) ([]PagePost, error) {
	if strings.TrimSpace(pageID) == "" {
		return nil, errors.New("meta: página vazia")
	}
	pageTok, err := c.pageToken(ctx, token, pageID)
	if err != nil {
		return nil, err
	}
	var page struct {
		Data []graphPagePost `json:"data"`
	}
	q := url.Values{"fields": {pagePostFields}, "limit": {strconv.Itoa(clampPosts(n))}}
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(pageID)+"/published_posts", pageTok, q, &page); err != nil {
		return nil, err
	}
	out := make([]PagePost, 0, len(page.Data))
	for _, p := range page.Data {
		if pp := p.post(); len(pp.Items) > 0 {
			out = append(out, pp) // post só de texto não vira anúncio com imagem
		}
	}
	return out, nil
}

// PagePost lê um post da Página. O post precisa ser da Página informada.
func (c *Client) PagePost(ctx context.Context, token, pageID, postID string) (PagePost, error) {
	if strings.TrimSpace(pageID) == "" || !strings.HasPrefix(postID, pageID+"_") {
		return PagePost{}, errors.New("meta: post não é da página conectada")
	}
	pageTok, err := c.pageToken(ctx, token, pageID)
	if err != nil {
		return PagePost{}, err
	}
	var p graphPagePost
	if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(postID), pageTok, url.Values{"fields": {pagePostFields}}, &p); err != nil {
		return PagePost{}, err
	}
	return p.post(), nil
}

type graphAttachment struct {
	MediaType string `json:"media_type"`
	Media     struct {
		Image struct {
			Src string `json:"src"`
		} `json:"image"`
		Source string `json:"source"`
	} `json:"media"`
}

type graphPagePost struct {
	ID          string `json:"id"`
	Message     string `json:"message"`
	Permalink   string `json:"permalink_url"`
	CreatedTime string `json:"created_time"`
	FullPicture string `json:"full_picture"`
	Attachments struct {
		Data []struct {
			graphAttachment
			Subattachments struct {
				Data []graphAttachment `json:"data"`
			} `json:"subattachments"`
		} `json:"data"`
	} `json:"attachments"`
}

func (p graphPagePost) post() PagePost {
	out := PagePost{ID: p.ID, Message: p.Message, Permalink: p.Permalink, Created: graphTime(p.CreatedTime)}
	add := func(a graphAttachment) {
		switch {
		case strings.Contains(strings.ToLower(a.MediaType), "video") && a.Media.Source != "":
			out.Items = append(out.Items, PagePostItem{Kind: "video", URL: a.Media.Source})
		case a.Media.Image.Src != "" && !strings.Contains(strings.ToLower(a.MediaType), "video"):
			out.Items = append(out.Items, PagePostItem{Kind: "image", URL: a.Media.Image.Src})
		}
	}
	for _, a := range p.Attachments.Data {
		if len(a.Subattachments.Data) > 0 {
			for _, s := range a.Subattachments.Data {
				add(s)
			}
			continue
		}
		add(a.graphAttachment)
	}
	if len(out.Items) == 0 && p.FullPicture != "" {
		out.Items = []PagePostItem{{Kind: "image", URL: p.FullPicture}}
	}
	return out
}

func clampPosts(n int) int {
	return min(max(n, 1), recentPostsLimit)
}

// graphTime lê a data da Graph API ("2026-10-20T14:03:11+0000"). Inválida = zero.
func graphTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
