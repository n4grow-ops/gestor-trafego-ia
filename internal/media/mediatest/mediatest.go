// Package mediatest monta arquivos de imagem e vídeo mínimos para testes.
package mediatest

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// PNG devolve uma imagem PNG lisa do tamanho pedido.
func PNG(w, h int) []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)))
	return b.Bytes()
}

// JPEG devolve uma foto JPEG com degradê do tamanho pedido.
func JPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y += 8 {
		for x := 0; x < w; x += 8 {
			c := color.RGBA{uint8(x * 255 / w), 90, uint8(y * 255 / h), 255}
			for dy := 0; dy < 8 && y+dy < h; dy++ {
				for dx := 0; dx < 8 && x+dx < w; dx++ {
					img.SetRGBA(x+dx, y+dy, c)
				}
			}
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

// MP4 devolve só as caixas que importam (ftyp, moov/mvhd, trak/tkhd): duração e tamanho.
// rotated grava o vídeo "deitado" com rotação de 90°, como o celular faz ao filmar em pé.
func MP4(seconds, w, h uint32, rotated bool) []byte {
	mvhd := box("mvhd", []byte{0, 0, 0, 0}, u32(0), u32(0), u32(1000), u32(seconds*1000), make([]byte, 80))
	matrix := []uint32{0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000}
	if rotated {
		matrix = []uint32{0, 0x10000, 0, 0xFFFF0000, 0, 0, 0, 0, 0x40000000}
	}
	var m []byte
	for _, v := range matrix {
		m = append(m, u32(v)...)
	}
	tkhd := box("tkhd", []byte{0, 0, 0, 7}, u32(0), u32(0), u32(1), u32(0), u32(seconds*1000), make([]byte, 8),
		make([]byte, 8), m, u32(w<<16), u32(h<<16))
	return append(box("ftyp", []byte("isom")), box("moov", mvhd, box("trak", tkhd))...)
}

func box(kind string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	b := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(8+len(body)))
	copy(b[4:], kind)
	return append(b, body...)
}

func u32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }

// WithOrientation põe no JPEG o bloco EXIF com a tag Orientation (1 a 8), como a câmera do celular grava
// a foto "deitada" e manda girar na exibição.
func WithOrientation(jpg []byte, o uint16) []byte {
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, byte(o >> 8), byte(o), 0, 0, 0, 0, 0, 0}
	app1 := append([]byte("Exif\x00\x00"), tiff...)
	seg := append([]byte{0xFF, 0xE1, byte((len(app1) + 2) >> 8), byte(len(app1) + 2)}, app1...)
	out := append([]byte{}, jpg[:2]...) // SOI
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}
