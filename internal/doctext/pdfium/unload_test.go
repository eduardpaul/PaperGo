package pdfium_test

import (
	"context"
	"testing"
	"time"

	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
)

// waitUnloaded waits until the reader has PDFium unloaded.
func waitUnloaded(t *testing.T, reader *pdfium.Reader) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); pdfium.Loaded(reader); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("PDFium stayed loaded")
		}
	}
}

// A warm-up from the cache leaves nothing loaded, and the next open loads
// PDFium from the cache again.
func TestWarmUnloadsAStartFromTheCache(t *testing.T) {
	reader := pdfium.New(pdfium.Options{CacheDir: doctexttest.CacheDir(t)})
	if cache, err := reader.Warm(); err != nil || cache == pdfium.CacheOff {
		t.Fatal(cache, err)
	}
	if pdfium.Loaded(reader) {
		t.Fatal("the warm-up kept PDFium loaded")
	}
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Again"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if text, err := doc.Text(0); err != nil || text != "Again" {
		t.Fatalf("%q %v", text, err)
	}
	if n := pdfium.Starts(reader); n != 2 {
		t.Fatalf("PDFium started %d times", n)
	}
}

// PDFium unloads once its last PDF has been closed for a while, never
// while one is open, and loads again for the next.
func TestIdleUnloadsAfterTheLastClose(t *testing.T) {
	pdfium.UnloadAfter(t, 20*time.Millisecond)
	reader := pdfium.New(pdfium.Options{CacheDir: doctexttest.CacheDir(t)})
	first, err := reader.Open(context.Background(), doctexttest.TextPDF("One"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.Open(context.Background(), doctexttest.TextPDF("Two"))
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	time.Sleep(100 * time.Millisecond)
	if !pdfium.Loaded(reader) {
		t.Fatal("PDFium unloaded while a PDF was open")
	}
	if text, err := second.Text(0); err != nil || text != "Two" {
		t.Fatalf("%q %v", text, err)
	}
	_ = second.Close()
	waitUnloaded(t, reader)
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Three"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if text, err := doc.Text(0); err != nil || text != "Three" {
		t.Fatalf("%q %v", text, err)
	}
	if n := pdfium.Starts(reader); n != 2 {
		t.Fatalf("PDFium started %d times", n)
	}
}

// An open before the idle time ends keeps PDFium loaded.
func TestOpenKeepsPDFiumLoaded(t *testing.T) {
	pdfium.UnloadAfter(t, time.Hour)
	reader := pdfium.New(pdfium.Options{CacheDir: doctexttest.CacheDir(t)})
	for _, text := range []string{"One", "Two", "Three"} {
		doc, err := reader.Open(context.Background(), doctexttest.TextPDF(text))
		if err != nil {
			t.Fatal(err)
		}
		_ = doc.Close()
	}
	if !pdfium.Loaded(reader) || pdfium.Starts(reader) != 1 || pdfium.Users(reader) != 0 {
		t.Fatal(pdfium.Loaded(reader), pdfium.Starts(reader), pdfium.Users(reader))
	}
}

// A start that compiled without a cache would compile again after an
// unload, so it stays loaded.
func TestStartWithoutACacheStays(t *testing.T) {
	pdfium.UnloadAfter(t, time.Millisecond)
	reader := pdfium.New(pdfium.Options{})
	if cache, err := reader.Warm(); err != nil || cache != pdfium.CacheOff {
		t.Fatal(cache, err)
	}
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Stays"))
	if err != nil {
		t.Fatal(err)
	}
	_ = doc.Close()
	time.Sleep(50 * time.Millisecond)
	if !pdfium.Loaded(reader) || pdfium.Starts(reader) != 1 {
		t.Fatal(pdfium.Loaded(reader), pdfium.Starts(reader))
	}
}
