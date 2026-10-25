package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/instagram"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// Post que o cliente já tem vira anúncio sem mandar o link: na etapa dos anúncios ele pede ("quero usar
// um post que já tenho", POSTS), a Mia lista os mais recentes do Instagram e da Página conectados e ele
// responde o número. Cada escolha vira a mesma busca do link (job instagram.fetch); o anúncio sai com o
// post como está.

const (
	// postsShown: quantos posts recentes a lista mostra (das duas contas juntas).
	postsShown = 6
	// postPickWindow: depois da lista, o número sozinho ("2", "o 1 e o 3") vale como escolha por este tempo.
	postPickWindow = 2 * time.Hour
	postsTimeout   = 20 * time.Second
)

// PostSource lista os posts recentes e baixa o post da Página. Opcional ao InstagramFetcher;
// *instagram.Fetcher implementa.
type PostSource interface {
	Recent(ctx context.Context, token, igUserID, pageID string, n int) ([]instagram.Choice, error)
	Facebook(ctx context.Context, token, pageID, postID string, max int, sink instagram.Sink) (instagram.Post, error)
}

func (e *Engine) postSource() (PostSource, bool) {
	if e.Instagram == nil {
		return nil, false
	}
	ps, ok := e.Instagram.(PostSource)
	return ps, ok
}

var (
	postWords = map[string]bool{"post": true, "posts": true, "postagem": true, "postagens": true, "publicacao": true,
		"publicacoes": true, "reels": true, "reel": true}
	// postAsks: junto com "post", o pedido de usar o que já está publicado.
	postAsks = []string{"ja tenho", "que tenho", "ja fiz", "ja postei", "ja publiquei", "existente", "ja existe", "ja feito",
		"do meu insta", "do insta", "do instagram", "no instagram", "no insta", "da pagina", "na pagina", "do face", "no face",
		"impulsionar", "impulsiona", "turbinar", "turbina", "usar um", "usar uma", "usar o", "usar a", "usar meu", "usar minha",
		"mostra", "lista", "meus", "minhas", "ultimos", "ultimas", "recentes", "antigo", "antiga"}
	// postPickFiller: palavras que podem vir junto do número na escolha ("usa o post 2", "quero o 1 e o 3").
	postPickFiller = map[string]bool{"o": true, "a": true, "os": true, "as": true, "e": true, "numero": true, "n": true,
		"no": true, "usa": true, "usar": true, "use": true, "quero": true, "pode": true, "ser": true, "esse": true,
		"este": true, "esses": true, "estes": true, "pfv": true, "por": true, "favor": true, "ok": true, "sim": true,
		"vai": true, "com": true, "do": true, "da": true, "de": true, "opcao": true, "escolho": true, "fico": true,
		"pega": true, "pegar": true, "coloca": true, "so": true, "mais": true, "tambem": true, "nr": true, "numeros": true}
)

// postWordsOf normaliza o texto em palavras (minúsculas, sem acento nem pontuação).
func postWordsOf(s string) []string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Fields(normalizeName(s))
}

// wantsPosts: o cliente pediu pra usar um post que já publicou.
func wantsPosts(s string) bool {
	words := postWordsOf(s)
	if len(words) == 0 || len(words) > 25 {
		return false
	}
	text := " " + strings.Join(words, " ") + " "
	switch text {
	case " posts ", " post ", " meus posts ", " ver posts ", " ver meus posts ", " lista de posts ", " minhas publicacoes ":
		return true
	}
	if !slices.ContainsFunc(words, func(w string) bool { return postWords[w] }) {
		return false
	}
	return slices.ContainsFunc(postAsks, func(a string) bool { return strings.Contains(text, " "+a+" ") })
}

// parsePostPick lê a escolha ("POST 2", "o 1 e o 3", "2", "o primeiro"). named: veio com a palavra post.
// Qualquer outra palavra no meio = não é escolha (nil).
func parsePostPick(s string) (picks []int, named bool) {
	words := postWordsOf(s)
	if len(words) == 0 || len(words) > 12 {
		return nil, false
	}
	for _, w := range words {
		if n, err := strconv.Atoi(w); err == nil {
			if n >= 1 && n <= 99 && !slices.Contains(picks, n) {
				picks = append(picks, n)
			}
			continue
		}
		if postWords[w] {
			named = true
			continue
		}
		if postPickFiller[w] {
			continue
		}
		pos := 0
		for _, o := range ordinals {
			if o.pos > 0 && strings.HasPrefix(w, nameAccents.Replace(o.prefix)) {
				pos = o.pos
				break
			}
		}
		if pos == 0 {
			return nil, false
		}
		if !slices.Contains(picks, pos) {
			picks = append(picks, pos)
		}
	}
	if len(picks) == 0 {
		return nil, false
	}
	return picks, named
}

