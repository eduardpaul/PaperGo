package paddle

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
)

// mitchell is the Mitchell–Netravali cubic (B = C = 1/3), the resampler the
// PaddleOCR models were tuned with in PaperDotNet (Skia's Mitchell filter).
var mitchell = &xdraw.Kernel{Support: 2, At: func(t float64) float64 {
	const b, c = 1.0 / 3, 1.0 / 3
	t = math.Abs(t)
	switch {
	case t < 1:
		return ((12-9*b-6*c)*t*t*t + (-18+12*b+6*c)*t*t + (6 - 2*b)) / 6
	case t < 2:
		return ((-b-6*c)*t*t*t + (6*b+30*c)*t*t + (-12*b-48*c)*t + (8*b + 24*c)) / 6
	}
	return 0
}}

// opaque returns img as RGBA with transparency flattened onto white, the
// background the models expect.
func opaque(img image.Image) *image.RGBA {
	b := img.Bounds()
	out := filled(b.Dx(), b.Dy(), color.White)
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Over)
	return out
}

// filled returns a w×h image of one color.
func filled(w, h int, c color.Color) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return out
}

// resize scales src to w×h with the Mitchell filter onto background.
func resize(src image.Image, w, h int, background color.Color) *image.RGBA {
	out := filled(w, h, background)
	mitchell.Scale(out, out.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return out
}

// tensor packs img as a 1×3×H×W tensor in BGR channel order, the order
// PaddleOCR was trained with: (value - mean[c]) / std[c] for channel c.
func tensor(img *image.RGBA, mean, std [3]float32) ([]float32, []int64) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	plane := w * h
	out := make([]float32, 3*plane)
	for y := range h {
		row := img.Pix[y*img.Stride:]
		for x := range w {
			p := row[x*4:]
			i := y*w + x
			// RGBA bytes; channel 0 is blue.
			out[i] = (float32(p[2]) - mean[0]) / std[0]
			out[plane+i] = (float32(p[1]) - mean[1]) / std[1]
			out[2*plane+i] = (float32(p[0]) - mean[2]) / std[2]
		}
	}
	return out, []int64{1, 3, int64(h), int64(w)}
}

// rotate90 turns img a quarter turn clockwise.
func rotate90(img *image.RGBA) *image.RGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := range h {
		for x := range w {
			copy(out.Pix[x*out.Stride+(h-1-y)*4:][:4], img.Pix[y*img.Stride+x*4:][:4])
		}
	}
	return out
}

// rotate180 turns img upside down.
func rotate180(img *image.RGBA) *image.RGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			copy(out.Pix[(h-1-y)*out.Stride+(w-1-x)*4:][:4], img.Pix[y*img.Stride+x*4:][:4])
		}
	}
	return out
}
