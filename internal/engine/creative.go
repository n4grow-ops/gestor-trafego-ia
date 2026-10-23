package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/humanize"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/instagram"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/media"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// Storage guarda os arquivos dos anúncios (Supabase Storage, bucket privado).
type Storage interface {
	Upload(ctx context.Context, bucket, path, contentType string, data []byte) error
	Download(ctx context.Context, bucket, path string) ([]byte, error)
	Delete(ctx context.Context, bucket, path string) error
	SignedURL(ctx context.Context, bucket, path string, expiresIn int) (string, error)
}

const (
	CreativesBucket = "creatives"
	maxCreatives    = 5

	// Marcadores gravados como texto da mensagem recebida; o turno lê e responde.
	creativeSaved    = "[anúncio recebido]"
	creativeWarned   = "[anúncio recebido com aviso: "
	creativeRejected = "[anúncio recusado: "
	creativeFailed   = "[anúncio não salvo]"
)

// saveCreative confere a qualidade e guarda a imagem ou o vídeo junto do rascunho aprovado.
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
	var data []byte
	var mime string
	err = retryMedia(ctx, func() error {
		var err error
		data, mime, err = e.WA.DownloadMedia(ctx, in)
		return err
	})
	if err != nil {
		e.Log.Error("falha ao baixar criativo", "conversation_id", conv.ID, "erro", err)
		e.audit(ctx, ident.OrgID, member(ident), "creative_failed", map[string]any{"draft_id": d.ID, "step": "download", "error": err.Error()})
		return creativeFailed
	}
	mime = mediaMime(mime, data)
	kind := "image"
	if in.Kind == whatsapp.KindVideo || strings.HasPrefix(mime, "video/") {
		kind = "video"
	}
	return e.keepCreative(ctx, ident, conv.ID, d, creativeFile{data: data, mime: mime, kind: kind, name: in.ExternalID})
}

// MediaRetryDelays: pausas entre as tentativas de baixar e guardar o arquivo. A Wame às vezes falha
// logo que o webhook chega (mídia ainda não pronta, timeout); de novo, em segundos, costuma dar certo.
var MediaRetryDelays = []time.Duration{time.Second, 3 * time.Second}

func retryMedia(ctx context.Context, fn func() error) error {
	err := fn()
	for _, d := range MediaRetryDelays {
		if err == nil || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(d):
		}
		err = fn()
	}
	return err
}

// mediaMime confia no tipo do provedor só quando é imagem ou vídeo; foto mandada como documento pode chegar
// como application/octet-stream, e aí o tipo sai do conteúdo. Vazio quando nem o conteúdo diz.
func mediaMime(mime string, data []byte) string {
	mime = strings.TrimSpace(strings.Split(mime, ";")[0])
	if strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "video/") {
		return mime
	}
	if sniff := http.DetectContentType(data); strings.HasPrefix(sniff, "image/") || strings.HasPrefix(sniff, "video/") {
		return sniff
	}
	return ""
}

// creativeFile é o arquivo a guardar como anúncio, com a origem (vazia = upload pelo WhatsApp).
type creativeFile struct {
	data          []byte
	mime, kind    string
	name          string // parte do nome do arquivo no Storage
	origin        string
	igMediaID     string
	rightsPending bool
	sourceURL     string
	caption       string
	position      *int // posição do item no post do Instagram (nil = upload)
}

