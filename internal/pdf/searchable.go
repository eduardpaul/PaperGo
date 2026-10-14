// Package pdf writes searchable PDFs: page images with an invisible text
// layer at the positions OCR recognized, so viewers show the scan and can
// search, select and copy its text. It needs no system fonts. The output has
// the structure of PaperDotNet's searchable PDFs: the same font, text
// placement and image encoding.
package pdf

import (
	"bufio"
	"bytes"
	"compress/zlib"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// textFont is a placeholder TrueType font whose only glyph, one em wide,
// stands for every character of the invisible text; ToUnicode maps
// character IDs back to text. scripts/generate-pdf-text-font.py builds it.
//
//go:embed text.ttf
var textFont []byte

// PointsPerPixel places page images at 150 pixels per inch.
const PointsPerPixel = 72.0 / 150

// MaxPagePixels bounds each page image.
const MaxPagePixels = 64_000_000

// Point is a position in a page image's pixels.
type Point struct{ X, Y float64 }

// Text is a recognized text line: its text and its corners in the page
// image's pixels, in reading order (top left, top right, bottom right,
// bottom left of the text).
type Text struct {
	Text string
	Quad [4]Point
}

// Page is an opaque page image with its text lines. Transparent pixels are
// flattened onto white in lossless images only, so callers flatten first.
type Page struct {
	Image image.Image
	Lines []Text
}

// Writer writes a searchable PDF page by page, so only one page image is
// in memory at a time.
type Writer struct {
	pdf     *objects
	quality int
	catalog int
	tree    int
	font    int
	pages   []string
	codes   map[rune]int
	order   []rune
	closed  bool
}

// NewWriter starts a searchable PDF. Each page image is embedded as JPEG at
// quality (1 to 100), or losslessly with Flate when that is smaller, as flat
// scans often are.
func NewWriter(w io.Writer, quality int) (*Writer, error) {
	if quality < 1 || quality > 100 {
		return nil, errors.New("the PDF image quality must be between 1 and 100")
	}
	pdf := &objects{w: bufio.NewWriter(w)}
	pdf.write("%PDF-1.7\n%\xE2\xE3\xCF\xD3\n")
	// The font, which needs every character, is written last.
	return &Writer{pdf: pdf, quality: quality, catalog: pdf.reserve(), tree: pdf.reserve(), font: pdf.reserve(), codes: map[rune]int{}}, nil
}

// AddPage writes a page.
func (w *Writer) AddPage(p Page) error {
	if w.closed {
		return errors.New("the PDF is closed")
	}
	b := p.Image.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || int64(b.Dx())*int64(b.Dy()) > MaxPagePixels {
		return fmt.Errorf("a PDF page image is empty or above %d pixels", MaxPagePixels)
	}
	for _, l := range p.Lines {
		for _, r := range l.Text {
			if _, ok := w.codes[r]; !ok {
				if len(w.order) == 0xFFFF {
					return errors.New("too many distinct characters for the PDF text layer")
				}
				w.order = append(w.order, r)
				w.codes[r] = len(w.order)
			}
		}
	}
	width, height := float64(b.Dx())*PointsPerPixel, float64(b.Dy())*PointsPerPixel
	img, err := w.pdf.image(p.Image, w.quality)
	if err != nil {
		return err
	}
	var content strings.Builder
	fmt.Fprintf(&content, "q %s 0 0 %s 0 0 cm /Im0 Do Q\n", num(width), num(height))
	for _, l := range p.Lines {
		appendText(&content, l, height, w.codes)
	}
	ops := w.pdf.stream("", []byte(content.String()))
	w.pages = append(w.pages, fmt.Sprintf("%d 0 R", w.pdf.add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources << /XObject << /Im0 %d 0 R >> /Font << /F0 %d 0 R >> >> /Contents %d 0 R >>",
		w.tree, num(width), num(height), img, w.font, ops))))
	return w.pdf.err
}

// Close writes the font, the page tree and the trailer.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.pages) == 0 {
		return errors.New("a PDF needs at least one page")
	}
	w.pdf.fontAt(w.font, w.order, w.codes)
	w.pdf.set(w.tree, fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(w.pages), strings.Join(w.pages, " ")))
	w.pdf.set(w.catalog, fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", w.tree))
	w.pdf.trailer(w.catalog)
	if w.pdf.err != nil {
		return w.pdf.err
	}
	return w.pdf.w.Flush()
}

// Write writes pages as a searchable PDF.
func Write(w io.Writer, pages []Page, quality int) error {
	pw, err := NewWriter(w, quality)
	if err != nil {
		return err
	}
	for _, p := range pages {
		if err = pw.AddPage(p); err != nil {
			return err
		}
	}
	return pw.Close()
}

// appendText places a line's text, invisible (render mode 3), along its
// quad: each character takes an equal share of the baseline, and the font
// size is the line's height.
func appendText(b *strings.Builder, l Text, pageHeight float64, codes map[rune]int) {
	n := utf8.RuneCountInString(l.Text)
	if n == 0 {
		return
	}
	q, k := l.Quad, PointsPerPixel
	a := (q[2].X - q[3].X) * k / float64(n)
	bb := -(q[2].Y - q[3].Y) * k / float64(n)
	c := (q[0].X - q[3].X) * k
	d := -(q[0].Y - q[3].Y) * k
	x, y := q[3].X*k, pageHeight-q[3].Y*k
	fmt.Fprintf(b, "BT /F0 1 Tf 3 Tr %s %s %s %s %s %s Tm <", num(a), num(bb), num(c), num(d), num(x), num(y))
	for _, r := range l.Text {
		fmt.Fprintf(b, "%04X", codes[r])
	}
	b.WriteString("> Tj ET\n")
}

