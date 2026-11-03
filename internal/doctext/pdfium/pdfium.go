// Package pdfium reads text layers and renders PDF pages with PDFium
// compiled to WebAssembly: go-pdfium embeds the module, and wazero runs it
// inside the process, so no native library is loaded.
package pdfium

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"runtime"
	"slices"
	"sync"

	sdk "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	sdkerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"papergo/internal/doctext"
)

// maxOpen bounds the PDFs open at once, each in its own WebAssembly
// instance: as many as read them at once by default, which is the page
// renderer's slots (half the CPUs) and the documents and previews workers.
// Further opens wait for a close.
var maxOpen = max(1, runtime.NumCPU()/2) + 2

// pool compiles the module on first use, which takes a few seconds. Every
// open gets a fresh instance, which returns its memory on close. PDFs are
// handed over in memory, so instances get no file system; PDFium then also
// draws missing fonts with its built-in ones on every host.
var pool = sync.OnceValues(func() (sdk.Pool, error) {
	return webassembly.Init(webassembly.Config{MaxTotal: maxOpen, FSConfig: wazero.NewFSConfig(), Stdout: os.Stderr})
})

// Reader opens PDFs with PDFium 8057 as WebAssembly, the module go-pdfium
// v1.21.3 embeds. Name keys stored text, and notices/ holds the licences
// the module ships under: update both with go-pdfium.
type Reader struct{}

func New() *Reader           { return &Reader{} }
func (*Reader) Name() string { return "pdfium-8057-wasm" }

func (*Reader) Open(ctx context.Context, data []byte) (doctext.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > math.MaxInt32 {
		return nil, errors.New("the PDF is empty or too large")
	}
	p, err := pool()
	if err != nil {
		return nil, fmt.Errorf("PDFium cannot start: %w", err)
	}
	instance, err := p.GetInstanceWithContext(ctx)
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
	if d.instance == nil {
		return 0, 0, errors.New("the PDF is closed")
	}
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
	// The pixels are the instance's memory until Cleanup; the image is
	// copied out so it outlives the document.
	defer rendered.Cleanup()
	img, ok := rendered.Result.RenderedImage.(*image.RGBA)
	if !ok {
		return nil, fmt.Errorf("PDFium rendered a %T", rendered.Result.RenderedImage)
	}
	return &image.RGBA{Pix: slices.Clone(img.Pix), Stride: img.Stride, Rect: img.Rect}, nil
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
