package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/humanize"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/media"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

// Storage guarda os arquivos dos anúncios (Supabase Storage, bucket privado).
type Storage interface {
	Upload(ctx context.Context, bucket, path, contentType string, data []byte) error
	Download(ctx context.Context, bucket, path string) ([]byte, error)
	Delete(ctx context.Context, bucket, path string) error
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
	data, mime, err := e.WA.DownloadMedia(ctx, in)
	if err != nil {
		e.Log.Error("falha ao baixar criativo", "conversation_id", conv.ID, "erro", err)
		return creativeFailed
	}
	kind := "image"
	if in.Kind == whatsapp.KindVideo || strings.HasPrefix(mime, "video/") {
		kind = "video"
	}
	c := store.Creative{OrgID: ident.OrgID, DraftID: d.ID, ProjectID: d.ProjectID, Kind: kind, Mime: mime,
		Size: int64(len(data)), Format: string(media.Other), Placement: string(media.Feed)}
	warning := ""
	if mime == "" {
		mime = kind + "/*"
	}
	if info, err := media.Inspect(mime, data); err != nil {
		// arquivo que não sei ler (ex.: formato raro) segue: a Meta confere de novo no envio
		e.Log.Warn("criativo sem leitura de formato", "conversation_id", conv.ID, "mime", mime, "erro", err)
	} else {
		v := media.Check(info)
		if !v.OK {
			e.audit(ctx, ident.OrgID, member(ident), "creative_rejected", map[string]any{"draft_id": d.ID, "reason": v.Reason})
			return creativeRejected + v.Reason + "]"
		}
		warning = v.Warning
		f := info.Format()
		c.Width, c.Height, c.DurationMS = info.Width, info.Height, int(info.Duration/time.Millisecond)
		c.Format, c.Placement = string(f), string(f.Placement())
	}
	c.Path = fmt.Sprintf("org/%d/draft/%d/%d-%s.%s", ident.OrgID, d.ID, e.now().UnixNano(), safeName(in.ExternalID), extension(mime, kind))
	contentType := c.Mime
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := e.Storage.Upload(ctx, CreativesBucket, c.Path, contentType, data); err != nil {
		e.Log.Error("falha ao guardar criativo", "conversation_id", conv.ID, "erro", err)
		return creativeFailed
	}
	if _, err := e.Store.AddCreative(ctx, c); err != nil {
		e.Log.Error("falha ao registrar criativo", "conversation_id", conv.ID, "erro", err)
		return creativeFailed
	}
	e.audit(ctx, ident.OrgID, member(ident), "creative_received", map[string]any{"draft_id": d.ID, "kind": kind, "format": c.Format})
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
	var received, failed, done, changed, noted bool
	for _, m := range msgs {
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
	if n >= maxCreatives || (done && n > 0) {
		if miss := missingPlacement(list); done && n < maxCreatives && miss != "" && !d.Nudged {
			if err := e.Store.MarkCreativesNudged(ctx, d.ID); err != nil {
				return err
			}
			return e.send(ctx, conv, join(append(parts, fmt.Sprintf(msgCreativesNudge, miss))...))
		}
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

// creativesStatus mostra quantos anúncios chegaram e quais posicionamentos já estão cobertos.
func creativesStatus(list []store.Creative) string {
	var feed, stories bool
	for _, c := range list {
		if c.Placement == string(media.Stories) {
			stories = true
		} else {
			feed = true
		}
	}
	mark := func(ok bool, missing string) string {
		if ok {
			return "✅"
		}
		return "⏳ falta " + missing
	}
	s := fmt.Sprintf("Até agora: %d de %d anúncios.\n• Feed: %s\n• Stories e Reels: %s", len(list), maxCreatives,
		mark(feed, "uma quadrada ou em pé (4:5)"), mark(stories, "uma vertical de tela cheia (9:16)"))
	if len(list) < maxCreatives {
		s += "\n\nPode mandar mais ou escrever PRONTO."
	}
	return s
}

// missingPlacement descreve o formato que falta para cobrir feed e Stories/Reels ("" = nada falta).
func missingPlacement(list []store.Creative) string {
	var feed, stories bool
	for _, c := range list {
		if c.Placement == string(media.Stories) {
			stories = true
		} else {
			feed = true
		}
	}
	switch {
	case !stories:
		return "nenhum vertical de tela cheia (9:16), que é o formato dos Stories e Reels"
	case !feed:
		return "nenhum quadrado ou em pé (4:5), que é o formato do feed"
	}
	return ""
}

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
	where := "Feed"
	if c.Placement == string(media.Stories) {
		where = "Stories e Reels"
	}
	return s + " — " + where
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