// keepCreative confere a qualidade, sobe no Storage e registra. Devolve o marcador do resultado
// (creativeSaved, creativeWarned..., creativeRejected... ou creativeFailed).
func (e *Engine) keepCreative(ctx context.Context, ident store.Identity, convID int64, d store.ApprovedDraft, f creativeFile) string {
	mime, kind, data := f.mime, f.kind, f.data
	upscaled := ""
	if kind == "image" {
		// Foto do celular gravada deitada com a tag de giro: endireita antes de medir, guardar e publicar.
		if up, changed, err := media.Upright(data); err != nil {
			e.Log.Warn("foto sem endireitar", "conversation_id", convID, "erro", err)
		} else if changed {
			data, mime = up, "image/jpeg"
		}
		// Print de tela inteira ou panorama: vai inteiro num quadro que a Meta aceita, em vez de recusar.
		if fit, changed, err := media.FitExtreme(data); err != nil {
			e.Log.Warn("imagem sem encaixe de proporção", "conversation_id", convID, "erro", err)
		} else if changed {
			data, mime = fit, "image/jpeg"
		}
		// Imagem pequena: amplia até o mínimo da Meta e segue com aviso, em vez de recusar.
		if big, from, changed, err := media.UpscaleSmall(data); err != nil {
			e.Log.Warn("imagem sem ampliar", "conversation_id", convID, "erro", err)
		} else if changed {
			data, mime = big, "image/jpeg"
			upscaled = fmt.Sprintf("a imagem era pequena (%dx%d): aumentei pra Meta aceitar, mas pode sair menos nítida. Se tiver uma versão maior, manda como *documento*", from.X, from.Y)
		}
	}
	c := store.Creative{OrgID: ident.OrgID, DraftID: d.ID, ProjectID: d.ProjectID, Kind: kind, Mime: mime,
		Size: int64(len(data)), Format: string(media.Other), Placement: string(media.Feed),
		Origin: f.origin, IGMediaID: f.igMediaID, RightsPending: f.rightsPending, SourceURL: f.sourceURL, SourceCaption: f.caption,
		SourcePosition: f.position}
	warning := ""
	if mime == "" {
		mime = kind + "/*"
	}
	if info, err := media.Inspect(mime, data); err != nil {
		// arquivo que não sei ler (ex.: formato raro) segue: a Meta confere de novo no envio
		e.Log.Warn("criativo sem leitura de formato", "conversation_id", convID, "mime", mime, "erro", err)
	} else {
		v := media.Check(info)
		if !v.OK {
			e.audit(ctx, ident.OrgID, member(ident), "creative_rejected", map[string]any{"draft_id": d.ID, "reason": v.Reason})
			return creativeRejected + v.Reason + "]"
		}
		warning = v.Warning
		if upscaled != "" {
			warning = upscaled
		}
		fm := info.Format()
		c.Width, c.Height, c.DurationMS = info.Width, info.Height, int(info.Duration/time.Millisecond)
		c.Format, c.Placement = string(fm), string(fm.Placement())
	}
	c.Path = fmt.Sprintf("org/%d/draft/%d/%d-%s.%s", ident.OrgID, d.ID, e.now().UnixNano(), safeName(f.name), extension(mime, kind))
	contentType := c.Mime
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := retryMedia(ctx, func() error { return e.Storage.Upload(ctx, CreativesBucket, c.Path, contentType, data) }); err != nil {
		e.Log.Error("falha ao guardar criativo", "conversation_id", convID, "erro", err)
		e.audit(ctx, ident.OrgID, member(ident), "creative_failed", map[string]any{"draft_id": d.ID, "step": "upload", "error": err.Error()})
		return creativeFailed
	}
	id, err := e.Store.AddCreative(ctx, c)
	if err != nil {
		e.Log.Error("falha ao registrar criativo", "conversation_id", convID, "erro", err)
		e.audit(ctx, ident.OrgID, member(ident), "creative_failed", map[string]any{"draft_id": d.ID, "step": "register", "error": err.Error()})
		return creativeFailed
	}
	if err := e.linkFile(ctx, "creative", id, CreativesBucket, c.Path, c.Mime); err != nil {
		e.Log.Warn("anúncio sem link para o painel", "creative_id", id, "erro", err) // o anúncio vale mesmo assim
	}
	e.audit(ctx, ident.OrgID, member(ident), "creative_received", map[string]any{"draft_id": d.ID, "kind": kind, "format": c.Format, "origin": c.Origin})
	if warning != "" {
		return creativeWarned + warning + "]"
	}
	return creativeSaved
}

