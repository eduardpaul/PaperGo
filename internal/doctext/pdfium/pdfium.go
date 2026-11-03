// Package pdfium reads text layers and renders PDF pages with PDFium
// compiled to WebAssembly: go-pdfium embeds the module, and wazero runs it
// inside the process, so no native library is loaded.
package pdfium

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	sdk "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	sdkerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"papergo/internal/doctext"
)

// maxOpen bounds the PDFs open at once, each in its own WebAssembly
// instance: as many as read them at once by default, which is the page
// renderer's slots (half the CPUs) and the documents and previews workers.
// Further opens wait for a close.
var maxOpen = max(1, runtime.NumCPU()/2) + 2

// Options configure a Reader.
type Options struct {
	// CacheDir keeps PDFium's compiled machine code between starts, so
	// only the first start compiles it. Empty compiles it at every start.
	CacheDir string
	// Log receives a warning when CacheDir cannot be used; nil discards it.
	Log *slog.Logger
}

// Where a start took PDFium's compiled code from.
const (
	// CacheHit loaded it from the cache directory.
	CacheHit = "hit"
	// CacheMiss compiled it and stored it in the cache directory.
	CacheMiss = "miss"
	// CacheOff compiled it without a cache directory, or without one that
	// could be used.
	CacheOff = "off"
)

// Reader opens PDFs with PDFium 8111 as WebAssembly, the module go-pdfium
// v1.21.4 embeds. Name keys stored text, and notices/ holds the licences
// the module ships under: update both with go-pdfium.
type Reader struct {
	// start compiles the module, or loads it from the cache, on first use;
	// compiling takes a few seconds. Every open gets a fresh instance,
	// which returns its memory on close.
	start func() (sdk.Pool, error)
	// cache is where start took the compiled code from.
	cache string
}

func New(o Options) *Reader {
	r := &Reader{}
	r.start = sync.OnceValues(func() (sdk.Pool, error) { return r.init(o) })
	return r
}

func (*Reader) Name() string { return "pdfium-8111-wasm" }

// Warm starts PDFium as the first Open would: it compiles the module, or
// loads it from the cache, and starts one instance. Opens meanwhile wait
// for it and share it. It returns CacheHit, CacheMiss or CacheOff.
func (r *Reader) Warm() (string, error) {
	_, err := r.start()
	return r.cache, err
}

func (r *Reader) init(o Options) (sdk.Pool, error) {
	if o.CacheDir != "" {
		before := cached(o.CacheDir)
		p, err := startPool(o.CacheDir)
		if err == nil {
			r.cache = CacheHit
			if cached(o.CacheDir) > before {
				r.cache = CacheMiss
			}
			return p, nil
		}
		if o.Log != nil {
			o.Log.Warn("PDFium starts without its compilation cache", "cache", o.CacheDir, "error", err)
		}
	}
	r.cache = CacheOff
	return startPool("")
}

// startPool compiles PDFium, through a compilation cache in dir unless dir
// is empty, and checks that an instance starts. PDFs are handed over in
// memory, so instances get no file system; PDFium then also draws missing
// fonts with its built-in ones on every host.
func startPool(dir string) (sdk.Pool, error) {
	config := webassembly.Config{MaxTotal: maxOpen, FSConfig: wazero.NewFSConfig(), Stdout: os.Stderr}
	var cache wazero.CompilationCache
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		// wazero keeps entries per wazero version, platform and CPU
		// features, keyed by the module's hash, and writes them atomically.
		var err error
		if cache, err = wazero.NewCompilationCacheWithDir(dir); err != nil {
			return nil, err
		}
		// The features go-pdfium's module needs, as its default runtime has.
		config.RuntimeConfig = wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).WithCompilationCache(cache)
	}
	p, err := webassembly.Init(config)
	if err == nil {
		var instance sdk.Pdfium
		if instance, err = p.GetInstanceWithContext(context.Background()); err == nil {
			err = instance.Close()
		}
		if err != nil {
			_ = p.Close()
		}
	}
	if err != nil {
		if cache != nil {
			_ = cache.Close(context.Background())
		}
		return nil, err
	}
	return p, nil
}

// cached counts the compiled modules stored in a cache directory.
func cached(dir string) int {
	entries, _ := filepath.Glob(filepath.Join(dir, "wazero-*", "*"))
	n := 0
	for _, e := range entries {
		if !strings.HasSuffix(e, ".tmp") {
			n++
		}
	}
	return n
}

func (r *Reader) Open(ctx context.Context, data []byte) (doctext.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > math.MaxInt32 {
		return nil, errors.New("the PDF is empty or too large")
	}
	p, err := r.start()
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
