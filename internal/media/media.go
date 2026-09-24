// Package media confere se uma imagem ou vídeo serve para anúncio na Meta: tamanho, formato e duração.
// Lê só o necessário do arquivo (cabeçalho da imagem, caixas do MP4), sem ferramentas externas.
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

type Format string

const (
	Square    Format = "square"    // 1:1, feed
	Portrait  Format = "portrait"  // 4:5, feed
	Vertical  Format = "vertical"  // 9:16, Stories e Reels
	Landscape Format = "landscape" // 1.91:1 a 16:9, feed
	Other     Format = "other"
)

type Placement string

const (
	Feed    Placement = "feed"
	Stories Placement = "stories" // Stories e Reels
)

func (f Format) Placement() Placement {
	if f == Vertical {
		return Stories
	}
	return Feed
}

// Label é o formato em português, para as mensagens.
func (f Format) Label() string {
	switch f {
	case Square:
		return "quadrada"
	case Portrait:
		return "em pé (4:5)"
	case Vertical:
		return "vertical (tela cheia)"
	case Landscape:
		return "deitada"
	}
	return "fora do padrão"
}

type Info struct {
	Kind     string // image ou video
	Width    int
	Height   int
	Duration time.Duration
}

var ErrUnreadable = errors.New("media: não consegui ler o arquivo")

