package engine

import (
	"bytes"
	"context"
	"errors"

	"image"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/media"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/preview"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// JobCreativesPreview manda a prévia do anúncio no Instagram depois que a etapa dos anúncios fecha.
const JobCreativesPreview = "creatives.preview"

type PreviewJob struct {
	ConversationID int64 `json:"conversation_id"`
	DraftID        int64 `json:"draft_id"`
}

func (e *Engine) sendPreview(ctx context.Context, j PreviewJob) error {
	conv, err := e.Store.GetConversation(ctx, j.ConversationID)
	if err != nil {
		return err
	}
	data, err := e.Store.PreviewData(ctx, j.DraftID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	list, err := e.Store.ListCreatives(ctx, j.DraftID)
	if err != nil || len(list) == 0 {
		return err
	}
	feed, story := pickPreview(list)
	if feed != nil {
		post, err := e.previewPost(ctx, data, *feed)
		if err != nil {
			return err
		}
		img, err := preview.Feed(post)
		if err != nil {
			return err
		}
		if err := e.WA.SendImage(ctx, conv.ChatID, img, "image/jpeg", msgPreviewFeed); err != nil {
			return err
		}
	}
	if story != nil {
		post, err := e.previewPost(ctx, data, *story)
		if err != nil {
			return err
		}
		img, err := preview.Story(post)
		if err != nil {
			return err
		}
		caption := msgPreviewStory
		if feed == nil {
			caption = msgPreviewStoryOnly
		}
		if err := e.WA.SendImage(ctx, conv.ChatID, img, "image/jpeg", caption); err != nil {
			return err
		}
	}
	if err := e.Store.AddMessage(ctx, conv.ID, "out", "image", "[prévia do anúncio no Instagram enviada]"); err != nil {
		return err
	}
	if e.MetaConnectBaseURL == "" {
		return nil
	}
	// Projeto pronto sem conta conectada: já pede a conexão.
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	if _, ok, err := e.Store.ActiveMeta(ctx, ident.OrgID); err != nil || ok {
		return err
	}
	return e.sendMetaLink(ctx, ident, conv, "")
}

// pickPreview escolhe o anúncio do feed (imagem antes de vídeo) e o dos Stories.
func pickPreview(list []store.Creative) (feed, story *store.Creative) {
	for i := range list {
		c := &list[i]
		if c.Placement == string(media.Stories) {
			if story == nil || (story.Kind == "video" && c.Kind == "image") {
				story = c
			}
			continue
		}
		if feed == nil || (feed.Kind == "video" && c.Kind == "image") {
			feed = c
		}
	}
	return feed, story
}

func (e *Engine) previewPost(ctx context.Context, data store.PreviewData, c store.Creative) (preview.Post, error) {
	p := preview.Post{Account: data.Account, Headline: data.Copy.Headline, Text: data.Copy.Text, CTA: data.Copy.CTA,
		Format: c.Format, VideoSeconds: (c.DurationMS + 500) / 1000}
	if c.Kind != "image" {
		return p, nil
	}
	raw, err := e.Storage.Download(ctx, CreativesBucket, c.Path)
	if err != nil {
		return p, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		// imagem que o Go não decodifica (ex.: HEIC): a prévia sai com a capa neutra
		e.Log.Warn("prévia sem a imagem do anúncio", "creative_id", c.ID, "erro", err)
		return p, nil
	}
	p.Media = img
	return p, nil
}