// creativesTurn responde enquanto a conversa espera os anúncios (imagens ou vídeos).
func (e *Engine) creativesTurn(ctx context.Context, conv store.Conversation, msgs []string) error {
	d, err := e.Store.LatestApprovedDraft(ctx, conv.ID)
	if errors.Is(err, store.ErrNotFound) {
		// sem proposta aprovada não há anúncio a pedir: a conversa volta ao normal
		return e.Store.UpdateConversation(ctx, conv.ID, store.StateIdle, conv.Fields)
	}
	if err != nil {
		return err
	}
	list, err := e.Store.ListCreatives(ctx, d.ID)
	if err != nil {
		return err
	}
	var parts, warnings, rejected []string
	var received, failed, done, changed, noted, fetching bool
	for _, m := range msgs {
		if ok, err := e.enqueueInstagram(ctx, conv, d.ID, m); err != nil {
			return err
		} else if ok {
			// o link vira busca do post; o resto da mensagem (PRONTO, comando, referência) segue abaixo
			fetching = true
			if m = instagram.StripLinks(m); m == "" {
				continue
			}
		}
		if a := parseRights(m); a != rightsNone && hasPendingRights(list) {
			// resposta à pergunta do direito de uso do post público
			reply, err := e.applyRights(ctx, list[0].OrgID, d.ID, a)
			if err != nil {
				return err
			}
			parts = append(parts, reply)
			changed = true
			if list, err = e.Store.ListCreatives(ctx, d.ID); err != nil {
				return err
			}
			continue
		}
		switch {
		case strings.HasPrefix(m, creativeSaved):
			received = true
		case strings.HasPrefix(m, creativeWarned):
			received = true
			warnings = append(warnings, strings.TrimSuffix(strings.TrimPrefix(m, creativeWarned), "]"))
		case strings.HasPrefix(m, creativeRejected):
			rejected = append(rejected, strings.TrimSuffix(strings.TrimPrefix(m, creativeRejected), "]"))
		case strings.HasPrefix(m, creativeFailed):
			failed = true
		case isDone(m):
			done = true
		default:
			cmd := parseCreativeCommand(m)
			if cmd.kind == cmdNone {
				if !isCopyReference(m) {
					continue
				}
				// texto livre do cliente: referência pros textos do anúncio, usada na publicação
				if err := e.pubs().AppendClientCopy(ctx, d.ID, strings.TrimSpace(m)); err != nil {
					return err
				}
				noted = true
				continue
			}
			reply, deleted, err := e.applyCreativeCommand(ctx, d.ID, list, cmd)
			if err != nil {
				return err
			}
			parts = append(parts, reply)
			if deleted {
				changed = true
				if list, err = e.Store.ListCreatives(ctx, d.ID); err != nil {
					return err
				}
			}
		}
	}
	n := len(list)
	if done {
		// post do Instagram ainda baixando: o PRONTO espera, senão o post chega com a etapa fechada
		busy, err := e.Store.HasPendingJob(ctx, JobInstagramFetch, "draft_id", d.ID)
		if err != nil {
			return err
		}
		if busy {
			return e.send(ctx, conv, join(append(parts, msgInstagramHold)...))
		}
	}
	if (n >= maxCreatives || done) && hasPendingRights(list) {
		// post público sem resposta: não fecha a etapa nem vai para a Meta antes do É MEU ou SÓ REFERÊNCIA
		return e.send(ctx, conv, join(append(parts, msgInstagramRightsReminder)...))
	}
	if n >= maxCreatives || (done && n > 0) {
		return e.finishCreatives(ctx, conv, d, n, parts)
	}
	for _, r := range rejected {
		parts = append(parts, fmt.Sprintf(msgCreativeRejected, r))
	}
	if failed {
		parts = append(parts, msgCreativeFailed)
	}
	if noted {
		parts = append(parts, msgCopyNoted)
	}
	if fetching {
		parts = append(parts, msgInstagramFetching)
	}
	if received {
		ack := "Recebi! ✅"
		for _, w := range warnings {
			ack += "\n⚠️ " + upperFirst(w) + "."
		}
		parts = append(parts, ack)
	}
	switch {
	case done: // pronto sem nenhum anúncio
		parts = append(parts, msgCreativesNeedOne)
	case received || changed || len(rejected) > 0:
		parts = append(parts, creativesStatus(list))
	case fetching: // o job responde quando o post chegar
	case noted && n == 0:
		parts = append(parts, msgCopyNotedNeedMedia)
	case noted:
		parts = append(parts, msgCopyNotedMore)
	case len(parts) == 0 && !failed:
		parts = append(parts, msgCreativesWaiting)
	}
	return e.send(ctx, conv, join(parts...))
}

