package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/media"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/preview"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// JobCreativesPreview manda a prévia do anúncio no Instagram depois que a etapa dos anúncios fecha.
const JobCreativesPreview = "creatives.preview"

// PreviewsBucket guarda as prévias que vão por link na conta oficial do WhatsApp.
const PreviewsBucket = "previews"

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
	if err := e.sendPreviewImages(ctx, conv, j.DraftID, data, list); err != nil {
		if ctx.Err() != nil {
			return err // desligando: o job volta e tenta de novo
		}
		// A prévia é um extra: se o WhatsApp recusa a imagem, o projeto segue para a Meta mesmo assim.
		e.Log.Error("prévia do anúncio não enviada", "conversation_id", conv.ID, "erro", err)
		ident, _ := e.Store.GetIdentity(ctx, conv.IdentityID)
		e.audit(ctx, ident.OrgID, "system", "preview_failed", map[string]any{"conversation_id": conv.ID, "draft_id": j.DraftID, "error": err.Error()})
		if err := e.send(ctx, conv, msgPreviewFailed); err != nil {
			return err
		}
	} else if err := e.Store.AddMessage(ctx, conv.ID, "out", "image", "[prévia do anúncio no Instagram enviada]"); err != nil {
		return err
	}
	// Projeto pronto: com conta conectada vai para a Meta; sem ela, já pede a conexão.
	// Falha aqui não reenvia a prévia.
	if err := e.afterPreview(ctx, conv, j.DraftID); err != nil {
		e.Log.Error("falha ao seguir depois da prévia", "conversation_id", conv.ID, "erro", err)
	}
	return nil
}

// sendPreviewImages monta e manda as imagens da prévia (feed e Stories).
func (e *Engine) sendPreviewImages(ctx context.Context, conv store.Conversation, draftID int64, data store.PreviewData, list []store.Creative) error {
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
		e.keepPreview(ctx, draftID, "feed", img)
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
		e.keepPreview(ctx, draftID, "story", img)
		caption := msgPreviewStory
		if feed == nil {
			caption = msgPreviewStoryOnly
		}
		if err := e.WA.SendImage(ctx, conv.ChatID, img, "image/jpeg", caption); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) afterPreview(ctx context.Context, conv store.Conversation, draftID int64) error {
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	_, ok, err := e.Store.ActiveMeta(ctx, ident.OrgID)
	switch {
	case err != nil:
		return err
	case ok:
		return e.Store.Enqueue(ctx, publishJob(conv.ID, draftID, 2*time.Second))
	case e.MetaConnectBaseURL == "":
		return nil
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

// keepPreview guarda a imagem da prévia com link, para o painel mostrar na aba do projeto. Falha só vai pro log.
func (e *Engine) keepPreview(ctx context.Context, draftID int64, name string, img []byte) {
	path := fmt.Sprintf("draft/%d/%s.jpg", draftID, name)
	if err := e.Storage.Upload(ctx, PreviewsBucket, path, "image/jpeg", img); err != nil {
		e.Log.Warn("prévia não guardada", "draft_id", draftID, "erro", err)
		return
	}
	if err := e.linkFile(ctx, "preview", draftID, PreviewsBucket, path, "image/jpeg"); err != nil {
		e.Log.Warn("prévia sem link", "draft_id", draftID, "erro", err)
	}
}
