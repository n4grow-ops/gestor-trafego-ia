package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/instagram"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Post do Instagram na etapa dos anúncios: o cliente manda o link, um job baixa a mídia (da conta
// conectada pela Graph API ou, sem isso, da página pública do post) e guarda como anúncio. Post
// público só vai para a Meta depois do *É MEU*; *SÓ REFERÊNCIA* apaga e guarda link e legenda como
// referência dos textos.

// JobInstagramFetch baixa o post do link que o cliente mandou.
const JobInstagramFetch = "instagram.fetch"

// InstagramTimeout é o prazo do job no worker: vídeo de até 300 MB (a trava do job é de 5 min).
const InstagramTimeout = 4 * time.Minute

type InstagramJob struct {
	ConversationID int64  `json:"conversation_id"`
	DraftID        int64  `json:"draft_id"`
	Link           string `json:"link"`
}

// InstagramFetcher baixa o post. *instagram.Fetcher implementa; os testes trocam.
type InstagramFetcher interface {
	Own(ctx context.Context, token, igUserID string, l instagram.Link, max int) (instagram.Post, error)
	Public(ctx context.Context, l instagram.Link, max int) (instagram.Post, error)
}

// enqueueInstagram agenda a busca de cada post do texto. ok=false: o texto não tem link de post.
func (e *Engine) enqueueInstagram(ctx context.Context, conv store.Conversation, draftID int64, text string) (bool, error) {
	if e.Instagram == nil {
		return false, nil
	}
	links := instagram.ParseLinks(text)
	for _, l := range links {
		if err := e.Store.Enqueue(ctx, store.JobSpec{Kind: JobInstagramFetch, MaxAttempts: 2,
			Payload: InstagramJob{ConversationID: conv.ID, DraftID: draftID, Link: l.URL}}); err != nil {
			return true, err
		}
	}
	return len(links) > 0, nil
}

// fetchInstagram baixa o post e guarda como anúncio. Erro devolvido = o outbox tenta de novo (banco);
// falha do Instagram vira mensagem ao cliente, sem nova tentativa.
func (e *Engine) fetchInstagram(ctx context.Context, j InstagramJob) error {
	if e.Instagram == nil || e.Storage == nil {
		return nil
	}
	link, ok := instagram.ParseLink(j.Link)
	if !ok {
		return nil
	}
	conv, ident, d, ok, err := e.instagramTarget(ctx, j)
	if err != nil || !ok {
		return err
	}
	n, err := e.Store.CountCreatives(ctx, d.ID)
	if err != nil {
		return err
	}
	if n >= maxCreatives {
		list, err := e.Store.ListCreatives(ctx, d.ID)
		if err != nil {
			return err
		}
		return e.send(ctx, conv, join(msgInstagramFull, creativesStatus(list)))
	}
	// A busca roda fora da trava da conversa: o cliente pode seguir mandando mensagens enquanto baixa.
	post, origin, err := e.downloadInstagram(ctx, ident, link, maxCreatives-n)
	if err != nil {
		e.Log.Warn("post do instagram não baixou", "conversation_id", conv.ID, "erro", err)
		e.audit(ctx, ident.OrgID, "system", "instagram_fetch_failed", map[string]any{"draft_id": d.ID, "error": err.Error()})
		return e.send(ctx, conv, msgInstagramFailed)
	}

	unlock, err := e.Store.LockConversation(ctx, conv.ID)
	if err != nil {
		return err
	}
	defer unlock()
	// A conversa pode ter seguido enquanto baixava (PRONTO, atendimento humano): aí o post é descartado.
	if conv, ident, d, ok, err = e.instagramTarget(ctx, j); err != nil || !ok {
		return err
	}
	if n, err = e.Store.CountCreatives(ctx, d.ID); err != nil {
		return err
	}
	pending := origin == store.OriginInstagramPublic
	var warnings, rejected []string
	saved, failed := 0, false
	for i, it := range post.Items {
		if n+saved >= maxCreatives {
			break
		}
		mark := e.keepCreative(ctx, ident, conv.ID, d, creativeFile{data: it.Data, mime: it.Mime, kind: it.Kind,
			name: fmt.Sprintf("ig%s%d", safeName(link.Shortcode), i+1), origin: origin, igMediaID: post.MediaID,
			rightsPending: pending, sourceURL: firstText(post.Permalink, link.URL), caption: post.Caption})
		switch {
		case strings.HasPrefix(mark, creativeWarned):
			warnings = append(warnings, strings.TrimSuffix(strings.TrimPrefix(mark, creativeWarned), "]"))
			saved++
		case strings.HasPrefix(mark, creativeSaved):
			saved++
		case strings.HasPrefix(mark, creativeRejected):
			rejected = append(rejected, strings.TrimSuffix(strings.TrimPrefix(mark, creativeRejected), "]"))
		default:
			failed = true
		}
	}
	e.audit(ctx, ident.OrgID, member(ident), "instagram_post_saved", map[string]any{"draft_id": d.ID, "origin": origin,
		"saved": saved, "total": post.Total, "ig_media_id": post.MediaID})
	list, err := e.Store.ListCreatives(ctx, d.ID)
	if err != nil {
		return err
	}
	var parts []string
	for _, r := range rejected {
		parts = append(parts, fmt.Sprintf(msgCreativeRejected, r))
	}
	if failed {
		parts = append(parts, msgCreativeFailed)
	}
	if saved == 0 {
		return e.send(ctx, conv, join(append(parts, creativesStatus(list))...))
	}
	ack := msgInstagramOwnSaved
	if pending {
		ack = msgInstagramPublicSaved + " " + msgInstagramRightsQuestion
	}
	for _, w := range warnings {
		ack += "\n⚠️ " + upperFirst(w) + "."
	}
	parts = append([]string{ack}, parts...)
	if post.Total > saved && len(rejected) == 0 && !failed {
		parts = append(parts, fmt.Sprintf(msgInstagramPartial, post.Total, saved, maxCreatives))
	}
	if !pending {
		parts = append(parts, creativesStatus(list))
	}
	return e.send(ctx, conv, join(parts...))
}

