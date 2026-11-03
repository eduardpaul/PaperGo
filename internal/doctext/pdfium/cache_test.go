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

// seeded is a cache directory holding a copy of the shared test cache's
// compiled module, and that module's path in it.
func seeded(t *testing.T) (string, string) {
	t.Helper()
	if _, err := shared(t).Warm(); err != nil {
		t.Fatal(err)
	}
	from := doctexttest.CacheDir(t)
	modules, _ := filepath.Glob(filepath.Join(from, "wazero-*", "*"))
	if len(modules) != 1 {
		t.Fatal(modules)
	}
	rel, _ := filepath.Rel(from, modules[0])
	dir := filepath.Join(t.TempDir(), "pdfium")
	data, err := os.ReadFile(modules[0])
	if err == nil {
		err = os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o700)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, rel), data, 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	return dir, rel
}

func warm(t *testing.T, dir string, log *slog.Logger) string {
	t.Helper()
	cache, err := pdfium.New(pdfium.Options{CacheDir: dir, Log: log}).Warm()
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// Starts drop what the cache no longer needs: other wazero versions'
// directories at once, and the modules of earlier builds when they write
// a new one. Nothing else in the directory is touched.
func TestCachePrunesOldModules(t *testing.T) {
	dir, module := seeded(t)
	old := filepath.Join(dir, "wazero-v0.0.0-amd64-linux", "0123")
	earlier := filepath.Join(dir, filepath.Dir(module), "0123")
	other := filepath.Join(dir, "notes.txt")
	write(t, old, []byte("old"))
	write(t, earlier, []byte("earlier"))
	write(t, other, []byte("kept"))
	if cache := warm(t, dir, nil); cache != pdfium.CacheHit {
		t.Fatal(cache)
	}
	if exists(filepath.Dir(old)) || !exists(earlier) || !exists(other) {
		t.Fatal("other wazero versions are kept, or other files removed")
	}
	// A module of another wazero version is replaced under its name; the
	// start that writes it is a miss and removes the earlier module.
	path := filepath.Join(dir, module)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	version := data[7 : 7+int(data[6])]
	copy(version, strings.Repeat("9", len(version)))
	write(t, path, data)
	if cache := warm(t, dir, nil); cache != pdfium.CacheMiss {
		t.Fatal(cache)
	}
	if exists(earlier) || !exists(path) || !exists(other) {
		t.Fatal("earlier modules are kept, or the new one or other files removed")
	}
}

// A damaged module is cleared and compiled again, so the next start loads
// it again.
func TestCacheRepairsADamagedModule(t *testing.T) {
	dir, module := seeded(t)
	path := filepath.Join(dir, module)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	write(t, path, data)
	var logged bytes.Buffer
	if cache := warm(t, dir, slog.New(slog.NewTextHandler(&logged, nil))); cache != pdfium.CacheMiss {
		t.Fatal(cache, logged.String())
	}
	if !strings.Contains(logged.String(), "cleared") || strings.Count(logged.String(), "level=WARN") != 1 {
		t.Fatal(logged.String())
	}
	if repaired, err := os.ReadFile(path); err != nil || bytes.Equal(repaired, data) {
		t.Fatal("the module was not compiled again", err)
	}
	if cache := warm(t, dir, nil); cache != pdfium.CacheHit {
		t.Fatal(cache)
	}
}
