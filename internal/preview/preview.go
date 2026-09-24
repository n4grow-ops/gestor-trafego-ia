// Package preview desenha uma prévia ilustrativa de como o anúncio aparece no Instagram
// (post no feed e Stories), para o cliente conferir antes da publicação na Meta.
package preview

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"strings"
	"unicode"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Post é o anúncio a desenhar. Media nil = vídeo (sem quadro extraído: vira capa com play).
type Post struct {
	Account      string
	Headline     string
	Text         string
	CTA          string // WHATSAPP_MESSAGE, LEARN_MORE, CONTACT_US
	Media        image.Image
	Format       string // square, portrait, landscape, vertical, other
	VideoSeconds int
}

const width = 1080

var (
	ink     = color.RGBA{0x0c, 0x0c, 0x0d, 0xff}
	gray    = color.RGBA{0x73, 0x73, 0x73, 0xff}
	line    = color.RGBA{0xdb, 0xdb, 0xdb, 0xff}
	blue    = color.RGBA{0x00, 0x95, 0xf6, 0xff}
	white   = color.RGBA{0xff, 0xff, 0xff, 0xff}
	barBG   = color.RGBA{0xf3, 0xf5, 0xf7, 0xff}
	brand   = color.RGBA{0x1f, 0xd6, 0x88, 0xff}
	brand2  = color.RGBA{0x1a, 0x8c, 0xff, 0xff}
	videoBG = color.RGBA{0x16, 0x18, 0x1d, 0xff}
)

func CTALabel(code string) string {
	switch code {
	case "LEARN_MORE":
		return "Saiba mais"
	case "CONTACT_US":
		return "Fale conosco"
	}
	return "Enviar mensagem"
}

type faces struct {
	name, body, bold, small, cta, title font.Face
}

func loadFaces() (faces, error) {
	reg, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return faces{}, err
	}
	bld, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return faces{}, err
	}
	face := func(f *opentype.Font, size float64) font.Face {
		ff, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		return ff
	}
	return faces{name: face(bld, 38), body: face(reg, 38), bold: face(bld, 38), small: face(reg, 32), cta: face(bld, 38), title: face(bld, 48)}, nil
}

// Feed desenha o post patrocinado no feed e devolve um JPEG.
func Feed(p Post) ([]byte, error) {
	fc, err := loadFaces()
	if err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	mediaH := width // quadrado
	switch p.Format {
	case "portrait", "vertical":
		mediaH = 1350 // o feed mostra no máximo 4:5
	case "landscape":
		mediaH = 566 // 1,91:1
	}
	const header, cta, actions = 168, 124, 132
	name := handle(p.Account) + " "
	caption := wrap(fc.body, captionText(p), width-72, font.MeasureString(fc.bold, name).Round(), 4)
	h := header + mediaH + cta + actions + len(caption)*52 + 60 + 40
	img := image.NewRGBA(image.Rect(0, 0, width, h))
	fill(img, img.Bounds(), white)

	// cabeçalho: avatar, nome, "Patrocinado", menu
	avatar(img, fc, 36+48, header/2, 48, p.Account)
	text(img, fc.name, 36+96+26, header/2-6, ink, handle(p.Account))
	text(img, fc.small, 36+96+26, header/2+38, gray, "Patrocinado")
	for i := range 3 {
		circle(img, float64(width-60+i*16-16), float64(header/2), 5, ink)
	}

	// mídia
	area := image.Rect(0, header, width, header+mediaH)
	drawMedia(img, fc, area, p)

	// barra do botão
	bar := image.Rect(0, area.Max.Y, width, area.Max.Y+cta)
	fill(img, bar, barBG)
	fill(img, image.Rect(0, bar.Max.Y-2, width, bar.Max.Y), line)
	text(img, fc.cta, 36, bar.Min.Y+cta/2+13, blue, CTALabel(p.CTA))
	chevron(img, float64(width-52), float64(bar.Min.Y+cta/2), 14, blue, false)

	// ações: curtir, comentar, compartilhar, salvar
	y := float64(bar.Max.Y + actions/2)
	heart(img, 36+30, y, 30)
	comment(img, 36+30+100, y, 28)
	plane(img, 36+30+196, y, 28)
	bookmark(img, float64(width-36-24), y, 24)

	// legenda
	cy := bar.Max.Y + actions + 30
	for i, l := range caption {
		x := 36
		if i == 0 {
			x += text(img, fc.bold, x, cy, ink, name)
		}
		text(img, fc.body, x, cy, ink, l)
		cy += 52
	}
	return encode(img)
}

