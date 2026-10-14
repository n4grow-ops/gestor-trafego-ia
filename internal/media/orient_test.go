package media_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/media"
	"github.com/n4grow-ops/gestor-trafego-ia/internal/media/mediatest"
)

// marked: 64x32, metade esquerda vermelha e direita azul. Gravada "deitada" com Orientation 6 (girar 90°
// no sentido horário), em pé ela tem 32x64 com o vermelho em cima.
func marked() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 64; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x < 32 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 95})
	return b.Bytes()
}

func TestOrientation(t *testing.T) {
	if got := media.Orientation(mediatest.WithOrientation(marked(), 6)); got != 6 {
		t.Fatalf("orientation = %d", got)
	}
	if got := media.Orientation(marked()); got != 1 {
		t.Fatalf("sem EXIF = %d", got)
	}
	if got := media.Orientation(mediatest.PNG(10, 10)); got != 1 {
		t.Fatalf("png = %d", got)
	}
}

// Caso real (conversa 51): foto 4608x3456 com Orientation 6 virou anúncio deitado.
func TestUprightRotates(t *testing.T) {
	out, changed, err := media.Upright(mediatest.WithOrientation(marked(), 6))
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 64 {
		t.Fatalf("tamanho = %v", b)
	}
	top, bottom := img.At(16, 8), img.At(16, 56)
	if r, _, b, _ := top.RGBA(); r>>8 < 200 || b>>8 > 60 {
		t.Errorf("em cima devia ser vermelho: %v", top)
	}
	if r, _, b, _ := bottom.RGBA(); b>>8 < 200 || r>>8 > 60 {
		t.Errorf("embaixo devia ser azul: %v", bottom)
	}
	if media.Orientation(out) != 1 {
		t.Error("a foto endireitada não pode levar a tag de giro")
	}
	info, err := media.Inspect("image/jpeg", out)
	if err != nil || info.Width != 32 || info.Height != 64 {
		t.Fatalf("inspect = %+v %v", info, err)
	}
}

func TestUprightLeavesNormalAlone(t *testing.T) {
	in := marked()
	out, changed, err := media.Upright(in)
	if err != nil || changed || !bytes.Equal(out, in) {
		t.Fatalf("foto sem giro mudou: changed=%v err=%v", changed, err)
	}
	png := mediatest.PNG(10, 10)
	if out, changed, _ := media.Upright(png); changed || !bytes.Equal(out, png) {
		t.Fatal("png mudou")
	}
}

// Todas as orientações saem com o vermelho (metade esquerda original) no lugar que a câmera pediu.
func TestUprightAllOrientations(t *testing.T) {
	// onde fica o vermelho depois de endireitar: "top", "bottom", "left" ou "right"
	want := map[uint16]string{2: "right", 3: "right", 4: "left", 5: "top", 6: "top", 7: "bottom", 8: "bottom"}
	for o, side := range want {
		out, changed, err := media.Upright(mediatest.WithOrientation(marked(), o))
		if err != nil || !changed {
			t.Fatalf("o=%d changed=%v err=%v", o, changed, err)
		}
		img, _ := jpeg.Decode(bytes.NewReader(out))
		b := img.Bounds()
		var x, y int
		switch side {
		case "top":
			x, y = b.Dx()/2, b.Dy()/8
		case "bottom":
			x, y = b.Dx()/2, b.Dy()*7/8
		case "left":
			x, y = b.Dx()/8, b.Dy()/2
		case "right":
			x, y = b.Dx()*7/8, b.Dy()/2
		}
		if r, _, bl, _ := img.At(x, y).RGBA(); r>>8 < 200 || bl>>8 > 60 {
			t.Errorf("o=%d: vermelho não está em %s (%dx%d)", o, side, b.Dx(), b.Dy())
		}
	}
}
