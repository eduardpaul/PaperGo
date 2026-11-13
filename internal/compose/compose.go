// Package compose makes files from photos, each turned upright and shrunk
// so its smallest text keeps a readable height. PDF combines page photos
// into one searchable PDF, with an invisible text layer at the positions OCR
// recognized; Image makes a single photo the smaller JPEG it can be without
// that text getting smaller. It ports PaperDotNet's photo to document
// conversion and image storage optimization.
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

// Options tune a composition or an image.
type Options struct {
	// TargetHeight is the height in pixels the smallest text of each page
	// keeps, 8 to 200. Pages never grow.
	TargetHeight float64 `json:"target_height"`
	// Quality is the JPEG quality of the images, 1 to 100.
	Quality int `json:"quality"`
}

// Defaults are PaperDotNet's defaults.
var Defaults = Options{TargetHeight: 32, Quality: 80}

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
	if math.IsNaN(o.TargetHeight) || o.TargetHeight < 8 || o.TargetHeight > 200 || o.Quality < 1 || o.Quality > 100 {
		return errors.New("invalid composition settings: target height 8 to 200 px, quality 1 to 100")
	}
	return nil
}

// Page describes how a photo became a page or an image.
type Page struct {
	SourceWidth  int `json:"source_width"`
	SourceHeight int `json:"source_height"`
	Width        int `json:"width"`
	Height       int `json:"height"`
	// SmallestTextHeight is the smallest reliable text line, in source
	// pixels; zero on a page without text, which keeps its size.
	SmallestTextHeight float64 `json:"smallest_text_height,omitempty"`
	Scale              float64 `json:"scale"`
	// TextLines are the lines OCR read on a PDF page, and the reliable
	// lines measured on an image, which reads none.
	TextLines int `json:"text_lines"`
	// Orientation is the EXIF orientation applied, when not 1.
	Orientation int `json:"orientation,omitempty"`
}

// Metrics describe a composition or an image, for reviewers and run history.
type Metrics struct {
	Pages        int         `json:"pages"`
	TextLines    int         `json:"text_lines"`
	SourceBytes  int64       `json:"source_bytes"`
	Bytes        int64       `json:"bytes"`
	TargetHeight float64     `json:"target_height"`
	Quality      int         `json:"quality"`
	Detector     string      `json:"detector"`
	Models       []ocr.Model `json:"models,omitempty"`
	PageDetails  []Page      `json:"page_details"`
}

// PDF writes the photos as a searchable PDF to w, one page per photo in
// order. photo returns the encoded JPEG, PNG or WebP file of page i, of at
// most maxPixels; pages are read and processed one at a time.
func PDF(ctx context.Context, engine ocr.Engine, w io.Writer, pages int, photo func(i int) ([]byte, error), maxPixels int64, o Options) (Metrics, error) {
	if err := o.Validate(); err != nil {
		return Metrics{}, err
	}
	if pages < 1 || pages > MaxPages {
		return Metrics{}, fmt.Errorf("a PDF combines 1 to %d photos", MaxPages)
	}
	info := engine.Info()
	m := Metrics{TargetHeight: o.TargetHeight, Quality: o.Quality, Detector: info.Engine, Models: info.Models}
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
		page, details, err := prepare(ctx, engine, data, maxPixels, o.TargetHeight)
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

// Image writes a photo as the JPEG at quality that keeps its smallest
// reliable text line targetHeight pixels high: upright, flattened onto white
// and never enlarged. data is the encoded JPEG, PNG or WebP file, of at most
// maxPixels.
func Image(ctx context.Context, engine ocr.Engine, data []byte, maxPixels int64, o Options) ([]byte, Metrics, error) {
	if err := o.Validate(); err != nil {
		return nil, Metrics{}, err
	}
	info := engine.Info()
	m := Metrics{Pages: 1, SourceBytes: int64(len(data)), TargetHeight: o.TargetHeight, Quality: o.Quality, Detector: info.Engine, Models: info.Models}
	upright, p, lines, err := fit(ctx, engine, data, maxPixels, o.TargetHeight)
	if err != nil {
		return nil, Metrics{}, err
	}
	var img image.Image = upright
	if p.Width != p.SourceWidth || p.Height != p.SourceHeight {
		img = imaging.Resize(upright, p.Width, p.Height)
	}
	out, err := imaging.EncodeJPEG(img, o.Quality)
	if err != nil {
		return nil, Metrics{}, err
	}
	p.TextLines = lines
	m.TextLines, m.Bytes, m.PageDetails = lines, int64(len(out)), []Page{p}
	return out, m, nil
}

// fit decodes a photo, turns it upright, flattens it onto white and works
// out the size at which its smallest reliable text line is targetHeight
// pixels high, never enlarged: the upright photo, its page and the number of
// reliable lines measured.
func fit(ctx context.Context, engine ocr.Engine, data []byte, maxPixels int64, targetHeight float64) (*image.RGBA, Page, int, error) {
	img, err := imaging.Decode(data, maxPixels)
	if errors.Is(err, imaging.ErrAnimated) {
		return nil, Page{}, 0, errAnimated
	}
	if err != nil {
		return nil, Page{}, 0, err
	}
	upright := imaging.Flatten(img)
	w, h := upright.Rect.Dx(), upright.Rect.Dy()
	p := Page{SourceWidth: w, SourceHeight: h, Width: w, Height: h, Scale: 1}
	if img.Orientation > 1 {
		p.Orientation = img.Orientation
	}
	heights, err := analyze(ctx, engine, upright, minConfidence, analysisDimension)
	if err != nil {
		return nil, Page{}, 0, err
	}
	if len(heights) > 0 {
		p.SmallestTextHeight = slices.Min(heights)
		p.Scale = sizeForText(p.SmallestTextHeight, targetHeight)
		p.Width, p.Height = max(1, round(float64(w)*p.Scale)), max(1, round(float64(h)*p.Scale))
	}
	return upright, p, len(heights), nil
}

// prepare turns a photo into a PDF page: fitted to its smallest text, with
// the text OCR read on the full-resolution photo placed at the scaled
// positions.
func prepare(ctx context.Context, engine ocr.Engine, data []byte, maxPixels int64, targetHeight float64) (pdf.Page, Page, error) {
	upright, p, _, err := fit(ctx, engine, data, maxPixels, targetHeight)
	if err != nil {
		return pdf.Page{}, Page{}, err
	}
	w, h := p.SourceWidth, p.SourceHeight
	text, err := engine.Recognize(ctx, upright)
	if err != nil {
		return pdf.Page{}, Page{}, fmt.Errorf("text recognition failed: %w", err)
	}
	page := pdf.Page{Image: upright}
	if p.Width != w || p.Height != h {
		page.Image = imaging.Resize(upright, p.Width, p.Height)
	}
	sx, sy := float64(p.Width)/float64(w), float64(p.Height)/float64(h)
	for _, l := range text.Lines {
		var q [4]pdf.Point
		for i, c := range l.Quad {
			q[i] = pdf.Point{X: float64(c.X) * sx, Y: float64(c.Y) * sy}
		}
		page.Lines = append(page.Lines, pdf.Text{Text: l.Text, Quad: q})
	}
	p.TextLines = len(page.Lines)
	return page, p, nil
}

// sizeForText is the scale that brings the smallest text to the target
// height, at most 1.
func sizeForText(smallest, target float64) float64 {
	return math.Min(1, target/smallest)
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
