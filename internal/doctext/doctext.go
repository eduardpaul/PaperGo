// Package doctext reads the text of stored files, page by page, for search
// and for workflows: the text layer of PDF pages, and OCR of scanned pages
// and photos when an OCR engine is configured. Readers and engines are
// ports: a PDF reader (package pdfium) and an ocr.Engine are handed in by
// the caller, so this package never reads storage or the database.
package doctext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"strconv"
	"strings"
	"unicode"

	"papergo/internal/imaging"
	"papergo/internal/ocr"

	_ "golang.org/x/image/tiff" // first page of TIFF scans
)

// PDF opens PDF files. Implementations are safe for concurrent use.
type PDF interface {
	// Name identifies the reader and its version, for reuse of results.
	Name() string
	Open(ctx context.Context, data []byte) (Document, error)
}

// Document is an open PDF.
type Document interface {
	Pages() int
	// Text is the text layer of page i (from 0), as the PDF draws it.
	Text(i int) (string, error)
	// Render draws page i upright, at most dpi dots per inch and at most
	// maxPixels pixels.
	Render(i, dpi int, maxPixels int64) (image.Image, error)
	Close() error
}

// ErrEncrypted is returned by readers for password-protected PDFs.
var ErrEncrypted = errors.New("the PDF is protected by a password")

// OCR modes.
const (
	// OCRAuto reads text layers and OCRs pages and images without one, when
	// an engine is configured.
	OCRAuto = "auto"
	// OCRAlways OCRs every page, also pages with a text layer.
	OCRAlways = "always"
	// OCRNever reads text layers only.
	OCRNever = "never"
)

// Options tune an extraction.
type Options struct {
	OCR string
	// MaxPages bounds the pages read; later pages are left out (Truncated).
	MaxPages int
	// MaxPixels bounds decoded and rendered images.
	MaxPixels int64
	// DPI is the resolution pages are rendered at for OCR; default 200.
	DPI int
	// Hints are the file's languages as OCR codes (eng+deu), and Languages
	// the languages to detect among (Snowball names).
	Hints     string
	Languages []string
}

// Version changes when extraction changes what it reads, so stored results
// made before are not reused.
const Version = "doctext-1"

// Settings identify how text is read, so a stored result is reused only for
// the same reader, OCR engine and models, mode, page bound and languages.
// pdf and engine may be nil.
func Settings(o Options, pdf PDF, engine ocr.Engine) string {
	parts := []string{Version, "ocr=" + o.OCR, "pages=" + strconv.Itoa(o.MaxPages), "languages=" + o.Hints + "/" + strings.Join(o.Languages, ",")}
	if pdf != nil {
		parts = append(parts, "pdf="+pdf.Name())
	}
	if engine != nil && o.OCR != OCRNever {
		info := engine.Info()
		parts = append(parts, "engine="+info.Engine)
		for _, m := range info.Models {
			parts = append(parts, m.Name+"="+m.SHA256)
		}
	} else {
		parts = append(parts, "engine=none")
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:])
}

// Page is the text of one page, numbered from 1.
type Page struct {
	Number int    `json:"page"`
	Text   string `json:"text"`
	// OCR says the text was recognized in the page image.
	OCR bool `json:"ocr,omitempty"`
}

// Result is a file's text.
type Result struct {
	Pages []Page `json:"pages"`
	// Chars counts the letters, digits and other characters of all pages.
	Chars int `json:"chars"`
	// OCRPages counts pages read by OCR; Skipped pages without a text layer
	// that were not OCRed (no engine, or OCR never).
	OCRPages int `json:"ocr_pages"`
	Skipped  int `json:"skipped_pages"`
	// Truncated says pages past MaxPages were left out.
	Truncated bool   `json:"truncated"`
	Language  string `json:"language,omitempty"`
}

// ErrUnsupported is returned for files whose text cannot be read: other
// formats, protected or broken PDFs.
var ErrUnsupported = errors.New("the file's text cannot be read")

