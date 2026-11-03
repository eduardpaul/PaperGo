package pdfium_test

import (
	"context"
	"image"
	"image/color"
	"runtime/debug"
	"sync"
	"testing"

	"papergo/internal/doctext/doctexttest"
)

func TestReadAndRender(t *testing.T) {
	reader := doctexttest.PDF(t)
	// The name keys stored text: it names the PDFium build go-pdfium embeds,
	// so it changes with go-pdfium.
	if reader.Name() != "pdfium-8111-wasm" {
		t.Fatal(reader.Name())
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, m := range info.Deps {
			if m.Path == "github.com/klippa-app/go-pdfium" && m.Version != "v1.21.4" {
				t.Fatalf("go-pdfium is %s: update Name to the PDFium build its module embeds, and startPool's runtime features to its default runtime's", m.Version)
			}
		}
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
	if w, h, err := doc.Size(1); err != nil || w != 612 || h != 792 {
		t.Fatalf("size: %v×%v %v", w, h, err)
	}
	if _, _, err = doc.Size(2); err == nil {
		t.Fatal("the size of a page past the end")
	}
	// Letter size at 100 DPI, and lowered to stay within the pixel bound.
	img, err := doc.Render(0, 100, 64_000_000)
	if err != nil || img.Bounds().Dx() != 850 || img.Bounds().Dy() != 1100 {
		t.Fatalf("render: %v %v", img.Bounds(), err)
	}
	ink := inked(img)
	if ink == 0 || img.At(800, 1000) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("the text is not drawn on white: %d dark pixels", ink)
	}
	bounded, err := doc.Render(0, 300, 1_000_000)
	if err != nil || bounded.Bounds().Dx()*bounded.Bounds().Dy() > 1_000_000 {
		t.Fatalf("bounded render: %v %v", bounded.Bounds(), err)
	}
	// Renders outlive later renders and the document.
	if err = doc.Close(); err != nil {
		t.Fatal(err)
	}
	if inked(img) != ink || img.At(800, 1000) != (color.RGBA{255, 255, 255, 255}) {
		t.Fatal("the render changed after the document closed")
	}
	if _, err = doc.Text(0); err == nil {
		t.Fatal("a closed PDF read")
	}
}

func inked(img image.Image) int {
	ink := 0
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			if r, _, _, _ := img.At(x, y).RGBA(); r < 0x8000 {
				ink++
			}
		}
	}
	return ink
}

// PDFs open and read from many goroutines at once.
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
