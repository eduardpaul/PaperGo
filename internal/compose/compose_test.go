package compose

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"papergo/internal/ocr"
	"papergo/internal/ocr/paddle"
)

// fake finds text lines of a fixed height (in the analyzed image's pixels)
// and reads one line, named after the image's width, at a fixed position.
type fake struct{ height int }

func (f fake) Detect(_ context.Context, img image.Image, _ ocr.DetectOptions) (ocr.Detection, error) {
	b := img.Bounds()
	if f.height == 0 {
		return ocr.Detection{Width: b.Dx(), Height: b.Dy()}, nil
	}
	q := ocr.Quad{{10, 10}, {10 + 10*f.height, 10}, {10 + 10*f.height, 10 + f.height}, {10, 10 + f.height}}
	return ocr.Detection{Regions: []ocr.Region{{Quad: q, Score: 0.9}}, Width: b.Dx(), Height: b.Dy()}, nil
}

func (f fake) Recognize(_ context.Context, img image.Image) (ocr.Page, error) {
	b := img.Bounds()
	text := fmt.Sprintf("Page width %04d", b.Dx())
	return ocr.Page{Width: b.Dx(), Height: b.Dy(), Lines: []ocr.Line{{Text: text, Confidence: 0.9, Quad: ocr.Quad{{100, 100}, {500, 100}, {500, 140}, {100, 140}}}}}, nil
}
func (fake) Info() ocr.Info { return ocr.Info{Engine: "fake"} }
func (fake) Close() error   { return nil }