// Story desenha o anúncio nos Stories (tela cheia, 9:16) e devolve um JPEG.
func Story(p Post) ([]byte, error) {
	fc, err := loadFaces()
	if err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	const h = 1920
	img := image.NewRGBA(image.Rect(0, 0, width, h))
	drawMedia(img, fc, img.Bounds(), p)
	shade(img, 0, 320, 0.55, true)
	shade(img, h-520, h, 0.65, false)

	// barra de progresso e cabeçalho
	fill(img, image.Rect(24, 28, width-24, 34), color.RGBA{0xff, 0xff, 0xff, 0x80})
	fill(img, image.Rect(24, 28, 24+(width-48)/3, 34), white)
	avatar(img, fc, 24+42, 110, 42, p.Account)
	text(img, fc.name, 24+84+22, 104, white, handle(p.Account))
	text(img, fc.small, 24+84+22, 146, color.RGBA{0xee, 0xee, 0xee, 0xff}, "Patrocinado")

	// chamada: título + botão
	if hl := strings.TrimSpace(p.Headline); hl != "" {
		lines := wrap(fc.title, hl, width-160, 0, 2)
		y := h - 330 - (len(lines)-1)*60
		for _, l := range lines {
			w := font.MeasureString(fc.title, l).Round()
			text(img, fc.title, (width-w)/2, y, white, l)
			y += 60
		}
	}
	label := CTALabel(p.CTA)
	lw := font.MeasureString(fc.cta, label).Round()
	pill := image.Rect((width-lw)/2-56, h-230, (width+lw)/2+56, h-130)
	roundRect(img, pill, 50, white)
	text(img, fc.cta, (width-lw)/2, pill.Min.Y+63, ink, label)
	chevron(img, width/2, float64(pill.Min.Y-40), 16, white, true)
	return encode(img)
}

func captionText(p Post) string {
	hl, body := strings.TrimSpace(p.Headline), strings.TrimSpace(p.Text)
	switch {
	case hl != "" && body != "":
		return hl + " — " + body
	case hl != "":
		return hl
	}
	return body
}

// handle transforma o nome da conta no jeito do Instagram (@loja → loja).
func handle(account string) string {
	a := strings.TrimSpace(account)
	if i := strings.LastIndex(a, "instagram.com/"); i >= 0 {
		a = strings.Trim(a[i+len("instagram.com/"):], "/ ")
	}
	a = strings.TrimPrefix(a, "@")
	if strings.ContainsRune(a, ' ') { // nome do negócio vira usuário: "Loja da Ana" → "lojadaana"
		a = strings.Map(func(r rune) rune {
			if r = unaccent(unicode.ToLower(r)); unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' {
				return r
			}
			return -1
		}, a)
	}
	if a == "" {
		return "sua_empresa"
	}
	if r := []rune(a); len(r) > 28 {
		a = string(r[:27]) + "…"
	}
	return a
}

func unaccent(r rune) rune {
	if i := strings.IndexRune("áàâãäéèêëíìîïóòôõöúùûüçñ", r); i >= 0 {
		return []rune("aaaaaeeeeiiiiooooouuuucn")[len([]rune("áàâãäéèêëíìîïóòôõöúùûüçñ"[:i]))]
	}
	return r
}

func drawMedia(dst *image.RGBA, fc faces, area image.Rectangle, p Post) {
	if p.Media != nil {
		cover(dst, area, p.Media)
		return
	}
	// vídeo: capa escura com play e duração
	fill(dst, area, videoBG)
	grad(dst, area, brand2, brand)
	cx, cy := float64(area.Min.X+area.Dx()/2), float64(area.Min.Y+area.Dy()/2)
	circle(dst, cx, cy, 92, color.RGBA{0, 0, 0, 0x70})
	var r vector.Rasterizer
	r.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
	r.MoveTo(float32(cx-28), float32(cy-44))
	r.LineTo(float32(cx+46), float32(cy))
	r.LineTo(float32(cx-28), float32(cy+44))
	r.ClosePath()
	r.Draw(dst, dst.Bounds(), image.NewUniform(white), image.Point{})
	label := "Seu vídeo"
	if p.VideoSeconds > 0 {
		label = fmt.Sprintf("Seu vídeo · %d s", p.VideoSeconds)
	}
	w := font.MeasureString(fc.bold, label).Round()
	text(dst, fc.bold, int(cx)-w/2, int(cy)+160, white, label)
}

// cover escala a imagem para cobrir a área inteira, cortando o excesso no centro.
func cover(dst *image.RGBA, area image.Rectangle, src image.Image) {
	sb := src.Bounds()
	scale := math.Max(float64(area.Dx())/float64(sb.Dx()), float64(area.Dy())/float64(sb.Dy()))
	cw, ch := int(float64(area.Dx())/scale), int(float64(area.Dy())/scale)
	crop := image.Rect(sb.Min.X+(sb.Dx()-cw)/2, sb.Min.Y+(sb.Dy()-ch)/2, 0, 0)
	crop.Max = crop.Min.Add(image.Pt(cw, ch))
	xdraw.CatmullRom.Scale(dst, area, src, crop, xdraw.Src, nil)
}

