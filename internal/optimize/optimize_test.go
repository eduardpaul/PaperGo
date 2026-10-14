package optimize

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"papergo/internal/imaging"
	"papergo/internal/ocr"
	"papergo/internal/ocr/paddle"
)

func rect(w, h int) ocr.Quad {
	return ocr.Quad{{0, 0}, {w, 0}, {w, h}, {0, h}}
}

func TestTextHeightsProjectRotatedBoxesOnBothAxes(t *testing.T) {
	for _, angle := range []float64{0, 30, 90, 180} {
		rad := angle * math.Pi / 180
		rot := func(x, y float64) image.Point {
			return image.Pt(int(math.RoundToEven(x*math.Cos(rad)-y*math.Sin(rad))), int(math.RoundToEven(x*math.Sin(rad)+y*math.Cos(rad))))
		}
		q := ocr.Quad{rot(0, 0), rot(100, 0), rot(100, 20), rot(0, 20)}
		got := TextHeights([]ocr.Region{{Quad: q, Score: 0.9}}, 50, 2, 2)
		if len(got) != 1 || got[0] < 39 || got[0] > 41 {
			t.Errorf("%v°: heights %v, want about 40", angle, got)
		}
	}
	if got := TextHeights([]ocr.Region{{Quad: rect(100, 20), Score: 0.9}}, 50, 2, 3); len(got) != 1 || got[0] != 60 {
		t.Errorf("heights %v, want 60", got)
	}
}

func TestTextHeightsFilterUnreliableRegions(t *testing.T) {
	for name, r := range map[string]ocr.Region{
		"low score":  {Quad: rect(100, 20), Score: 0.49},
		"no score":   {Quad: rect(100, 20), Score: math.NaN()},
		"tiny":       {Quad: rect(100, 5), Score: 0.9},
		"square":     {Quad: rect(10, 10), Score: 0.9},
		"degenerate": {Quad: ocr.Quad{}, Score: 0.9},
	} {
		if got := TextHeights([]ocr.Region{r}, 50, 1, 1); len(got) != 0 {
			t.Errorf("%s region kept: %v", name, got)
		}
	}
}

func TestPercentileInterpolatesAndRoundsToEven(t *testing.T) {
	for _, c := range []struct {
		heights []float64
		p, want float64
	}{
		{[]float64{10, 110}, 5, 15},
		{[]float64{10}, 5, 10},
		{[]float64{10, 110}, 100, 110},
		{[]float64{30, 10, 20}, 50, 20},
		// 10.5 rounds to even.
		{[]float64{10, 11}, 50, 10},
		{[]float64{11, 12}, 50, 12},
	} {
		if got := Percentile(c.heights, c.p); got != c.want {
			t.Errorf("Percentile(%v, %v) = %v, want %v", c.heights, c.p, got, c.want)
		}
	}
}

func TestScaleRespectsTargetMinimumAndNeverGrows(t *testing.T) {
	for _, c := range []struct{ height, want float64 }{{6, 1}, {24, 0.5}, {1000, 0.05}} {
		if got := ScaleForHeight(c.height, Defaults); got != c.want {
			t.Errorf("ScaleForHeight(%v) = %v, want %v", c.height, got, c.want)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Defaults.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Options){
		func(o *Options) { o.TargetHeight = 3 },
		func(o *Options) { o.TargetHeight = math.NaN() },
		func(o *Options) { o.MinConfidence = 101 },
		func(o *Options) { o.Percentile = -1 },
		func(o *Options) { o.MinimumScale = 0 },
		func(o *Options) { o.MinimumScale = 1.5 },
		func(o *Options) { o.MaxAnalysisDimension = 255 },
		func(o *Options) { o.MaxAnalysisDimension = ocr.MaxDetectionDimension + 1 },
		func(o *Options) { o.Quality = 0 },
	} {
		o := Defaults
		change(&o)
		if o.Validate() == nil {
			t.Errorf("%+v is accepted", o)
		}
	}
}

// fake is an engine that finds fixed regions, in the analyzed image's
// pixels, and records what it analyzed.
type fake struct {
	regions  []ocr.Region
	analyzed image.Rectangle
	opts     ocr.DetectOptions
	calls    int
}

func (f *fake) Detect(_ context.Context, img image.Image, opts ocr.DetectOptions) (ocr.Detection, error) {
	f.calls++
	f.analyzed, f.opts = img.Bounds(), opts
	return ocr.Detection{Regions: f.regions, Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}, nil
}
func (f *fake) Recognize(context.Context, image.Image) (ocr.Page, error) { return ocr.Page{}, nil }
func (f *fake) Info() ocr.Info                                           { return ocr.Info{Engine: "fake"} }
func (f *fake) Close() error                                             { return nil }

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// noisy is a w×h image that compresses badly as PNG.
func noisy(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(7)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = byte(seed >> 24)
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	return img
}

func saltAndPepper(w, h int) *image.Paletted {
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.Black, color.White})
	seed := uint32(3)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = uint8(seed >> 31)
	}
	return img
}

