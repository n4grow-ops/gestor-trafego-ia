package engine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"strings"
	"sync"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/engine"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/media/mediatest"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type fakeStorage struct {
	mu    sync.Mutex
	files map[string]string
	data  map[string][]byte
	fail  bool
}

func (f *fakeStorage) Upload(_ context.Context, bucket, path, contentType string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("storage fora")
	}
	if f.files == nil {
		f.files, f.data = map[string]string{}, map[string][]byte{}
	}
	f.files[bucket+"/"+path] = contentType
	f.data[bucket+"/"+path] = data
	return nil
}

func (f *fakeStorage) Download(_ context.Context, bucket, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[bucket+"/"+path]
	if !ok {
		return nil, errors.New("arquivo não existe")
	}
	return d, nil
}

func (f *fakeStorage) Delete(_ context.Context, bucket, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, bucket+"/"+path)
	delete(f.data, bucket+"/"+path)
	return nil
}

func (f *fakeStorage) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.files)
}

func newCreativeHarness(t *testing.T) (*harness, *fakeStorage) {
	h := newHarness(t, "owner")
	fs := &fakeStorage{}
	h.eng.Storage = fs
	return h, fs
}

// Arquivos de exemplo: o formato define o posicionamento (feed ou Stories/Reels).
var (
	square   = func() ([]byte, string) { return mediatest.JPEG(1080, 1080), "image/jpeg" }
	portrait = func() ([]byte, string) { return mediatest.PNG(1080, 1350), "image/png" }
	vertical = func() ([]byte, string) { return mediatest.MP4(15, 1080, 1920, false), "video/mp4" }
	tiny     = func() ([]byte, string) { return mediatest.PNG(400, 400), "image/png" }
	lowRes   = func() ([]byte, string) { return mediatest.PNG(800, 800), "image/png" }
)

func (h *harness) sendFile(file func() ([]byte, string)) {
	h.t.Helper()
	h.seq++
	data, mime := file()
	kind := whatsapp.KindImage
	if strings.HasPrefix(mime, "video/") {
		kind = whatsapp.KindVideo
	}
	h.wa.media, h.wa.mime = data, mime
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: fmt.Sprintf("m%d", h.seq), ChatID: chatID, Phone: phone, Kind: kind}))
}

func (h *harness) creatives() int { return h.count(`SELECT count(*) FROM creatives`) }

