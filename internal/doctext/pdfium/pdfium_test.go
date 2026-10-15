package pdfium_test

import (
	"context"
	"image/color"
	"strings"
	"sync"
	"testing"

	"papergo/internal/doctext/doctexttest"
)

func TestReadAndRender(t *testing.T) {
	reader := doctexttest.PDF(t)
	if !strings.HasPrefix(reader.Name(), "pdfium-") {
		t.Fatal(reader.Name())
	}
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Invoice March", "Zweite Seite"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if doc.Pages() != 2 {
		t.Fatal(doc.Pages())
	}
	if text, err := doc.Text(1); err != nil || text != "Zweite Seite" {
		t.Fatalf("%q %v", text, err)
	}
	if _, err = doc.Text(2); err == nil {
		t.Fatal("a page past the end")
	}
	// Letter size at 100 DPI, and lowered to stay within the pixel bound.
	img, err := doc.Render(0, 100, 64_000_000)
	if err != nil || img.Bounds().Dx() != 850 || img.Bounds().Dy() != 1100 {
		t.Fatalf("render: %v %v", img.Bounds(), err)
	}
	ink := 0
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			if r, _, _, _ := img.At(x, y).RGBA(); r < 0x8000 {
				ink++
			}
		}
	}
	if ink == 0 || img.At(800, 1000) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("the text is not drawn on white: %d dark pixels", ink)
	}
	if img, err = doc.Render(0, 300, 1_000_000); err != nil || img.Bounds().Dx()*img.Bounds().Dy() > 1_000_000 {
		t.Fatalf("bounded render: %v %v", img.Bounds(), err)
	}
}

// Calls from many goroutines are serialized, not crashed.
func TestConcurrentReads(t *testing.T) {
	reader := doctexttest.PDF(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Hello"))
			if err != nil {
				t.Error(err)
				return
			}
			defer doc.Close()
			if text, err := doc.Text(0); err != nil || text != "Hello" {
				t.Errorf("%q %v", text, err)
			}
		})
	}
	wg.Wait()
	if _, err := reader.Open(context.Background(), []byte("%PDF-1.4 broken")); err == nil {
		t.Fatal("a broken PDF opened")
	}
}
