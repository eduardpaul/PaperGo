// Package doctexttest builds files for tests of text reading.
package doctexttest

import (
	"fmt"
	"strings"
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

// PDF returns the linked native PDF reader.
func PDF(t testing.TB) doctext.PDF { t.Helper(); return pdfium.New() }
