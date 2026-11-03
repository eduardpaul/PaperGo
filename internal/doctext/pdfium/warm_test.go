package pdfium_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/klippa-app/go-pdfium"
	"papergo/internal/doctext"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
)

// Opens while PDFium warms up wait for that start instead of starting
// their own, and later warm-ups reuse it.
func TestWarmSharesTheStart(t *testing.T) {
	reader := pdfium.New(pdfium.Options{CacheDir: doctexttest.CacheDir(t)})
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
	if cache, err := reader.Warm(); err != nil || cache == pdfium.CacheOff {
		t.Fatal(cache, err)
	}
	if n := pdfium.Starts(reader); n != 1 {
		t.Fatalf("PDFium started %d times", n)
	}
}

// An open waiting for PDFium to start gives up with its context, and the
// start goes on for the next one.
func TestOpenWaitingForTheStartEndsWithItsContext(t *testing.T) {
	gate := make(chan struct{})
	pdfium.ReplaceStarts(t, time.Hour, func(dir string, real func(string) (sdk.Pool, error)) (sdk.Pool, error) {
		<-gate
		return real(dir)
	})
	reader := pdfium.New(pdfium.Options{CacheDir: doctexttest.CacheDir(t)})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := reader.Open(ctx, doctexttest.TextPDF("Late")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(gate)
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Late"))
	if err != nil {
		t.Fatal(err)
	}
	_ = doc.Close()
	if n := pdfium.Starts(reader); n != 1 {
		t.Fatalf("PDFium started %d times", n)
	}
}

// A failed start answers opens as unavailable, not as unreadable files,
// and is tried again only after a while.
func TestFailedStartIsRetried(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	pdfium.ReplaceStarts(t, time.Hour, func(dir string, real func(string) (sdk.Pool, error)) (sdk.Pool, error) {
		if failing.Load() {
			return nil, errors.New("no PDFium")
		}
		return real(dir)
	})
	// Without a cache: a failed start with one clears it.
	reader := pdfium.New(pdfium.Options{})
	for range 2 {
		if _, err := reader.Open(context.Background(), doctexttest.TextPDF("Retry")); !errors.Is(err, doctext.ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if _, err := reader.Warm(); !errors.Is(err, doctext.ErrUnavailable) {
		t.Fatal(err)
	}
	if n := pdfium.Starts(reader); n != 1 {
		t.Fatalf("a failed start was retried at once: %d starts", n)
	}
	failing.Store(false)
	pdfium.AgeStart(reader)
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Retry"))
	if err != nil {
		t.Fatal(err)
	}
	_ = doc.Close()
	if n := pdfium.Starts(reader); n != 2 {
		t.Fatalf("PDFium started %d times", n)
	}
}