func TestApprovalAsksForCreatives(t *testing.T) {
	h, fs := newCreativeHarness(t)
	code := h.reachApproval()
	h.say("SIM " + code)
	msg := h.wa.last().Text
	if h.conv().State != store.StateAwaitingCreatives || !strings.Contains(msg, "imagens ou vídeos") ||
		!strings.Contains(msg, "pro feed") || !strings.Contains(msg, "Stories e Reels") {
		t.Fatalf("estado = %s msg = %q", h.conv().State, msg)
	}
	h.sendFile(square)
	if msg := h.wa.last().Text; !strings.Contains(msg, "1 de 5") || !strings.Contains(msg, "Feed: ✅") || !strings.Contains(msg, "Stories e Reels: ⏳") {
		t.Fatalf("status depois da quadrada = %q", msg)
	}
	h.sendFile(vertical)
	if msg := h.wa.last().Text; !strings.Contains(msg, "2 de 5") || !strings.Contains(msg, "Stories e Reels: ✅") {
		t.Fatalf("status depois do vertical = %q", msg)
	}
	if h.creatives() != 2 || fs.count() != 2 {
		t.Fatalf("criativos = %d arquivos = %v", h.creatives(), fs.files)
	}
	var format, placement string
	var dur int
	h.must(h.pool.QueryRow(ctx, `SELECT format, placement, duration_ms FROM creatives WHERE kind = 'video'`).Scan(&format, &placement, &dur))
	if format != "vertical" || placement != "stories" || dur != 15000 {
		t.Fatalf("vídeo gravado como %s/%s/%d", format, placement, dur)
	}
	if stage := h.projectStage(); stage != store.StageAwaitingCreatives {
		t.Fatalf("etapa do projeto durante os anúncios = %s", stage)
	}
	h.say("pronto")
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "recebi 2") || !strings.Contains(h.wa.last().Text, "prévia") {
		t.Fatalf("conclusão: estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
	if stage := h.projectStage(); stage != store.StageReady {
		t.Fatalf("etapa do projeto depois dos anúncios = %s", stage)
	}

	// prévia: feed com a foto quadrada e Stories com a capa do vídeo
	h.runKind(engine.JobCreativesPreview)
	imgs := h.wa.sentImages()
	if len(imgs) != 2 || !strings.Contains(imgs[0].Caption, "feed do Instagram") || !strings.Contains(imgs[1].Caption, "Stories") {
		t.Fatalf("prévias = %d %+v", len(imgs), imgs)
	}
	for _, im := range imgs {
		if _, err := jpeg.Decode(bytes.NewReader(im.Data)); err != nil || im.ChatID != chatID || im.Mime != "image/jpeg" {
			t.Fatalf("prévia inválida: %v %s %s", err, im.ChatID, im.Mime)
		}
	}
}

func (h *harness) projectStage() string {
	h.t.Helper()
	var stage string
	h.must(h.pool.QueryRow(ctx, `SELECT stage FROM projects ORDER BY id DESC LIMIT 1`).Scan(&stage))
	return stage
}

func TestCreativesNeedAtLeastOne(t *testing.T) {
	h, _ := newCreativeHarness(t)
	h.say("SIM " + h.reachApproval())
	h.say("pronto")
	if h.conv().State != store.StateAwaitingCreatives || !strings.Contains(h.wa.last().Text, "pelo menos uma") {
		t.Fatalf("estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
	h.say("tenho uma dúvida")
	if !strings.Contains(h.wa.last().Text, "esperando as imagens") {
		t.Fatalf("lembrete = %q", h.wa.last().Text)
	}
}

func TestCreativesFinishAtFive(t *testing.T) {
	h, _ := newCreativeHarness(t)
	h.say("SIM " + h.reachApproval())
	for range 5 {
		h.sendFile(portrait)
	}
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "recebi 5") {
		t.Fatalf("estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
}

func TestCreativeUploadFailure(t *testing.T) {
	h, fs := newCreativeHarness(t)
	h.say("SIM " + h.reachApproval())
	fs.fail = true
	h.sendFile(square)
	if h.creatives() != 0 || !strings.Contains(h.wa.last().Text, "Não consegui salvar") {
		t.Fatalf("msg = %q", h.wa.last().Text)
	}
}

func TestCreativeQualityCheck(t *testing.T) {
	h, fs := newCreativeHarness(t)
	h.say("SIM " + h.reachApproval())
	h.sendFile(tiny)
	if msg := h.wa.last().Text; h.creatives() != 0 || fs.count() != 0 || !strings.Contains(msg, "não deu pra usar") || !strings.Contains(msg, "pequena demais") {
		t.Fatalf("imagem pequena: criativos = %d msg = %q", h.creatives(), msg)
	}
	h.sendFile(lowRes)
	if msg := h.wa.last().Text; h.creatives() != 1 || !strings.Contains(msg, "Recebi") || !strings.Contains(msg, "⚠️") {
		t.Fatalf("imagem baixa: criativos = %d msg = %q", h.creatives(), msg)
	}
	h.wa.media, h.wa.mime = mediatest.MP4(300, 1080, 1920, false), "video/mp4"
	h.seq++
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: fmt.Sprintf("m%d", h.seq), ChatID: chatID, Phone: phone, Kind: whatsapp.KindVideo}))
	if msg := h.wa.last().Text; h.creatives() != 1 || !strings.Contains(msg, "4 minutos") {
		t.Fatalf("vídeo longo: criativos = %d msg = %q", h.creatives(), msg)
	}
}

func TestCreativesNudgeMissingPlacementOnce(t *testing.T) {
	h, _ := newCreativeHarness(t)
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.say("pronto")
	if msg := h.wa.last().Text; h.conv().State != store.StateAwaitingCreatives || !strings.Contains(msg, "vertical de tela cheia") || !strings.Contains(msg, "PRONTO de novo") {
		t.Fatalf("aviso: estado = %s msg = %q", h.conv().State, msg)
	}
	h.say("pronto")
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "recebi 1") {
		t.Fatalf("segundo pronto: estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
	h.runKind(engine.JobCreativesPreview)
	if imgs := h.wa.sentImages(); len(imgs) != 1 || !strings.Contains(imgs[0].Caption, "feed") {
		t.Fatalf("prévia só do feed: %+v", imgs)
	}
}

func TestCreativesDeleteReplaceAndList(t *testing.T) {
	h, fs := newCreativeHarness(t)
	h.say("SIM " + h.reachApproval())
	h.sendFile(square)
	h.sendFile(portrait)
	h.sendFile(vertical)

	h.say("quais eu mandei?")
	if msg := h.wa.last().Text; !strings.Contains(msg, "1. Imagem quadrada — Feed") || !strings.Contains(msg, "2. Imagem em pé (4:5) — Feed") ||
		!strings.Contains(msg, "3. Vídeo vertical (tela cheia), 15 s — Stories e Reels") {
		t.Fatalf("lista = %q", msg)
	}

	h.say("apaga a segunda")
	if msg := h.wa.last().Text; h.creatives() != 2 || fs.count() != 2 || !strings.Contains(msg, "apaguei a 2") || !strings.Contains(msg, "2 de 5") {
		t.Fatalf("apagar: criativos = %d arquivos = %d msg = %q", h.creatives(), fs.count(), msg)
	}
	if h.count(`SELECT count(*) FROM creatives WHERE format = 'portrait'`) != 0 {
		t.Fatal("apagou o anúncio errado")
	}

	h.say("troca a última")
	if msg := h.wa.last().Text; h.creatives() != 1 || !strings.Contains(msg, "Tirei a 2") || !strings.Contains(msg, "Stories e Reels: ⏳") {
		t.Fatalf("trocar: criativos = %d msg = %q", h.creatives(), msg)
	}

	h.say("apaga a 7")
	if msg := h.wa.last().Text; h.creatives() != 1 || !strings.Contains(msg, "Não achei o anúncio 7") {
		t.Fatalf("número inexistente: %q", msg)
	}

	h.sendFile(portrait)
	h.say("apaga")
	if msg := h.wa.last().Text; h.creatives() != 2 || !strings.Contains(msg, "Qual deles") {
		t.Fatalf("sem número: %q", msg)
	}

	h.say("apaga todas")
	if msg := h.wa.last().Text; h.creatives() != 0 || fs.count() != 0 || !strings.Contains(msg, "apaguei todos") {
		t.Fatalf("apagar todas: %q", msg)
	}
	if h.conv().State != store.StateAwaitingCreatives {
		t.Fatalf("estado = %s", h.conv().State)
	}
}
