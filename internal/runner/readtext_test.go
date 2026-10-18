package runner

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"papergo/ent/blobtext"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/pdf"
)

// readText is a workflow that reads the text of PDFs and images as they
// are added.
const readText = `{"triggers": [{"type": "file.added", "collection_id": "$LIST", "media_types": ["application/pdf", "image/*"]}],
	"flow": {"start": "read", "nodes": {"read": {"activity": "document.read_text", "next": {"done": "end", "no_text": "end", "unsupported": "end"}}, "end": {"activity": "end"}}}}`

func pdfOf(t *testing.T, lines ...string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 600, 800))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	page := pdf.Page{Image: img}
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

func (f *fixture) text(t *testing.T, itemID string) (string, int) {
	t.Helper()
	_, text, err := f.dms.ItemText(ctx, "alice", itemID, "")
	if err != nil {
		t.Fatalf("text of %s: %v", itemID, err)
	}
	return text.ID, text.OcrPages
}

// Text PDFs give their text layer, scans and photos their OCR text,
// PaperGo's own searchable PDFs are not OCRed again, and identical bytes
// are read once.
func TestReadTextOfPDFsScansAndPhotos(t *testing.T) {
	doctexttest.PDF(t)
	f := setup(t)
	f.searchOff(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Archive", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.list = lib
	w := f.workflow(t, "Read text", readText)
	var photo bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	if err = png.Encode(&photo, img); err != nil {
		t.Fatal(err)
	}
	textPDF := f.upload(t, lib.ID, "invoice.pdf", "application/pdf", doctexttest.TextPDF("Invoice March", "Page two"))
	scanned := f.upload(t, lib.ID, "scan.pdf", "application/pdf", pdfOf(t))
	composedPDF := f.upload(t, lib.ID, "receipt.pdf", "application/pdf", pdfOf(t, "Receipt 42"))
	image := f.upload(t, lib.ID, "photo.png", "image/png", photo.Bytes())
	// Background file processing can hold the SQLite writer under race
	// instrumentation. Dispatch every upload before starting the execution wait.
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, w.ID, 4)
	for _, run := range runs {
		if run.Status != "completed" {
			t.Fatalf("run: %+v", run)
		}
	}
	for _, c := range []struct {
		item *dms.File
		want string
		ocr  int
	}{{textPDF, "Invoice March", 0}, {scanned, "Total 21.30", 1}, {composedPDF, "Receipt 42", 0}, {image, "Total 21.30", 1}} {
		_, text, err := f.dms.ItemText(ctx, "alice", c.item.ID, "")
		if err != nil || text.OcrPages != c.ocr || text.Chars == 0 {
			t.Fatalf("%s: %+v %v", c.item.Name, text, err)
		}
		obj, err := f.store.Open(ctx, text.ObjectKey)
		if err != nil {
			t.Fatal(err)
		}
		stored := new(bytes.Buffer)
		_, err = stored.ReadFrom(obj)
		obj.Close()
		if err != nil || !strings.Contains(stored.String(), c.want) {
			t.Fatalf("%s: text %s, want %q: %v", c.item.Name, stored, c.want, err)
		}
	}
	// The same bytes again reuse the text.
	before, _ := f.text(t, textPDF.ID)
	copyOf := f.upload(t, lib.ID, "copy.pdf", "application/pdf", doctexttest.TextPDF("Invoice March", "Page two"))
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	f.runs(t, w.ID, 5)
	if again, _ := f.text(t, copyOf.ID); again != before {
		t.Fatalf("identical bytes read again: %s, %s", again, before)
	}
	if n, err := f.db.Client.BlobText.Query().Where(blobtext.Sha256EQ(copyOf.Blob.Sha256)).Count(ctx); err != nil || n != 1 {
		t.Fatalf("texts of identical bytes: %d %v", n, err)
	}
}

// media_types keeps other files from starting runs.
func TestReadTextMediaTypes(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	lib, err := f.dms.Create(ctx, "alice", f.ws.ID, dms.CreateResource{Kind: "library", Name: "Archive", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f.list = lib
	w := f.workflow(t, "PDFs only", strings.Replace(readText, `"application/pdf", "image/*"`, `"application/pdf"`, 1))
	var photo bytes.Buffer
	if err = png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	f.upload(t, lib.ID, "photo.png", "image/png", photo.Bytes())
	f.upload(t, lib.ID, "a.pdf", "application/pdf", doctexttest.TextPDF("Hello"))
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	f.runs(t, w.ID, 1)
}
