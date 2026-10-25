package doctext_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/image/tiff"
	"papergo/internal/imaging"
	"papergo/internal/imaging/imagingtest"

	"papergo/internal/doctext"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/ocr"
	"papergo/internal/pdf"
)

var ctx = context.Background()

// engine recognizes one line naming the image's size, and counts calls.
type engine struct{ calls atomic.Int32 }

func (e *engine) Detect(context.Context, image.Image, ocr.DetectOptions) (ocr.Detection, error) {
	return ocr.Detection{}, nil
}

func (e *engine) Recognize(_ context.Context, img image.Image) (ocr.Page, error) {
	e.calls.Add(1)
	return ocr.Page{Lines: []ocr.Line{{Text: "  Scanned invoice  "}, {Text: ""}}}, nil
}
func (e *engine) Info() ocr.Info { return ocr.Info{Engine: "test"} }
func (e *engine) Close() error   { return nil }

func blank(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	img.Set(5, 5, color.Black)
	return img
}

func composed(t *testing.T, lines ...string) []byte {
	t.Helper()
	page := pdf.Page{Image: blank(600, 800)}
	for i, l := range lines {
		y := float64(40 + 30*i)
		page.Lines = append(page.Lines, pdf.Text{Text: l, Quad: [4]pdf.Point{{X: 40, Y: y}, {X: 400, Y: y}, {X: 400, Y: y + 20}, {X: 40, Y: y + 20}}})
	}
	var buf bytes.Buffer
	if err := pdf.Write(&buf, []pdf.Page{page}, 80); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTextPDFReadsTheTextLayerWithoutOCR(t *testing.T) {
	reader := doctexttest.PDF(t)
	e := &engine{}
	r, err := doctext.Extract(ctx, doctexttest.TextPDF("Invoice March", "Second page"), "application/pdf", doctext.Options{}, reader, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Pages) != 2 || r.Pages[0].Text != "Invoice March" || r.Pages[1].Text != "Second page" || r.Pages[0].OCR || r.OCRPages != 0 || e.calls.Load() != 0 {
		t.Fatalf("%+v", r)
	}
	if r.Chars != len("Invoice March")+len("Second page") || r.Truncated {
		t.Fatalf("%+v", r)
	}
	// Always OCRs every page.
	if r, err = doctext.Extract(ctx, doctexttest.TextPDF("Invoice March"), "application/pdf", doctext.Options{OCR: doctext.OCRAlways}, reader, e); err != nil || r.OCRPages != 1 || r.Pages[0].Text != "Scanned invoice" {
		t.Fatalf("always: %+v %v", r, err)
	}
	// Pages past the bound are left out.
	if r, err = doctext.Extract(ctx, doctexttest.TextPDF("a", "b", "c"), "application/pdf", doctext.Options{MaxPages: 2}, reader, e); err != nil || len(r.Pages) != 2 || !r.Truncated {
		t.Fatalf("max pages: %+v %v", r, err)
	}
}

func TestScannedPagesAreOCRedComposedPagesAreNot(t *testing.T) {
	reader := doctexttest.PDF(t)
	e := &engine{}
	scanned := composed(t)
	r, err := doctext.Extract(ctx, scanned, "application/pdf", doctext.Options{}, reader, e)
	if err != nil || r.OCRPages != 1 || !r.Pages[0].OCR || r.Pages[0].Text != "Scanned invoice" || e.calls.Load() != 1 {
		t.Fatalf("scanned: %+v %v", r, err)
	}
	// Without OCR the page is reported, not failed.
	if r, err = doctext.Extract(ctx, scanned, "application/pdf", doctext.Options{}, reader, nil); err != nil || r.Skipped != 1 || r.Pages[0].Text != "" {
		t.Fatalf("no engine: %+v %v", r, err)
	}
	if r, err = doctext.Extract(ctx, scanned, "application/pdf", doctext.Options{OCR: doctext.OCRNever}, reader, e); err != nil || r.Skipped != 1 || e.calls.Load() != 1 {
		t.Fatalf("never: %+v %v", r, err)
	}
	// PaperGo's own searchable PDFs carry their OCR text.
	r, err = doctext.Extract(ctx, composed(t, "Total 21.30", "Paid"), "application/pdf", doctext.Options{}, reader, e)
	if err != nil || r.OCRPages != 0 || e.calls.Load() != 1 || !strings.Contains(r.Pages[0].Text, "Total 21.30") || !strings.Contains(r.Pages[0].Text, "Paid") {
		t.Fatalf("composed: %+v %v", r, err)
	}
}

func TestImagesAreOCRed(t *testing.T) {
	var reader doctext.PDF
	e := &engine{}
	var photo, scan bytes.Buffer
	if err := png.Encode(&photo, blank(300, 200)); err != nil {
		t.Fatal(err)
	}
	if err := tiff.Encode(&scan, blank(300, 200), nil); err != nil {
		t.Fatal(err)
	}
	for media, data := range map[string][]byte{"image/png": photo.Bytes(), "image/tiff": scan.Bytes()} {
		r, err := doctext.Extract(ctx, data, media, doctext.Options{}, reader, e)
		if err != nil || len(r.Pages) != 1 || r.Pages[0].Text != "Scanned invoice" || r.OCRPages != 1 {
			t.Fatalf("%s: %+v %v", media, r, err)
		}
	}
	// Every page of a multi-page scan is read, up to MaxPages.
	pages := imagingtest.TIFF(imagingtest.Page(60, 40, 255), imagingtest.Page(60, 40, 255), imagingtest.Page(60, 40, 255))
	r, err := doctext.Extract(ctx, pages, "image/tiff", doctext.Options{}, reader, e)
	if err != nil || len(r.Pages) != 3 || r.OCRPages != 3 || r.Pages[2].Number != 3 || r.Pages[2].Text != "Scanned invoice" || r.Truncated {
		t.Fatalf("multi-page TIFF: %+v %v", r, err)
	}
	if r, err = doctext.Extract(ctx, pages, "image/tiff", doctext.Options{MaxPages: 2}, reader, e); err != nil || len(r.Pages) != 2 || !r.Truncated {
		t.Fatalf("bounded TIFF: %+v %v", r, err)
	}
	if r, err = doctext.Extract(ctx, pages, "image/tiff", doctext.Options{}, reader, nil); err != nil || len(r.Pages) != 3 || r.Skipped != 3 {
		t.Fatalf("TIFF without an engine: %+v %v", r, err)
	}
	// A scan with more pages than a TIFF lists says its text is incomplete.
	long := make([]*image.Gray, imaging.MaxTIFFPages+1)
	for i := range long {
		long[i] = imagingtest.Page(1, 1, 255)
	}
	if r, err = doctext.Extract(ctx, imagingtest.TIFF(long...), "image/tiff", doctext.Options{MaxPages: imaging.MaxTIFFPages + 10}, reader, nil); err != nil || len(r.Pages) != imaging.MaxTIFFPages || !r.Truncated {
		t.Fatalf("TIFF past the page bound: %d pages, truncated %v, %v", len(r.Pages), r.Truncated, err)
	}
	if r, err := doctext.Extract(ctx, photo.Bytes(), "image/png", doctext.Options{}, reader, nil); err != nil || r.Skipped != 1 {
		t.Fatalf("no engine: %+v %v", r, err)
	}
	if _, err := doctext.Extract(ctx, photo.Bytes(), "image/png", doctext.Options{OCR: doctext.OCRAlways}, reader, nil); err == nil {
		t.Fatal("always without an engine")
	}
	if _, err := doctext.Extract(ctx, photo.Bytes(), "image/png", doctext.Options{MaxPixels: 100}, reader, e); err == nil {
		t.Fatal("an image above the pixel bound")
	}
}

func TestUnreadableFiles(t *testing.T) {
	reader := doctexttest.PDF(t)
	for media, data := range map[string][]byte{"application/pdf": []byte("not a pdf"), "application/zip": []byte("PK"), "image/png": []byte("not a png")} {
		if _, err := doctext.Extract(ctx, data, media, doctext.Options{}, reader, &engine{}); !errors.Is(err, doctext.ErrUnsupported) {
			t.Errorf("%s: %v", media, err)
		}
	}
	if _, err := doctext.Extract(ctx, doctexttest.TextPDF("x"), "application/pdf", doctext.Options{OCR: "sometimes"}, reader, nil); err == nil {
		t.Error("unknown OCR mode")
	}
}

func TestLanguage(t *testing.T) {
	english := "The invoice is for the work that was done in March and the payment is due with this letter"
	german := "Die Rechnung ist für die Arbeit, die im März gemacht wurde, und die Zahlung ist mit dem Brief fällig"
	for _, c := range []struct{ text, hints, want string }{
		{english, "", "english"},
		{german, "", "german"},
		{german, "eng", "english"},
		{german, "eng+deu", "german"},
		{"Invoice 42", "", ""},
		{english, "xyz", "english"},
	} {
		if got := doctext.Language(c.text, c.hints, []string{"english", "german", "french"}); got != c.want {
			t.Errorf("%q %q: %q, want %q", c.text[:12], c.hints, got, c.want)
		}
	}
	if got := doctext.Language(german, "", []string{"english"}); got != "" {
		t.Errorf("detected outside the languages: %q", got)
	}
}
