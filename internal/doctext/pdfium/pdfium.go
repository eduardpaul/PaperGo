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
	"sync/atomic"
	"time"

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
	o Options
	// mu guards the current or last start and the count of starts.
	mu     sync.Mutex
	run    *start
	starts int
	// slots holds one entry per open PDF, up to maxOpen; waiting counts
	// the opens waiting for one.
	slots   chan struct{}
	waiting atomic.Int32
}

// start compiles the module, or loads it from the cache, in the
// background; compiling takes a few seconds. Every open gets a fresh
// instance of its pool, which returns its memory on close.
type start struct {
	done chan struct{}
	// Set before done closes: the pool, where its code came from, or why
	// it failed, and when the start ended.
	pool  sdk.Pool
	cache string
	err   error
	ended time.Time
}

// retryAfter is how long a failed start answers callers before the next
// one tries again, so a lasting failure does not compile on every request.
var retryAfter = time.Minute

// newPool starts PDFium's pool; tests replace it.
var newPool = startPool

func New(o Options) *Reader { return &Reader{o: o, slots: make(chan struct{}, maxOpen)} }

func (*Reader) Name() string { return "pdfium-8111-wasm" }

// Warm starts PDFium as the first Open would: it compiles the module, or
// loads it from the cache, and starts one instance. Opens meanwhile wait
// for the same start. It returns CacheHit, CacheMiss or CacheOff.
func (r *Reader) Warm() (string, error) {
	s, err := r.started(context.Background())
	if err != nil {
		return "", err
	}
	return s.cache, nil
}

// started waits, as long as ctx allows, for the one start of PDFium that
// runs at a time, beginning it unless it runs, succeeded, or failed less
// than retryAfter ago.
func (r *Reader) started(ctx context.Context) (*start, error) {
	r.mu.Lock()
	s := r.run
	if s != nil {
		select {
		case <-s.done:
			if s.err != nil && time.Since(s.ended) >= retryAfter {
				s = nil
			}
		default:
		}
	}
	if s == nil {
		s = &start{done: make(chan struct{})}
		r.run = s
		r.starts++
		go func() {
			s.pool, s.cache, s.err = r.init()
			s.ended = time.Now()
			close(s.done)
		}()
	}
	r.mu.Unlock()
	select {
	case <-s.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, fmt.Errorf("%w: PDFium cannot start: %w", doctext.ErrUnavailable, s.err)
	}
	return s, nil
}

func (r *Reader) init() (sdk.Pool, string, error) {
	if dir := r.o.CacheDir; dir != "" {
		before := cached(dir)
		p, err := newPool(dir)
		if err == nil {
			if cached(dir) > before {
				return p, CacheMiss, nil
			}
			return p, CacheHit, nil
		}
		if r.o.Log != nil {
			r.o.Log.Warn("PDFium starts without its compilation cache", "cache", dir, "error", err)
		}
	}
	p, err := newPool("")
	return p, CacheOff, err
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
	s, err := r.started(ctx)
	if err != nil {
		return nil, err
	}
	p := s.pool
	// The pool is never asked for more instances than it may hold, so its
	// borrowers never wait inside it: go-commons-pool can miss the wakeup
	// of a borrower that found it full just before an instance closed.
	r.waiting.Add(1)
	select {
	case r.slots <- struct{}{}:
		r.waiting.Add(-1)
	case <-ctx.Done():
		r.waiting.Add(-1)
		return nil, ctx.Err()
	}
	release := sync.OnceFunc(func() { <-r.slots })
	instance, err := p.GetInstanceWithContext(ctx)
	if err != nil {
		release()
		// The pool reports a cancelled wait as its own timeout.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	fail := func(err error) (doctext.Document, error) {
		_ = instance.Close()
		release()
		return nil, err
	}
	opened, err := instance.OpenDocument(&requests.OpenDocument{File: &data})
	if err != nil {
		if errors.Is(err, sdkerrors.ErrPassword) {
			return fail(doctext.ErrEncrypted)
		}
		return fail(fmt.Errorf("the PDF cannot be read: %w", err))
	}
	count, err := instance.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: opened.Document})
	if err != nil {
		return fail(err)
	}
	if count.PageCount < 0 || count.PageCount > 1<<20 {
		return fail(errors.New("the PDF has an invalid page count"))
	}
	return &document{instance: instance, release: release, doc: opened.Document, pages: count.PageCount}, nil
}

type document struct {
	mu       sync.Mutex
	instance sdk.Pdfium
	// release frees the reader's slot once the instance is closed.
	release func()
	doc     references.FPDF_DOCUMENT
	pages   int
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
	d.release()
	return err
}