// finishCreatives fecha a etapa, avança o projeto e agenda a prévia do anúncio.
func (e *Engine) finishCreatives(ctx context.Context, conv store.Conversation, d store.ApprovedDraft, n int, parts []string) error {
	if err := e.Store.UpdateConversation(ctx, conv.ID, store.StateIdle, conv.Fields); err != nil {
		return err
	}
	if conv.ActiveProjectID != nil {
		if err := e.Store.SetProjectStage(ctx, *conv.ActiveProjectID, store.StageReady); err != nil {
			return err
		}
	}
	text := join(append(parts, fmt.Sprintf(msgCreativesDone, n))...)
	if err := e.send(ctx, conv, text); err != nil {
		return err
	}
	delay := 2 * time.Second
	if e.Humanize {
		delay += humanize.ChainDuration(text)
	}
	return e.Store.Enqueue(ctx, store.JobSpec{Kind: JobCreativesPreview, MaxAttempts: 3, Delay: delay,
		Payload: PreviewJob{ConversationID: conv.ID, DraftID: d.ID}})
}

// creativesStatus mostra só quantos anúncios chegaram. Não cobra formato: o cliente manda o que quiser.
func creativesStatus(list []store.Creative) string {
	s := fmt.Sprintf("Até agora: %d de %d anúncios.", len(list), maxCreatives)
	if len(list) < maxCreatives {
		s += "\n\nPode mandar mais ou escrever PRONTO."
	}
	return s
}

// msgAskCreatives é o pedido dos anúncios depois da aprovação: qualquer imagem ou vídeo, em qualquer proporção.
const msgAskCreatives = "Campanha aprovada! ✅\n\nPróxima etapa: os anúncios. Manda as imagens ou vídeos que quiser usar (até 5), do jeito que tiver: feed, stories, reels, na proporção que for. Eu subo na Meta como você mandar.\n\nPra não perder qualidade, manda a foto como *documento* (📎 → Documento) em vez de foto; o WhatsApp comprime as fotos normais.\n\nSe tiver headlines, textos ou anúncios que você gosta, manda aqui também (é opcional): eu uso como referência pros textos. Quando terminar, é só escrever PRONTO."

// askCreatives é o pedido dos anúncios depois da aprovação. Não depende dos lugares escolhidos nem pede formato.
func askCreatives(_ []string) string { return msgAskCreatives }

func describeCreative(c store.Creative) string {
	kind := "Imagem"
	if c.Kind == "video" {
		kind = "Vídeo"
	}
	format := media.Format(c.Format).Label()
	if c.Kind == "video" {
		format = strings.NewReplacer("quadrada", "quadrado", "deitada", "deitado").Replace(format)
	}
	s := kind + " " + format
	if c.Kind == "video" && c.DurationMS > 0 {
		s += fmt.Sprintf(", %d s", (c.DurationMS+500)/1000)
	}
	return s
}

func listCreatives(list []store.Creative) string {
	var b strings.Builder
	for i, c := range list {
		fmt.Fprintf(&b, "\n%d. %s", i+1, describeCreative(c))
	}
	return b.String()
}

