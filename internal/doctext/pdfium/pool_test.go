package pdfium_test

import (
	"context"
	"testing"
	"time"

	"papergo/internal/doctext"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
)

// Opens past the bound wait for a close, and give up with their context.
func TestOpenWaitsForAnInstance(t *testing.T) {
	reader, data := pdfium.New(), doctexttest.TextPDF("Held")
	open := make([]doctext.Document, 0, pdfium.MaxOpen)
	defer func() {
		for _, doc := range open {
			_ = doc.Close()
		}
	}()
	for range pdfium.MaxOpen {
		doc, err := reader.Open(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, doc)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if doc, err := reader.Open(ctx, data); err == nil {
		_ = doc.Close()
		t.Fatal("opened past the bound")
	}
	if err := open[0].Close(); err != nil {
		t.Fatal(err)
	}
	doc, err := reader.Open(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	open[0] = doc
	if text, err := doc.Text(0); err != nil || text != "Held" {
		t.Fatalf("%q %v", text, err)
	}
}
