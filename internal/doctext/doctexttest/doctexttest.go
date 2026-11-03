// Package doctexttest builds files for tests of text reading.
package doctexttest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"

	"papergo/internal/doctext"
	"papergo/internal/doctext/pdfium"
)

// TextPDF is a PDF whose pages draw these texts in Helvetica: a text layer
// and no images. Texts must be printable ASCII without parentheses or
// backslashes.
func TextPDF(pages ...string) []byte {
	var b strings.Builder
	offsets := []int{}
	object := func(body string) {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	b.WriteString("%PDF-1.4\n")
	kids := make([]string, len(pages))
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	object("<</Type/Catalog/Pages 2 0 R>>")
	object(fmt.Sprintf("<</Type/Pages/Kids[%s]/Count %d>>", strings.Join(kids, " "), len(pages)))
	object("<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>")
	for i, text := range pages {
		stream := fmt.Sprintf("BT /F1 14 Tf 40 700 Td (%s) Tj ET", text)
		object(fmt.Sprintf("<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]/Contents %d 0 R/Resources<</Font<</F1 3 0 R>>>>>>", 5+2*i))
		object(fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(stream), stream))
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return []byte(b.String())
}

// reader is shared by a test binary's tests, and its compiled code by
// every test binary and run.
var reader = sync.OnceValue(func() *pdfium.Reader { return pdfium.New(pdfium.Options{CacheDir: cacheDir()}) })

// cacheDir keeps PDFium's compiled code for tests on disk, per go-pdfium
// version: under the user cache directory, or TMPDIR without one.
func cacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	version := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, m := range info.Deps {
			if m.Path == "github.com/klippa-app/go-pdfium" {
				version = m.Version
			}
		}
	}
	return filepath.Join(base, "papergo-test", "pdfium-"+version)
}

// CacheDir is the compilation cache tests share; tests of the cache itself
// use their own.
func CacheDir(t testing.TB) string { t.Helper(); return cacheDir() }

// PDF returns the PDF reader, started: a cold compile takes tens of seconds
// under the race detector and would otherwise count against the deadlines
// of the test that first opens a PDF.
func PDF(t testing.TB) doctext.PDF {
	t.Helper()
	if _, err := reader().Warm(); err != nil {
		t.Fatalf("start PDFium: %v", err)
	}
	return reader()
}
