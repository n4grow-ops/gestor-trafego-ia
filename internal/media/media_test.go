package media_test

import (
	"strings"
	"testing"
	"time"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/media"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/media/mediatest"
)

var pngOf = mediatest.PNG

func TestImageFormatsAndChecks(t *testing.T) {
	cases := []struct {
		w, h   int
		format media.Format
		ok     bool
		warn   bool
	}{
		{1080, 1080, media.Square, true, false},
		{1080, 1350, media.Portrait, true, false},
		{1080, 1920, media.Vertical, true, false},
		{1920, 1080, media.Landscape, true, false},
		{800, 800, media.Square, true, true},    // aceita, mas avisa da nitidez
		{500, 500, media.Square, false, false},  // pequena demais
		{1080, 3000, media.Other, false, false}, // proporção absurda: a Meta recusa
		{1620, 1080, media.Other, true, false},  // fora do padrão: passa, sem aviso nem cobrança
		{1080, 1500, media.Other, true, false},
	}
	for _, c := range cases {
		info, err := media.Inspect("image/png", pngOf(c.w, c.h))
		if err != nil {
			t.Fatal(err)
		}
		if info.Width != c.w || info.Height != c.h || info.Format() != c.format {
			t.Errorf("%dx%d: info = %+v formato = %s", c.w, c.h, info, info.Format())
		}
		v := media.Check(info)
		if v.OK != c.ok || (v.Warning != "") != c.warn {
			t.Errorf("%dx%d: veredito = %+v", c.w, c.h, v)
		}
		if !v.OK && v.Reason == "" {
			t.Errorf("%dx%d: recusa sem motivo", c.w, c.h)
		}
	}
}

func TestPlacement(t *testing.T) {
	if media.Vertical.Placement() != media.Stories || media.Square.Placement() != media.Feed || media.Portrait.Placement() != media.Feed {
		t.Fatal("formato → posicionamento errado")
	}
}

var mp4 = mediatest.MP4

func TestVideoDurationAndSize(t *testing.T) {
	info, err := media.Inspect("video/mp4", mp4(15, 1080, 1920, false))
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 15*time.Second || info.Width != 1080 || info.Height != 1920 || info.Format() != media.Vertical {
		t.Fatalf("vídeo = %+v", info)
	}
	if v := media.Check(info); !v.OK || v.Warning != "" {
		t.Fatalf("vídeo de 15 s vertical deveria passar: %+v", v)
	}
	// celular grava em pé, mas o arquivo vem deitado com rotação de 90°
	info, _ = media.Inspect("video/quicktime", mp4(20, 1920, 1080, true))
	if info.Width != 1080 || info.Height != 1920 {
		t.Fatalf("rotação não aplicada: %+v", info)
	}
	long, _ := media.Inspect("video/mp4", mp4(90, 1080, 1080, false))
	if v := media.Check(long); !v.OK || !strings.Contains(v.Warning, "60") {
		t.Fatalf("vídeo de 90 s: %+v", v)
	}
	tooLong, _ := media.Inspect("video/mp4", mp4(300, 1080, 1080, false))
	if v := media.Check(tooLong); v.OK {
		t.Fatal("vídeo de 5 min deveria ser recusado")
	}
	if _, err := media.Inspect("video/mp4", []byte("não é mp4")); err == nil {
		t.Fatal("arquivo inválido deveria dar erro")
	}
}

// Vídeo deitado (16:9) vale como qualquer outro; só duração e proporção absurda são barradas.
func TestVideoAnyReasonableRatioPasses(t *testing.T) {
	info, err := media.Inspect("video/mp4", mp4(20, 1920, 1080, false))
	if err != nil {
		t.Fatal(err)
	}
	if v := media.Check(info); !v.OK || v.Warning != "" {
		t.Fatalf("vídeo 16:9 deveria passar sem aviso: %+v", v)
	}
	wide, _ := media.Inspect("video/mp4", mp4(20, 3000, 1000, false))
	if v := media.Check(wide); v.OK || v.Reason == "" {
		t.Fatalf("proporção 3:1 deveria ser recusada com motivo: %+v", v)
	}
}