// ---------- apagar, trocar e listar ----------

type cmdKind int

const (
	cmdNone cmdKind = iota
	cmdDelete
	cmdReplace
	cmdList
)

type creativeCmd struct {
	kind    cmdKind
	all     bool
	targets []int // posições 1..n; -1 = último, -2 = penúltimo
	onlyOf  string
}

var (
	reDelete  = regexp.MustCompile(`^(?:pode\s+|por favor\s+|quero\s+)?(?:apag\w*|remov\w*|exclu\w*|tira|tire|tirar|delet\w*|descart\w*)\b(.*)$`)
	reReplace = regexp.MustCompile(`^(?:pode\s+|quero\s+)?(?:troc\w*|substitu\w*)\b(.*)$`)
	reList    = regexp.MustCompile(`(?:quais|lista|listar|mostra|mostrar|ver)\b.*\b(?:mandei|enviei|anúnci\w*|anunci\w*|fotos?|vídeos?|videos?|arquivos?)|o que (?:eu )?(?:já )?(?:mandei|enviei)`)
	reNumber  = regexp.MustCompile(`\b([1-9])\b`)
	ordinals  = []struct {
		prefix string
		pos    int
	}{{"penúltim", -2}, {"penultim", -2}, {"últim", -1}, {"ultim", -1}, {"primeir", 1}, {"segund", 2}, {"terceir", 3}, {"quart", 4}, {"quint", 5}}
)

func parseCreativeCommand(s string) creativeCmd {
	s = strings.ToLower(strings.TrimSpace(s))
	var rest string
	cmd := creativeCmd{}
	if m := reDelete.FindStringSubmatch(s); m != nil {
		cmd.kind, rest = cmdDelete, m[1]
	} else if m := reReplace.FindStringSubmatch(s); m != nil {
		cmd.kind, rest = cmdReplace, m[1]
	} else if reList.MatchString(s) {
		return creativeCmd{kind: cmdList}
	} else {
		return cmd
	}
	for _, w := range strings.FieldsFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		switch {
		case w == "todas" || w == "todos" || w == "tudo":
			cmd.all = true
		case w == "foto" || w == "imagem" || w == "fotos" || w == "imagens":
			cmd.onlyOf = "image"
		case w == "vídeo" || w == "video" || w == "vídeos" || w == "videos":
			cmd.onlyOf = "video"
		}
		for _, o := range ordinals {
			if strings.HasPrefix(w, o.prefix) {
				cmd.targets = append(cmd.targets, o.pos)
				break
			}
		}
	}
	for _, m := range reNumber.FindAllStringSubmatch(rest, -1) {
		v, _ := strconv.Atoi(m[1])
		cmd.targets = append(cmd.targets, v)
	}
	return cmd
}

