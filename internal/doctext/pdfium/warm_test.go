package pdfium_test

import (
	"context"
	"sync"
	"testing"

	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
)

// Opens while PDFium warms up wait for that start instead of starting
// their own, and later warm-ups reuse it.
func TestWarmSharesTheStart(t *testing.T) {
	reader := pdfium.New(pdfium.Options{})
	warmed := make(chan error, 1)
	go func() { _, err := reader.Warm(); warmed <- err }()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Warm"))
			if err != nil {
				t.Error(err)
				return
			}
			defer doc.Close()
			if text, err := doc.Text(0); err != nil || text != "Warm" {
				t.Errorf("%q %v", text, err)
			}
		})
	}
	wg.Wait()
	if err := <-warmed; err != nil {
		t.Fatal(err)
	}
	if cache, err := reader.Warm(); err != nil || cache != pdfium.CacheOff {
		t.Fatal(cache, err)
	}
	if n := pdfium.Starts(reader); n != 1 {
		t.Fatalf("PDFium started %d times", n)
	}
}
