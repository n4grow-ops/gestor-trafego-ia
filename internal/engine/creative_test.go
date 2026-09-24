package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/store"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/whatsapp"
)

type fakeStorage struct {
	mu    sync.Mutex
	files map[string]string
	fail  bool
}

func (f *fakeStorage) Upload(_ context.Context, bucket, path, contentType string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("storage fora")
	}
	if f.files == nil {
		f.files = map[string]string{}
	}
	f.files[bucket+"/"+path] = contentType
	return nil
}

func newCreativeHarness(t *testing.T) (*harness, *fakeStorage) {
	h := newHarness(t, "owner")
	fs := &fakeStorage{}
	h.eng.Storage = fs
	return h, fs
}

func (h *harness) sendMedia(kind whatsapp.Kind, mime string) {
	h.t.Helper()
	h.seq++
	h.wa.media, h.wa.mime = []byte("ARQUIVO"), mime
	h.must(h.eng.HandleInbound(ctx, whatsapp.Inbound{ExternalID: fmt.Sprintf("m%d", h.seq), ChatID: chatID, Phone: phone, Kind: kind}))
}

func TestApprovalAsksForCreatives(t *testing.T) {
	h, fs := newCreativeHarness(t)
	code := h.reachApproval()
	h.say("SIM " + code)
	if h.conv().State != store.StateAwaitingCreatives || !strings.Contains(h.wa.last().Text, "imagens ou vídeos") {
		t.Fatalf("estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
	h.sendMedia(whatsapp.KindImage, "image/jpeg")
	h.sendMedia(whatsapp.KindVideo, "video/mp4")
	if n := h.count(`SELECT count(*) FROM creatives`); n != 2 || len(fs.files) != 2 {
		t.Fatalf("criativos = %d arquivos = %v", n, fs.files)
	}
	if !strings.Contains(h.wa.last().Text, "2 de 5") {
		t.Fatalf("confirmação = %q", h.wa.last().Text)
	}
	for p, ct := range fs.files {
		if !strings.HasPrefix(p, "creatives/org/") || (ct != "image/jpeg" && ct != "video/mp4") {
			t.Fatalf("arquivo %s %s", p, ct)
		}
	}
	if stage := h.projectStage(); stage != store.StageAwaitingCreatives {
		t.Fatalf("etapa do projeto durante os anúncios = %s", stage)
	}
	h.say("pronto")
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "recebi 2") {
		t.Fatalf("conclusão: estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
	if stage := h.projectStage(); stage != store.StageReady {
		t.Fatalf("etapa do projeto depois dos anúncios = %s", stage)
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
		h.sendMedia(whatsapp.KindImage, "image/png")
	}
	if h.conv().State != store.StateIdle || !strings.Contains(h.wa.last().Text, "recebi 5") {
		t.Fatalf("estado = %s msg = %q", h.conv().State, h.wa.last().Text)
	}
}

func TestCreativeUploadFailure(t *testing.T) {
	h, fs := newCreativeHarness(t)
	h.say("SIM " + h.reachApproval())
	fs.fail = true
	h.sendMedia(whatsapp.KindImage, "image/jpeg")
	if h.count(`SELECT count(*) FROM creatives`) != 0 || !strings.Contains(h.wa.last().Text, "Não consegui salvar") {
		t.Fatalf("msg = %q", h.wa.last().Text)
	}
}
