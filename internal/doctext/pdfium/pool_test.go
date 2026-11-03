package pdfium_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"papergo/internal/doctext"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
)

// shared is the test binary's reader, so these tests compile PDFium once.
func shared(t *testing.T) *pdfium.Reader { return doctexttest.PDF(t).(*pdfium.Reader) }

// holdAll opens as many PDFs as the reader allows and closes them at the
// end of the test.
func holdAll(t *testing.T, reader *pdfium.Reader, data []byte) []doctext.Document {
	t.Helper()
	open := make([]doctext.Document, 0, pdfium.MaxOpen)
	t.Cleanup(func() {
		for _, doc := range open {
			_ = doc.Close()
		}
	})
	for range pdfium.MaxOpen {
		doc, err := reader.Open(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, doc)
	}
	if pdfium.Held(reader) != pdfium.MaxOpen {
		t.Fatalf("%d of %d open", pdfium.Held(reader), pdfium.MaxOpen)
	}
	return open
}

// An open past the bound waits until a PDF closes, and then proceeds.
func TestOpenWaitsForAClose(t *testing.T) {
	reader, data := shared(t), doctexttest.TextPDF("Held")
	open := holdAll(t, reader, data)
	type result struct {
		doc doctext.Document
		err error
	}
	opened := make(chan result, 1)
	go func() {
		doc, err := reader.Open(context.Background(), data)
		opened <- result{doc, err}
	}()
	for pdfium.Waiting(reader) == 0 {
		time.Sleep(time.Millisecond)
	}
	// It waits for a slot while every slot is held, so it has not opened.
	select {
	case r := <-opened:
		t.Fatalf("opened past the bound: %v", r.err)
	default:
	}
	if err := open[0].Close(); err != nil {
		t.Fatal(err)
	}
	r := <-opened
	if r.err != nil {
		t.Fatal(r.err)
	}
	open[0] = r.doc
	if text, err := r.doc.Text(0); err != nil || text != "Held" {
		t.Fatalf("%q %v", text, err)
	}
}

// An open past the bound gives up with its context's error.
func TestOpenPastTheBoundEndsWithItsContext(t *testing.T) {
	reader, data := shared(t), doctexttest.TextPDF("Held")
	holdAll(t, reader, data)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if doc, err := reader.Open(ctx, data); !errors.Is(err, context.DeadlineExceeded) {
		if doc != nil {
			_ = doc.Close()
		}
		t.Fatalf("%v", err)
	}
	if pdfium.Waiting(reader) != 0 || pdfium.Held(reader) != pdfium.MaxOpen {
		t.Fatal(pdfium.Waiting(reader), pdfium.Held(reader))
	}
}

// Many more goroutines than slots open and close PDFs without stalling.
func TestOpenAndCloseUnderContention(t *testing.T) {
	reader, data := shared(t), doctexttest.TextPDF("Busy")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var wg sync.WaitGroup
	for range 4 * pdfium.MaxOpen {
		wg.Go(func() {
			for range 10 {
				doc, err := reader.Open(ctx, data)
				if err != nil {
					t.Error(err)
					return
				}
				if err = doc.Close(); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if pdfium.Held(reader) != 0 {
		t.Fatal(pdfium.Held(reader))
	}
}