// instagramTarget confere se a conversa ainda espera os anúncios do mesmo rascunho.
func (e *Engine) instagramTarget(ctx context.Context, j InstagramJob) (store.Conversation, store.Identity, store.ApprovedDraft, bool, error) {
	conv, err := e.Store.GetConversation(ctx, j.ConversationID)
	if err != nil {
		return conv, store.Identity{}, store.ApprovedDraft{}, false, err
	}
	if conv.State != store.StateAwaitingCreatives {
		return conv, store.Identity{}, store.ApprovedDraft{}, false, nil
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil || ident.Status == "blocked" {
		return conv, ident, store.ApprovedDraft{}, false, err
	}
	d, err := e.Store.LatestApprovedDraft(ctx, conv.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && d.ID != j.DraftID) {
		return conv, ident, d, false, nil
	}
	return conv, ident, d, err == nil, err
}

// downloadInstagram tenta a conta conectada (Graph API) e, se o post não é dela ou falta permissão,
// a página pública.
func (e *Engine) downloadInstagram(ctx context.Context, ident store.Identity, link instagram.Link, max int) (instagram.Post, string, error) {
	if token, igUser, ok := e.instagramAccount(ctx, ident.OrgID); ok {
		post, err := e.Instagram.Own(ctx, token, igUser, link, max)
		switch {
		case err == nil:
			return post, store.OriginInstagramOwn, nil
		case errors.Is(err, instagram.ErrPermission):
			if e.igPermWarned.CompareAndSwap(false, true) {
				e.Log.Warn("sem permissão para ler os posts do Instagram do cliente; usando a página pública. "+
					"Inclua instagram_basic e pages_read_engagement na configuração do Facebook Login for Business",
					"org_id", ident.OrgID, "erro", err)
			}
		case errors.Is(err, instagram.ErrNotOwn):
			// post de outra conta: segue para a página pública
		default:
			e.Log.Warn("post da conta do cliente não baixou; tentando a página pública", "org_id", ident.OrgID, "erro", err)
		}
	}
	post, err := e.Instagram.Public(ctx, link, max)
	return post, store.OriginInstagramPublic, err
}

// instagramAccount devolve o token do cliente e o Instagram vinculado à Página da conexão.
func (e *Engine) instagramAccount(ctx context.Context, orgID int64) (string, string, bool) {
	if e.MetaTokenKey == nil {
		return "", "", false
	}
	conn, ok, err := e.Store.ActiveMeta(ctx, orgID)
	if err != nil || !ok || conn.InstagramID == "" {
		return "", "", false
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, orgID)
	if err != nil {
		return "", "", false
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		e.Log.Error("token da meta não abre", "org_id", orgID, "erro", err)
		return "", "", false
	}
	return string(token), conn.InstagramID, true
}

// instagramGaveUp: o job esgotou as tentativas (banco fora); o cliente não fica esperando.
func (e *Engine) instagramGaveUp(ctx context.Context, j InstagramJob) error {
	conv, err := e.Store.GetConversation(ctx, j.ConversationID)
	if err != nil {
		return err
	}
	if conv.State != store.StateAwaitingCreatives {
		return nil
	}
	return e.Store.Enqueue(ctx, sendJob(conv.ID, conv.ChatID, msgInstagramFailed))
}

// ---------- É MEU / SÓ REFERÊNCIA ----------

type rightsAnswer int

const (
	rightsNone rightsAnswer = iota
	rightsMine
	rightsReference
)

var (
	rightsMineWords = map[string]bool{"e meu": true, "e minha": true, "eh meu": true, "e nosso": true, "e nossa": true,
		"meu": true, "sim e meu": true, "e meu sim": true, "sim e nosso": true, "e meu mesmo": true,
		"tenho autorizacao": true, "eu tenho autorizacao": true, "tenho permissao": true, "pode usar": true}
	rightsRefWords = map[string]bool{"so referencia": true, "referencia": true, "so como referencia": true,
		"apenas referencia": true, "so pra referencia": true, "so para referencia": true, "so inspiracao": true,
		"so como inspiracao": true, "nao e meu": true, "nao e meu so referencia": true}
)

// parseRights reconhece a resposta curta à pergunta do direito de uso.
func parseRights(s string) rightsAnswer {
	s = strings.ToLower(strings.ReplaceAll(s, "*", ""))
	s = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return ' '
		}
		return r
	}, s)
	s = normalizeName(s)
	switch {
	case rightsMineWords[s]:
		return rightsMine
	case rightsRefWords[s]:
		return rightsReference
	}
	return rightsNone
}

