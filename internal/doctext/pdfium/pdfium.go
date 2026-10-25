// Package pdfium reads text layers and renders PDF pages with the cgo PDFium SDK.
package pdfium

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"sync"

	sdk "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	sdkerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/single_threaded"
	"papergo/internal/doctext"
)

var pool = sync.OnceValue(func() sdk.Pool { return single_threaded.Init(single_threaded.Config{}) })

// Reader opens PDFs with the pinned, linked PDFium library.
type Reader struct{}

func New() *Reader           { return &Reader{} }
func (*Reader) Name() string { return "pdfium-8076" }

func (*Reader) Open(ctx context.Context, data []byte) (doctext.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > math.MaxInt32 {
		return nil, errors.New("the PDF is empty or too large")
	}
	instance, err := pool().GetInstanceWithContext(ctx)
	if err != nil {
		return nil, err
	}
	opened, err := instance.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		_ = instance.Close()
		if errors.Is(err, sdkerrors.ErrPassword) {
			return nil, doctext.ErrEncrypted
		}
		return nil, fmt.Errorf("the PDF cannot be read: %w", err)
	}
	count, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: opened.Document})
	if err != nil || count.PageCount < 0 || count.PageCount > 1<<20 {
		_ = instance.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("the PDF has an invalid page count")
	}
	return &document{instance: instance, doc: opened.Document, pages: count.PageCount}, nil
}

type document struct {
	mu       sync.Mutex
	instance sdk.Pdfium
	doc      references.FPDF_DOCUMENT
	pages    int
}

func (d *document) Pages() int { return d.pages }
func (d *document) page(index int) (requests.Page, error) {
	if d.instance == nil {
		return requests.Page{}, errors.New("the PDF is closed")
	}
	if index < 0 || index >= d.pages {
		return requests.Page{}, fmt.Errorf("page %d is out of range", index+1)
	}
	return requests.Page{ByIndex: &requests.PageByIndex{Document: d.doc, Index: index}}, nil
}
func (d *document) Text(index int) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	page, err := d.page(index)
	if err != nil {
		return "", err
	}
	text, err := d.instance.GetPageText(&requests.GetPageText{Page: page})
	if err != nil {
		return "", err
	}
	return text.Text, nil
}
func (d *document) Size(index int) (float64, float64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.size(index)
}
func (d *document) size(index int) (float64, float64, error) {
	if index < 0 || index >= d.pages {
		return 0, 0, fmt.Errorf("page %d is not in the PDF", index+1)
	}
	size, err := d.instance.FPDF_GetPageSizeByIndex(&requests.FPDF_GetPageSizeByIndex{Document: d.doc, Index: index})
	if err != nil {
		return 0, 0, err
	}
	if size.Width <= 0 || size.Height <= 0 || math.IsNaN(size.Width*size.Height) || math.IsInf(size.Width*size.Height, 0) {
		return 0, 0, errors.New("the page has no size")
	}
	return size.Width, size.Height, nil
}
func (d *document) Render(index, dpi int, maxPixels int64) (image.Image, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	page, err := d.page(index)
	if err != nil {
		return nil, err
	}
	pw, ph, err := d.size(index)
	if err != nil {
		return nil, err
	}
	w, h := pw/72, ph/72
	scale := float64(dpi)
	if scale*scale*w*h > float64(maxPixels) {
		scale = math.Sqrt(float64(maxPixels) / (w * h))
	}
	width, height := int(math.Floor(w*scale)), int(math.Floor(h*scale))
	if width < 1 || height < 1 || width > 1<<15 || height > 1<<15 {
		return nil, errors.New("the page is too large or too small to render")
	}
	rendered, err := d.instance.RenderPageInPixels(&requests.RenderPageInPixels{Page: page, Width: width, Height: height, RenderFlags: enums.FPDF_RENDER_FLAG_ANNOT})
	if err != nil {
		return nil, err
	}
	return rendered.Result.RenderedImage, nil
}
func (d *document) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.instance == nil {
		return nil
	}
	err := d.instance.Close()
	d.instance = nil
	return err
}