// Extract reads the text of a file of a media type. pdf and engine may be
// nil: without a reader PDFs are unsupported, without an engine nothing is
// OCRed (OCRAlways then fails).
func Extract(ctx context.Context, data []byte, mediaType string, o Options, pdf PDF, engine ocr.Engine) (Result, error) {
	if o.MaxPages <= 0 {
		o.MaxPages = 500
	}
	if o.MaxPixels <= 0 {
		o.MaxPixels = ocr.MaxPixels
	}
	if o.DPI <= 0 {
		o.DPI = 200
	}
	switch o.OCR {
	case "":
		o.OCR = OCRAuto
	case OCRAuto, OCRNever:
	case OCRAlways:
		if engine == nil {
			return Result{}, errors.New("OCR is not available in this deployment")
		}
	default:
		return Result{}, fmt.Errorf("ocr must be %s, %s or %s", OCRAuto, OCRAlways, OCRNever)
	}
	if o.OCR == OCRNever {
		engine = nil
	}
	var r Result
	var err error
	switch {
	case mediaType == "application/pdf":
		if pdf == nil {
			return Result{}, fmt.Errorf("%w: no PDF reader is configured", ErrUnsupported)
		}
		r, err = extractPDF(ctx, data, o, pdf, engine)
	case strings.HasPrefix(mediaType, "image/"):
		r, err = extractImage(ctx, data, mediaType, o, engine)
	default:
		return Result{}, fmt.Errorf("%w: %s files hold no text this reader knows", ErrUnsupported, mediaType)
	}
	if err != nil {
		return Result{}, err
	}
	var all strings.Builder
	for _, p := range r.Pages {
		r.Chars += len([]rune(p.Text))
		all.WriteString(p.Text)
		all.WriteByte('\n')
	}
	r.Language = Language(all.String(), o.Hints, o.Languages)
	return r, nil
}

func extractPDF(ctx context.Context, data []byte, o Options, pdf PDF, engine ocr.Engine) (Result, error) {
	doc, err := pdf.Open(ctx, data)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	defer doc.Close()
	var r Result
	n := doc.Pages()
	if n > o.MaxPages {
		n, r.Truncated = o.MaxPages, true
	}
	for i := range n {
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		text, err := doc.Text(i)
		if err != nil {
			return Result{}, fmt.Errorf("page %d: %w", i+1, err)
		}
		text = clean(text)
		page := Page{Number: i + 1, Text: text}
		if engine != nil && (o.OCR == OCRAlways || !hasWords(text)) {
			img, err := doc.Render(i, o.DPI, o.MaxPixels)
			if err != nil {
				return Result{}, fmt.Errorf("render page %d: %w", i+1, err)
			}
			read, err := engine.Recognize(ctx, img)
			if err != nil {
				return Result{}, fmt.Errorf("OCR page %d: %w", i+1, err)
			}
			page.Text, page.OCR = clean(read.Text()), true
			r.OCRPages++
		} else if !hasWords(text) {
			r.Skipped++
		}
		r.Pages = append(r.Pages, page)
	}
	return r, nil
}

func extractImage(ctx context.Context, data []byte, mediaType string, o Options, engine ocr.Engine) (Result, error) {
	if engine == nil {
		return Result{Pages: []Page{{Number: 1}}, Skipped: 1}, nil
	}
	var img image.Image
	if mediaType == "image/tiff" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			return Result{}, fmt.Errorf("%w: the TIFF cannot be decoded", ErrUnsupported)
		}
		if int64(cfg.Width)*int64(cfg.Height) > o.MaxPixels {
			return Result{}, fmt.Errorf("%w: %d×%d pixels is above %d", imaging.ErrTooLarge, cfg.Width, cfg.Height, o.MaxPixels)
		}
		if img, _, err = image.Decode(bytes.NewReader(data)); err != nil {
			return Result{}, fmt.Errorf("%w: the TIFF cannot be decoded: %w", ErrUnsupported, err)
		}
	} else {
		decoded, err := imaging.Decode(data, o.MaxPixels)
		if errors.Is(err, imaging.ErrUnsupported) || errors.Is(err, imaging.ErrAnimated) {
			return Result{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		if err != nil {
			return Result{}, err
		}
		img = decoded.Image
	}
	read, err := engine.Recognize(ctx, img)
	if err != nil {
		return Result{}, fmt.Errorf("OCR: %w", err)
	}
	return Result{Pages: []Page{{Number: 1, Text: clean(read.Text()), OCR: true}}, OCRPages: 1}, nil
}

// clean trims each line, drops control characters, and keeps one empty line
// where a run of them separated paragraphs.
func clean(text string) string {
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) && r != '￾' && r != '￿' && r != '�' {
			return r
		}
		if r == '\r' || r == '\f' || r == '\v' {
			return '\n'
		}
		return -1
	}, text)
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" || len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// hasWords reports whether text holds a letter or digit: a page with only
// stray marks has no text layer worth keeping.
func hasWords(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) >= 0
}
