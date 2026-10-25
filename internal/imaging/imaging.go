// Package imaging decodes, orients, resizes and encodes the page images that
// document workflows process: static JPEG, PNG and WebP. It bounds decoded
// pixels before allocating them and applies EXIF orientation, so callers
// always work on upright images.
package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"

	"github.com/kolesa-team/go-webp/encoder"
	"github.com/kolesa-team/go-webp/webp"
	xdraw "golang.org/x/image/draw"

	"papergo/internal/storage"
)

// Errors decoding returns.
var (
	ErrUnsupported = errors.New("the file is not a JPEG, PNG or WebP image")
	ErrAnimated    = errors.New("animated images are kept unchanged")
	ErrTooLarge    = errors.New("the image has too many pixels")
)

// Mitchell is the Mitchell–Netravali cubic filter (B = C = 1/3), the
// resampler PaperDotNet uses for documents and OCR.
var Mitchell = &xdraw.Kernel{Support: 2, At: func(t float64) float64 {
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

// Image is a decoded, upright image.
type Image struct {
	image.Image
	// MediaType is the encoded file's type.
	MediaType string
	// Orientation is the EXIF orientation that was applied (1 is none).
	Orientation int
}

// Decode decodes a static JPEG, PNG or WebP file of at most maxPixels and
// turns it upright.
func Decode(data []byte, maxPixels int64) (Image, error) {
	media := storage.Signature(data[:min(len(data), 16)])
	if media != "image/jpeg" && media != "image/png" && media != "image/webp" {
		return Image{}, ErrUnsupported
	}
	if animated(data, media) {
		return Image{}, ErrAnimated
	}
	var cfg image.Config
	var err error
	switch media {
	case "image/webp":
		cfg, err = webp.DecodeConfig(bytes.NewReader(data), nil)
	case "image/png":
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	default:
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	}
	if err != nil {
		return Image{}, fmt.Errorf("the image cannot be decoded: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return Image{}, fmt.Errorf("%w: %d×%d is above %d", ErrTooLarge, cfg.Width, cfg.Height, maxPixels)
	}
	var img image.Image
	switch media {
	case "image/webp":
		img, err = webp.Decode(bytes.NewReader(data), nil)
	case "image/png":
		img, err = png.Decode(bytes.NewReader(data))
	default:
		img, err = jpeg.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return Image{}, fmt.Errorf("the image is malformed or incomplete: %w", err)
	}
	o := Orientation(data, media)
	return Image{Image: Orient(img, o), MediaType: media, Orientation: o}, nil
}

// animated reports animated PNG (an acTL chunk before IDAT; Go's PNG
// decoder would show only the default frame) and animated WebP (the
// animation flag of VP8X).
func animated(data []byte, media string) bool {
	switch media {
	case "image/png":
		for _, c := range pngChunks(data) {
			switch c.typ {
			case "acTL":
				return true
			case "IDAT", "IEND":
				return false
			}
		}
	case "image/webp":
		if len(data) >= 21 && string(data[12:16]) == "VP8X" {
			return data[20]&0x02 != 0
		}
	}
	return false
}

type chunk struct {
	typ  string
	data []byte
}

// pngChunks lists a PNG's chunks; it stops at malformed data.
func pngChunks(data []byte) []chunk {
	var out []chunk
	for at := 8; at+8 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[at:]))
		typ := string(data[at+4 : at+8])
		if n < 0 || at+12+n > len(data) {
			break
		}
		out = append(out, chunk{typ: typ, data: data[at+8 : at+8+n]})
		if typ == "IEND" {
			break
		}
		at += 12 + n
	}
	return out
}

// Orient turns img upright for EXIF orientation o (1 to 8); other values
// leave it unchanged.
func Orient(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	src := RGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := range h {
		row := src.Pix[y*src.Stride:]
		for x := range w {
			var dx, dy int
			switch o {
			case 2: // mirrored horizontally
				dx, dy = w-1-x, y
			case 3: // rotated 180°
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored vertically
				dx, dy = x, h-1-y
			case 5: // transposed
				dx, dy = y, x
			case 6: // rotated 90° clockwise to view
				dx, dy = h-1-y, x
			case 7: // transversed
				dx, dy = h-1-y, w-1-x
			case 8: // rotated 90° counter-clockwise to view
				dx, dy = y, w-1-x
			}
			copy(out.Pix[dy*out.Stride+dx*4:][:4], row[x*4:][:4])
		}
	}
	return out
}

// RGBA returns img as an RGBA image with its origin at zero.
func RGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}

// Flatten returns img as an opaque RGBA image, transparency flattened onto
// white.
func Flatten(img image.Image) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Over)
	return out
}

// Resize scales img to w×h with the Mitchell filter, flattening
// transparency onto white.
func Resize(img image.Image, w, h int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	Mitchell.Scale(out, out.Bounds(), img, img.Bounds(), xdraw.Over, nil)
	return out
}

// EncodeWebP encodes img as lossy WebP at quality 1 to 100.
func EncodeWebP(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	options, err := encoder.NewLossyEncoderOptions(encoder.PresetDefault, float32(quality))
	if err != nil {
		return nil, err
	}
	options.Method = 4
	if err := webp.Encode(&buf, img, options); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeJPEG encodes img as JPEG at quality 1 to 100.
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