func hasPendingRights(list []store.Creative) bool {
	for _, c := range list {
		if c.RightsPending {
			return true
		}
	}
	return false
}

// applyRights executa o É MEU ou o SÓ REFERÊNCIA nos posts públicos pendentes.
func (e *Engine) applyRights(ctx context.Context, orgID, draftID int64, a rightsAnswer) (string, error) {
	if a == rightsMine {
		n, err := e.Store.ConfirmCreativeRights(ctx, draftID)
		if err != nil {
			return "", err
		}
		e.audit(ctx, orgID, "system", "creative_rights_confirmed", map[string]any{"draft_id": draftID, "count": n})
		return msgInstagramRightsMine, nil
	}
	gone, err := e.Store.DeletePendingCreatives(ctx, draftID)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, c := range gone {
		if err := e.Storage.Delete(ctx, CreativesBucket, c.Path); err != nil {
			e.Log.Error("arquivo do anúncio não apagado no storage", "path", c.Path, "erro", err)
		}
		key := c.SourceURL + "\x00" + c.SourceCaption
		if seen[key] {
			continue
		}
		seen[key] = true
		ref := "Post de referência do Instagram: " + c.SourceURL
		if strings.TrimSpace(c.SourceCaption) != "" {
			ref += "\nLegenda do post: " + c.SourceCaption
		}
		if err := e.pubs().AppendClientCopy(ctx, draftID, ref); err != nil {
			return "", err
		}
	}
	e.audit(ctx, orgID, "system", "creative_reference_only", map[string]any{"draft_id": draftID, "count": len(gone)})
	return msgInstagramRightsRef, nil
}

func firstText(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
