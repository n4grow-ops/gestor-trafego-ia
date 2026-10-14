package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

const (
	// JobAdsReview confere na Meta se os anúncios publicados foram aprovados ou reprovados.
	JobAdsReview     = "ads.review"
	adsReviewFirst   = 30 * time.Minute
	adsReviewEvery   = 6 * time.Hour
	adsReviewMaxRuns = 12 // 3 dias
)

// AdsReviewJob é a conferência da revisão de uma publicação.
type AdsReviewJob struct {
	PublicationID int64 `json:"publication_id"`
	Round         int   `json:"round"`
}

func adsReviewJob(pubID int64, round int, delay time.Duration) store.JobSpec {
	return store.JobSpec{Kind: JobAdsReview, Payload: AdsReviewJob{PublicationID: pubID, Round: round}, Delay: delay}
}

// scheduleAdsReview agenda a primeira conferência (depois de publicar ou ativar). Falha só vai pro log.
func (e *Engine) scheduleAdsReview(ctx context.Context, pubID int64) {
	if _, ok := e.Publisher.(meta.AdReviewer); !ok {
		return
	}
	if err := e.Store.Enqueue(ctx, adsReviewJob(pubID, 0, adsReviewFirst)); err != nil {
		e.Log.Warn("conferência da revisão não agendada", "publication_id", pubID, "erro", err)
	}
}

func (e *Engine) adsReview(ctx context.Context, raw []byte) error {
	var j AdsReviewJob
	if err := json.Unmarshal(raw, &j); err != nil {
		return fmt.Errorf("engine: payload da revisão inválido: %w", err)
	}
	rv, ok := e.Publisher.(meta.AdReviewer)
	if !ok || e.MetaTokenKey == nil {
		return nil
	}
	p, err := e.pubs().GetPublication(ctx, j.PublicationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.Status != "paused" && p.Status != "active" {
		return nil // falhou, foi reprovada ou refeita: nada a conferir
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, p.OrgID)
	if err != nil {
		return nil // sem conexão: o aviso de conexão cuida disso
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		return nil
	}
	reviews, err := rv.AdsReview(ctx, string(token), p.AdIDs)
	if err != nil {
		return err
	}
	var rejected, pending int
	var reasons []string
	for _, r := range reviews {
		switch {
		case r.Rejected():
			rejected++
			reasons = append(reasons, r.Reasons...)
		case r.InReview():
			pending++
		}
	}
	switch {
	case rejected > 0 && rejected == len(reviews):
		return e.rejectPublication(ctx, string(token), p, reasons)
	case rejected > 0:
		e.partlyRejected(ctx, p, rejected, reasons)
	case pending > 0 && j.Round+1 < adsReviewMaxRuns:
		return e.Store.Enqueue(ctx, adsReviewJob(p.ID, j.Round+1, adsReviewEvery))
	}
	return nil
}

func reasonText(reasons []string) string {
	if len(reasons) == 0 {
		return "a Meta não detalhou o motivo"
	}
	s := strings.Join(reasons, " ")
	if r := []rune(s); len(r) > 400 {
		s = string(r[:399]) + "…"
	}
	return s
}

// rejectPublication: todos os anúncios reprovados. Pausa a campanha, marca a publicação, volta o rascunho
// para a etapa dos anúncios e avisa o cliente (com o motivo) e a equipe.
func (e *Engine) rejectPublication(ctx context.Context, token string, p store.Publication, reasons []string) error {
	if err := e.Publisher.SetStatus(ctx, token, p.CampaignID, "PAUSED"); err != nil {
		e.Log.Warn("campanha reprovada não pausou", "campaign_id", p.CampaignID, "erro", err)
	}
	why := reasonText(reasons)
	if err := e.pubs().RejectPublication(ctx, p.ID, why); err != nil {
		return err
	}
	convID, moved, err := e.Store.ReopenForCreatives(ctx, p.DraftID)
	if err != nil {
		return err
	}
	conv, err := e.Store.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return err
	}
	name := "sua campanha"
	if data, err := e.pubs().PublishData(ctx, p.DraftID); err == nil && data.ProjectName != "" {
		name = data.ProjectName
	}
	e.audit(ctx, p.OrgID, "system", "ad_rejected", map[string]any{"publication_id": p.ID, "campaign_id": p.CampaignID, "reason": why})
	e.alertTeam(ctx, ident, fmt.Sprintf("a Meta reprovou os anúncios da campanha %s (%s): %s", p.CampaignID, name, why))
	next := msgAdRejectedRedo
	if !moved {
		next = msgAdRejectedLater
	}
	// O motivo da Meta traduzido para português simples, com o que mudar (e o texto sugerido, se o ajuste de copy estiver ligado).
	return e.send(ctx, conv, join(fmt.Sprintf(msgAdRejected, name, why), e.explainRejection(ctx, ident, p.DraftID, reasons), next))
}

// partlyRejected: parte dos anúncios reprovada; a campanha segue com os outros. Avisa uma vez.
func (e *Engine) partlyRejected(ctx context.Context, p store.Publication, n int, reasons []string) {
	conv, err := e.Store.OwnerConversation(ctx, p.OrgID)
	if err != nil {
		return
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return
	}
	why := reasonText(reasons)
	e.audit(ctx, p.OrgID, "system", "ad_partly_rejected", map[string]any{"publication_id": p.ID, "rejected": n, "reason": why})
	e.alertTeam(ctx, ident, fmt.Sprintf("a Meta reprovou %d anúncio(s) da campanha %s: %s", n, p.CampaignID, why))
	if err := e.send(ctx, conv, join(fmt.Sprintf(msgAdPartlyRejected, n, why), e.explainRejection(ctx, ident, p.DraftID, reasons))); err != nil {
		e.Log.Warn("aviso de reprovação parcial", "publication_id", p.ID, "erro", err)
	}
}
