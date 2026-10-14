package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

const (
	// JobMediaArchive guarda no Storage a foto ou o vídeo que o cliente mandou (o WhatsApp apaga em ~30 dias).
	JobMediaArchive = "media.archive"
	// ChatMediaBucket guarda a mídia da conversa.
	ChatMediaBucket = "chat-media"

	mediaLinkTTL   = 30 * 24 * time.Hour
	mediaLinkRenew = 7 * 24 * time.Hour // renova o que vence antes disso
	renewBatch     = 500
)

// MediaArchiveJob é a mídia recebida a guardar.
type MediaArchiveJob struct {
	ConversationID int64  `json:"conversation_id"`
	MessageID      int64  `json:"message_id"`
	ExternalID     string `json:"external_id"`
	Kind           string `json:"kind"` // image ou video
}

// linkFile assina o arquivo já guardado e grava o link (o painel lê por ele, sem a chave do Storage).
func (e *Engine) linkFile(ctx context.Context, kind string, ref int64, bucket, path, mime string) error {
	url, err := e.Storage.SignedURL(ctx, bucket, path, int(mediaLinkTTL/time.Second))
	if err != nil {
		return err
	}
	_, err = e.Store.SaveMediaLink(ctx, store.MediaLink{Kind: kind, RefID: ref, Bucket: bucket, Path: path, Mime: mime,
		URL: url, URLUntil: e.now().Add(mediaLinkTTL)})
	return err
}

// archiveMedia baixa a mídia do provedor e guarda com link, ligada à mensagem.
func (e *Engine) archiveMedia(ctx context.Context, j MediaArchiveJob) error {
	if e.Storage == nil {
		return nil
	}
	data, mime, err := e.WA.DownloadMedia(ctx, whatsapp.Inbound{ExternalID: j.ExternalID, Kind: whatsapp.Kind(j.Kind)})
	if err != nil {
		return err
	}
	mime = mediaMime(mime, data)
	path := fmt.Sprintf("conv/%d/%d.%s", j.ConversationID, j.MessageID, extension(mime, j.Kind))
	contentType := mime
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := e.Storage.Upload(ctx, ChatMediaBucket, path, contentType, data); err != nil {
		return err
	}
	return e.linkFile(ctx, "message", j.MessageID, ChatMediaBucket, path, mime)
}

// renewMediaLinks troca os links que vencem em menos de 7 dias (roda na limpeza diária).
func (e *Engine) renewMediaLinks(ctx context.Context) (int, error) {
	if e.Storage == nil {
		return 0, nil
	}
	list, err := e.Store.MediaLinksExpiring(ctx, e.now().Add(mediaLinkRenew), renewBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, l := range list {
		url, err := e.Storage.SignedURL(ctx, l.Bucket, l.Path, int(mediaLinkTTL/time.Second))
		if err != nil {
			e.Log.Warn("link de mídia não renovado", "media_link_id", l.ID, "erro", err)
			continue
		}
		if err := e.Store.RenewMediaLink(ctx, l.ID, url, e.now().Add(mediaLinkTTL)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func decodeMediaArchive(raw []byte) (MediaArchiveJob, error) {
	var j MediaArchiveJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, fmt.Errorf("engine: payload de mídia inválido: %w", err)
	}
	return j, nil
}
