package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Caso real (Método v3): print de tela inteira 1080x2519 era recusado por proporção extrema. Agora vai
// inteiro num quadro 9:16 e passa no Check.
func TestFitExtremeTallScreenshot(t *testing.T) {
	out, changed, err := FitExtreme(jpegOf(t, 1080, 2519))
	if err != nil || !changed {
		t.Fatalf("changed = %v err = %v", changed, err)
	}
	info, err := Inspect("image/jpeg", out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Height != 2519 || info.Width != 1417 || !Check(info).OK || info.Format() != Vertical {
		t.Fatalf("info = %+v check = %+v", info, Check(info))
	}
}

func TestFitExtremeWidePanorama(t *testing.T) {
	out, changed, err := FitExtreme(jpegOf(t, 3000, 1000))
	if err != nil || !changed {
		t.Fatalf("changed = %v err = %v", changed, err)
	}
	info, _ := Inspect("image/jpeg", out)
	if info.Width != 3000 || info.Height != 1571 || !Check(info).OK {
		t.Fatalf("info = %+v check = %+v", info, Check(info))
	}
}

func TestFitExtremeKeepsNormalImage(t *testing.T) {
	in := jpegOf(t, 1080, 1350)
	out, changed, err := FitExtreme(in)
	if err != nil || changed || !bytes.Equal(in, out) {
		t.Fatalf("imagem 4:5 mudou: changed = %v err = %v", changed, err)
	}
}
