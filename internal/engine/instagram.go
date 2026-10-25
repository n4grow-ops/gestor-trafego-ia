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

const (
	// InstagramBudget é o prazo total dos downloads de um job (API, página e arquivos). Esgotou, para e
	// fica com o que já baixou.
	InstagramBudget = 3 * time.Minute
	// igUploadTimeout e igWriteTimeout: subir um item no Storage e gravar/travar/avisar, com contexto
	// próprio (o do job pode ter acabado).
	igUploadTimeout = time.Minute
	igWriteTimeout  = 30 * time.Second
	// InstagramTimeout é o prazo do job no worker: orçamento + gravação do último item + aviso, com folga.
	// A trava do job no store (ClaimJob) é maior.
	InstagramTimeout = 5 * time.Minute
)

type InstagramJob struct {
	ConversationID int64  `json:"conversation_id"`
	DraftID        int64  `json:"draft_id"`
	Link           string `json:"link"`
	// FacebookPostID: post da Página escolhido na lista dos recentes (Link é o permalink). Vazio = Instagram.
	FacebookPostID string `json:"facebook_post_id,omitempty"`
}

// InstagramFetcher baixa o post e entrega os itens um a um ao sink. *instagram.Fetcher implementa; os
// testes trocam.
type InstagramFetcher interface {
	Own(ctx context.Context, token, igUserID string, l instagram.Link, max int, sink instagram.Sink) (instagram.Post, error)
	Public(ctx context.Context, l instagram.Link, max int, sink instagram.Sink) (instagram.Post, error)
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

// fetchInstagram baixa o post e guarda como anúncio, um item por vez. Erro devolvido = o outbox tenta de
// novo (banco); falha do Instagram vira mensagem ao cliente, sem nova tentativa. Os downloads têm um prazo
// total (InstagramBudget); gravações, trava e aviso usam contexto próprio e curto, para o prazo do job
// não descartar o que já baixou.
func (e *Engine) fetchInstagram(ctx context.Context, j InstagramJob) error {
	if e.Instagram == nil || e.Storage == nil {
		return nil
	}
	link, ok := instagram.ParseLink(j.Link)
	if j.FacebookPostID != "" {
		link, ok = instagram.Link{URL: j.Link, Shortcode: "fb" + j.FacebookPostID[strings.LastIndex(j.FacebookPostID, "_")+1:]}, true
	}
	if !ok {
		return nil
	}
	conv, ident, d, target, err := e.instagramTarget(ctx, j)
	switch {
	case err != nil:
		return err
	case target == targetClosed:
		return e.notifyInstagram(ctx, conv, msgInstagramLate)
	case target != targetOpen:
		return nil
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
	f := &igFetch{e: e, ctx: ctx, job: j, link: link}
	budget, cancel := context.WithTimeout(ctx, e.instagramBudget())
	defer cancel()
	// A busca roda fora da trava da conversa: o cliente pode seguir mandando mensagens enquanto baixa.
	var post instagram.Post
	var origin string
	if j.FacebookPostID != "" {
		post, origin, err = e.downloadFacebook(budget, ident, j.FacebookPostID, maxCreatives-n, f)
	} else {
		post, origin, err = e.downloadInstagram(budget, ident, link, maxCreatives-n, f)
	}
	if f.fatal != nil {
		return f.fatal // banco fora: o outbox tenta de novo
	}

	wctx, wcancel := writeCtx(ctx, igWriteTimeout)
	defer wcancel()
	if err != nil && f.saved == 0 {
		e.Log.Warn("post do instagram não baixou", "conversation_id", conv.ID, "erro", err)
		e.audit(wctx, ident.OrgID, "system", "instagram_fetch_failed", map[string]any{"draft_id": d.ID, "error": err.Error()})
		return e.notifyInstagram(wctx, conv, msgInstagramFailed)
	}
	switch {
	case f.closed:
		// a etapa fechou enquanto baixava: o post não entra na campanha e o cliente fica sabendo
		return e.notifyInstagram(wctx, conv, msgInstagramLate)
	case f.gone:
		return nil // atendimento humano ou conversa sem o rascunho: o post é descartado em silêncio
	}
	e.audit(wctx, ident.OrgID, member(ident), "instagram_post_saved", map[string]any{"draft_id": d.ID, "origin": origin,
		"saved": f.saved, "total": post.Total, "ig_media_id": post.MediaID})
	list, err := e.Store.ListCreatives(wctx, d.ID)
	if err != nil {
		e.Log.Error("post do instagram guardado, mas a lista dos anúncios não leu", "conversation_id", conv.ID, "erro", err)
		return nil // já guardou: refazer baixaria tudo de novo
	}
	var parts []string
	for _, r := range f.rejected {
		parts = append(parts, fmt.Sprintf(msgCreativeRejected, r))
	}
	if f.failed {
		parts = append(parts, msgCreativeFailed)
	}
	if f.saved == 0 {
		return e.notifyInstagram(wctx, conv, join(append(parts, creativesStatus(list))...))
	}
	pending := origin == store.OriginInstagramPublic
	ack := msgInstagramOwnSaved
	if pending {
		ack = msgInstagramPublicSaved + " " + msgInstagramRightsQuestion
	}
	for _, w := range f.warnings {
		ack += "\n⚠️ " + upperFirst(w) + "."
	}
	parts = append([]string{ack}, parts...)
	if post.Total > f.saved && len(f.rejected) == 0 && !f.failed {
		parts = append(parts, fmt.Sprintf(msgInstagramPartial, post.Total, f.saved, maxCreatives))
	}
	if !pending {
		parts = append(parts, creativesStatus(list))
	}
	return e.notifyInstagram(wctx, conv, join(parts...))
}

// notifyInstagram avisa o cliente. Falha no envio não faz o job baixar tudo de novo: fica no log.
func (e *Engine) notifyInstagram(ctx context.Context, conv store.Conversation, text string) error {
	if err := e.send(ctx, conv, text); err != nil {
		e.Log.Error("aviso do post do instagram não saiu", "conversation_id", conv.ID, "erro", err)
	}
	return nil
}

// writeCtx: contexto próprio e curto para gravar e avisar, mesmo com o do job vencido ou cancelado.
func writeCtx(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

func (e *Engine) instagramBudget() time.Duration {
	if e.InstagramBudget > 0 {
		return e.InstagramBudget
	}
	return InstagramBudget
}

// igFetch guarda os itens do post conforme chegam e anota o resultado.
type igFetch struct {
	e    *Engine
	ctx  context.Context // do job: só a origem dos contextos próprios de gravação
	job  InstagramJob
	link instagram.Link

	saved, skipped     int // skipped: itens que uma tentativa anterior já guardou
	warnings, rejected []string
	failed             bool
	closed             bool  // a etapa dos anúncios deste rascunho fechou no meio
	gone               bool  // atendimento humano, cliente bloqueado ou rascunho sumiu
	fatal              error // banco fora
}

// sink é o destino dos itens de uma origem (conta conectada ou página pública).
func (f *igFetch) sink(origin string) instagram.Sink {
	return instagram.Sink{
		Skip: f.already,
		Save: func(p instagram.Post, pos int, it instagram.Item) error { return f.save(origin, p, pos, it) },
	}
}

// already: o item já foi guardado neste rascunho (nova tentativa do job). Na dúvida (banco), baixa: o save
// confere de novo dentro da trava.
func (f *igFetch) already(p instagram.Post, pos int) bool {
	ctx, cancel := writeCtx(f.ctx, igWriteTimeout)
	defer cancel()
	ok, err := f.e.Store.HasInstagramCreative(ctx, f.job.DraftID, f.sourceURL(p), p.MediaID, pos)
	if ok {
		f.skipped++
	}
	return err == nil && ok
}

func (f *igFetch) sourceURL(p instagram.Post) string { return firstText(p.Permalink, f.link.URL) }

// stopped: a busca não deve tentar outra origem (já guardou algo, a conversa seguiu ou o banco caiu).
func (f *igFetch) stopped() bool {
	return f.saved > 0 || f.skipped > 0 || f.closed || f.gone || f.fatal != nil
}

// target anota a conversa que não espera mais o post. stop=true: a busca para.
func (f *igFetch) target(t igTarget, err error) (stop bool, _ error) {
	switch {
	case err != nil:
		f.fatal = err
		return true, err
	case t == targetClosed:
		f.closed = true
		return true, instagram.ErrStop
	case t != targetOpen:
		f.gone = true
		return true, instagram.ErrStop
	}
	return false, nil
}

// save guarda um item: trava a conversa só durante a gravação e confere de novo se ela ainda espera os
// anúncios do mesmo rascunho.
func (f *igFetch) save(origin string, p instagram.Post, pos int, it instagram.Item) error {
	e := f.e
	ctx, cancel := writeCtx(f.ctx, igUploadTimeout+igWriteTimeout)
	defer cancel()
	conv, _, _, t, err := e.instagramTarget(ctx, f.job)
	if stop, err := f.target(t, err); stop {
		return err
	}
	unlock, err := e.Store.LockConversation(ctx, conv.ID)
	if err != nil {
		f.fatal = err
		return err
	}
	defer unlock()
	conv, ident, d, t, err := e.instagramTarget(ctx, f.job)
	if stop, err := f.target(t, err); stop {
		return err
	}
	n, err := e.Store.CountCreatives(ctx, d.ID)
	if err != nil {
		f.fatal = err
		return err
	}
	if n >= maxCreatives {
		return instagram.ErrStop
	}
	if dup, err := e.Store.HasInstagramCreative(ctx, d.ID, f.sourceURL(p), p.MediaID, pos); err != nil {
		f.fatal = err
		return err
	} else if dup {
		f.skipped++
		return nil
	}
	igMediaID, fbPostID := p.MediaID, ""
	if origin == store.OriginFacebookOwn {
		igMediaID, fbPostID = "", p.MediaID
	}
	mark := e.keepCreative(ctx, ident, conv.ID, d, creativeFile{data: it.Data, mime: it.Mime, kind: it.Kind,
		name: fmt.Sprintf("ig%s%d", safeName(f.link.Shortcode), pos+1), origin: origin, igMediaID: igMediaID, fbPostID: fbPostID,
		rightsPending: origin == store.OriginInstagramPublic, sourceURL: f.sourceURL(p), caption: p.Caption, position: &pos})
	switch {
	case strings.HasPrefix(mark, creativeWarned):
		f.warnings = append(f.warnings, strings.TrimSuffix(strings.TrimPrefix(mark, creativeWarned), "]"))
		f.saved++
	case strings.HasPrefix(mark, creativeSaved):
		f.saved++
	case strings.HasPrefix(mark, creativeRejected):
		f.rejected = append(f.rejected, strings.TrimSuffix(strings.TrimPrefix(mark, creativeRejected), "]"))
	default:
		f.failed = true
	}
	return nil
}

// igTarget diz se a conversa ainda espera o post.
type igTarget int

const (
	targetOpen   igTarget = iota // espera os anúncios do mesmo rascunho
	targetClosed                 // a etapa fechou (ou outro rascunho foi aprovado): o cliente é avisado
	targetGone                   // atendimento humano, cliente bloqueado ou sem rascunho: sem aviso
)

// instagramTarget confere se a conversa ainda espera os anúncios do mesmo rascunho.
func (e *Engine) instagramTarget(ctx context.Context, j InstagramJob) (store.Conversation, store.Identity, store.ApprovedDraft, igTarget, error) {
	conv, err := e.Store.GetConversation(ctx, j.ConversationID)
	if err != nil {
		return conv, store.Identity{}, store.ApprovedDraft{}, targetGone, err
	}
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return conv, ident, store.ApprovedDraft{}, targetGone, err
	}
	if ident.Status == "blocked" || conv.State == store.StateNeedsHuman {
		return conv, ident, store.ApprovedDraft{}, targetGone, nil
	}
	if conv.State != store.StateAwaitingCreatives {
		return conv, ident, store.ApprovedDraft{}, targetClosed, nil
	}
	d, err := e.Store.LatestApprovedDraft(ctx, conv.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return conv, ident, d, targetGone, nil
	case err != nil:
		return conv, ident, d, targetGone, err
	case d.ID != j.DraftID:
		return conv, ident, d, targetClosed, nil
	}
	return conv, ident, d, targetOpen, nil
}

// downloadInstagram tenta a conta conectada (Graph API) e, se o post não é dela ou falta permissão,
// a página pública. Prazo esgotado, arquivo grande demais ou item já guardado não tentam a outra origem.
func (e *Engine) downloadInstagram(ctx context.Context, ident store.Identity, link instagram.Link, max int, f *igFetch) (instagram.Post, string, error) {
	if token, igUser, ok := e.instagramAccount(ctx, ident.OrgID); ok {
		post, err := e.Instagram.Own(ctx, token, igUser, link, max, f.sink(store.OriginInstagramOwn))
		switch {
		case err == nil || f.stopped() || ctx.Err() != nil:
			return post, store.OriginInstagramOwn, err
		case errors.Is(err, instagram.ErrTooLarge):
			// o arquivo é o mesmo na página pública: baixar de novo só gastaria o prazo
			return post, store.OriginInstagramOwn, err
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
	post, err := e.Instagram.Public(ctx, link, max, f.sink(store.OriginInstagramPublic))
	return post, store.OriginInstagramPublic, err
}

// instagramAccount devolve o token do cliente e o Instagram vinculado à Página da conexão.
func (e *Engine) instagramAccount(ctx context.Context, orgID int64) (string, string, bool) {
	token, conn, ok := e.metaAccount(ctx, orgID)
	if !ok || conn.InstagramID == "" {
		return "", "", false
	}
	return token, conn.InstagramID, true
}

// metaAccount devolve o token aberto do cliente e a conexão Meta ativa.
func (e *Engine) metaAccount(ctx context.Context, orgID int64) (string, store.MetaConnection, bool) {
	if e.MetaTokenKey == nil {
		return "", store.MetaConnection{}, false
	}
	conn, ok, err := e.Store.ActiveMeta(ctx, orgID)
	if err != nil || !ok {
		return "", store.MetaConnection{}, false
	}
	_, sealed, err := e.pubs().MetaTokenSealed(ctx, orgID)
	if err != nil {
		return "", store.MetaConnection{}, false
	}
	token, err := e.MetaTokenKey.Open(sealed)
	if err != nil {
		e.Log.Error("token da meta não abre", "org_id", orgID, "erro", err)
		return "", store.MetaConnection{}, false
	}
	return string(token), conn, true
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
		"sim e meu": true, "e meu sim": true, "sim e nosso": true, "e meu mesmo": true,
		"tenho autorizacao": true, "eu tenho autorizacao": true, "tenho permissao": true}
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
