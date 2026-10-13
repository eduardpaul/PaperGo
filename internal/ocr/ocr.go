// Package ocr is PaperGo's text recognition port. An engine finds text
// regions in a decoded page image and reads its text lines with their
// positions. Callers decode files, apply EXIF orientation and bound the pixel
// count before they call an engine; engines never read storage or the
// database. Workflow activities use an Engine through this package only, so
// engines stay replaceable.
package ocr

import (
	"context"
	"errors"
	"image"
	"math"
	"strings"
)

// Quad is a text region's corners in pixels from the image's top-left
// corner, in reading order: top left, top right, bottom right, bottom left of
// the text, which is rotated when the text is.
type Quad [4]image.Point

func (q Quad) side(i int) float64 {
	a, b := q[i], q[(i+1)%4]
	return math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y))
}

// Height is the quad's shortest side: the height of the text line it holds,
// also for vertical or rotated text.
func (q Quad) Height() float64 {
	return min(q.side(0), q.side(1), q.side(2), q.side(3))
}

// Length is the quad's longest side.
func (q Quad) Length() float64 {
	return max(q.side(0), q.side(1), q.side(2), q.side(3))
}

// Region is a detected text region.
type Region struct {
	Quad Quad `json:"quad"`
	// Score is the detector's confidence that the region holds text, 0 to 1.
	Score float64 `json:"score"`
}

// Detection is the result of finding text regions in an image.
type Detection struct {
	// Regions are in the image's pixels, in reading order: top to bottom,
	// then left to right within a line.
	Regions []Region `json:"regions"`
	// Width and Height are the size of the bounded preview the detector
	// analyzed. Regions smaller than a few preview pixels are unreliable.
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Scale is the preview size divided by the source size, at most 1.
func (d Detection) Scale(source image.Rectangle) float64 {
	if source.Dx() == 0 {
		return 1
	}
	return float64(d.Width) / float64(source.Dx())
}

// DetectOptions tunes a detection.
type DetectOptions struct {
	// MinScore drops regions with a lower Score, 0 to 1.
	MinScore float64
}

// Word is a word of a recognized line. Scripts without spaces, such as
// Chinese, have one word per character.
type Word struct {
	Text string `json:"text"`
	Quad Quad   `json:"quad"`
}

// Line is a recognized text line.
type Line struct {
	Text string `json:"text"`
	// Confidence is the mean confidence of the line's characters, 0 to 1.
	Confidence float64 `json:"confidence"`
	Quad       Quad    `json:"quad"`
	Words      []Word  `json:"words"`
}

// Page is the recognized text of one image.
type Page struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Lines  []Line `json:"lines"`
}

// Text is the page's lines joined with newlines, in reading order.
func (p Page) Text() string {
	lines := make([]string, len(p.Lines))
	for i, l := range p.Lines {
		lines[i] = l.Text
	}
	return strings.Join(lines, "\n")
}

// Model identifies a model file an engine uses.
type Model struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// Info describes an engine for run metrics and diagnostics.
type Info struct {
	Engine string  `json:"engine"`
	Models []Model `json:"models"`
}

// Engine detects and recognizes text. Implementations are safe for
// concurrent use; they bound their own CPU and memory use, so callers may
// queue on them. A cancelled context stops the work and returns its error.
type Engine interface {
	// Detect finds text regions without reading them.
	Detect(ctx context.Context, img image.Image, opts DetectOptions) (Detection, error)
	// Recognize reads the image's text lines. An image without text gives
	// a page without lines, not an error.
	Recognize(ctx context.Context, img image.Image) (Page, error)
	Info() Info
	// Close releases the engine's models.
	Close() error
}

// MaxPixels bounds the images an engine accepts.
const MaxPixels = 64_000_000

// ErrTooLarge rejects an image above MaxPixels.
var ErrTooLarge = errors.New("the image exceeds the OCR limit of 64 million pixels")

// CheckSize returns ErrTooLarge for an image above MaxPixels.
func CheckSize(bounds image.Rectangle) error {
	if bounds.Empty() {
		return errors.New("the image is empty")
	}
	if int64(bounds.Dx())*int64(bounds.Dy()) > MaxPixels {
		return ErrTooLarge
	}
	return nil
}
