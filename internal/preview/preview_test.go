package preview_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/n4grow-ops/gestor-trafego-ia/internal/preview"
)

func photo(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), 120, uint8(y * 255 / h), 255})
		}
	}
	return img
}

func TestFeedAndStory(t *testing.T) {
	post := preview.Post{Account: "@pizzariaboa", Headline: "Pizza grande por R$ 39,90 🍕",
		Text: "Massa fina, forno a lenha e entrega grátis no bairro. Chama no WhatsApp e peça a sua agora mesmo, sai quentinha em 30 minutos!",
		CTA:  "WHATSAPP_MESSAGE", Format: "square", Media: photo(1600, 1200)}
	cases := map[string]struct {
		fn   func(preview.Post) ([]byte, error)
		post preview.Post
		w, h int
	}{
		"feed-quadrado": {preview.Feed, post, 1080, 0},
		"feed-video":    {preview.Feed, preview.Post{Account: "Loja da Ana", Text: "Promo", Format: "vertical", VideoSeconds: 15}, 1080, 0},
		"stories":       {preview.Story, preview.Post{Account: post.Account, Headline: post.Headline, CTA: "LEARN_MORE", Format: "vertical", Media: photo(1080, 1920)}, 1080, 1920},
	}
	for name, c := range cases {
		data, err := c.fn(c.post)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: JPEG inválido: %v", name, err)
		}
		if img.Bounds().Dx() != c.w || (c.h > 0 && img.Bounds().Dy() != c.h) {
			t.Fatalf("%s: tamanho %v", name, img.Bounds())
		}
		if dir := os.Getenv("PREVIEW_OUT"); dir != "" {
			os.WriteFile(filepath.Join(dir, name+".jpg"), data, 0o644)
		}
	}
}

func TestCTALabel(t *testing.T) {
	if preview.CTALabel("WHATSAPP_MESSAGE") != "Enviar mensagem" || preview.CTALabel("LEARN_MORE") != "Saiba mais" {
		t.Fatal("rótulo do botão")
	}
}
