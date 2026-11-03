// Package preview renders the first page of a document into a bounded JPEG.
package preview

import (
	"context"
	"errors"
	"image"
	"math"
	"slices"

	"papergo/internal/doctext"
	"papergo/internal/imaging"
)

const MaxDimension = 800
const MaxBytes = 100_000

// MediaType is the type of the images Generate renders.
const MediaType = "image/jpeg"

// qualities are the JPEG qualities Generate tries, best first, before it
// shrinks an image that does not fit MaxBytes.
var qualities = []int{75, 60, 45, 30}

type Result struct {
	JPEG          []byte
	Width, Height int
}

// Media are the types Generate renders.
var Media = []string{"application/pdf", "image/tiff", "image/jpeg", "image/png", "image/webp"}

// Supported reports whether Generate renders files of media.
func Supported(media string) bool { return slices.Contains(Media, media) }

// Generate renders the first page of a file of media as a JPEG of at most
// MaxDimension pixels on its longest side and fewer than MaxBytes, never
// enlarged. Transparency is flattened onto white. It lowers the quality,
// then the size, until the JPEG fits.
func Generate(ctx context.Context, data []byte, media string, pdf doctext.PDF, maxPixels int64) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var img image.Image
	var err error
	switch media {
	case "application/pdf":
		if pdf == nil {
			return Result{}, errors.New("PDF rendering is unavailable")
		}
		doc, e := pdf.Open(ctx, data)
		if e != nil {
			return Result{}, e
		}
		defer doc.Close()
		if doc.Pages() < 1 {
			return Result{}, errors.New("the PDF has no pages")
		}
		img, err = doc.Render(0, 100, int64(MaxDimension*MaxDimension))
	case "image/tiff":
		var doc *imaging.TIFF
		if doc, err = imaging.OpenTIFF(data); err == nil {
			img, err = doc.Decode(0, maxPixels)
		}
	default:
		var decoded imaging.Image
		decoded, err = imaging.Decode(data, maxPixels)
		img = decoded.Image
	}
	if err != nil {
		return Result{}, err
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	scale := math.Min(1, float64(MaxDimension)/float64(max(width, height)))
	width, height = max(1, int(float64(width)*scale)), max(1, int(float64(height)*scale))
	// Resize also flattens transparency onto white, as JPEG has no alpha.
	img = imaging.Resize(img, width, height)
	for {
		for _, quality := range qualities {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			encoded, e := imaging.EncodeJPEG(img, quality)
			if e != nil {
				return Result{}, e
			}
			if len(encoded) < MaxBytes {
				return Result{encoded, width, height}, nil
			}
		}
		if max(width, height) <= 100 {
			return Result{}, errors.New("preview cannot meet its byte bound")
		}
		width, height = max(1, width*3/4), max(1, height*3/4)
		img = imaging.Resize(img, width, height)
	}
}
