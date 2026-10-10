package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/instagram"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/media/mediatest"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/meta"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
)

// fakeIG faz o papel do instagram.Fetcher: posts por shortcode na conta do cliente e na página pública.
// Entrega os itens um a um ao Sink, como o Fetcher de verdade.
type fakeIG struct {
	mu        sync.Mutex
	own       map[string]instagram.Post
	ownErr    error
	pub       map[string]instagram.Post
	pubErr    error
	ownCalls  int
	pubCalls  int
	tokens    []string
	deadlines []time.Duration // prazo que sobrava no contexto de cada busca
	before    func()          // antes de entregar o primeiro item (ex.: fechar a etapa)
	afterSave func(pos int)   // depois de cada item guardado
}

func (f *fakeIG) Own(ctx context.Context, token, igUserID string, l instagram.Link, max int, sink instagram.Sink) (instagram.Post, error) {
	f.mu.Lock()
	f.ownCalls++
	f.tokens = append(f.tokens, token+"|"+igUserID)
	f.noteDeadline(ctx)
	err := f.ownErr
	p, ok := f.own[l.Shortcode]
	f.mu.Unlock()
	if err != nil {
		return instagram.Post{}, err
	}
	if !ok {
		return instagram.Post{}, instagram.ErrNotOwn
	}
	return f.deliver(ctx, p, max, sink)
}

func (f *fakeIG) Public(ctx context.Context, l instagram.Link, max int, sink instagram.Sink) (instagram.Post, error) {
	f.mu.Lock()
	f.pubCalls++
	f.noteDeadline(ctx)
	err := f.pubErr
	p, ok := f.pub[l.Shortcode]
	f.mu.Unlock()
	if err != nil {
		return instagram.Post{}, err
	}
	if !ok {
		return instagram.Post{}, instagram.ErrUnavailable
	}
	return f.deliver(ctx, p, max, sink)
}

func (f *fakeIG) noteDeadline(ctx context.Context) {
	if d, ok := ctx.Deadline(); ok {
		f.deadlines = append(f.deadlines, time.Until(d))
	}
}

