package engine

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// Storage guarda os arquivos dos anúncios (Supabase Storage, bucket privado).
type Storage interface {
	Upload(ctx context.Context, bucket, path, contentType string, data []byte) error
}

const (
	CreativesBucket = "creatives"
	maxCreatives    = 5

	creativeSaved  = "[anúncio recebido]"
	creativeFailed = "[anúncio não salvo]"
)

// saveCreative guarda a imagem ou o vídeo do anúncio junto do rascunho aprovado.
func (e *Engine) saveCreative(ctx context.Context, ident store.Identity, conv store.Conversation, in whatsapp.Inbound) string {
	d, err := e.Store.LatestApprovedDraft(ctx, conv.ID)
	if err != nil {
		e.Log.Error("criativo sem rascunho aprovado", "conversation_id", conv.ID, "erro", err)
		return creativeFailed
	}
	n, err := e.Store.CountCreatives(ctx, d.ID)
	if err != nil || n >= maxCreatives {
		return creativeSaved // já tem o bastante; o turno conclui
	}
	data, mime, err := e.WA.DownloadMedia(ctx, in)
	if err != nil {
		e.Log.Error("falha ao baixar criativo", "conversation_id", conv.ID, "erro", err)
		return creativeFailed
	}
	kind := "image"
	if in.Kind == whatsapp.KindVideo {
		kind = "video"
	}
	path := fmt.Sprintf("org/%d/draft/%d/%d-%s.%s", ident.OrgID, d.ID, e.now().UnixNano(), safeName(in.ExternalID), extension(mime, kind))
	if err := e.Storage.Upload(ctx, CreativesBucket, path, mime, data); err != nil {
		e.Log.Error("falha ao guardar criativo", "conversation_id", conv.ID, "erro", err)
		return creativeFailed
	}
	if _, err := e.Store.AddCreative(ctx, store.Creative{OrgID: ident.OrgID, DraftID: d.ID, ProjectID: d.ProjectID,
		Kind: kind, Path: path, Mime: mime, Size: int64(len(data))}); err != nil {
		e.Log.Error("falha ao registrar criativo", "conversation_id", conv.ID, "erro", err)
		return creativeFailed
	}
	e.audit(ctx, ident.OrgID, member(ident), "creative_received", map[string]any{"draft_id": d.ID, "kind": kind})
	return creativeSaved
}

// creativesTurn responde enquanto a conversa espera os anúncios (imagens ou vídeos).
func (e *Engine) creativesTurn(ctx context.Context, conv store.Conversation, msgs []string) error {
	d, err := e.Store.LatestApprovedDraft(ctx, conv.ID)
	if err != nil {
		return err
	}
	n, err := e.Store.CountCreatives(ctx, d.ID)
	if err != nil {
		return err
	}
	var received, failed, done bool
	for _, m := range msgs {
		switch {
		case strings.HasPrefix(m, creativeSaved):
			received = true
		case strings.HasPrefix(m, creativeFailed):
			failed = true
		case isDone(m):
			done = true
		}
	}
	switch {
	case n >= maxCreatives || (done && n > 0):
		if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateIdle, conv.Fields); err != nil {
			return err
		}
		if conv.ActiveProjectID != nil {
			if err := e.Store.SetProjectStage(ctx, *conv.ActiveProjectID, store.StageReady); err != nil {
				return err
			}
		}
		return e.send(ctx, conv, fmt.Sprintf(msgCreativesDone, n))
	case failed:
		return e.send(ctx, conv, msgCreativeFailed)
	case done:
		return e.send(ctx, conv, msgCreativesNeedOne)
	case received:
		return e.send(ctx, conv, fmt.Sprintf(msgCreativesAck, n, maxCreatives))
	default:
		return e.send(ctx, conv, msgCreativesWaiting)
	}
}

// isDone reconhece "pronto", "é isso", "só isso", "terminei"...
func isDone(s string) bool {
	s = strings.ToLower(strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) }))
	for _, w := range []string{"pronto", "é isso", "e isso", "só isso", "so isso", "terminei", "acabou", "finalizar", "finalizei", "ok pronto"} {
		if s == w || strings.HasPrefix(s, w+" ") {
			return true
		}
	}
	return false
}

func extension(mime, kind string) string {
	switch mime {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "video/mp4":
		return "mp4"
	case "video/quicktime":
		return "mov"
	}
	if kind == "video" {
		return "mp4"
	}
	return "jpg"
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return fmt.Sprint(time.Now().UnixNano())
	}
	return b.String()
}
