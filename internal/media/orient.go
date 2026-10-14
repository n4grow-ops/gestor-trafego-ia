package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
)

// uprightQuality é a qualidade do JPEG endireitado: alta para a foto não perder nitidez na Meta.
const uprightQuality = 95

// Orientation lê a tag EXIF Orientation de um JPEG (1 a 8). 1 quando não é JPEG, não tem EXIF ou a tag
// é inválida. A câmera do celular grava a foto "deitada" e usa essa tag para mandar girar na exibição.
func Orientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0xD9 || marker == 0xDA { // fim ou começo da imagem: o EXIF vem antes
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if o := int(bo.Uint16(t[e+8:])); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}

// Upright devolve a foto já de pé quando o JPEG manda girar (Orientation 2 a 8): aplica o giro nos pixels
// e regrava sem a tag, para a prévia, o formato e a Meta verem o mesmo que o cliente viu no celular.
// Sem giro (ou se não for JPEG), devolve os mesmos bytes e changed = false.
func Upright(data []byte) (out []byte, changed bool, err error) {
	o := Orientation(data)
	if o == 1 {
		return data, false, nil
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return data, false, fmt.Errorf("media: endireitar foto: %w", err)
	}
	b := src.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	dst := orient(rgba, o)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: uprightQuality}); err != nil {
		return data, false, fmt.Errorf("media: endireitar foto: %w", err)
	}
	return buf.Bytes(), true, nil
}

// orient aplica a transformação da tag EXIF (2 espelha, 3 gira 180°, 4 inverte, 5 transpõe,
// 6 gira 90° horário, 7 transpõe ao contrário, 8 gira 90° anti-horário).
func orient(src *image.RGBA, o int) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			si, di := sy*src.Stride+sx*4, y*dst.Stride+x*4
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