func (f *fakeIG) deliver(ctx context.Context, p instagram.Post, max int, sink instagram.Sink) (instagram.Post, error) {
	items := p.Items
	out := p
	out.Items, out.Total = nil, len(items)
	if f.before != nil {
		f.before()
	}
	for pos, it := range items {
		if max > 0 && out.Got >= max {
			break
		}
		if sink.Skip != nil && sink.Skip(out, pos) {
			out.Skipped++
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if sink.Save == nil {
			out.Items = append(out.Items, it)
			out.Got++
			continue
		}
		if err := sink.Save(out, pos, it); errors.Is(err, instagram.ErrStop) {
			return out, nil
		} else if err != nil {
			return out, err
		}
		out.Got++
		if f.afterSave != nil {
			f.afterSave(pos)
		}
	}
	if out.Got == 0 && out.Skipped == 0 && ctx.Err() != nil {
		return instagram.Post{}, fmt.Errorf("%w: %v", instagram.ErrUnavailable, ctx.Err())
	}
	return out, nil
}

var (
	igSquare   = instagram.Item{Kind: "image", Mime: "image/jpeg", Data: mediatest.JPEG(1080, 1080)}
	igVertical = instagram.Item{Kind: "video", Mime: "video/mp4", Data: mediatest.MP4(12, 1080, 1920, false)}
	igTiny     = instagram.Item{Kind: "image", Mime: "image/png", Data: mediatest.PNG(300, 300)}
)

func newIGHarness(t *testing.T) (*harness, *fakePubs, *fakePublisher, *fakeIG, *fakeStorage) {
	h, pubs, pub := newPublishHarness(t, "owner")
	fs := h.eng.Storage.(*fakeStorage)
	ig := &fakeIG{own: map[string]instagram.Post{}, pub: map[string]instagram.Post{}}
	h.eng.Instagram = ig
	return h, pubs, pub, ig, fs
}

// connectMetaIG conecta a conta Meta com o Instagram IG1 vinculado à Página.
func (h *harness) connectMetaIG() {
	h.t.Helper()
	h.connectMeta()
	_, err := h.pool.Exec(ctx, `UPDATE meta_connections SET instagram_id = 'IG1', instagram_username = 'pizzariaboa'`)
	h.must(err)
}

func (h *harness) creativeRows() []store.Creative {
	h.t.Helper()
	d, err := h.st.LatestApprovedDraft(ctx, h.conv().ID)
	h.must(err)
	list, err := h.st.ListCreatives(ctx, d.ID)
	h.must(err)
	return list
}

func TestInstagramOwnPostBecomesCreatives(t *testing.T) {
	h, _, _, ig, fs := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Permalink: "https://www.instagram.com/p/MEU1/", Caption: "Promo",
		Items: []instagram.Item{igSquare, igVertical}}
	h.say("SIM " + h.reachApproval())
	h.say("usa esse meu post https://www.instagram.com/p/MEU1/?igsh=abc")
	if msg := h.lastClient(); msg != "Deixa comigo, vou buscar esse post 🔎" {
		t.Fatalf("resposta imediata = %q", msg)
	}
	if h.pendingJobs(engine.JobInstagramFetch) != 1 || h.creatives() != 0 {
		t.Fatalf("job = %d criativos = %d", h.pendingJobs(engine.JobInstagramFetch), h.creatives())
	}
	h.runKind(engine.JobInstagramFetch)
	list := h.creativeRows()
	if len(list) != 2 || fs.count() != 2 {
		t.Fatalf("criativos = %d arquivos = %d", len(list), fs.count())
	}
	for _, c := range list {
		if c.Origin != store.OriginInstagramOwn || c.IGMediaID != "1789" || c.RightsPending || c.SourceURL != "https://www.instagram.com/p/MEU1/" {
			t.Fatalf("criativo = %+v", c)
		}
	}
	if list[1].Kind != "video" || list[1].Placement != "stories" {
		t.Fatalf("vídeo = %+v", list[1])
	}
	msg := h.lastClient()
	if !strings.Contains(msg, "Peguei seu post ✅") || !strings.Contains(msg, "2 de 5") || !strings.Contains(msg, "Stories e Reels: ✅") {
		t.Fatalf("resposta do job = %q", msg)
	}
	if ig.pubCalls != 0 || len(ig.tokens) != 1 || ig.tokens[0] != "token-do-cliente|IG1" {
		t.Fatalf("público = %d tokens = %v", ig.pubCalls, ig.tokens)
	}
	if h.conv().State != store.StateAwaitingCreatives {
		t.Fatalf("estado = %s", h.conv().State)
	}
}

func TestInstagramPublicPostAsksRightsThenMine(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	ig.pub["OUTRO1"] = instagram.Post{Permalink: "https://www.instagram.com/p/OUTRO1/", Caption: "Legenda boa", Items: []instagram.Item{igSquare}}
	h.say("SIM " + h.reachApproval())
	h.say("https://instagram.com/p/OUTRO1")
	h.runKind(engine.JobInstagramFetch)
	list := h.creativeRows()
	if len(list) != 1 || list[0].Origin != store.OriginInstagramPublic || !list[0].RightsPending || list[0].SourceCaption != "Legenda boa" {
		t.Fatalf("criativos = %+v", list)
	}
	want := "Baixei o post ✅ Só confirma uma coisa: esse conteúdo é seu ou você tem autorização pra usar no anúncio? Responde *É MEU* pra usar como anúncio, ou *SÓ REFERÊNCIA* pra eu usar só como inspiração."
	if msg := h.lastClient(); !strings.Contains(msg, want) {
		t.Fatalf("pergunta do direito de uso = %q", msg)
	}
	if ig.ownCalls != 0 {
		t.Fatal("sem conexão não procura na conta")
	}
	// PRONTO sem responder: não fecha a etapa
	h.say("pronto")
	if h.conv().State != store.StateAwaitingCreatives || !strings.Contains(h.lastClient(), "É MEU") {
		t.Fatalf("fechou sem confirmar: estado = %s msg = %q", h.conv().State, h.lastClient())
	}
	h.say("É meu!")
	if list := h.creativeRows(); list[0].RightsPending {
		t.Fatal("não confirmou")
	}
	if msg := h.lastClient(); !strings.Contains(msg, "1 de 5") {
		t.Fatalf("resposta do É MEU = %q", msg)
	}
	h.say("pronto")
	h.say("pronto")
	if h.conv().State != store.StateIdle {
		t.Fatalf("estado = %s msg = %q", h.conv().State, h.lastClient())
	}
}