// offerPosts lista os posts recentes e guarda a lista no rascunho.
func (e *Engine) offerPosts(ctx context.Context, conv store.Conversation, d store.ApprovedDraft) (string, error) {
	ident, err := e.Store.GetIdentity(ctx, conv.IdentityID)
	if err != nil {
		return "", err
	}
	orgID := ident.OrgID
	ps, ok := e.postSource()
	if !ok {
		return msgPostsFailed, nil
	}
	token, conn, ok := e.metaAccount(ctx, orgID)
	if !ok || (conn.InstagramID == "" && conn.PageID == "") {
		return msgPostsNoAccount, nil
	}
	lctx, cancel := context.WithTimeout(ctx, postsTimeout)
	defer cancel()
	list, err := ps.Recent(lctx, token, conn.InstagramID, conn.PageID, postsShown)
	if err != nil {
		e.Log.Warn("posts recentes não vieram", "org_id", orgID, "erro", err)
		e.audit(ctx, orgID, "system", "posts_list_failed", map[string]any{"draft_id": d.ID, "error": err.Error()})
		return msgPostsFailed, nil
	}
	if len(list) == 0 {
		return msgPostsNone, nil
	}
	offer := store.PostOffer{At: e.now()}
	lines := []string{msgPostsLead}
	for i, c := range list {
		offer.Items = append(offer.Items, store.PostChoice{Source: c.Source, ID: c.ID, Permalink: c.Permalink})
		lines = append(lines, postLine(i+1, c))
	}
	if err := e.Store.SetPostOffer(ctx, d.ID, offer); err != nil {
		return "", err
	}
	e.audit(ctx, orgID, "system", "posts_offered", map[string]any{"draft_id": d.ID, "count": len(list)})
	return strings.Join(lines, "\n\n") + "\n\n" + msgPostsPick, nil
}

// postLine é um post na lista: número, rede, data, tipo, começo da legenda e o link.
func postLine(n int, c instagram.Choice) string {
	where := "📸 Instagram"
	if c.Source == instagram.SourceFacebook {
		where = "📘 Facebook"
	}
	head := fmt.Sprintf("*%d.* %s", n, where)
	if !c.Created.IsZero() {
		head += " · " + c.Created.In(brt).Format("02/01")
	}
	switch c.Kind {
	case "video":
		head += " · vídeo"
	case "album":
		head += " · carrossel"
	default:
		head += " · foto"
	}
	if cap := shortCaption(c.Caption, 70); cap != "" {
		head += "\n“" + cap + "”"
	}
	return head + "\n" + c.Permalink
}

func shortCaption(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}

// pickPosts agenda a busca dos posts escolhidos. ok=false: não havia lista (a mensagem segue o fluxo).
func (e *Engine) pickPosts(ctx context.Context, conv store.Conversation, d store.ApprovedDraft, picks []int, named bool) (string, bool, error) {
	offer, err := e.Store.PostOffer(ctx, d.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", false, err
	}
	if len(offer.Items) == 0 || (!named && e.now().Sub(offer.At) > postPickWindow) {
		return "", false, nil
	}
	var chosen []string
	for _, n := range picks {
		if n < 1 || n > len(offer.Items) {
			return fmt.Sprintf(msgPostsPickInvalid, n, len(offer.Items)), true, nil
		}
	}
	for _, n := range picks {
		c := offer.Items[n-1]
		job := InstagramJob{ConversationID: conv.ID, DraftID: d.ID, Link: c.Permalink}
		if c.Source == instagram.SourceFacebook {
			job.FacebookPostID = c.ID
		}
		if err := e.Store.Enqueue(ctx, store.JobSpec{Kind: JobInstagramFetch, MaxAttempts: 2, Payload: job}); err != nil {
			return "", true, err
		}
		chosen = append(chosen, strconv.Itoa(n))
	}
	if len(chosen) == 1 {
		return fmt.Sprintf(msgPostsFetchingOne, chosen[0]), true, nil
	}
	return fmt.Sprintf(msgPostsFetchingMany, strings.Join(chosen[:len(chosen)-1], ", ")+" e "+chosen[len(chosen)-1]), true, nil
}

// downloadFacebook baixa o post da Página conectada.
func (e *Engine) downloadFacebook(ctx context.Context, ident store.Identity, postID string, max int, f *igFetch) (instagram.Post, string, error) {
	ps, ok := e.postSource()
	if !ok {
		return instagram.Post{}, store.OriginFacebookOwn, instagram.ErrNoGraph
	}
	token, conn, ok := e.metaAccount(ctx, ident.OrgID)
	if !ok || conn.PageID == "" {
		return instagram.Post{}, store.OriginFacebookOwn, instagram.ErrUnavailable
	}
	post, err := ps.Facebook(ctx, token, conn.PageID, postID, max, f.sink(store.OriginFacebookOwn))
	return post, store.OriginFacebookOwn, err
}