// Inspect lê dimensões (e duração, no vídeo) do arquivo.
func Inspect(mime string, data []byte) (Info, error) {
	if strings.HasPrefix(mime, "video/") {
		return inspectMP4(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	return Info{Kind: "image", Width: cfg.Width, Height: cfg.Height}, nil
}

func (i Info) Format() Format {
	if i.Width <= 0 || i.Height <= 0 {
		return Other
	}
	r := float64(i.Width) / float64(i.Height)
	switch {
	case r >= 0.9 && r <= 1.1:
		return Square
	case r >= 0.75 && r < 0.9:
		return Portrait
	case r >= 0.5 && r < 0.66:
		return Vertical
	case r >= 1.6 && r <= 2.0:
		return Landscape
	}
	return Other
}

// Verdict diz se o arquivo serve. Warning: serve, mas vale avisar. Reason: por que não serve.
type Verdict struct {
	OK      bool
	Warning string
	Reason  string
}

const (
	minSide         = 600
	sharpSide       = 1080
	maxVideo        = 240 * time.Second
	idealVideo      = 60 * time.Second
	minVideo        = time.Second
	extremeRatioMin = 0.45
	extremeRatioMax = 2.1
)

func Check(i Info) Verdict {
	short := min(i.Width, i.Height)
	r := float64(i.Width) / float64(max(i.Height, 1))
	if i.Kind == "video" {
		switch {
		case i.Duration < minVideo:
			return Verdict{Reason: "o vídeo é curto demais (menos de 1 segundo)"}
		case i.Duration > maxVideo:
			return Verdict{Reason: "o vídeo passa de 4 minutos; pra anúncio o ideal é até 60 segundos"}
		case r < extremeRatioMin || r > extremeRatioMax:
			return Verdict{Reason: "o formato do vídeo é muito comprido; o ideal é quadrado ou vertical (tela cheia)"}
		case i.Duration > idealVideo:
			return Verdict{OK: true, Warning: "o vídeo tem mais de 60 segundos: no feed funciona, mas nos Stories e Reels o ideal é até 60 segundos"}
		}
		return Verdict{OK: true}
	}
	switch {
	case short < minSide:
		return Verdict{Reason: fmt.Sprintf("a imagem é pequena demais (%dx%d); a Meta pede pelo menos 600 pixels no menor lado", i.Width, i.Height)}
	case r < extremeRatioMin || r > extremeRatioMax:
		return Verdict{Reason: "a imagem é comprida demais; o ideal é quadrada, em pé (4:5) ou vertical (tela cheia)"}
	case short < sharpSide:
		return Verdict{OK: true, Warning: "a imagem é um pouco pequena e pode sair menos nítida; se tiver uma maior (1080 pixels ou mais), manda também"}
	case i.Format() == Other:
		return Verdict{OK: true, Warning: "o formato foge do padrão; a Meta vai cortar um pouco as bordas"}
	}
	return Verdict{OK: true}
}

// ---------- MP4 / MOV: duração em mvhd, tamanho e rotação em tkhd ----------

func inspectMP4(data []byte) (Info, error) {
	moov, ok := findBox(data, "moov")
	if !ok {
		return Info{}, fmt.Errorf("%w: vídeo sem moov", ErrUnreadable)
	}
	info := Info{Kind: "video"}
	if mvhd, ok := findBox(moov, "mvhd"); ok && len(mvhd) >= 20 {
		var timescale, duration uint64
		if mvhd[0] == 1 && len(mvhd) >= 32 {
			timescale = uint64(binary.BigEndian.Uint32(mvhd[20:24]))
			duration = binary.BigEndian.Uint64(mvhd[24:32])
		} else {
			timescale = uint64(binary.BigEndian.Uint32(mvhd[12:16]))
			duration = uint64(binary.BigEndian.Uint32(mvhd[16:20]))
		}
		if timescale > 0 {
			info.Duration = time.Duration(duration * uint64(time.Second) / timescale)
		}
	}
	for _, trak := range allBoxes(moov, "trak") {
		tkhd, ok := findBox(trak, "tkhd")
		if !ok {
			continue
		}
		w, h, rot, ok := tkhdSize(tkhd)
		if ok && w > 0 && h > 0 {
			if rot {
				w, h = h, w
			}
			info.Width, info.Height = w, h
			break
		}
	}
	if info.Duration == 0 && info.Width == 0 {
		return Info{}, fmt.Errorf("%w: vídeo sem duração nem tamanho", ErrUnreadable)
	}
	return info, nil
}

// tkhdSize lê largura e altura (16.16) e se a matriz gira 90° ou 270°.
func tkhdSize(b []byte) (w, h int, rotated, ok bool) {
	off := 4 + 20 // versão/flags + campos da versão 0
	if b[0] == 1 {
		off = 4 + 32
	}
	off += 8 + 8 // reservado, camada, grupo, volume, reservado
	if len(b) < off+36+8 {
		return 0, 0, false, false
	}
	m := func(i int) int32 { return int32(binary.BigEndian.Uint32(b[off+4*i:])) }
	a, bb, c, d := m(0), m(1), m(3), m(4)
	rotated = a == 0 && d == 0 && ((bb == 0x10000 && c == -0x10000) || (bb == -0x10000 && c == 0x10000))
	w = int(binary.BigEndian.Uint32(b[off+36:]) >> 16)
	h = int(binary.BigEndian.Uint32(b[off+40:]) >> 16)
	return w, h, rotated, true
}

// findBox devolve o conteúdo da primeira caixa do tipo no nível atual.
func findBox(data []byte, kind string) ([]byte, bool) {
	boxes := allBoxes(data, kind)
	if len(boxes) == 0 {
		return nil, false
	}
	return boxes[0], true
}

func allBoxes(data []byte, kind string) [][]byte {
	var out [][]byte
	for i := 0; i+8 <= len(data); {
		size := uint64(binary.BigEndian.Uint32(data[i:]))
		typ := string(data[i+4 : i+8])
		hdr := uint64(8)
		switch size {
		case 1:
			if i+16 > len(data) {
				return out
			}
			size = binary.BigEndian.Uint64(data[i+8:])
			hdr = 16
		case 0:
			size = uint64(len(data) - i)
		}
		if size < hdr || uint64(i)+size > uint64(len(data)) {
			return out
		}
		if typ == kind {
			out = append(out, data[uint64(i)+hdr:uint64(i)+size])
		}
		i += int(size)
	}
	return out
}