func TestInstagramPublicPostReferenceOnly(t *testing.T) {
	h, pubs, _, ig, fs := newIGHarness(t)
	ig.pub["OUTRO1"] = instagram.Post{Permalink: "https://www.instagram.com/p/OUTRO1/", Caption: "Pizza que derrete",
		Items: []instagram.Item{igSquare, igVertical}}
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("https://www.instagram.com/reel/OUTRO1/")
	h.runKind(engine.JobInstagramFetch)
	if h.creatives() != 3 || fs.count() != 3 {
		t.Fatalf("criativos = %d", h.creatives())
	}
	h.say("só referência")
	list := h.creativeRows()
	if len(list) != 1 || list[0].Origin != store.OriginUpload || fs.count() != 1 {
		t.Fatalf("restaram %+v (arquivos %d)", list, fs.count())
	}
	refs := strings.Join(mapValues(pubs.copies), "\n")
	if !strings.Contains(refs, "https://www.instagram.com/p/OUTRO1/") || !strings.Contains(refs, "Pizza que derrete") || strings.Count(refs, "OUTRO1") != 1 {
		t.Fatalf("referências = %q", refs)
	}
	if msg := h.lastClient(); !strings.Contains(msg, "só como referência") || !strings.Contains(msg, "1 de 5") {
		t.Fatalf("resposta = %q", msg)
	}
	// sem post pendente, "é meu" não é comando (não quebra nada)
	h.say("é meu")
	if h.creatives() != 1 {
		t.Fatal("mexeu nos anúncios")
	}
}

func TestInstagramPermissionErrorFallsBackToPublic(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.ownErr = instagram.ErrPermission
	ig.pub["MEU1"] = instagram.Post{Items: []instagram.Item{igSquare}, Permalink: "https://www.instagram.com/p/MEU1/"}
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/MEU1/")
	h.runKind(engine.JobInstagramFetch)
	if ig.ownCalls != 1 || ig.pubCalls != 1 {
		t.Fatalf("conta = %d público = %d", ig.ownCalls, ig.pubCalls)
	}
	if list := h.creativeRows(); len(list) != 1 || list[0].Origin != store.OriginInstagramPublic || !list[0].RightsPending {
		t.Fatalf("criativos = %+v", list)
	}
	// post de outra conta também cai no público
	ig.ownErr = nil
	ig.pub["OUTRO2"] = instagram.Post{Items: []instagram.Item{igVertical}}
	h.say("SÓ REFERÊNCIA")
	h.say("https://www.instagram.com/p/OUTRO2/")
	h.runKind(engine.JobInstagramFetch)
	if ig.ownCalls != 2 || ig.pubCalls != 2 || h.creatives() != 1 {
		t.Fatalf("conta = %d público = %d criativos = %d", ig.ownCalls, ig.pubCalls, h.creatives())
	}
}

func TestInstagramFetchFailureAsksForFile(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	ig.pubErr = instagram.ErrUnavailable
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/PRIVADO/")
	h.runKind(engine.JobInstagramFetch)
	if msg := h.lastClient(); msg != "Não consegui baixar esse post (pode ser privado) 😕 Me manda a foto ou o vídeo direto aqui?" {
		t.Fatalf("resposta = %q", msg)
	}
	if h.creatives() != 0 || h.conv().State != store.StateAwaitingCreatives {
		t.Fatalf("criativos = %d estado = %s", h.creatives(), h.conv().State)
	}
}

