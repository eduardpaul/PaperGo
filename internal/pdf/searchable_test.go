package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func flat(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, image.NewUniform(color.White), image.Point{}, draw.Src)
	return img
}

func noisy(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(5)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	return img
}

func line(text string, x, y, w, h float64) Text {
	return Text{Text: text, Quad: [4]Point{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}}
}

func write(t *testing.T, pages []Page) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, pages, 80); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checkXref checks that the cross-reference table points at every object.
func checkXref(t *testing.T, doc []byte) int {
	t.Helper()
	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF\n$`).FindSubmatch(doc)
	if m == nil {
		t.Fatal("no startxref at the end")
	}
	at, _ := strconv.Atoi(string(m[1]))
	table := string(doc[at:])
	if !strings.HasPrefix(table, "xref\n0 ") {
		t.Fatalf("startxref %d does not point at the table", at)
	}
	rows := regexp.MustCompile(`(\d{10}) 00000 n `).FindAllStringSubmatch(table, -1)
	for i, row := range rows {
		off, _ := strconv.Atoi(row[1])
		if want := fmt.Sprintf("%d 0 obj\n", i+1); !bytes.HasPrefix(doc[off:], []byte(want)) {
			t.Fatalf("object %d is not at offset %d", i+1, off)
		}
	}
	return len(rows)
}

func TestWriteStructureAndImageEncoding(t *testing.T) {
	doc := write(t, []Page{
		{Image: flat(300, 200), Lines: []Text{line("Total 21.30", 10, 20, 110, 12)}},
		{Image: noisy(150, 100)},
	})
	if !bytes.HasPrefix(doc, []byte("%PDF-1.7\n")) {
		t.Fatal("no PDF header")
	}
	if n := checkXref(t, doc); n != 14 {
		t.Errorf("%d objects", n)
	}
	s := string(doc)
	for _, want := range []string{
		"/Type /Pages /Count 2",
		// 300×200 pixels at 150 per inch.
		"/MediaBox [0 0 144.000000 96.000000]",
		"/Width 300 /Height 200 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode",
		"/Width 150 /Height 100 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
		// The line's bottom left is at (10, 32) pixels, 11 characters over 110 pixels.
		"BT /F0 1 Tf 3 Tr 4.800000 -0.000000 0.000000 5.760000 4.800000 80.640000 Tm <000100020003000400050006000700080009000A000B> Tj ET",
		"<0001> <0054>", "/ToUnicode",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestWriteRejectsInvalidInput(t *testing.T) {
	var buf bytes.Buffer
	for name, c := range map[string]struct {
		pages   []Page
		quality int
	}{
		"quality":   {[]Page{{Image: flat(1, 1)}}, 0},
		"no pages":  {nil, 80},
		"empty":     {[]Page{{Image: image.NewRGBA(image.Rectangle{})}}, 80},
		"too large": {[]Page{{Image: huge{}}}, 80},
	} {
		if err := Write(&buf, c.pages, c.quality); err == nil {
			t.Errorf("%s is accepted", name)
		}
	}
}

// huge is an image above MaxPagePixels that holds no pixels.
type huge struct{}

func (huge) ColorModel() color.Model { return color.RGBAModel }
func (huge) Bounds() image.Rectangle { return image.Rect(0, 0, 10000, 10000) }
func (huge) At(int, int) color.Color { return color.White }

// TestTextCanBeExtracted reads the text layer back with pdftotext, when it
// is installed: every line, in order, with characters outside Latin-1 and
// outside the Basic Multilingual Plane.
func TestTextCanBeExtracted(t *testing.T) {
	tool, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext is not installed")
	}
	path := filepath.Join(t.TempDir(), "doc.pdf")
	doc := write(t, []Page{
		{Image: flat(1000, 400), Lines: []Text{line("Invoice 2026-0042", 50, 50, 400, 30), line("Grüße €12,50", 50, 150, 300, 30)}},
		{Image: flat(1000, 400), Lines: []Text{line("Second page 𝄞", 50, 50, 350, 30)}},
	})
	if err = os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(tool, "-layout", path, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("pdftotext: %v\n%s", err, out)
	}
	pages := strings.Split(string(out), "\f")
	if len(pages) < 2 {
		t.Fatalf("pages: %q", out)
	}
	for i, want := range [][]string{{"Invoice 2026-0042", "Grüße €12,50"}, {"Second page 𝄞"}} {
		for _, w := range want {
			if !strings.Contains(pages[i], w) {
				t.Errorf("page %d: %q has no %q", i+1, pages[i], w)
			}
		}
	}
}