func TestImageScalesToTheSmallestTextAndAnalyzesABoundedCopy(t *testing.T) {
	src := encodePNG(t, noisy(3000, 1500))
	// Analysis at 2600 px: 2600×1300, so heights project by 3000/2600.
	f := &fake{regions: []ocr.Region{
		{Quad: rect(400, 26), Score: 0.9},
		{Quad: rect(400, 52), Score: 0.9},
		{Quad: rect(400, 104), Score: 0.9},
	}}
	res, err := Image(context.Background(), f, src, 64e6, Defaults)
	if err != nil {
		t.Fatal(err)
	}
	if f.analyzed.Dx() != 2600 || f.analyzed.Dy() != 1300 || f.opts.MaxDimension != 2600 || f.opts.MinScore != 0.5 {
		t.Errorf("analyzed %v with %+v", f.analyzed, f.opts)
	}
	m := res.Metrics
	// The 5th percentile of 30, 60 and 120 interpolates to 33.
	if m.TextRegions != 3 || m.SmallestTextHeight != 33 || math.Abs(m.Scale-12.0/33) > 1e-12 {
		t.Errorf("metrics %+v", m)
	}
	if res.WebP == nil || res.Reason != "" {
		t.Fatalf("kept the original: %s", res.Reason)
	}
	out, err := imaging.Decode(res.WebP, 64e6)
	if err != nil {
		t.Fatal(err)
	}
	if out.MediaType != "image/webp" || out.Bounds().Dx() != m.Width || out.Bounds().Dy() != m.Height || m.Width != 1091 || m.Height != 545 {
		t.Errorf("result %s %v, metrics %dx%d", out.MediaType, out.Bounds(), m.Width, m.Height)
	}
	if m.Bytes != int64(len(res.WebP)) || m.SourceBytes != int64(len(src)) || m.SavingsPercent <= 0 {
		t.Errorf("sizes %+v", m)
	}
}

func TestImageKeepsTheOriginal(t *testing.T) {
	small := encodePNG(t, image.NewGray(image.Rect(0, 0, 400, 300)))
	text := []ocr.Region{{Quad: rect(200, 12), Score: 0.9}}
	webp, err := imaging.EncodeWebP(noisy(400, 300), 80)
	if err != nil {
		t.Fatal(err)
	}
	apng, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACGFjVEwAAAACAAAAAPONk3AAAAAaZmNUTAAAAAAAAAABAAAAAQAAAAAAAAAAAAEACgAAWn8w0AAAAA1JREFUeJxj+M/A8B8ABQAB/4mZPR0AAAAaZmNUTAAAAAEAAAABAAAAAQAAAAAAAAAAAAEACgAAwQzaBAAAABFmZEFUAAAAAnicY2Bg+P8fAAMCAf/1e6XXAAAAAElFTkSuQmCC")
	for _, c := range []struct {
		name    string
		data    []byte
		regions []ocr.Region
		reason  string
		detects int
	}{
		{"animated", apng, text, ReasonAnimated, 0},
		{"no text", small, nil, ReasonNoText, 1},
		// A two-color PNG of salt-and-pepper noise takes a bit per pixel;
		// lossy WebP takes far more.
		{"not smaller", encodePNG(t, saltAndPepper(300, 200)), text, ReasonNotSmaller, 1},
		{"already optimized", webp, text, ReasonOptimized, 1},
	} {
		f := &fake{regions: c.regions}
		res, err := Image(context.Background(), f, c.data, 64e6, Defaults)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.WebP != nil || res.Reason != c.reason || f.calls != c.detects {
			t.Errorf("%s: reason %q, %d detections, file %t", c.name, res.Reason, f.calls, res.WebP != nil)
		}
	}
}

func TestImageFailsForUnreadableFiles(t *testing.T) {
	f := &fake{}
	if _, err := Image(context.Background(), f, []byte("%PDF-1.7"), 64e6, Defaults); !errors.Is(err, imaging.ErrUnsupported) {
		t.Errorf("PDF: %v", err)
	}
	if _, err := Image(context.Background(), f, encodePNG(t, noisy(100, 100)), 5000, Defaults); !errors.Is(err, imaging.ErrTooLarge) {
		t.Errorf("pixel limit: %v", err)
	}
	bad := Defaults
	bad.Quality = 0
	if _, err := Image(context.Background(), f, encodePNG(t, noisy(10, 10)), 64e6, bad); err == nil {
		t.Error("invalid options are accepted")
	}
}

// TestImageWithPaddleOCR shrinks a rendered page so its smallest text
// lines are about 12 pixels high. It needs ONNX Runtime (ONNXRUNTIME_LIB).
func TestImageWithPaddleOCR(t *testing.T) {
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
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 48, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	page := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	draw.Draw(page, page.Rect, image.NewUniform(color.White), image.Point{}, draw.Src)
	d := font.Drawer{Dst: page, Src: image.NewUniform(color.Black), Face: face}
	for i, line := range []string{"Invoice 2026-0042", "Coffee beans 1 kg 18.90", "Whole milk 2.40", "Total amount EUR 21.30"} {
		d.Dot = fixed.P(96, 160+i*120)
		d.DrawString(line)
	}
	// Light scanner noise, so PNG does not compress the page to nothing.
	seed := uint32(1)
	for i := 0; i < len(page.Pix); i += 4 {
		seed = seed*1664525 + 1013904223
		if seed>>28 == 0 {
			page.Pix[i], page.Pix[i+1], page.Pix[i+2] = 230, 230, 230
		}
	}
	res, err := Image(context.Background(), e, encodePNG(t, page), 64e6, Defaults)
	if err != nil {
		t.Fatal(err)
	}
	m := res.Metrics
	if res.WebP == nil {
		t.Fatalf("kept the original: %s (%+v)", res.Reason, m)
	}
	// Detected line boxes of 48-point text are 40 to 70 pixels high.
	if m.TextRegions != 4 || m.SmallestTextHeight < 40 || m.SmallestTextHeight > 70 || m.Detector != "paddleocr" || len(m.Models) != 4 {
		t.Errorf("metrics %+v", m)
	}
	if want := 12 / m.SmallestTextHeight; math.Abs(m.Scale-want) > 1e-9 || m.Width != int(math.RoundToEven(1600*want)) {
		t.Errorf("scale %v and width %d for smallest text %v", m.Scale, m.Width, m.SmallestTextHeight)
	}
}
