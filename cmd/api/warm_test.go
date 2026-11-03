package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Warming PDFium returns at once and logs when it is ready, with its cache.
func TestWarmPDF(t *testing.T) {
	var logged lockedBuffer
	reader := pdfium.New(pdfium.Options{CacheDir: t.TempDir()})
	start := time.Now()
	warmPDF(slog.New(slog.NewTextHandler(&logged, nil)), reader)
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("warming took %s before returning", took)
	}
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Warm"))
	if err != nil {
		t.Fatal(err)
	}
	_ = doc.Close()
	for deadline := time.Now().Add(time.Minute); !strings.Contains(logged.String(), "pdf reader ready"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(logged.String())
		}
	}
	if !strings.Contains(logged.String(), "cache=miss") {
		t.Fatal(logged.String())
	}
}
