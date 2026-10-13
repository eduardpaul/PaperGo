package paddle

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"papergo/internal/ocr"
)

var modelDir = filepath.Join("..", "..", "..", "models", "paddleocr")

// testEngine needs the ONNX Runtime library in ONNXRUNTIME_LIB. Without it
// the test is skipped, unless PAPERGO_REQUIRE_OCR is set (as in CI).
func testEngine(t testing.TB) *Engine {
	t.Helper()
	library := os.Getenv("ONNXRUNTIME_LIB")
	if library == "" {
		if os.Getenv("PAPERGO_REQUIRE_OCR") != "" {
			t.Fatal("PAPERGO_REQUIRE_OCR is set but ONNXRUNTIME_LIB is not")
		}
		t.Skip("set ONNXRUNTIME_LIB to the ONNX Runtime library to run OCR tests")
	}
	e, err := New(Config{Library: library, Models: modelDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// page renders lines of black text on white, size pixels high, with a
// transparent background when transparent is set.
func page(t testing.TB, lines []string, size float64, transparent bool) *image.NRGBA {
	t.Helper()
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	img := image.NewNRGBA(image.Rect(0, 0, 1000, int(size*2)*len(lines)+int(size*2)))
	if !transparent {
		draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	}
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face}
	for i, l := range lines {
		d.Dot = fixed.P(int(size*2), int(size*2)*(i+1))
		d.DrawString(l)
	}
	return img
}

var invoice = []string{
	"Invoice 2026-0042",
	"Coffee beans 1 kg 18.90",
	"Whole milk 2.40",
	"Total amount EUR 21.30",
}

func lineTexts(p ocr.Page) []string {
	out := make([]string, len(p.Lines))
	for i, l := range p.Lines {
		out[i] = strings.TrimSpace(l.Text)
	}
	return out
}

func TestRecognize(t *testing.T) {
	e := testEngine(t)
	p, err := e.Recognize(context.Background(), page(t, invoice, 32, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := lineTexts(p); strings.Join(got, "\n") != strings.Join(invoice, "\n") {
		t.Fatalf("recognized %q, want %q", got, invoice)
	}
	for i, l := range p.Lines {
		if l.Confidence < 0.9 {
			t.Errorf("line %d confidence %.2f", i, l.Confidence)
		}
		if want := len(strings.Fields(invoice[i])); len(l.Words) != want {
			t.Errorf("line %d has %d words, want %d", i, len(l.Words), want)
		}
		if i > 0 && l.Quad[0].Y <= p.Lines[i-1].Quad[0].Y {
			t.Errorf("line %d is above the previous line", i)
		}
		if h := l.Quad.Height(); h < 20 || h > 70 {
			t.Errorf("line %d height %.1f for 32-pixel text", i, h)
		}
		// Words go left to right inside their line.
		for j, w := range l.Words {
			if j > 0 && w.Quad[0].X <= l.Words[j-1].Quad[0].X {
				t.Errorf("line %d word %d is left of the previous word", i, j)
			}
		}
	}
	if p.Text() != strings.Join(lineTexts(p), "\n") || p.Width != 1000 {
		t.Errorf("page text %q, width %d", p.Text(), p.Width)
	}
}

func TestRecognizeUpsideDownAndTransparent(t *testing.T) {
	e := testEngine(t)
	img := page(t, invoice, 32, true)
	turned := image.NewNRGBA(img.Rect)
	w, h := img.Rect.Dx(), img.Rect.Dy()
	for y := range h {
		for x := range w {
			turned.Set(w-1-x, h-1-y, img.At(x, y))
		}
	}
	p, err := e.Recognize(context.Background(), turned)
	if err != nil {
		t.Fatal(err)
	}
	// The page is upside down, so its last line comes first.
	got := lineTexts(p)
	if len(got) != len(invoice) {
		t.Fatalf("recognized %q", got)
	}
	for i, l := range got {
		if want := invoice[len(invoice)-1-i]; l != want {
			t.Errorf("line %d is %q, want %q", i, l, want)
		}
	}
	// Reading order corners: the text's top left is at the bottom right.
	if q := p.Lines[0].Quad; q[0].X <= q[1].X || q[0].Y <= q[3].Y {
		t.Errorf("quad %v is not in the turned text's reading order", q)
	}
}

func TestDetect(t *testing.T) {
	e := testEngine(t)
	img := page(t, invoice, 24, false)
	d, err := e.Detect(context.Background(), img, ocr.DetectOptions{MinScore: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Regions) != len(invoice) {
		t.Fatalf("detected %d regions, want %d", len(d.Regions), len(invoice))
	}
	if d.Width != img.Rect.Dx() || d.Scale(img.Rect) != 1 {
		t.Errorf("a small image is analyzed at full size, got preview %dx%d", d.Width, d.Height)
	}
	for i, r := range d.Regions {
		if r.Score < 0.5 || r.Score > 1 {
			t.Errorf("region %d score %.2f", i, r.Score)
		}
		if h := r.Quad.Height(); h < 15 || h > 55 {
			t.Errorf("region %d height %.1f for 24-pixel text", i, h)
		}
	}
	none, err := e.Detect(context.Background(), img, ocr.DetectOptions{MinScore: 1.01})
	if err != nil || len(none.Regions) != 0 {
		t.Errorf("a score above 1 keeps %d regions (%v)", len(none.Regions), err)
	}
}

func TestDetectMaxDimension(t *testing.T) {
	e := testEngine(t)
	// A tall 1000×3000 page: the default preview bounds its area, a
	// requested dimension only its longest side.
	lines := make([]string, 0, 60)
	for len(lines) < 60 {
		lines = append(lines, invoice...)
	}
	img := page(t, lines, 24, false)
	d, err := e.Detect(context.Background(), img, ocr.DetectOptions{MinScore: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	full, err := e.Detect(context.Background(), img, ocr.DetectOptions{MinScore: 0.5, MaxDimension: ocr.MaxDetectionDimension})
	if err != nil {
		t.Fatal(err)
	}
	if full.Height != ocr.MaxDetectionDimension || d.Height >= full.Height {
		t.Errorf("previews %dx%d (default) and %dx%d (requested)", d.Width, d.Height, full.Width, full.Height)
	}
	if len(full.Regions) != len(lines) {
		t.Errorf("detected %d of %d lines on the larger preview", len(full.Regions), len(lines))
	}
	for _, bad := range []int{100, ocr.MaxDetectionDimension + 1} {
		if _, err = e.Detect(context.Background(), img, ocr.DetectOptions{MaxDimension: bad}); err == nil {
			t.Errorf("dimension %d is accepted", bad)
		}
	}
}

func TestBlankPage(t *testing.T) {
	e := testEngine(t)
	blank := image.NewGray(image.Rect(0, 0, 640, 480))
	draw.Draw(blank, blank.Rect, image.NewUniform(color.White), image.Point{}, draw.Src)
	p, err := e.Recognize(context.Background(), blank)
	if err != nil || len(p.Lines) != 0 || p.Text() != "" {
		t.Fatalf("blank page: %v, %v", p.Lines, err)
	}
}

func TestConcurrentAndCancelled(t *testing.T) {
	e := testEngine(t)
	img := page(t, invoice[:2], 32, false)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			p, err := e.Recognize(context.Background(), img)
			if err == nil && len(p.Lines) != 2 {
				err = errors.New("wrong line count")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Recognize(ctx, img); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recognition returned %v", err)
	}
}

func TestNewChecksConfiguration(t *testing.T) {
	for _, c := range []Config{
		{Models: modelDir},
		{Library: "lib", Models: modelDir, Threads: 33},
		{Library: "lib", Models: modelDir, MaxDetectionDimension: 1000},
		{Library: "lib", Models: t.TempDir()},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("New(%+v) accepted", c)
		}
	}
	e, err := New(Config{Library: "lib", Models: modelDir})
	if err != nil {
		t.Fatal(err)
	}
	if e.cfg.Threads != 1 || e.cfg.MaxDetectionDimension != 1536 || len(e.Info().Models) != 4 {
		t.Errorf("defaults %+v, info %+v", e.cfg, e.Info())
	}
	// PaperDotNet's pinned detector.
	if got := e.Info().Models[0]; got.Name != DetectorModel || got.SHA256 != "d73e0058b7a8086bbd57f3d10b8bcd4ff95363f67e06e2762b5e814fe9c9410e" {
		t.Errorf("detector %+v", got)
	}
	if _, err = e.Detect(context.Background(), image.NewGray(image.Rect(0, 0, 9000, 8000)), ocr.DetectOptions{}); !errors.Is(err, ocr.ErrTooLarge) {
		t.Errorf("a 72-megapixel image returned %v", err)
	}
}

func TestDictionary(t *testing.T) {
	keys, err := readKeys(filepath.Join(modelDir, Dictionary))
	if err != nil {
		t.Fatal(err)
	}
	// The recognizer's 18,710 classes: blank, 18,708 characters, space.
	if len(keys) != 18710 || keys[0] != "#" || keys[len(keys)-1] != " " {
		t.Fatalf("%d keys, first %q, last %q", len(keys), keys[0], keys[len(keys)-1])
	}
}

func TestCTCDecode(t *testing.T) {
	keys := []string{"#", "a", "b", " "}
	// Columns: a, a (repeat), blank, a (new), b, space.
	best := []int{1, 1, 0, 1, 2, 3}
	probs := make([]float32, len(best)*len(keys))
	for t, c := range best {
		probs[t*len(keys)+c] = 0.9
	}
	chars, scores, cols := ctcDecode(probs, len(best), keys)
	if strings.Join(chars, "") != "aab " || len(scores) != 4 || cols[0] != 0 || cols[1] != 3 || cols[3] != 5 {
		t.Fatalf("decoded %q at %v", chars, cols)
	}
}

func TestWords(t *testing.T) {
	q := ocr.Quad{{0, 0}, {100, 0}, {100, 20}, {0, 20}}
	got := words([]string{"a", "b", " ", "c", "中", "文"}, []int{0, 1, 3, 5, 7, 8}, 10, q)
	want := []string{"ab", "c", "中", "文"}
	if len(got) != len(want) {
		t.Fatalf("words %+v", got)
	}
	for i, w := range got {
		if w.Text != want[i] {
			t.Errorf("word %d is %q, want %q", i, w.Text, want[i])
		}
	}
	// "ab" spans columns 0-1: from 0 (clamped) to (1+1.5)/10 of the width.
	if a := got[0].Quad; a[0] != (image.Point{0, 0}) || a[1].X != 25 || a[2].Y != 20 {
		t.Errorf("first word quad %v", a)
	}
}

func TestDimensions(t *testing.T) {
	for _, c := range []struct{ in, want int }{{10, 32}, {47, 32}, {48, 64}, {80, 64}, {1240, 1248}} {
		if got := networkDimension(c.in); got != c.want {
			t.Errorf("networkDimension(%d) = %d, want %d", c.in, got, c.want)
		}
	}
	// A square photo gets about a megapixel; a long receipt keeps the bound.
	if got := detectionDimension(4000, 4000, 1536); got != 1024 {
		t.Errorf("square: %d", got)
	}
	if got := detectionDimension(1000, 6000, 1536); got != 1536 {
		t.Errorf("receipt: %d", got)
	}
}

func TestBoxGeometry(t *testing.T) {
	// A rectangle of probability 0.9 from x 20-79 and y 10-29.
	rows, cols := 60, 120
	pred := make([]float32, rows*cols)
	for y := 10; y < 30; y++ {
		for x := 20; x < 80; x++ {
			pred[y*cols+x] = 0.9
		}
	}
	boxes := dbBoxes(pred, rows, cols, 0.5, detThreshold, unclipRatio)
	if len(boxes) != 1 {
		t.Fatalf("%d boxes", len(boxes))
	}
	b := boxes[0]
	// The mean covers the dilated region: 60×20 pixels at 0.9 in 62×22.
	if want := 0.9 * 60 * 20 / (62 * 22); math.Abs(b.score-want) > 0.01 {
		t.Errorf("score %.3f, want %.3f", b.score, want)
	}
	// Dilated to x 19-80, y 9-30 (61×21), then unclipped by 61·21·1.4/164.
	d := 61.0 * 21 * 1.4 / 164
	want := [4]point{{19 - d, 9 - d}, {80 + d, 9 - d}, {80 + d, 30 + d}, {19 - d, 30 + d}}
	for i, p := range b.pts {
		if math.Abs(p.X-want[i].X) > 1 || math.Abs(p.Y-want[i].Y) > 1 {
			t.Errorf("corner %d is %v, want about %v", i, p, want[i])
		}
	}
	if low := dbBoxes(pred, rows, cols, 0.85, detThreshold, unclipRatio); len(low) != 0 {
		t.Errorf("a box scoring 0.79 passes 0.85")
	}
	// A line one pixel high is too thin even after dilation.
	thin := make([]float32, rows*cols)
	for x := 20; x < 80; x++ {
		thin[40*cols+x] = 0.9
	}
	if got := dbBoxes(thin, rows, cols, 0.5, detThreshold, unclipRatio); len(got) != 0 {
		t.Errorf("a one-pixel line gave %d boxes", len(got))
	}
}

func TestMinAreaRectRotated(t *testing.T) {
	// A 40×10 rectangle turned 30 degrees.
	a := math.Pi / 6
	var pts []point
	for _, c := range [][2]float64{{0, 0}, {40, 0}, {40, 10}, {0, 10}} {
		pts = append(pts, point{100 + c[0]*math.Cos(a) - c[1]*math.Sin(a), 100 + c[0]*math.Sin(a) + c[1]*math.Cos(a)})
	}
	rect, w, h := minAreaRect(convexHull(pts))
	if math.Abs(math.Max(w, h)-40) > 1e-6 || math.Abs(math.Min(w, h)-10) > 1e-6 {
		t.Fatalf("rectangle %v×%v", w, h)
	}
	o := miniBoxOrder(rect)
	// Top left has the smaller y of the two leftmost corners.
	if o[0].X > o[1].X || o[3].X > o[2].X || o[0].Y > o[3].Y {
		t.Errorf("order %v", o)
	}
}

func TestCropRectifies(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 50, 50))
	draw.Draw(src, src.Rect, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(src, image.Rect(10, 20, 40, 30), image.NewUniform(color.Black), image.Point{}, draw.Src)
	line, q, err := crop(src, ocr.Quad{{10, 20}, {40, 20}, {40, 30}, {10, 30}})
	if err != nil {
		t.Fatal(err)
	}
	if line.Rect.Dx() != 30 || line.Rect.Dy() != 10 || q[0] != (image.Point{10, 20}) {
		t.Fatalf("crop %v, quad %v", line.Rect, q)
	}
	if c := line.RGBAAt(15, 5); c.R != 0 {
		t.Errorf("crop center is %v, want black", c)
	}
	// A tall region is vertical text, turned to read left to right.
	tall, q, err := crop(src, ocr.Quad{{20, 5}, {30, 5}, {30, 45}, {20, 45}})
	if err != nil {
		t.Fatal(err)
	}
	if tall.Rect.Dx() != 40 || tall.Rect.Dy() != 10 || q[0] != (image.Point{20, 45}) {
		t.Errorf("vertical crop %v, quad %v", tall.Rect, q)
	}
	if _, _, err = crop(src, ocr.Quad{{0, 0}, {200, 0}, {200, 1}, {0, 1}}); !errors.Is(err, errCropTooLarge) {
		t.Errorf("a 200:1 line: %v", err)
	}
}

// BenchmarkRecognize reads a page of 45 lines.
func BenchmarkRecognize(b *testing.B) {
	e := testEngine(b)
	lines := make([]string, 45)
	for i := range lines {
		lines[i] = fmt.Sprintf("Item %02d coffee beans and whole milk %d.%02d", i, 10+i, i)
	}
	img := page(b, lines, 24, false)
	for b.Loop() {
		p, err := e.Recognize(context.Background(), img)
		if err != nil || len(p.Lines) != len(lines) {
			b.Fatalf("%d lines, %v", len(p.Lines), err)
		}
	}
}