func TestInstagramRejectedMediaAndLimit(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1", Items: []instagram.Item{igTiny}}
	ig.own["MEU2"] = instagram.Post{MediaID: "2", Items: []instagram.Item{igSquare, igSquare, igSquare, igSquare, igSquare, igSquare}}
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/MEU1/")
	h.runKind(engine.JobInstagramFetch)
	if msg := h.lastClient(); h.creatives() != 0 || !strings.Contains(msg, "pequena demais") || strings.Contains(msg, "Peguei") {
		t.Fatalf("imagem pequena: criativos = %d msg = %q", h.creatives(), msg)
	}
	h.sendFile(vertical)
	h.say("https://www.instagram.com/p/MEU2/")
	h.runKind(engine.JobInstagramFetch)
	if h.creatives() != 5 {
		t.Fatalf("criativos = %d (limite 5)", h.creatives())
	}
	if msg := h.lastClient(); !strings.Contains(msg, "6 mídias") || !strings.Contains(msg, "5 de 5") {
		t.Fatalf("resposta = %q", msg)
	}
}

func TestInstagramLinkIgnoredOutsideCreatives(t *testing.T) {
	h, _, _, _, _ := newIGHarness(t)
	h.att.Responses = append(h.att.Responses, `{"intent":"question","reply":"Que post legal!"}`)
	h.say("https://www.instagram.com/p/MEU1/")
	if h.pendingJobs(engine.JobInstagramFetch) != 0 || h.att.Calls() != 1 {
		t.Fatal("link fora da etapa dos anúncios segue para a Atendente")
	}
}

const (
	msgIGHold = "Só um instante, ainda tô baixando o post que você mandou 🔎"
	msgIGLate = "O post chegou depois que fechamos os anúncios, então não entrou nessa campanha."
)

// I8: o link sai do texto e o resto da mensagem segue (comando, referência de texto, PRONTO).
func TestInstagramLinkKeepsRestOfMessage(t *testing.T) {
	h, pubs, _, ig, _ := newIGHarness(t)
	ig.pub["X1"] = instagram.Post{Items: []instagram.Item{igSquare}}
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("https://www.instagram.com/p/X1/\napaga a 1")
	if h.creatives() != 0 || h.pendingJobs(engine.JobInstagramFetch) != 1 {
		t.Fatalf("comando junto do link: criativos = %d jobs = %d msg = %q", h.creatives(), h.pendingJobs(engine.JobInstagramFetch), h.lastClient())
	}
	if msg := h.lastClient(); !strings.Contains(msg, "apaguei a 1") || !strings.Contains(msg, "vou buscar esse post") {
		t.Fatalf("resposta = %q", msg)
	}
	h.say(`olha esse post https://www.instagram.com/p/X2/ headline: "Pizza quentinha na sua porta em 30 minutos"`)
	refs := strings.Join(mapValues(pubs.copies), "\n")
	if !strings.Contains(refs, `headline: "Pizza quentinha na sua porta em 30 minutos"`) || strings.Contains(refs, "instagram.com") {
		t.Fatalf("referência = %q", refs)
	}
	h.say("https://www.instagram.com/p/X3/ pronto")
	if msg := h.lastClient(); msg != msgIGHold || h.pendingJobs(engine.JobInstagramFetch) != 3 {
		t.Fatalf("link + PRONTO: msg = %q jobs = %d", msg, h.pendingJobs(engine.JobInstagramFetch))
	}
}

// I7: PRONTO com post ainda baixando não fecha a etapa; fecha depois que o post chega.
func TestInstagramDoneWaitsForPendingFetch(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Items: []instagram.Item{igVertical}}
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("https://www.instagram.com/p/MEU1/")
	h.say("pronto")
	if msg := h.lastClient(); msg != msgIGHold || h.conv().State != store.StateAwaitingCreatives {
		t.Fatalf("PRONTO com post baixando: estado = %s msg = %q", h.conv().State, msg)
	}
	h.runKind(engine.JobInstagramFetch)
	if h.creatives() != 2 {
		t.Fatalf("criativos = %d", h.creatives())
	}
	h.say("pronto")
	if h.conv().State != store.StateIdle {
		t.Fatalf("depois do post: estado = %s msg = %q", h.conv().State, h.lastClient())
	}
}

