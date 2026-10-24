package documentpreview

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"sync"

	"papergo/internal/dal/blobs"
	"papergo/internal/httpapi"
	"papergo/internal/imaging"
	"papergo/internal/ocr"
	"papergo/internal/preview"
	"papergo/internal/storage"
	"papergo/internal/transfer"
)

// Widths are the widths, in pixels, pages render at; a request takes the
// smallest that is at least as wide as it asks, so renditions are shared.
var Widths = []int{320, 480, 640, 960, 1280, 1600, 2048}

// pagePixels bounds one rendered page, so tall pages render narrower.
const pagePixels = 2048 * 4096

// renditionCacheBytes bounds the rendered pages kept in memory.
const renditionCacheBytes = 64 << 20

// PageSize is a page's size in CSS pixels at 100 %: points at 96 DPI for
// PDF pages, pixels for images.
type PageSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// PageList lists the pages of a file version.
type PageList struct {
	BlobID string     `json:"blob_id"`
	Pages  []PageSize `json:"pages"`
	// Truncated says the file has more pages than are listed and rendered.
	Truncated bool `json:"truncated"`
}

// maxListedPages bounds the pages a file lists and renders.
const maxListedPages = imaging.MaxTIFFPages

// pages renders page images on request: every page of PDFs and TIFFs, and
// the one page of other images, for viewers that cannot draw the file
// themselves. Renders run a few at a time and are kept in memory.
type pages struct {
	renderer *Renderer
	slots    chan struct{}
	cache    *renditions
}

func newPages(r *Renderer) *pages {
	return &pages{renderer: r, slots: make(chan struct{}, max(1, runtime.NumCPU()/2)), cache: newRenditions(renditionCacheBytes)}
}

func (p *pages) list(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	file, err := p.file(r.Context(), in, r.PathValue("id"), r.URL.Query().Get("blob_id"))
	if err != nil {
		return err
	}
	data, err := p.bytes(r.Context(), file)
	if err != nil {
		return err
	}
	sizes, truncated, err := p.sizes(r.Context(), data, string(file.ContentType))
	if err != nil {
		return err
	}
	httpapi.Respond(w, r, http.StatusOK, PageList{BlobID: file.ID, Pages: sizes, Truncated: truncated})
	return nil
}

