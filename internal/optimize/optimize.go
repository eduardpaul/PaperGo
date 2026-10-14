// Package optimize makes smaller copies of page images without making their
// text unreadable. It measures the text lines an OCR detector finds, scales
// the image so that its smallest text keeps a target height in pixels, and
// encodes it as WebP. The analysis and its parameters are PaperDotNet's
// storage optimization, so both products shrink a file the same way.
package optimize

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"slices"
	"time"

	"papergo/internal/imaging"
	"papergo/internal/ocr"
)

// Options tune an optimization.
type Options struct {
	// TargetHeight is the height in pixels the smallest text keeps, 4 to 200.
	TargetHeight float64 `json:"target_height"`
	// MinConfidence is the detector box score a text line needs, 0 to 100.
	MinConfidence float64 `json:"min_confidence"`
	// Percentile of the text heights that counts as the smallest text,
	// 0 to 100: a low percentile ignores a few tiny false detections.
	Percentile float64 `json:"percentile"`
	// MaxAnalysisDimension bounds the longest side of the image the
	// detector analyzes, 256 to ocr.MaxDetectionDimension.
	MaxAnalysisDimension int `json:"max_analysis_dimension"`
	// MinimumScale bounds how far the image shrinks, above 0 to 1.
	MinimumScale float64 `json:"minimum_scale"`
	// Quality is the WebP quality, 1 to 100.
	Quality int `json:"quality"`
}

// Defaults are PaperDotNet's defaults.
var Defaults = Options{TargetHeight: 12, MinConfidence: 50, Percentile: 5, MaxAnalysisDimension: 2600, MinimumScale: 0.05, Quality: 80}

// Validate checks the options' ranges.
func (o Options) Validate() error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !finite(o.TargetHeight) || o.TargetHeight < 4 || o.TargetHeight > 200 ||
		!finite(o.MinConfidence) || o.MinConfidence < 0 || o.MinConfidence > 100 ||
		!finite(o.Percentile) || o.Percentile < 0 || o.Percentile > 100 ||
		!finite(o.MinimumScale) || o.MinimumScale <= 0 || o.MinimumScale > 1 ||
		o.MaxAnalysisDimension < 256 || o.MaxAnalysisDimension > ocr.MaxDetectionDimension || o.Quality < 1 || o.Quality > 100 {
		return fmt.Errorf("invalid optimization settings: target height 4 to 200 px, confidence and percentile 0 to 100, minimum scale above 0 to 1, analysis dimension 256 to %d px, quality 1 to 100", ocr.MaxDetectionDimension)
	}
	return nil
}

// Reasons an image is kept unchanged.
const (
	ReasonAnimated   = "Animated images are kept unchanged."
	ReasonNoText     = "No reliable text was detected; the original is kept."
	ReasonOptimized  = "The image is already WebP with its smallest text at the target height."
	ReasonNotSmaller = "The smaller image is not a smaller file; the original is kept."
)

// Metrics describe an analysis and its result, for reviewers and run
// history. Fields of steps that did not happen are left out.
type Metrics struct {
	Detector           string      `json:"detector"`
	Models             []ocr.Model `json:"models,omitempty"`
	TextRegions        int         `json:"text_regions"`
	AnalysisMS         float64     `json:"analysis_ms"`
	AnalysisDimension  int         `json:"analysis_dimension"`
	AnalysisScale      float64     `json:"analysis_scale"`
	TargetHeight       float64     `json:"target_height"`
	MinConfidence      float64     `json:"min_confidence"`
	Percentile         float64     `json:"percentile"`
	MinimumScale       float64     `json:"minimum_scale"`
	Quality            int         `json:"quality"`
	SourceWidth        int         `json:"source_width"`
	SourceHeight       int         `json:"source_height"`
	SourceBytes        int64       `json:"source_bytes"`
	Orientation        int         `json:"orientation,omitempty"`
	SmallestTextHeight float64     `json:"smallest_text_height,omitempty"`
	Scale              float64     `json:"scale,omitempty"`
	Width              int         `json:"width,omitempty"`
	Height             int         `json:"height,omitempty"`
	Bytes              int64       `json:"bytes,omitempty"`
	SavingsPercent     float64     `json:"savings_percent,omitempty"`
}

// Result is an optimization's outcome: a smaller WebP file, or the reason
// the original is kept.
type Result struct {
	// WebP is the smaller file; nil when the original is kept.
	WebP    []byte
	Reason  string
	Metrics Metrics
}

