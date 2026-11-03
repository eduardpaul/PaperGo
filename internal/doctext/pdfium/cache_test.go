package pdfium_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"papergo/internal/doctext/doctexttest"
	"papergo/internal/doctext/pdfium"
)

// The first start compiles into the cache directory, which it creates
// private; a later start, in a fresh runtime, loads from it.
func TestCompilationCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "pdfium")
	if cache, err := pdfium.New(pdfium.Options{CacheDir: dir}).Warm(); err != nil || cache != pdfium.CacheMiss {
		t.Fatal(cache, err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal(info, err)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "wazero-*", "*"))
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	reader := pdfium.New(pdfium.Options{CacheDir: dir})
	if cache, err := reader.Warm(); err != nil || cache != pdfium.CacheHit {
		t.Fatal(cache, err)
	}
	doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Cached"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if text, err := doc.Text(0); err != nil || text != "Cached" {
		t.Fatalf("%q %v", text, err)
	}
}

// A cache directory that cannot be created or written costs only the
// cache: PDFium compiles without it, with one warning.
func TestUnusableCacheDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{"under a file": filepath.Join(file, "pdfium")}
	if os.Geteuid() != 0 {
		readOnly := filepath.Join(root, "read-only")
		if err := os.Mkdir(readOnly, 0o500); err != nil {
			t.Fatal(err)
		}
		dirs["read-only"] = readOnly
	}
	for name, dir := range dirs {
		t.Run(name, func(t *testing.T) {
			var logged bytes.Buffer
			reader := pdfium.New(pdfium.Options{CacheDir: dir, Log: slog.New(slog.NewTextHandler(&logged, nil))})
			if cache, err := reader.Warm(); err != nil || cache != pdfium.CacheOff {
				t.Fatal(cache, err)
			}
			doc, err := reader.Open(context.Background(), doctexttest.TextPDF("Uncached"))
			if err != nil {
				t.Fatal(err)
			}
			defer doc.Close()
			if text, err := doc.Text(0); err != nil || text != "Uncached" {
				t.Fatalf("%q %v", text, err)
			}
			if strings.Count(logged.String(), "level=WARN") != 1 {
				t.Fatal(logged.String())
			}
		})
	}
}