// I7: a etapa fechou antes do post chegar (ou no meio do download): o cliente fica sabendo.
func TestInstagramLatePostTellsClient(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	ig.pub["X1"] = instagram.Post{Items: []instagram.Item{igSquare}}
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("https://www.instagram.com/p/X1/")
	_, err := h.pool.Exec(ctx, `UPDATE conversations SET state = $1`, store.StateIdle) // fechou antes do job
	h.must(err)
	h.runKind(engine.JobInstagramFetch)
	if h.creatives() != 1 || ig.pubCalls != 0 || h.lastClient() != msgIGLate {
		t.Fatalf("antes do job: criativos = %d chamadas = %d msg = %q", h.creatives(), ig.pubCalls, h.lastClient())
	}

	// fechou enquanto baixava
	h2, _, _, ig2, _ := newIGHarness(t)
	ig2.pub["X1"] = instagram.Post{Items: []instagram.Item{igSquare}}
	h2.say("SIM " + h2.reachApproval())
	h2.say("https://www.instagram.com/p/X1/")
	ig2.before = func() {
		_, err := h2.pool.Exec(ctx, `UPDATE conversations SET state = $1`, store.StateIdle)
		h2.must(err)
	}
	h2.runKind(engine.JobInstagramFetch)
	if h2.creatives() != 0 || h2.lastClient() != msgIGLate {
		t.Fatalf("durante o download: criativos = %d msg = %q", h2.creatives(), h2.lastClient())
	}

	// atendimento humano: a equipe conduz, sem aviso automático
	h3, _, _, ig3, _ := newIGHarness(t)
	ig3.pub["X1"] = instagram.Post{Items: []instagram.Item{igSquare}}
	h3.say("SIM " + h3.reachApproval())
	h3.say("https://www.instagram.com/p/X1/")
	before := h3.lastClient()
	_, err = h3.pool.Exec(ctx, `UPDATE conversations SET state = $1`, store.StateNeedsHuman)
	h3.must(err)
	h3.runKind(engine.JobInstagramFetch)
	if h3.creatives() != 0 || h3.lastClient() != before {
		t.Fatalf("humano: criativos = %d msg = %q", h3.creatives(), h3.lastClient())
	}
}

func TestPublishSkipsUnconfirmedCreatives(t *testing.T) {
	h, _, pub, _, _ := newIGHarness(t)
	h.connectMeta()
	h.readyProject()
	d, err := h.st.LatestApprovedDraft(ctx, h.conv().ID)
	h.must(err)
	ident := h.ident()
	_, err = h.st.AddCreative(ctx, store.Creative{OrgID: ident.OrgID, DraftID: d.ID, Kind: "image", Mime: "image/jpeg", Path: "p.jpg",
		Origin: store.OriginInstagramPublic, RightsPending: true})
	h.must(err)
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 1 || len(pub.inputs[0].Creatives) != 1 {
		t.Fatalf("publicou %d criativos", len(pub.inputs[0].Creatives))
	}
}

func TestPublishWithOnlyUnconfirmedAsksForMedia(t *testing.T) {
	h, _, pub, _, _ := newIGHarness(t)
	h.connectMeta()
	h.readyProject()
	_, err := h.pool.Exec(ctx, `UPDATE creatives SET origin = 'instagram_public', rights_confirmed = false`)
	h.must(err)
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 0 {
		t.Fatal("publicou post sem confirmação")
	}
	if h.conv().State != store.StateAwaitingCreatives || !strings.Contains(h.lastClient(), "foto ou vídeo") {
		t.Fatalf("estado = %s msg = %q", h.conv().State, h.lastClient())
	}
}

