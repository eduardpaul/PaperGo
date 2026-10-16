package preview

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"
	"time"

	"golang.org/x/image/tiff"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
	"papergo/internal/imaging"
)

func TestTwentyMegabyteImageProducesBoundedWebP(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2700, 2700))
	state := uint32(42)
	for i := range img.Pix {
		state = state*1664525 + 1013904223
		img.Pix[i] = byte(state >> 24)
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	var input bytes.Buffer
	if err := png.Encode(&input, img); err != nil {
		t.Fatal(err)
	}
	if input.Len() < 20_000_000 {
		t.Fatal("fixture must exercise a large original", input.Len())
	}
	start := time.Now()
	out, err := Generate(context.Background(), input.Bytes(), "image/png", nil, 10_000_000)
	if err != nil || len(out.WebP) >= 100_000 || max(out.Width, out.Height) > 800 {
		t.Fatal(len(out.WebP), out.Width, out.Height, err)
	}
	decoded, err := imaging.Decode(out.WebP, 800*800)
	if err != nil || decoded.Bounds().Dx() != out.Width || decoded.Bounds().Dy() != out.Height {
		t.Fatal(err)
	}
	t.Logf("%d-byte original to %d-byte preview in %s", input.Len(), len(out.WebP), time.Since(start))
	if _, err = Generate(context.Background(), input.Bytes(), "image/png", nil, 100); err == nil {
		t.Fatal("pixel bound ignored")
	}
}
func TestPDFPreviewRendersOnlyTheFirstPageAndTIFFIsSupported(t *testing.T) {
	pdf := pdfium.New()
	ctx := context.Background()
	one, err := Generate(ctx, doctexttest.TextPDF("First page"), "application/pdf", pdf, 1000000)
	if err != nil {
		t.Fatal(err)
	}
	many, err := Generate(ctx, doctexttest.TextPDF("First page", "Second page"), "application/pdf", pdf, 1000000)
	if err != nil || !bytes.Equal(one.WebP, many.WebP) {
		t.Fatal("preview did not use first page", err)
	}
	var data bytes.Buffer
	if err = tiff.Encode(&data, image.NewRGBA(image.Rect(0, 0, 100, 50)), nil); err != nil {
		t.Fatal(err)
	}
	scan, err := Generate(ctx, data.Bytes(), "image/tiff", nil, 10000)
	if err != nil || scan.Width != 100 || scan.Height != 50 {
		t.Fatal(scan, err)
	}
	if _, err = Generate(ctx, []byte("%PDF-broken"), "application/pdf", pdf, 10000); err == nil {
		t.Fatal("broken PDF accepted")
	}
}
