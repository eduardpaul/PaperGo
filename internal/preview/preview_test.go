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
	"papergo/internal/imaging"
)

func TestTwentyMegabyteImageProducesBoundedJPEG(t *testing.T) {
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
	if err != nil || len(out.JPEG) >= 100_000 || max(out.Width, out.Height) > 800 {
		t.Fatal(len(out.JPEG), out.Width, out.Height, err)
	}
	decoded, err := imaging.Decode(out.JPEG, 800*800)
	if err != nil || decoded.MediaType != MediaType || decoded.Bounds().Dx() != out.Width || decoded.Bounds().Dy() != out.Height {
		t.Fatal(err)
	}
	t.Logf("%d-byte original to %d-byte preview in %s", input.Len(), len(out.JPEG), time.Since(start))
	if _, err = Generate(context.Background(), input.Bytes(), "image/png", nil, 100); err == nil {
		t.Fatal("pixel bound ignored")
	}
}
func TestPDFPreviewRendersOnlyTheFirstPageAndTIFFIsSupported(t *testing.T) {
	pdf := doctexttest.PDF(t)
	ctx := context.Background()
	one, err := Generate(ctx, doctexttest.TextPDF("First page"), "application/pdf", pdf, 1000000)
	if err != nil {
		t.Fatal(err)
	}
	many, err := Generate(ctx, doctexttest.TextPDF("First page", "Second page"), "application/pdf", pdf, 1000000)
	if err != nil || !bytes.Equal(one.JPEG, many.JPEG) {
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

func TestTransparencyIsFlattenedOntoWhite(t *testing.T) {
	var input bytes.Buffer
	if err := png.Encode(&input, image.NewNRGBA(image.Rect(0, 0, 40, 20))); err != nil {
		t.Fatal(err)
	}
	out, err := Generate(context.Background(), input.Bytes(), "image/png", nil, 10000)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := imaging.Decode(out.JPEG, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := decoded.At(20, 10).RGBA(); r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Fatalf("transparent pixel became %d, %d, %d", r>>8, g>>8, b>>8)
	}
}
