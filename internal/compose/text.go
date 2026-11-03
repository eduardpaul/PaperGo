package compose

import (
	"context"
	"fmt"
	"image"
	"math"

	"papergo/internal/imaging"
	"papergo/internal/ocr"
)

// analyze measures the reliable text lines of an upright image: it detects
// text on a copy at most maxDimension pixels long and returns the lines'
// heights in the image's pixels (see textHeights).
func analyze(ctx context.Context, engine ocr.Engine, img image.Image, minConfidence float64, maxDimension int) ([]float64, error) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	k := math.Min(1, float64(maxDimension)/float64(max(w, h)))
	aw, ah := max(1, round(float64(w)*k)), max(1, round(float64(h)*k))
	analysis := img
	if aw != w || ah != h {
		analysis = imaging.Resize(img, aw, ah)
	}
	det, err := engine.Detect(ctx, analysis, ocr.DetectOptions{MinScore: minConfidence / 100, MaxDimension: maxDimension})
	if err != nil {
		return nil, fmt.Errorf("text analysis failed: %w", err)
	}
	return textHeights(det.Regions, minConfidence, float64(w)/float64(aw), float64(h)/float64(ah)), nil
}

// textHeights returns the heights in source pixels of the reliable text
// lines among regions, which are in analysis pixels; sx and sy are the
// source size divided by the analysis size. A reliable line scores at
// least minConfidence (0 to 100), is at least 6 analysis pixels high, and
// is at least 1.5 times as long as high.
func textHeights(regions []ocr.Region, minConfidence, sx, sy float64) []float64 {
	out := make([]float64, 0, len(regions))
	for _, r := range regions {
		if math.IsNaN(r.Score) || r.Score*100 < minConfidence {
			continue
		}
		short, long := r.Quad.Height(), r.Quad.Length()
		if short < 6 || long/short < 1.5 {
			continue
		}
		// The shortest side in source pixels, also for vertical text.
		height := math.Inf(1)
		for i := range 4 {
			a, b := r.Quad[i], r.Quad[(i+1)%4]
			height = math.Min(height, math.Hypot(float64(a.X-b.X)*sx, float64(a.Y-b.Y)*sy))
		}
		if !math.IsInf(height, 0) && height > 3 {
			out = append(out, height)
		}
	}
	return out
}
