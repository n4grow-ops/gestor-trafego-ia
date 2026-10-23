package media

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"

	xdraw "golang.org/x/image/draw"
)

// UpscaleSmall amplia a imagem com o menor lado abaixo de 600 pixels até 600, mantendo a proporção.
// A Meta recusa imagem menor que isso; ampliada ela passa (pode sair menos nítida, por isso o aviso).
// Imagem já grande o bastante volta sem mudança; from traz o tamanho original para o aviso.
func UpscaleSmall(data []byte) (out []byte, from image.Point, changed bool, err error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, image.Point{}, false, fmt.Errorf("media: ampliar imagem: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	from = image.Pt(w, h)
	short := min(w, h)
	if short <= 0 || short >= minSide {
		return data, from, false, nil
	}
	scale := float64(minSide) / float64(short)
	nw, nh := max(int(math.Ceil(float64(w)*scale)), minSide), max(int(math.Ceil(float64(h)*scale)), minSide)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: uprightQuality}); err != nil {
		return data, from, false, fmt.Errorf("media: ampliar imagem: %w", err)
	}
	return buf.Bytes(), from, true, nil
}
