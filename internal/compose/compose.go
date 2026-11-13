// Package compose makes files from photos. Each photo is turned upright,
// flattened onto white and sized by Options: to keep its smallest text a
// given height, measured by OCR, within maximum and minimum dimensions, and
// in color or gray. PDF combines page photos into one PDF, searchable with
// an invisible layer of the text OCR recognized; Image makes a single photo
// a JPEG. It ports PaperDotNet's photo to document conversion and image
// storage optimization.
package compose

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"slices"

	"papergo/internal/imaging"
	"papergo/internal/ocr"
	"papergo/internal/pdf"
)

// The colors a photo is kept in.
const (
	ColorRGB  = "rgb"
	ColorGray = "gray"
)

// Options size and encode a photo, as a PDF page or as an image. Every
// sizing option is off when zero. Photos never grow: the size is the one
// that keeps the smallest text TextHeight high, at most MaxWidth×MaxHeight,
// and, unless that exceeds a maximum or the photo, at least
// MinWidth×MinHeight.
type Options struct {
	// TextHeight is the height in pixels, 8 to 200, the smallest reliable
	// text line keeps; it needs OCR to measure the lines.
	TextHeight float64 `json:"text_height,omitempty"`
	// MaxWidth and MaxHeight bound the size in pixels.
	MaxWidth  int `json:"max_width,omitempty"`
	MaxHeight int `json:"max_height,omitempty"`
	// MinWidth and MinHeight are the size in pixels shrinking stops at.
	MinWidth  int `json:"min_width,omitempty"`
	MinHeight int `json:"min_height,omitempty"`
	// Quality is the JPEG quality, 1 to 100.
	Quality int `json:"quality"`
	// Color is ColorRGB or ColorGray.
	Color string `json:"color"`
}

// Defaults leave the size alone and keep the color, at JPEG quality 80.
var Defaults = Options{Quality: 80, Color: ColorRGB}

// maxDimension bounds the sizing options.
const maxDimension = 100_000

// Text analysis settings, as in PaperDotNet.
const (
	minConfidence     = 50
	analysisDimension = 2600
)

// MaxPages bounds a composition.
const MaxPages = 100

// errAnimated rejects animated photos, which have no single page.
var errAnimated = errors.New("animated images cannot be combined into a PDF")

// Validate checks the options' ranges.
func (o Options) Validate() error {
	if o.TextHeight != 0 && (math.IsNaN(o.TextHeight) || o.TextHeight < 8 || o.TextHeight > 200) {
		return errors.New("invalid text height: 8 to 200 px")
	}
	for _, d := range []int{o.MaxWidth, o.MaxHeight, o.MinWidth, o.MinHeight} {
		if d < 0 || d > maxDimension {
			return fmt.Errorf("invalid size: dimensions are 1 to %d px", maxDimension)
		}
	}
	if (o.MaxWidth > 0 && o.MinWidth > o.MaxWidth) || (o.MaxHeight > 0 && o.MinHeight > o.MaxHeight) {
		return errors.New("invalid size: a minimum is larger than its maximum")
	}
	if o.Quality < 1 || o.Quality > 100 {
		return errors.New("invalid quality: 1 to 100")
	}
	if o.Color != ColorRGB && o.Color != ColorGray {
		return errors.New(`invalid color: "rgb" or "gray"`)
	}
	return nil
}

// NeedsOCR says whether the options measure text.
func (o Options) NeedsOCR() bool { return o.TextHeight > 0 }

// Page describes how a photo became a page or an image.
type Page struct {
	SourceWidth  int `json:"source_width"`
	SourceHeight int `json:"source_height"`
	Width        int `json:"width"`
	Height       int `json:"height"`
	// SmallestTextHeight is the smallest reliable text line, in source
	// pixels; zero when no text was measured.
	SmallestTextHeight float64 `json:"smallest_text_height,omitempty"`
	Scale              float64 `json:"scale"`
	// MeasuredLines are the reliable text lines measured for TextHeight.
	MeasuredLines int `json:"measured_lines"`
	// TextLines are the lines of a PDF page's text layer.
	TextLines int `json:"text_lines"`
	// Orientation is the EXIF orientation applied, when not 1.
	Orientation int `json:"orientation,omitempty"`
}