func TestPublishOwnPostUsesInstagramMedia(t *testing.T) {
	h, _, pub, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Items: []instagram.Item{igSquare, igVertical}}
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/MEU1/")
	h.runKind(engine.JobInstagramFetch)
	h.say("pronto")
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 1 {
		t.Fatalf("Publish = %d", pub.calls())
	}
	in := pub.inputs[0]
	if len(in.Creatives) != 1 || in.Creatives[0].InstagramMediaID != "1789" || in.InstagramActorID != "IG1" || len(in.Creatives[0].Data) != 0 {
		t.Fatalf("criativos = %+v", in.Creatives)
	}
	if strings.Contains(h.lastClient(), "Os textos que vão no ar") || !strings.Contains(h.lastClient(), "post do Instagram") {
		t.Fatalf("mensagem = %q", h.lastClient())
	}

	// misturado com upload: sobe os arquivos normalmente
	h2, _, pub2, ig2, _ := newIGHarness(t)
	h2.connectMetaIG()
	ig2.own["MEU1"] = instagram.Post{MediaID: "1789", Items: []instagram.Item{igVertical}}
	h2.say("SIM " + h2.reachApproval())
	h2.sendFile(square)
	h2.say("https://www.instagram.com/p/MEU1/")
	h2.runKind(engine.JobInstagramFetch)
	h2.say("pronto")
	h2.runKind(engine.JobCreativesPreview)
	h2.runKind(engine.JobMetaPublish)
	for _, c := range pub2.inputs[0].Creatives {
		if c.InstagramMediaID != "" || len(c.Data) == 0 {
			t.Fatalf("misturado: %+v", c)
		}
	}
	if len(pub2.inputs[0].Creatives) != 2 {
		t.Fatalf("misturado: %d criativos", len(pub2.inputs[0].Creatives))
	}
}

// I4: a Meta recusa o criativo feito do post: a mesma publicação sobe os arquivos já guardados e tenta de novo
// (mesmo nome com a marca #p<id>, sem campanha duplicada).
func TestPublishOwnPostFallsBackToUpload(t *testing.T) {
	h, pubs, pub, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Items: []instagram.Item{igSquare, igVertical}}
	pub.errs = []error{&meta.PublishError{Step: "creative", Code: 100, Message: "source_instagram_media_id inválido",
		UserMessage: "A Meta recusou o anúncio."}}
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/MEU1/")
	h.runKind(engine.JobInstagramFetch)
	h.say("pronto")
	h.runKind(engine.JobCreativesPreview)
	h.runKind(engine.JobMetaPublish)
	if pub.calls() != 2 {
		t.Fatalf("Publish = %d", pub.calls())
	}
	first, second := pub.inputs[0], pub.inputs[1]
	if first.Creatives[0].InstagramMediaID != "1789" || first.Name != second.Name || !strings.Contains(second.Name, "#p") {
		t.Fatalf("1ª = %+v / nomes %q %q", first.Creatives, first.Name, second.Name)
	}
	if len(second.Creatives) != 2 {
		t.Fatalf("2ª tentativa com %d criativos", len(second.Creatives))
	}
	for _, c := range second.Creatives {
		if c.InstagramMediaID != "" || len(c.Data) == 0 {
			t.Fatalf("2ª tentativa sem os arquivos: %+v", c)
		}
	}
	if len(pubs.pubs) != 1 || pubs.pubs[0].Status != "paused" || pubs.starts != 1 {
		t.Fatalf("publicações = %+v (starts %d)", pubs.pubs, pubs.starts)
	}
	msg := h.lastClient()
	if !strings.Contains(msg, "Prontinho") || !strings.Contains(msg, "Os textos que vão no ar") || strings.Contains(msg, "legenda, curtidas") {
		t.Fatalf("mensagem = %q", msg)
	}
	if h.pendingJobs(engine.JobMetaPublish) != 0 {
		t.Fatal("job de publicação ficou para nova tentativa")
	}
}

// runKindCtx roda os jobs do tipo com o contexto dado (o do worker: prazo ou desligamento).
func (h *harness) runKindCtx(jctx context.Context, kind string) {
	h.t.Helper()
	_, err := h.pool.Exec(ctx, `UPDATE outbox_jobs SET run_after = CASE WHEN kind = $1 THEN now() ELSE now() + interval '1 hour' END
		WHERE status = 'pending'`, kind)
	h.must(err)
	for {
		job, ok, err := h.st.ClaimJob(ctx)
		h.must(err)
		if !ok {
			return
		}
		_, err = h.st.FinishJob(ctx, job.ID, h.eng.HandleJob(jctx, job))
		h.must(err)
	}
}