// num formats a coordinate with six decimals, as PaperDotNet does.
func num(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

// objects writes numbered PDF objects and records their offsets.
type objects struct {
	w       *bufio.Writer
	offsets []int64
	at      int64
	err     error
}

func (o *objects) write(s string) { o.bytes([]byte(s)) }

func (o *objects) bytes(p []byte) {
	if o.err != nil {
		return
	}
	n, err := o.w.Write(p)
	o.at += int64(n)
	o.err = err
}

func (o *objects) reserve() int {
	o.offsets = append(o.offsets, 0)
	return len(o.offsets)
}

func (o *objects) start(id int) {
	o.offsets[id-1] = o.at
	o.write(fmt.Sprintf("%d 0 obj\n", id))
}

func (o *objects) set(id int, body string) {
	o.start(id)
	o.write(body)
	o.write("\nendobj\n")
}

func (o *objects) add(body string) int {
	id := o.reserve()
	o.set(id, body)
	return id
}

func (o *objects) stream(dict string, data []byte) int {
	id := o.reserve()
	o.start(id)
	o.write(fmt.Sprintf("<< %s /Length %d >>\nstream\n", dict, len(data)))
	o.bytes(data)
	o.write("\nendstream\nendobj\n")
	return id
}

func (o *objects) trailer(catalog int) {
	xref := o.at
	var b strings.Builder
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(o.offsets)+1)
	for _, off := range o.offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(o.offsets)+1, catalog, xref)
	o.write(b.String())
}

// fontAt writes the invisible text font as object id: a Type0 font whose
// character IDs are the text's distinct characters, numbered from 1, all
// drawn with the placeholder glyph, and mapped back to Unicode by ToUnicode.
func (o *objects) fontAt(id int, order []rune, codes map[rune]int) {
	file := o.stream(fmt.Sprintf("/Length1 %d", len(textFont)), textFont)
	descriptor := o.add(fmt.Sprintf("<< /Type /FontDescriptor /FontName /PaperDotNetOcrText /Flags 4 /FontBBox [0 0 1000 1000] /ItalicAngle 0 /Ascent 1000 /Descent 0 /CapHeight 1000 /StemV 80 /FontFile2 %d 0 R >>", file))
	glyphs := o.stream("", make([]byte, (len(order)+1)*2))
	descendant := o.add(fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /PaperDotNetOcrText /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /FontDescriptor %d 0 R /DW 1000 /CIDToGIDMap %d 0 R >>", descriptor, glyphs))
	var cmap strings.Builder
	cmap.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n/CMapName /PaperDotNetUnicode def\n/CMapType 2 def\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for start := 0; start < len(order); start += 100 {
		chunk := order[start:min(start+100, len(order))]
		fmt.Fprintf(&cmap, "%d beginbfchar\n", len(chunk))
		for _, r := range chunk {
			fmt.Fprintf(&cmap, "<%04X> <", codes[r])
			for _, u := range utf16.Encode([]rune{r}) {
				fmt.Fprintf(&cmap, "%04X", u)
			}
			cmap.WriteString(">\n")
		}
		cmap.WriteString("endbfchar\n")
	}
	cmap.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	unicode := o.stream("", []byte(cmap.String()))
	o.set(id, fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /PaperDotNetOcrText /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", descendant, unicode))
}

// image adds a page image as JPEG, or as Flate-compressed RGB when smaller.
func (o *objects) image(img image.Image, quality int) (int, error) {
	b := img.Bounds()
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: quality}); err != nil {
		return 0, fmt.Errorf("encode PDF image: %w", err)
	}
	var flate bytes.Buffer
	z, err := zlib.NewWriterLevel(&flate, zlib.BestCompression)
	if err != nil {
		return 0, err
	}
	row := make([]byte, b.Dx()*3)
	rgba, _ := img.(*image.RGBA)
	complete := true
	for y := b.Min.Y; y < b.Max.Y; y++ {
		if rgba != nil {
			// Opaque RGBA rows need no color conversion.
			pix := rgba.Pix[rgba.PixOffset(b.Min.X, y):]
			for i := range b.Dx() {
				white := 255 - pix[i*4+3]
				row[i*3], row[i*3+1], row[i*3+2] = pix[i*4]+white, pix[i*4+1]+white, pix[i*4+2]+white
			}
		} else {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, a := img.At(x, y).RGBA()
				white := 0xffff - a
				i := (x - b.Min.X) * 3
				row[i], row[i+1], row[i+2] = byte((r+white)>>8), byte((g+white)>>8), byte((bl+white)>>8)
			}
		}
		if _, err = z.Write(row); err != nil {
			return 0, err
		}
		// Give up on Flate as soon as it is larger.
		if flate.Len() >= jpg.Len() {
			complete = false
			break
		}
	}
	if err = z.Close(); err != nil {
		return 0, err
	}
	dict := fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8", b.Dx(), b.Dy())
	if complete && flate.Len() < jpg.Len() {
		return o.stream(dict+" /Filter /FlateDecode", flate.Bytes()), nil
	}
	return o.stream(dict+" /Filter /DCTDecode", jpg.Bytes()), nil
}