// applyCreativeCommand executa o pedido e devolve a resposta; deleted indica se algo foi apagado.
func (e *Engine) applyCreativeCommand(ctx context.Context, draftID int64, list []store.Creative, cmd creativeCmd) (string, bool, error) {
	if cmd.kind == cmdList {
		if len(list) == 0 {
			return msgCreativesNone, false, nil
		}
		return "Seus anúncios até agora:" + listCreatives(list) + "\n\n" + msgCreativesHowToDelete, false, nil
	}
	if len(list) == 0 {
		return msgCreativesNone, false, nil
	}
	var pick []int // índices 0..n-1
	switch {
	case cmd.all:
		for i := range list {
			if cmd.onlyOf == "" || list[i].Kind == cmd.onlyOf {
				pick = append(pick, i)
			}
		}
	case len(cmd.targets) > 0:
		for _, t := range cmd.targets {
			i := t - 1
			if t < 0 {
				i = len(list) + t
			}
			if i < 0 || i >= len(list) {
				return fmt.Sprintf(msgCreativeNotFound, abs(t)) + listCreatives(list), false, nil
			}
			if !slices.Contains(pick, i) {
				pick = append(pick, i)
			}
		}
	default:
		// sem número: vale se só houver um candidato
		for i := range list {
			if cmd.onlyOf == "" || list[i].Kind == cmd.onlyOf {
				pick = append(pick, i)
			}
		}
		if len(pick) != 1 {
			return msgCreativeWhich + listCreatives(list), false, nil
		}
	}
	if len(pick) == 0 {
		return msgCreativeWhich + listCreatives(list), false, nil
	}
	slices.Sort(pick)
	var names []string
	for j := len(pick) - 1; j >= 0; j-- { // do fim para o começo: a numeração vista pelo cliente não muda
		c := list[pick[j]]
		path, err := e.Store.DeleteCreative(ctx, draftID, c.ID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if err := e.Storage.Delete(ctx, CreativesBucket, path); err != nil {
			e.Log.Error("arquivo do anúncio não apagado no storage", "path", path, "erro", err)
		}
		e.audit(ctx, c.OrgID, "system", "creative_deleted", map[string]any{"draft_id": draftID, "creative_id": c.ID})
		names = append([]string{strconv.Itoa(pick[j] + 1)}, names...)
	}
	which := "a " + strings.Join(names, " e a ")
	if len(pick) == len(list) && len(list) > 1 {
		which = "todos"
	}
	if cmd.kind == cmdReplace {
		return fmt.Sprintf("Tirei %s. Pode mandar o novo aqui 👇", which), true, nil
	}
	return fmt.Sprintf("Pronto, apaguei %s.", which), true, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func join(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

func upperFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// reQuestion: pergunta ou pedido de ajuda, que não é referência de texto.
// "?" só no fim: headline de referência pode ter pergunta no meio ("Fome? A pizza chega em 30 min").
var reQuestion = regexp.MustCompile(`\?\s*$|^(?:como|quando|quanto|quantos|quantas|qual|quais|onde|por ?que|porque|o que|pode|posso|consegue|dá pra|da pra|tem como|e se|e agora|e aí|e ai)(?:\s|$)|\b(?:dúvida|duvida|pergunta|ajuda)\b`)

// reCopyMarker: o cliente diz que é texto de anúncio (aspas, "headline", "texto", "título", "tipo:"...).
var reCopyMarker = regexp.MustCompile(`["“”«»]|(?:^|[^\pL])(?:headlines?|text(?:o|os)|t[ií]tulos?|refer[êe]ncias?|frases?|legendas?|slogans?|chamadas?|copy|an[úu]ncios?)(?:$|[^\pL])|(?:^|[^\pL])(?:tipo|assim|parecido com)\s*:`)

// reChatter: conversa sobre o envio das mídias ou combinação de horário, não texto de anúncio.
// Fronteira por letra (\pL), não \b: \b do Go é só ASCII e falha em "amanhã".
var reChatter = regexp.MustCompile(`(?:^|[^\pL])(?:fotos?|v[íi]deos?|imagens?|arquivos?|amanh[ãa]|mais tarde|daqui a pouco|semana que vem|depois eu|vou mandar|j[áa] mando|ainda vou|aguarda|espera)(?:$|[^\pL])`)

// isCopyReference diz se o texto livre na etapa dos anúncios é uma referência de headline ou texto:
// não é pergunta nem conversa ("ok", "vou mandar as fotos amanhã"). Vale com marca de texto (aspas,
// "headline", "texto", "título", "tipo:") ou, sem marca, com 8 palavras ou mais e sem falar do envio.
func isCopyReference(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || strings.HasPrefix(s, "[") { // marcadores de mídia
		return false
	}
	if reQuestion.MatchString(s) {
		return false
	}
	words := len(strings.Fields(s))
	if reCopyMarker.MatchString(s) {
		return words >= 3
	}
	return words >= 8 && !reChatter.MatchString(s)
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
