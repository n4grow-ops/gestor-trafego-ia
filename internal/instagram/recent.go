package instagram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
)

// Posts que o cliente já tem: os mais recentes do Instagram e da Página do Facebook conectados, para ele
// escolher qual vira anúncio sem mandar o link.

// Fontes dos posts recentes.
const (
	SourceInstagram = "instagram"
	SourceFacebook  = "facebook"
)

// PostGraph é a parte da Graph API dos posts recentes e da Página. *meta.Client implementa.
type PostGraph interface {
	RecentInstagramMedia(ctx context.Context, token, igUserID string, n int) ([]meta.IGMedia, error)
	RecentPagePosts(ctx context.Context, token, pageID string, n int) ([]meta.PagePost, error)
	PagePost(ctx context.Context, token, pageID, postID string) (meta.PagePost, error)
}

// Choice é um post recente oferecido ao cliente.
type Choice struct {
	Source    string    `json:"source"` // instagram | facebook
	ID        string    `json:"id"`     // id do post na Graph API
	Permalink string    `json:"permalink"`
	Caption   string    `json:"caption,omitempty"`
	Kind      string    `json:"kind,omitempty"` // image | video | album
	Created   time.Time `json:"created"`
}

// Recent junta os n posts mais recentes das duas contas (do mais novo ao mais antigo). Conta vazia fica
// de fora; erro em uma só vale se a outra também não trouxe nada.
func (f *Fetcher) Recent(ctx context.Context, token, igUserID, pageID string, n int) ([]Choice, error) {
	g, ok := f.Graph.(PostGraph)
	if !ok {
		return nil, ErrNoGraph
	}
	var out []Choice
	var errs []error
	if igUserID != "" {
		list, err := g.RecentInstagramMedia(ctx, token, igUserID, n)
		errs = append(errs, err)
		for _, m := range list {
			out = append(out, Choice{Source: SourceInstagram, ID: m.ID, Permalink: m.Permalink, Caption: clip(m.Caption),
				Kind: igKind(m.MediaType), Created: m.Created})
		}
	}
	if pageID != "" {
		list, err := g.RecentPagePosts(ctx, token, pageID, n)
		errs = append(errs, err)
		for _, p := range list {
			out = append(out, Choice{Source: SourceFacebook, ID: p.ID, Permalink: p.Permalink, Caption: clip(p.Message),
				Kind: pageKind(p.Items), Created: p.Created})
		}
	}
	if len(out) == 0 {
		if err := errors.Join(errs...); err != nil {
			if slices.ContainsFunc(errs, meta.IsPermissionError) {
				return nil, fmt.Errorf("%w: %v", ErrPermission, err)
			}
			return nil, err
		}
		return nil, nil
	}
	slices.SortStableFunc(out, func(a, b Choice) int { return b.Created.Compare(a.Created) })
	return out[:min(n, len(out))], nil
}

// Facebook baixa o post da Página e entrega até max itens ao sink, um por vez. MediaID do Post é o id do
// post da Página (vira o object_story_id do anúncio).
func (f *Fetcher) Facebook(ctx context.Context, token, pageID, postID string, max int, sink Sink) (Post, error) {
	g, ok := f.Graph.(PostGraph)
	if !ok {
		return Post{}, ErrNoGraph
	}
	pp, err := g.PagePost(ctx, token, pageID, postID)
	switch {
	case meta.IsPermissionError(err):
		return Post{}, fmt.Errorf("%w: %v", ErrPermission, err)
	case err != nil:
		return Post{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	srcs := make([]source, 0, len(pp.Items))
	for _, it := range pp.Items {
		srcs = append(srcs, source{it.Kind, it.URL})
	}
	if len(srcs) == 0 {
		return Post{}, fmt.Errorf("%w: post sem foto nem vídeo", ErrUnavailable)
	}
	p := Post{MediaID: pp.ID, Permalink: pp.Permalink, Caption: clip(pp.Message), Total: len(srcs)}
	return f.collect(ctx, p, srcs, max, sink)
}

func igKind(mediaType string) string {
	switch strings.ToUpper(mediaType) {
	case "VIDEO":
		return "video"
	case "CAROUSEL_ALBUM":
		return "album"
	}
	return "image"
}

func pageKind(items []meta.PagePostItem) string {
	switch {
	case len(items) > 1:
		return "album"
	case len(items) == 1 && items[0].Kind == "video":
		return "video"
	}
	return "image"
}
