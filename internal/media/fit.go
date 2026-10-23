package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
)

// Proporções do quadro onde cabe a imagem de proporção extrema: vertical de tela cheia (9:16) e deitada
// (1,91:1), os limites que a Meta aceita.
const (
	fitTall = 9.0 / 16.0
	fitWide = 1.91
)

// FitExtreme põe a imagem comprida demais (print de tela inteira, panorama) inteira num quadro 9:16 ou
// 1,91:1, centralizada, com o fundo na cor média da borda. Assim a Meta aceita sem cortar o conteúdo.
// Imagem dentro dos limites volta sem mudança.
func FitExtreme(data []byte) (out []byte, changed bool, err error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, false, fmt.Errorf("media: encaixar imagem: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return data, false, nil
	}
	r := float64(w) / float64(h)
	cw, ch := w, h
	switch {
	case r < extremeRatioMin:
		cw = int(math.Ceil(float64(h) * fitTall))
	case r > extremeRatioMax:
		ch = int(math.Ceil(float64(w) / fitWide))
	default:
		return data, false, nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, cw, ch))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{edgeColor(src, r < extremeRatioMin)}, image.Point{}, draw.Src)
	at := image.Pt((cw-w)/2, (ch-h)/2)
	draw.Draw(dst, image.Rectangle{Min: at, Max: at.Add(image.Pt(w, h))}, src, b.Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: uprightQuality}); err != nil {
		return data, false, fmt.Errorf("media: encaixar imagem: %w", err)
	}
	return buf.Bytes(), true, nil
}

// edgeColor é a cor média das bordas que encostam no fundo novo: esquerda e direita na imagem alta,
// topo e base na larga.
func edgeColor(img image.Image, tall bool) color.Color {
	b := img.Bounds()
	var r, g, bl, n uint64
	add := func(x, y int) {
		cr, cg, cb, _ := img.At(x, y).RGBA()
		r, g, bl, n = r+uint64(cr>>8), g+uint64(cg>>8), bl+uint64(cb>>8), n+1
	}
	if tall {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			add(b.Min.X, y)
			add(b.Max.X-1, y)
		}
	} else {
		for x := b.Min.X; x < b.Max.X; x++ {
			add(x, b.Min.Y)
			add(x, b.Max.Y-1)
		}
	}
	return color.RGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255}
}
