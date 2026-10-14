package paddle

import (
	"errors"
	"image"
	"math"
	"strings"

	"papergo/internal/ocr"
)

// Text lines are rectified directly from the original pixels, never from the
// detection preview, as PaperDotNet's PaddleTextCrop does.

const (
	maxCropPixels = 16_000_000
	maxCropAspect = 128
)

var errCropTooLarge = errors.New("a text line exceeds the OCR recognition memory bound; crop or split the page")

// homography maps (u, v) in a width×height rectangle onto a quad, the
// projective square-to-quad mapping (Heckbert).
type homography struct{ a, b, c, d, e, f, g, h float64 }

func rectangleToQuad(q ocr.Quad, width, height float64) (homography, error) {
	p := func(i int) (float64, float64) { return float64(q[i].X), float64(q[i].Y) }
	x0, y0 := p(0)
	x1, y1 := p(1)
	x2, y2 := p(2)
	x3, y3 := p(3)
	dx1, dx2, dx3 := x1-x2, x3-x2, x0-x1+x2-x3
	dy1, dy2, dy3 := y1-y2, y3-y2, y0-y1+y2-y3
	var g, h float64
	if dx3 != 0 || dy3 != 0 {
		den := dx1*dy2 - dx2*dy1
		if math.Abs(den) < 1e-9 {
			return homography{}, errors.New("a detected text region is degenerate")
		}
		g = (dx3*dy2 - dx2*dy3) / den
		h = (dx1*dy3 - dx3*dy1) / den
	}
	return homography{
		a: (x1 - x0 + g*x1) / width, b: (x3 - x0 + h*x3) / height, c: x0,
		d: (y1 - y0 + g*y1) / width, e: (y3 - y0 + h*y3) / height, f: y0,
		g: g / width, h: h / height,
	}, nil
}

func (m homography) apply(u, v float64) (float64, float64) {
	w := m.g*u + m.h*v + 1
	return (m.a*u + m.b*v + m.c) / w, (m.d*u + m.e*v + m.f) / w
}

// crop rectifies the text region q of src into an upright line image and
// returns the region's corners in the line's reading order. A region at
// least 1.5 times taller than wide is vertical text and is turned a quarter
// turn clockwise.
func crop(src *image.RGBA, q ocr.Quad) (*image.RGBA, ocr.Quad, error) {
	edge := func(a, b int) float64 {
		return math.Hypot(float64(q[a].X-q[b].X), float64(q[a].Y-q[b].Y))
	}
	w := max(1, int(math.Round(math.Max(edge(0, 1), edge(2, 3)))))
	h := max(1, int(math.Round(math.Max(edge(0, 3), edge(1, 2)))))
	if int64(w)*int64(h) > maxCropPixels || float64(max(w, h))/float64(min(w, h)) > maxCropAspect {
		return nil, q, errCropTooLarge
	}
	m, err := rectangleToQuad(q, float64(w), float64(h))
	if err != nil {
		return nil, q, err
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	for y := range h {
		for x := range w {
			sx, sy := m.apply(float64(x)+0.5, float64(y)+0.5)
			px := out.Pix[y*out.Stride+x*4:][:4]
			if sx < 0 || sy < 0 || sx > float64(sw) || sy > float64(sh) {
				px[0], px[1], px[2], px[3] = 255, 255, 255, 255
				continue
			}
			bilinear(src, sx-0.5, sy-0.5, px)
		}
	}
	if float64(h) < float64(w)*1.5 {
		return out, q, nil
	}
	return rotate90(out), ocr.Quad{q[3], q[0], q[1], q[2]}, nil
}

// bilinear samples src at pixel coordinates (x, y), clamping at the edges.
func bilinear(src *image.RGBA, x, y float64, out []uint8) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	clamp := func(v, hi int) int { return max(0, min(v, hi-1)) }
	xa, xb := clamp(x0, w), clamp(x0+1, w)
	ya, yb := clamp(y0, h), clamp(y0+1, h)
	p := func(x, y, c int) float64 { return float64(src.Pix[y*src.Stride+x*4+c]) }
	for c := range 4 {
		top := p(xa, ya, c)*(1-fx) + p(xb, ya, c)*fx
		bottom := p(xa, yb, c)*(1-fx) + p(xb, yb, c)*fx
		out[c] = uint8(math.Round(top*(1-fy) + bottom*fy))
	}
}

// words places the words of a recognized line on its region. Each character
// carries the CTC column it was read at; a word spans from half a column
// before its first character to one and a half after its last, as in
// PaperDotNet. Characters of scripts without spaces are words of their own.
func words(chars []string, columns []int, count int, q ocr.Quad) []ocr.Word {
	if len(chars) == 0 || len(columns) != len(chars) || count <= 0 {
		return nil
	}
	m, err := rectangleToQuad(q, 1, 1)
	if err != nil {
		return []ocr.Word{{Text: strings.Join(chars, ""), Quad: q}}
	}
	at := func(u, v float64) image.Point {
		x, y := m.apply(u, v)
		return image.Point{int(math.Round(x)), int(math.Round(y))}
	}
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }
	var out []ocr.Word
	for i := 0; i < len(chars); {
		if blank(chars[i]) {
			i++
			continue
		}
		start := i
		i++
		if !ideographic(chars[start]) {
			for i < len(chars) && !blank(chars[i]) && !ideographic(chars[i]) {
				i++
			}
		}
		left := math.Max(0, math.Min(1, (float64(columns[start])-0.5)/float64(count)))
		right := math.Max(left, math.Min(1, (float64(columns[i-1])+1.5)/float64(count)))
		out = append(out, ocr.Word{
			Text: strings.Join(chars[start:i], ""),
			Quad: ocr.Quad{at(left, 0), at(right, 0), at(right, 1), at(left, 1)},
		})
	}
	return out
}

// ideographic reports CJK and Hangul characters, which are written without
// spaces between words (PaperDotNet's ranges).
func ideographic(s string) bool {
	for _, r := range s {
		return r >= 0x2E80 && r <= 0x9FFF || r >= 0xAC00 && r <= 0xD7AF || r >= 0xF900 && r <= 0xFAFF || r >= 0x20000 && r <= 0x3134F
	}
	return false
}