// Metrics describe a composition or an image, for reviewers and run history.
type Metrics struct {
	Options
	Pages       int         `json:"pages"`
	TextLines   int         `json:"text_lines"`
	SourceBytes int64       `json:"source_bytes"`
	Bytes       int64       `json:"bytes"`
	Detector    string      `json:"detector,omitempty"`
	Models      []ocr.Model `json:"models,omitempty"`
	PageDetails []Page      `json:"page_details"`
}

func metrics(engine ocr.Engine, o Options) Metrics {
	m := Metrics{Options: o}
	if engine != nil {
		info := engine.Info()
		m.Detector, m.Models = info.Engine, info.Models
	}
	return m
}

// PDF writes the photos as a PDF to w, one page per photo in order, with a
// text layer of what OCR recognizes on each when text is set. photo returns
// the encoded JPEG, PNG or WebP file of page i, of at most maxPixels; pages
// are read and processed one at a time. engine may be nil when neither the
// options nor text need OCR.
func PDF(ctx context.Context, engine ocr.Engine, w io.Writer, pages int, photo func(i int) ([]byte, error), maxPixels int64, o Options, text bool) (Metrics, error) {
	if err := o.Validate(); err != nil {
		return Metrics{}, err
	}
	if pages < 1 || pages > MaxPages {
		return Metrics{}, fmt.Errorf("a PDF combines 1 to %d photos", MaxPages)
	}
	if engine == nil && (o.NeedsOCR() || text) {
		return Metrics{}, errors.New("text height and the text layer need OCR")
	}
	m := metrics(engine, o)
	counter := &countingWriter{w: w}
	out, err := pdf.NewWriter(counter, o.Quality)
	if err != nil {
		return Metrics{}, err
	}
	for i := range pages {
		data, err := photo(i)
		if err != nil {
			return Metrics{}, err
		}
		m.SourceBytes += int64(len(data))
		page, details, err := prepare(ctx, engine, data, maxPixels, o, text)
		if err != nil {
			return Metrics{}, fmt.Errorf("page %d: %w", i+1, err)
		}
		if err = out.AddPage(page); err != nil {
			return Metrics{}, fmt.Errorf("page %d: %w", i+1, err)
		}
		m.PageDetails = append(m.PageDetails, details)
		m.TextLines += details.TextLines
	}
	if err = out.Close(); err != nil {
		return Metrics{}, err
	}
	m.Pages, m.Bytes = pages, counter.n
	return m, nil
}

// Image writes a photo as a JPEG sized and colored by o. data is the
// encoded JPEG, PNG or WebP file, of at most maxPixels. engine may be nil
// when the options need no OCR.
func Image(ctx context.Context, engine ocr.Engine, data []byte, maxPixels int64, o Options) ([]byte, Metrics, error) {
	if err := o.Validate(); err != nil {
		return nil, Metrics{}, err
	}
	if engine == nil && o.NeedsOCR() {
		return nil, Metrics{}, errors.New("text height needs OCR")
	}
	m := metrics(engine, o)
	m.Pages, m.SourceBytes = 1, int64(len(data))
	upright, p, err := fit(ctx, engine, data, maxPixels, o)
	if err != nil {
		return nil, Metrics{}, err
	}
	out, err := imaging.EncodeJPEG(render(upright, p, o), o.Quality)
	if err != nil {
		return nil, Metrics{}, err
	}
	m.Bytes, m.PageDetails = int64(len(out)), []Page{p}
	return out, m, nil
}