func (p *pages) page(w http.ResponseWriter, r *http.Request, in httpapi.Request) error {
	number, err := strconv.Atoi(r.PathValue("page"))
	if err != nil || number < 1 || number > maxListedPages {
		return &httpapi.Problem{Status: http.StatusNotFound, Code: "page_not_found", Detail: "pages are numbered from 1"}
	}
	width, err := Width(r.URL.Query().Get("width"))
	if err != nil {
		return &httpapi.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Detail: err.Error()}
	}
	blobID := r.URL.Query().Get("blob_id")
	file, err := p.file(r.Context(), in, r.PathValue("id"), blobID)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%d/%d", file.Sha256, number, width)
	webp := p.cache.get(key)
	if webp == nil {
		data, err := p.bytes(r.Context(), file)
		if err != nil {
			return err
		}
		if webp, err = p.render(r.Context(), data, string(file.ContentType), number-1, width); err != nil {
			return err
		}
		p.cache.put(key, webp)
	}
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="page-%d.webp"`, number))
	transfer.StoredFile(w)
	w.Header().Set("ETag", strconv.Quote(key))
	// A named file version never changes; the item's current file can.
	if blobID != "" {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	w.Header().Set("X-Page-Blob-ID", file.ID)
	http.ServeContent(transfer.Writer(w), r, "", file.CreatedAt, bytes.NewReader(webp))
	return nil
}

// Width returns the rendition width for a requested width; empty asks for 960.
func Width(raw string) (int, error) {
	if raw == "" {
		return 960, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, errors.New("width is a positive number of pixels")
	}
	if i := slices.IndexFunc(Widths, func(w int) bool { return w >= n }); i >= 0 {
		return Widths[i], nil
	}
	return Widths[len(Widths)-1], nil
}

// file is the file the caller may read: the item's file, or an earlier
// version by its blob ID, of a type whose pages render.
func (p *pages) file(ctx context.Context, in httpapi.Request, itemID, blobID string) (*blobs.Blob, error) {
	file, err := in.DMS.GetBlob(ctx, in.Subject, itemID, blobID)
	if err != nil {
		return nil, err
	}
	if !preview.Supported(string(file.ContentType)) {
		return nil, fmt.Errorf("%w: PaperGo does not render pages of %s files", storage.ErrUnsupportedFile, file.ContentType)
	}
	return file, nil
}

func (p *pages) bytes(ctx context.Context, file *blobs.Blob) ([]byte, error) {
	f, err := p.renderer.Storage.Open(ctx, file.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (p *pages) maxPixels() int64 {
	if p.renderer.MaxPixels > 0 {
		return p.renderer.MaxPixels
	}
	return ocr.MaxPixels
}

// unreadable answers for a file whose pages cannot be read.
func unreadable(err error) error {
	return &httpapi.Problem{Status: http.StatusUnprocessableEntity, Code: "unreadable_file", Detail: "the file's pages cannot be read: " + err.Error()}
}

func (p *pages) sizes(ctx context.Context, data []byte, media string) ([]PageSize, bool, error) {
	switch media {
	case "application/pdf":
		if p.renderer.PDF == nil {
			return nil, false, fmt.Errorf("%w: PDF rendering is unavailable", storage.ErrUnsupportedFile)
		}
		doc, err := p.renderer.PDF.Open(ctx, data)
		if err != nil {
			return nil, false, unreadable(err)
		}
		defer doc.Close()
		n := min(doc.Pages(), maxListedPages)
		out := make([]PageSize, 0, n)
		for i := range n {
			w, h, err := doc.Size(i)
			if err != nil {
				return nil, false, unreadable(err)
			}
			out = append(out, PageSize{int(math.Round(w * 96 / 72)), int(math.Round(h * 96 / 72))})
		}
		return out, doc.Pages() > n, nil
	case "image/tiff":
		offsets, err := imaging.TIFFPages(data)
		if err != nil {
			return nil, false, unreadable(err)
		}
		out := make([]PageSize, 0, len(offsets))
		for i := range offsets {
			cfg, err := imaging.TIFFPageConfig(data, i)
			if err != nil {
				return nil, false, unreadable(err)
			}
			out = append(out, PageSize{cfg.Width, cfg.Height})
		}
		return out, false, nil
	default:
		img, err := imaging.Decode(data, p.maxPixels())
		if err != nil {
			return nil, false, unreadable(err)
		}
		b := img.Bounds()
		return []PageSize{{b.Dx(), b.Dy()}}, false, nil
	}
}

// render draws page i at width pixels, or narrower for a tall page or a
// smaller image, as WebP.
func (p *pages) render(ctx context.Context, data []byte, media string, i, width int) ([]byte, error) {
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var img image.Image
	switch media {
	case "application/pdf":
		if p.renderer.PDF == nil {
			return nil, fmt.Errorf("%w: PDF rendering is unavailable", storage.ErrUnsupportedFile)
		}
		doc, err := p.renderer.PDF.Open(ctx, data)
		if err != nil {
			return nil, unreadable(err)
		}
		defer doc.Close()
		if i >= doc.Pages() {
			return nil, &httpapi.Problem{Status: http.StatusNotFound, Code: "page_not_found", Detail: fmt.Sprintf("the file has %d pages", doc.Pages())}
		}
		pw, _, err := doc.Size(i)
		if err != nil {
			return nil, unreadable(err)
		}
		if img, err = doc.Render(i, int(math.Ceil(float64(width)*72/pw)), pagePixels); err != nil {
			return nil, unreadable(err)
		}
	case "image/tiff":
		pages, err := imaging.TIFFPages(data)
		if err != nil {
			return nil, unreadable(err)
		}
		if i >= len(pages) {
			return nil, &httpapi.Problem{Status: http.StatusNotFound, Code: "page_not_found", Detail: fmt.Sprintf("the file has %d pages", len(pages))}
		}
		if img, err = imaging.DecodeTIFFPage(data, i, p.maxPixels()); err != nil {
			return nil, unreadable(err)
		}
	default:
		if i > 0 {
			return nil, &httpapi.Problem{Status: http.StatusNotFound, Code: "page_not_found", Detail: "the file has 1 page"}
		}
		decoded, err := imaging.Decode(data, p.maxPixels())
		if err != nil {
			return nil, unreadable(err)
		}
		img = decoded.Image
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := img.Bounds()
	scale := math.Min(1, float64(width)/float64(b.Dx()))
	if float64(b.Dx())*float64(b.Dy())*scale*scale > pagePixels {
		scale = math.Sqrt(pagePixels / (float64(b.Dx()) * float64(b.Dy())))
	}
	if w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale)); w != b.Dx() || h != b.Dy() {
		img = imaging.Resize(img, w, h)
	} else {
		img = imaging.Flatten(img)
	}
	return imaging.EncodeWebP(img, 82)
}

// renditions keeps recently rendered pages, least recently used first out,
// within a byte bound.
type renditions struct {
	mu      sync.Mutex
	limit   int
	size    int
	order   *list.List
	entries map[string]*list.Element
}

type rendition struct {
	key  string
	data []byte
}

func newRenditions(limit int) *renditions {
	return &renditions{limit: limit, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *renditions) get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e == nil {
		return nil
	}
	c.order.MoveToFront(e)
	return e.Value.(*rendition).data
}

func (c *renditions) put(key string, data []byte) {
	if len(data) > c.limit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.order.MoveToFront(e)
		return
	}
	c.entries[key] = c.order.PushFront(&rendition{key, data})
	c.size += len(data)
	for c.size > c.limit {
		oldest := c.order.Back()
		r := oldest.Value.(*rendition)
		c.order.Remove(oldest)
		delete(c.entries, r.key)
		c.size -= len(r.data)
	}
}
