// Package preview renders the first page of a document into a bounded WebP.
package preview

import (
	"bytes"
	"context"
	"errors"
	"image"
	"math"

	_ "golang.org/x/image/tiff"
	"papergo/internal/doctext"
	"papergo/internal/imaging"
)

const MaxDimension = 800
const MaxBytes = 100_000

type Result struct {
	WebP          []byte
	Width, Height int
}

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
		cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil {
			return Result{}, e
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
			return Result{}, imaging.ErrTooLarge
		}
		img, _, err = image.Decode(bytes.NewReader(data))
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
	img = imaging.Resize(img, width, height)
	for {
		for _, quality := range []int{75, 60, 45, 30} {
			if err = ctx.Err(); err != nil {
				return Result{}, err
			}
			encoded, e := imaging.EncodeWebP(img, quality)
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