// ---------- desenho básico ----------

func fill(dst *image.RGBA, r image.Rectangle, c color.Color) {
	xdraw.Draw(dst, r, image.NewUniform(c), image.Point{}, xdraw.Over)
}

func encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}
	return b.Bytes(), nil
}

// text escreve a partir da linha de base e devolve a largura usada. Caractere sem desenho na fonte (emoji) é pulado.
func text(dst *image.RGBA, f font.Face, x, y int, c color.Color, s string) int {
	s = printable(f, s)
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
	return (d.Dot.X - fixed.I(x)).Round()
}

func printable(f font.Face, s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if _, ok := f.GlyphAdvance(r); !ok || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// wrap quebra o texto em linhas (a primeira começa depois de indent px); a última termina em "… mais" quando sobra texto.
func wrap(f font.Face, s string, maxW, indent, maxLines int) []string {
	words := strings.Fields(printable(f, s))
	var lines []string
	cur := ""
	room := func() int {
		if len(lines) == 0 {
			return maxW - indent
		}
		return maxW
	}
	for _, w := range words {
		try := strings.TrimSpace(cur + " " + w)
		if font.MeasureString(f, try).Round() <= room() || cur == "" {
			cur = try
			continue
		}
		lines = append(lines, cur)
		cur = w
		if len(lines) == maxLines {
			last := lines[maxLines-1]
			limit := maxW
			if maxLines == 1 {
				limit -= indent
			}
			for font.MeasureString(f, last+"… mais").Round() > limit && strings.Contains(last, " ") {
				last = last[:strings.LastIndex(last, " ")]
			}
			lines[maxLines-1] = last + "… mais"
			return lines
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func circle(dst *image.RGBA, cx, cy, r float64, c color.Color) {
	var ras vector.Rasterizer
	ras.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
	addCircle(&ras, cx, cy, r)
	ras.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
}

func addCircle(r *vector.Rasterizer, cx, cy, rad float64) {
	const k = 0.5522847498
	p := func(x, y float64) (float32, float32) { return float32(cx + x*rad), float32(cy + y*rad) }
	x, y := p(1, 0)
	r.MoveTo(x, y)
	seg := [][6]float64{{1, k, k, 1, 0, 1}, {-k, 1, -1, k, -1, 0}, {-1, -k, -k, -1, 0, -1}, {k, -1, 1, -k, 1, 0}}
	for _, s := range seg {
		ax, ay := p(s[0], s[1])
		bx, by := p(s[2], s[3])
		ex, ey := p(s[4], s[5])
		r.CubeTo(ax, ay, bx, by, ex, ey)
	}
	r.ClosePath()
}

func roundRect(dst *image.RGBA, rc image.Rectangle, rad float64, c color.Color) {
	var r vector.Rasterizer
	r.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
	x0, y0, x1, y1 := float32(rc.Min.X), float32(rc.Min.Y), float32(rc.Max.X), float32(rc.Max.Y)
	q := float32(rad)
	r.MoveTo(x0+q, y0)
	r.LineTo(x1-q, y0)
	r.QuadTo(x1, y0, x1, y0+q)
	r.LineTo(x1, y1-q)
	r.QuadTo(x1, y1, x1-q, y1)
	r.LineTo(x0+q, y1)
	r.QuadTo(x0, y1, x0, y1-q)
	r.LineTo(x0, y0+q)
	r.QuadTo(x0, y0, x0+q, y0)
	r.ClosePath()
	r.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
}

// avatar: anel no gradiente da marca e a inicial do negócio.
func avatar(dst *image.RGBA, fc faces, cx, cy, r int, account string) {
	x, y, rad := float64(cx), float64(cy), float64(r)
	circle(dst, x, y, rad, brand)
	circle(dst, x, y, rad-5, white)
	circle(dst, x, y, rad-9, brand2)
	initial := "M"
	for _, ch := range handle(account) {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) {
			initial = strings.ToUpper(string(ch))
			break
		}
	}
	w := font.MeasureString(fc.bold, initial).Round()
	text(dst, fc.bold, cx-w/2, cy+13, white, initial)
}

func chevron(dst *image.RGBA, x, y, s float64, c color.Color, up bool) {
	var r vector.Rasterizer
	r.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
	t := s * 0.32
	if up {
		r.MoveTo(float32(x-s), float32(y+s/2))
		r.LineTo(float32(x), float32(y-s/2))
		r.LineTo(float32(x+s), float32(y+s/2))
		r.LineTo(float32(x+s), float32(y+s/2+t))
		r.LineTo(float32(x), float32(y-s/2+t))
		r.LineTo(float32(x-s), float32(y+s/2+t))
	} else {
		r.MoveTo(float32(x-s/2), float32(y-s))
		r.LineTo(float32(x+s/2), float32(y))
		r.LineTo(float32(x-s/2), float32(y+s))
		r.LineTo(float32(x-s/2-t), float32(y+s))
		r.LineTo(float32(x+s/2-t), float32(y))
		r.LineTo(float32(x-s/2-t), float32(y-s))
	}
	r.ClosePath()
	r.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
}

// Ícones em contorno: forma cheia escura e a mesma forma menor em branco por cima.
func heart(dst *image.RGBA, cx, cy, s float64) {
	shape := func(k float64, c color.Color) {
		var r vector.Rasterizer
		r.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
		p := func(x, y float64) (float32, float32) { return float32(cx + x*s*k), float32(cy + y*s*k) }
		x, y := p(0, 0.95)
		r.MoveTo(x, y)
		a, b := p(-1.1, 0.2)
		c1, d := p(-1.2, -0.9)
		e, f := p(0, -0.45)
		r.CubeTo(a, b, c1, d, e, f)
		a, b = p(1.2, -0.9)
		c1, d = p(1.1, 0.2)
		e, f = p(0, 0.95)
		r.CubeTo(a, b, c1, d, e, f)
		r.ClosePath()
		r.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
	}
	shape(1, ink)
	shape(0.8, white)
}

func comment(dst *image.RGBA, cx, cy, s float64) {
	var r vector.Rasterizer
	r.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
	addCircle(&r, cx, cy, s)
	r.MoveTo(float32(cx-s*0.9), float32(cy+s*0.3))
	r.LineTo(float32(cx-s*1.05), float32(cy+s*1.05))
	r.LineTo(float32(cx-s*0.3), float32(cy+s*0.9))
	r.ClosePath()
	r.Draw(dst, dst.Bounds(), image.NewUniform(ink), image.Point{})
	circle(dst, cx, cy, s*0.8, white)
}

func plane(dst *image.RGBA, cx, cy, s float64) {
	tri := func(k float64, c color.Color) {
		var r vector.Rasterizer
		r.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
		p := func(x, y float64) (float32, float32) { return float32(cx + x*s*k), float32(cy + y*s*k) }
		x, y := p(-1.1, -0.15)
		r.MoveTo(x, y)
		x, y = p(1.1, -1.0)
		r.LineTo(x, y)
		x, y = p(0.2, 1.1)
		r.LineTo(x, y)
		r.ClosePath()
		r.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
	}
	tri(1, ink)
	tri(0.72, white)
}

func bookmark(dst *image.RGBA, cx, cy, s float64) {
	shape := func(k float64, c color.Color) {
		var r vector.Rasterizer
		r.Reset(dst.Bounds().Dx(), dst.Bounds().Dy())
		p := func(x, y float64) (float32, float32) { return float32(cx + x*s*k), float32(cy + y*s*k) }
		pts := [][2]float64{{-0.8, -1.1}, {0.8, -1.1}, {0.8, 1.1}, {0, 0.45}, {-0.8, 1.1}}
		x, y := p(pts[0][0], pts[0][1])
		r.MoveTo(x, y)
		for _, pt := range pts[1:] {
			x, y = p(pt[0], pt[1])
			r.LineTo(x, y)
		}
		r.ClosePath()
		r.Draw(dst, dst.Bounds(), image.NewUniform(c), image.Point{})
	}
	shape(1, ink)
	shape(0.72, white)
}

// shade escurece uma faixa em degradê para o texto branco ler bem sobre a foto.
func shade(dst *image.RGBA, y0, y1 int, max float64, fromTop bool) {
	for y := y0; y < y1; y++ {
		t := float64(y-y0) / float64(y1-y0)
		if fromTop {
			t = 1 - t
		}
		a := uint8(max * t * 255)
		fill(dst, image.Rect(0, y, dst.Bounds().Dx(), y+1), color.RGBA{0, 0, 0, a})
	}
}

// grad pinta um degradê diagonal suave entre duas cores, bem escurecido, como capa do vídeo.
func grad(dst *image.RGBA, area image.Rectangle, a, b color.RGBA) {
	for y := area.Min.Y; y < area.Max.Y; y++ {
		t := float64(y-area.Min.Y) / float64(max(area.Dy(), 1))
		c := color.RGBA{
			uint8((float64(a.R)*(1-t) + float64(b.R)*t) * 0.35),
			uint8((float64(a.G)*(1-t) + float64(b.G)*t) * 0.35),
			uint8((float64(a.B)*(1-t) + float64(b.B)*t) * 0.35), 0xff}
		fill(dst, image.Rect(area.Min.X, y, area.Max.X, y+1), c)
	}
}