func photo(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Rect, image.NewUniform(color.NRGBA{240, 240, 240, 255}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pdftotext(t *testing.T, doc []byte) []string {
	t.Helper()
	tool, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext is not installed")
	}
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err = os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(tool, "-layout", path, "-").CombinedOutput()
	if err != nil {
		t.Fatalf("pdftotext: %v\n%s", err, out)
	}
	return strings.Split(string(out), "\f")
}

func TestPagesScaleToTheSmallestTextAndKeepTheirOrder(t *testing.T) {
	photos := [][]byte{photo(t, 1000, 800), photo(t, 1200, 900), photo(t, 600, 400)}
	engines := []fake{{height: 64}, {height: 0}, {height: 16}}
	var buf bytes.Buffer
	page := 0
	m, err := PDF(context.Background(), engineFunc(func() fake { return engines[page] }), &buf, len(photos), func(i int) ([]byte, error) {
		page = i
		return photos[i], nil
	}, 64e6, Defaults)
	if err != nil {
		t.Fatal(err)
	}
	if m.Pages != 3 || m.TextLines != 3 || m.Bytes != int64(buf.Len()) || len(m.PageDetails) != 3 {
		t.Fatalf("metrics %+v", m)
	}
	// 64-pixel text halves the page; a page without text, and 16-pixel
	// text, keep their size: pages never grow.
	for i, want := range []Page{
		{SourceWidth: 1000, SourceHeight: 800, Width: 500, Height: 400, SmallestTextHeight: 64, Scale: 0.5, TextLines: 1},
		{SourceWidth: 1200, SourceHeight: 900, Width: 1200, Height: 900, Scale: 1, TextLines: 1},
		{SourceWidth: 600, SourceHeight: 400, Width: 600, Height: 400, SmallestTextHeight: 16, Scale: 1, TextLines: 1},
	} {
		if m.PageDetails[i] != want {
			t.Errorf("page %d: %+v, want %+v", i+1, m.PageDetails[i], want)
		}
	}
	text := pdftotext(t, buf.Bytes())
	for i, want := range []string{"Page width 1000", "Page width 1200", "Page width 0600"} {
		if !strings.Contains(text[i], want) {
			t.Errorf("page %d text %q, want %q", i+1, text[i], want)
		}
	}
}

// engineFunc is an engine that delegates to the fake current gives.
type engineFunc func() fake

func (e engineFunc) Detect(ctx context.Context, img image.Image, o ocr.DetectOptions) (ocr.Detection, error) {
	return e().Detect(ctx, img, o)
}
func (e engineFunc) Recognize(ctx context.Context, img image.Image) (ocr.Page, error) {
	return e().Recognize(ctx, img)
}
func (engineFunc) Info() ocr.Info { return ocr.Info{Engine: "fake"} }
func (engineFunc) Close() error   { return nil }

func TestPDFRejects(t *testing.T) {
	apng, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACGFjVEwAAAACAAAAAPONk3AAAAAaZmNUTAAAAAAAAAABAAAAAQAAAAAAAAAAAAEACgAAWn8w0AAAAA1JREFUeJxj+M/A8B8ABQAB/4mZPR0AAAAaZmNUTAAAAAEAAAABAAAAAQAAAAAAAAAAAAEACgAAwQzaBAAAABFmZEFUAAAAAnicY2Bg+P8fAAMCAf/1e6XXAAAAAElFTkSuQmCC")
	read := func(data []byte) func(int) ([]byte, error) { return func(int) ([]byte, error) { return data, nil } }
	var buf bytes.Buffer
	if _, err := PDF(context.Background(), fake{}, &buf, 1, read(apng), 64e6, Defaults); !errors.Is(err, ErrAnimated) {
		t.Errorf("animated: %v", err)
	}
	if _, err := PDF(context.Background(), fake{}, &buf, 1, read([]byte("%PDF-1.7")), 64e6, Defaults); err == nil {
		t.Error("a PDF page is accepted")
	}
	if _, err := PDF(context.Background(), fake{}, &buf, 0, read(nil), 64e6, Defaults); err == nil {
		t.Error("no pages are accepted")
	}
	if _, err := PDF(context.Background(), fake{}, &buf, MaxPages+1, read(nil), 64e6, Defaults); err == nil {
		t.Error("too many pages are accepted")
	}
	if _, err := PDF(context.Background(), fake{}, &buf, 1, read(nil), 64e6, Options{TargetHeight: 4, Quality: 80}); err == nil {
		t.Error("a target of 4 px is accepted")
	}
	failing := errors.New("storage is down")
	if _, err := PDF(context.Background(), fake{}, &buf, 1, func(int) ([]byte, error) { return nil, failing }, 64e6, Defaults); !errors.Is(err, failing) {
		t.Errorf("read error: %v", err)
	}
}

// TestPDFWithPaddleOCR composes two rendered pages and reads their text
// back from the PDF. It needs ONNX Runtime (ONNXRUNTIME_LIB) and pdftotext.
func TestPDFWithPaddleOCR(t *testing.T) {
	lib := os.Getenv("ONNXRUNTIME_LIB")
	if lib == "" {
		if os.Getenv("PAPERGO_REQUIRE_OCR") != "" {
			t.Fatal("ONNXRUNTIME_LIB is not set")
		}
		t.Skip("ONNXRUNTIME_LIB is not set")
	}
	e, err := paddle.New(paddle.Config{Library: lib, Models: filepath.Join("..", "..", "models", "paddleocr")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	render := func(size float64, lines ...string) []byte {
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			t.Fatal(err)
		}
		defer face.Close()
		img := image.NewRGBA(image.Rect(0, 0, 1400, 1000))
		draw.Draw(img, img.Rect, image.NewUniform(color.White), image.Point{}, draw.Src)
		d := font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face}
		for i, l := range lines {
			d.Dot = fixed.P(80, 150+i*int(size*2.5))
			d.DrawString(l)
		}
		var buf bytes.Buffer
		if err = png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	photos := [][]byte{render(72, "Invoice 2026-0042", "Total EUR 21.30"), render(40, "Second page")}
	var buf bytes.Buffer
	m, err := PDF(context.Background(), e, &buf, 2, func(i int) ([]byte, error) { return photos[i], nil }, 64e6, Defaults)
	if err != nil {
		t.Fatal(err)
	}
	if p := m.PageDetails[0]; p.TextLines != 2 || p.Scale >= 1 || p.Width >= 1400 {
		t.Errorf("first page %+v", p)
	}
	text := pdftotext(t, buf.Bytes())
	for i, want := range [][]string{{"Invoice 2026-0042", "Total EUR 21.30"}, {"Second page"}} {
		for _, w := range want {
			if !strings.Contains(text[i], w) {
				t.Errorf("page %d: %q has no %q", i+1, text[i], w)
			}
		}
	}
}
