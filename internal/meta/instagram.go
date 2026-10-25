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

// Posts da conta do Instagram do cliente (a vinculada à Página da conexão). Pede instagram_basic
// (e pages_read_engagement) na configuração do Facebook Login for Business.

const (
	igPageSize = 50
	igMaxPages = 4 // até 200 posts mais recentes
	igFields   = "id,permalink,media_type,media_url,thumbnail_url,caption,shortcode,children{media_type,media_url,thumbnail_url}"
)

// ErrIGMediaNotFound: o post não está entre os mais recentes da conta (ou é de outra conta).
var ErrIGMediaNotFound = errors.New("meta: post não encontrado na conta do Instagram")

// IGMedia é um post da conta do Instagram do cliente. Carrossel traz os itens em Children.
type IGMedia struct {
	ID           string
	Permalink    string
	Shortcode    string
	MediaType    string // IMAGE | VIDEO | CAROUSEL_ALBUM
	MediaURL     string
	ThumbnailURL string
	Caption      string
	Children     []IGChild
	Created      time.Time // só na lista dos recentes
}

// IGChild é um item do carrossel.
type IGChild struct {
	MediaType    string
	MediaURL     string
	ThumbnailURL string
}

// FindInstagramMedia procura o post pelo shortcode (ou pelo permalink) entre os 200 mais recentes da conta.
func (c *Client) FindInstagramMedia(ctx context.Context, token, igUserID, shortcode string) (IGMedia, error) {
	if strings.TrimSpace(igUserID) == "" || strings.TrimSpace(shortcode) == "" {
		return IGMedia{}, errors.New("meta: conta do Instagram ou post vazio")
	}
	type child struct {
		MediaType    string `json:"media_type"`
		MediaURL     string `json:"media_url"`
		ThumbnailURL string `json:"thumbnail_url"`
	}
	var after string
	for range igMaxPages {
		var page struct {
			Data []struct {
				ID           string `json:"id"`
				Permalink    string `json:"permalink"`
				Shortcode    string `json:"shortcode"`
				MediaType    string `json:"media_type"`
				MediaURL     string `json:"media_url"`
				ThumbnailURL string `json:"thumbnail_url"`
				Caption      string `json:"caption"`
				Children     struct {
					Data []child `json:"data"`
				} `json:"children"`
			} `json:"data"`
			Paging struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				Next string `json:"next"`
			} `json:"paging"`
		}
		q := url.Values{"fields": {igFields}, "limit": {strconv.Itoa(igPageSize)}}
		if after != "" {
			q.Set("after", after)
		}
		if err := c.call(ctx, http.MethodGet, "/"+url.PathEscape(igUserID)+"/media", token, q, &page); err != nil {
			return IGMedia{}, err
		}
		for _, m := range page.Data {
			if m.Shortcode != shortcode && !permalinkHas(m.Permalink, shortcode) {
				continue
			}
			out := IGMedia{ID: m.ID, Permalink: m.Permalink, Shortcode: shortcode, MediaType: m.MediaType,
				MediaURL: m.MediaURL, ThumbnailURL: m.ThumbnailURL, Caption: m.Caption}
			for _, ch := range m.Children.Data {
				out.Children = append(out.Children, IGChild(ch))
			}
			return out, nil
		}
		after = page.Paging.Cursors.After
		if after == "" || page.Paging.Next == "" || len(page.Data) == 0 {
			break
		}
	}
	return IGMedia{}, ErrIGMediaNotFound
}

// permalinkHas: o permalink aponta o shortcode (/p/<code>/, /reel/<code>/, /tv/<code>/).
func permalinkHas(permalink, shortcode string) bool {
	return permalink != "" && strings.Contains(permalink+"/", "/"+shortcode+"/")
}

// IsPermissionError: a Meta recusou por falta de permissão (código 10 ou 200-299).
func IsPermissionError(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == 10 || (ae.Code >= 200 && ae.Code <= 299)
}