// fit decodes a photo, turns it upright, flattens it onto white and works
// out its size by o: the upright photo and its page.
func fit(ctx context.Context, engine ocr.Engine, data []byte, maxPixels int64, o Options) (*image.RGBA, Page, error) {
	img, err := imaging.Decode(data, maxPixels)
	if errors.Is(err, imaging.ErrAnimated) {
		return nil, Page{}, errAnimated
	}
	if err != nil {
		return nil, Page{}, err
	}
	upright := imaging.Flatten(img)
	w, h := upright.Rect.Dx(), upright.Rect.Dy()
	p := Page{SourceWidth: w, SourceHeight: h}
	if img.Orientation > 1 {
		p.Orientation = img.Orientation
	}
	if o.NeedsOCR() {
		heights, err := analyze(ctx, engine, upright, minConfidence, analysisDimension)
		if err != nil {
			return nil, Page{}, err
		}
		p.MeasuredLines = len(heights)
		if len(heights) > 0 {
			p.SmallestTextHeight = slices.Min(heights)
		}
	}
	p.Scale = scale(w, h, p.SmallestTextHeight, o)
	p.Width, p.Height = w, h
	if p.Scale < 1 {
		p.Width, p.Height = max(1, round(float64(w)*p.Scale)), max(1, round(float64(h)*p.Scale))
	}
	return upright, p, nil
}

// scale is the factor, at most 1, that sizes a w×h photo whose smallest
// text is smallest pixels high (zero when unknown) by o: the text height,
// within the maximums, and at least the minimums unless a maximum or the
// photo itself is smaller.
func scale(w, h int, smallest float64, o Options) float64 {
	limit := 1.0
	if o.MaxWidth > 0 {
		limit = math.Min(limit, float64(o.MaxWidth)/float64(w))
	}
	if o.MaxHeight > 0 {
		limit = math.Min(limit, float64(o.MaxHeight)/float64(h))
	}
	s := limit
	if o.TextHeight > 0 && smallest > 0 {
		s = math.Min(s, o.TextHeight/smallest)
	}
	floor := 0.0
	if o.MinWidth > 0 {
		floor = math.Max(floor, float64(o.MinWidth)/float64(w))
	}
	if o.MinHeight > 0 {
		floor = math.Max(floor, float64(o.MinHeight)/float64(h))
	}
	return math.Min(limit, math.Max(s, floor))
}

// render is the upright photo at its page's size, in the options' color.
func render(upright *image.RGBA, p Page, o Options) image.Image {
	var img image.Image = upright
	if p.Width != p.SourceWidth || p.Height != p.SourceHeight {
		img = imaging.Resize(upright, p.Width, p.Height)
	}
	if o.Color == ColorGray {
		img = imaging.Gray(img)
	}
	return img
}

// prepare turns a photo into a PDF page sized by o, with, when text is set,
// the text OCR read on the full-resolution photo placed at the scaled
// positions.
func prepare(ctx context.Context, engine ocr.Engine, data []byte, maxPixels int64, o Options, text bool) (pdf.Page, Page, error) {
	upright, p, err := fit(ctx, engine, data, maxPixels, o)
	if err != nil {
		return pdf.Page{}, Page{}, err
	}
	page := pdf.Page{Image: render(upright, p, o)}
	if !text {
		return page, p, nil
	}
	read, err := engine.Recognize(ctx, upright)
	if err != nil {
		return pdf.Page{}, Page{}, fmt.Errorf("text recognition failed: %w", err)
	}
	sx, sy := float64(p.Width)/float64(p.SourceWidth), float64(p.Height)/float64(p.SourceHeight)
	for _, l := range read.Lines {
		var q [4]pdf.Point
		for i, c := range l.Quad {
			q[i] = pdf.Point{X: float64(c.X) * sx, Y: float64(c.Y) * sy}
		}
		page.Lines = append(page.Lines, pdf.Text{Text: l.Text, Quad: q})
	}
	p.TextLines = len(page.Lines)
	return page, p, nil
}

func round(v float64) int { return int(math.RoundToEven(v)) }

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