// Image optimizes an encoded JPEG, PNG or WebP image of at most maxPixels.
// It fails for files it cannot decode; it keeps animated images, images
// without reliable text and images it cannot make smaller.
func Image(ctx context.Context, engine ocr.Engine, data []byte, maxPixels int64, o Options) (Result, error) {
	if err := o.Validate(); err != nil {
		return Result{}, err
	}
	info := engine.Info()
	m := Metrics{Detector: info.Engine, Models: info.Models, AnalysisDimension: o.MaxAnalysisDimension, TargetHeight: o.TargetHeight,
		MinConfidence: o.MinConfidence, Percentile: o.Percentile, MinimumScale: o.MinimumScale, Quality: o.Quality, SourceBytes: int64(len(data))}
	img, err := imaging.Decode(data, maxPixels)
	if errors.Is(err, imaging.ErrAnimated) {
		return Result{Reason: ReasonAnimated, Metrics: m}, nil
	}
	if err != nil {
		return Result{}, err
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	m.SourceWidth, m.SourceHeight, m.Orientation = w, h, img.Orientation
	start := time.Now()
	heights, k, err := Analyze(ctx, engine, img, o.MinConfidence, o.MaxAnalysisDimension)
	if err != nil {
		return Result{}, err
	}
	m.AnalysisScale = k
	m.AnalysisMS = float64(time.Since(start).Microseconds()) / 1000
	m.TextRegions = len(heights)
	if len(heights) == 0 {
		return Result{Reason: ReasonNoText, Metrics: m}, nil
	}
	m.SmallestTextHeight = math.Max(4, Percentile(heights, o.Percentile))
	m.Scale = ScaleForHeight(m.SmallestTextHeight, o)
	if m.Scale >= 1 && img.MediaType == "image/webp" {
		return Result{Reason: ReasonOptimized, Metrics: m}, nil
	}
	m.Width, m.Height = max(1, round(float64(w)*m.Scale)), max(1, round(float64(h)*m.Scale))
	// The cubic filter is not interpolating, so an image that keeps its
	// size is encoded as it is.
	out := img.Image
	if m.Width != w || m.Height != h {
		out = imaging.Resize(img, m.Width, m.Height)
	}
	if err = ctx.Err(); err != nil {
		return Result{}, err
	}
	webp, err := imaging.EncodeWebP(out, o.Quality)
	if err != nil {
		return Result{}, fmt.Errorf("encode WebP: %w", err)
	}
	m.Bytes = int64(len(webp))
	m.SavingsPercent = 100 * float64(m.SourceBytes-m.Bytes) / float64(m.SourceBytes)
	if m.Bytes >= m.SourceBytes {
		return Result{Reason: ReasonNotSmaller, Metrics: m}, nil
	}
	return Result{WebP: webp, Metrics: m}, nil
}

// Analyze measures the reliable text lines of an upright image: it detects
// text on a copy at most maxDimension pixels long and returns the lines'
// heights in the image's pixels (see TextHeights), and the copy's scale.
func Analyze(ctx context.Context, engine ocr.Engine, img image.Image, minConfidence float64, maxDimension int) ([]float64, float64, error) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	k := math.Min(1, float64(maxDimension)/float64(max(w, h)))
	aw, ah := max(1, round(float64(w)*k)), max(1, round(float64(h)*k))
	analysis := img
	if aw != w || ah != h {
		analysis = imaging.Resize(img, aw, ah)
	}
	det, err := engine.Detect(ctx, analysis, ocr.DetectOptions{MinScore: minConfidence / 100, MaxDimension: maxDimension})
	if err != nil {
		return nil, k, fmt.Errorf("text analysis failed: %w", err)
	}
	return TextHeights(det.Regions, minConfidence, float64(w)/float64(aw), float64(h)/float64(ah)), k, nil
}

// TextHeights returns the heights in source pixels of the reliable text
// lines among regions, which are in analysis pixels; sx and sy are the
// source size divided by the analysis size. A reliable line scores at
// least minConfidence (0 to 100), is at least 6 analysis pixels high, and
// is at least 1.5 times as long as high.
func TextHeights(regions []ocr.Region, minConfidence, sx, sy float64) []float64 {
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

// Percentile interpolates the p-th percentile (0 to 100) of heights and
// rounds it to a whole pixel, halves to even.
func Percentile(heights []float64, p float64) float64 {
	sorted := slices.Clone(heights)
	slices.Sort(sorted)
	index := p / 100 * float64(len(sorted)-1)
	lower, upper := int(math.Floor(index)), int(math.Ceil(index))
	return math.RoundToEven(sorted[lower]*(1-(index-float64(lower))) + sorted[upper]*(index-float64(lower)))
}

// ScaleForHeight is the scale that brings text of height to the target,
// between the minimum scale and 1: images never grow.
func ScaleForHeight(height float64, o Options) float64 {
	return math.Max(o.MinimumScale, math.Min(1, o.TargetHeight/height))
}

func round(v float64) int { return int(math.RoundToEven(v)) }
