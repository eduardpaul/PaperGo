package compose

import (
	"context"
	"image"
	"math"
	"testing"

	"papergo/internal/ocr"
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
		got := textHeights([]ocr.Region{{Quad: q, Score: 0.9}}, 50, 2, 2)
		if len(got) != 1 || got[0] < 39 || got[0] > 41 {
			t.Errorf("%v°: heights %v, want about 40", angle, got)
		}
	}
	if got := textHeights([]ocr.Region{{Quad: rect(100, 20), Score: 0.9}}, 50, 2, 3); len(got) != 1 || got[0] != 60 {
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
		if got := textHeights([]ocr.Region{r}, 50, 1, 1); len(got) != 0 {
			t.Errorf("%s region kept: %v", name, got)
		}
	}
}

// recorder finds fixed regions, in the analyzed image's pixels, and
// records what it analyzed.
type recorder struct {
	regions  []ocr.Region
	analyzed image.Rectangle
	opts     ocr.DetectOptions
}

func (r *recorder) Detect(_ context.Context, img image.Image, opts ocr.DetectOptions) (ocr.Detection, error) {
	r.analyzed, r.opts = img.Bounds(), opts
	return ocr.Detection{Regions: r.regions, Width: img.Bounds().Dx(), Height: img.Bounds().Dy()}, nil
}
func (*recorder) Recognize(context.Context, image.Image) (ocr.Page, error) { return ocr.Page{}, nil }
func (*recorder) Info() ocr.Info                                           { return ocr.Info{Engine: "recorder"} }
func (*recorder) Close() error                                             { return nil }

func TestAnalyzeDetectsOnABoundedCopy(t *testing.T) {
	// Analysis at 2600 px: 2600×1300, so heights project by 3000/2600.
	r := &recorder{regions: []ocr.Region{{Quad: rect(400, 26), Score: 0.9}, {Quad: rect(400, 52), Score: 0.9}}}
	heights, err := analyze(context.Background(), r, image.NewGray(image.Rect(0, 0, 3000, 1500)), minConfidence, analysisDimension)
	if err != nil {
		t.Fatal(err)
	}
	if r.analyzed.Dx() != 2600 || r.analyzed.Dy() != 1300 || r.opts.MaxDimension != 2600 || r.opts.MinScore != 0.5 {
		t.Errorf("analyzed %v with %+v", r.analyzed, r.opts)
	}
	if len(heights) != 2 || math.Abs(heights[0]-30) > 1e-9 || math.Abs(heights[1]-60) > 1e-9 {
		t.Errorf("heights %v, want 30 and 60", heights)
	}
}
