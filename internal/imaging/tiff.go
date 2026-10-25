package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"

	"golang.org/x/image/tiff"
)

// ErrTIFF reports a TIFF whose pages cannot be found.
var ErrTIFF = errors.New("the TIFF cannot be read")

// MaxTIFFPages bounds the pages TIFFPages lists.
const MaxTIFFPages = 2000

// TIFF is a classic TIFF (BigTIFF is not read) whose pages (image file
// directories) were found once, at most MaxTIFFPages of them, so each page
// is then sized or decoded without walking the file again.
type TIFF struct {
	data  []byte
	pages []uint32
	// truncated says more pages follow the last one found.
	truncated bool
}

// OpenTIFF finds the pages of data.
func OpenTIFF(data []byte) (*TIFF, error) {
	order, first, err := tiffHeader(data)
	if err != nil {
		return nil, err
	}
	var pages []uint32
	seen := map[uint32]bool{}
	// A chain that loops back or runs off the file ends at its last readable page.
	readable := func(offset uint32) bool { return offset != 0 && !seen[offset] && int64(offset)+2 <= int64(len(data)) }
	offset := first
	for ; readable(offset) && len(pages) < MaxTIFFPages; offset = next(order, data, offset) {
		seen[offset] = true
		pages = append(pages, offset)
	}
	if len(pages) == 0 {
		return nil, ErrTIFF
	}
	return &TIFF{data: data, pages: pages, truncated: readable(offset)}, nil
}

// next returns the offset of the page after the one at offset, or 0.
func next(order binary.ByteOrder, data []byte, offset uint32) uint32 {
	at := int64(offset) + 2 + int64(order.Uint16(data[offset:]))*12
	if at+4 > int64(len(data)) {
		return 0
	}
	return order.Uint32(data[at:])
}

// Pages returns how many pages the TIFF has, at most MaxTIFFPages.
func (t *TIFF) Pages() int { return len(t.pages) }

// Truncated reports a TIFF with more than MaxTIFFPages pages, of which Pages
// lists the first.
func (t *TIFF) Truncated() bool { return t.truncated }

func tiffHeader(data []byte) (binary.ByteOrder, uint32, error) {
	if len(data) < 8 {
		return nil, 0, ErrTIFF
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, 0, ErrTIFF
	}
	if order.Uint16(data[2:]) != 42 {
		return nil, 0, fmt.Errorf("%w: only classic TIFF is read", ErrTIFF)
	}
	return order, order.Uint32(data[4:]), nil
}

// tiffPage reads data as a TIFF whose first page is the page at offset: the
// header's first-directory offset is replaced, nothing is copied.
func tiffPage(data []byte, offset uint32) (io.Reader, error) {
	order, _, err := tiffHeader(data)
	if err != nil {
		return nil, err
	}
	header := [8]byte{}
	copy(header[:], data[:8])
	order.PutUint32(header[4:], offset)
	return io.NewSectionReader(overlay{data, header}, 0, int64(len(data))), nil
}

type overlay struct {
	data   []byte
	header [8]byte
}

func (o overlay) ReadAt(p []byte, off int64) (int, error) {
	n, err := bytes.NewReader(o.data).ReadAt(p, off)
	for i := int64(0); i < int64(n) && off+i < 8; i++ {
		p[i] = o.header[off+i]
	}
	return n, err
}

// Config returns the size of page i from 0.
func (t *TIFF) Config(i int) (image.Config, error) {
	r, err := t.page(i)
	if err != nil {
		return image.Config{}, err
	}
	cfg, err := tiff.DecodeConfig(r)
	if err != nil {
		return image.Config{}, fmt.Errorf("%w: %w", ErrTIFF, err)
	}
	return cfg, nil
}

// Decode decodes page i from 0, of at most maxPixels.
func (t *TIFF) Decode(i int, maxPixels int64) (image.Image, error) {
	cfg, err := t.Config(i)
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("%w: %d×%d is above %d", ErrTooLarge, cfg.Width, cfg.Height, maxPixels)
	}
	r, err := t.page(i)
	if err != nil {
		return nil, err
	}
	img, err := tiff.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTIFF, err)
	}
	return img, nil
}

func (t *TIFF) page(i int) (io.Reader, error) {
	if i < 0 || i >= len(t.pages) {
		return nil, fmt.Errorf("%w: the TIFF has %d pages", ErrTIFF, len(t.pages))
	}
	return tiffPage(t.data, t.pages[i])
}