// I1: cada item é guardado assim que baixa, antes do próximo (só um arquivo em memória por vez).
func TestInstagramSavesEachItemBeforeNext(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Items: []instagram.Item{igSquare, igVertical}}
	var counts []int
	ig.afterSave = func(int) { counts = append(counts, h.creatives()) }
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/MEU1/")
	h.runKind(engine.JobInstagramFetch)
	if len(counts) != 2 || counts[0] != 1 || counts[1] != 2 {
		t.Fatalf("criativos guardados depois de cada item = %v", counts)
	}
}

// I6: nova tentativa do mesmo job não duplica os anúncios já guardados (nem baixa de novo).
func TestInstagramRetryDoesNotDuplicate(t *testing.T) {
	for _, own := range []bool{true, false} {
		h, _, _, ig, fs := newIGHarness(t)
		post := instagram.Post{Permalink: "https://www.instagram.com/p/CAR1/", Items: []instagram.Item{igSquare, igVertical}}
		if own {
			h.connectMetaIG()
			post.MediaID = "1789"
			ig.own["CAR1"] = post
		} else {
			ig.pub["CAR1"] = post
		}
		h.say("SIM " + h.reachApproval())
		h.say("https://www.instagram.com/p/CAR1/")
		h.runKind(engine.JobInstagramFetch)
		saves := 0
		ig.afterSave = func(int) { saves++ }
		// o job roda de novo (worker caiu depois de guardar, antes de fechar o job)
		_, err := h.pool.Exec(ctx, `INSERT INTO outbox_jobs (kind, payload) SELECT kind, payload FROM outbox_jobs WHERE kind = $1`, engine.JobInstagramFetch)
		h.must(err)
		h.runKind(engine.JobInstagramFetch)
		if h.creatives() != 2 || fs.count() != 2 || saves != 0 {
			t.Fatalf("own=%v: criativos = %d arquivos = %d guardados de novo = %d", own, h.creatives(), fs.count(), saves)
		}
	}
}

// I6: guardou e o aviso não saiu: o job fica concluído, sem baixar tudo de novo.
func TestInstagramSendFailureDoesNotRetry(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Items: []instagram.Item{igSquare}}
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/MEU1/")
	h.wa.fail = true
	h.runKind(engine.JobInstagramFetch)
	h.wa.fail = false
	if n := h.count(`SELECT count(*) FROM outbox_jobs WHERE kind = $1 AND status = 'done'`, engine.JobInstagramFetch); n != 1 || h.creatives() != 1 {
		t.Fatalf("jobs concluídos = %d criativos = %d", n, h.creatives())
	}
}

// I5: prazo total de 3 min para baixar; acabou o prazo do job no meio, o que baixou fica e o aviso sai
// (gravação e aviso com contexto próprio).
func TestInstagramBudgetKeepsDownloadedAndNotifies(t *testing.T) {
	h, _, _, ig, _ := newIGHarness(t)
	h.connectMetaIG()
	ig.own["MEU1"] = instagram.Post{MediaID: "1789", Items: []instagram.Item{igSquare, igVertical}}
	h.say("SIM " + h.reachApproval())
	h.say("https://www.instagram.com/p/MEU1/")
	jctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ig.afterSave = func(pos int) {
		if pos == 0 {
			cancel() // prazo do job ou worker parando depois do primeiro item
		}
	}
	h.runKindCtx(jctx, engine.JobInstagramFetch)
	if engine.InstagramBudget != 3*time.Minute {
		t.Fatalf("orçamento = %v", engine.InstagramBudget)
	}
	if len(ig.deadlines) != 1 || ig.deadlines[0] > engine.InstagramBudget || ig.deadlines[0] < engine.InstagramBudget-time.Minute {
		t.Fatalf("prazo da busca = %v (orçamento %v)", ig.deadlines, engine.InstagramBudget)
	}
	if h.creatives() != 1 {
		t.Fatalf("criativos = %d", h.creatives())
	}
	if msg := h.lastClient(); !strings.Contains(msg, "Peguei seu post") {
		t.Fatalf("aviso depois do prazo = %q", msg)
	}
}
