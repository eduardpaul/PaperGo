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
}

// OpenTIFF finds the pages of data.
func OpenTIFF(data []byte) (*TIFF, error) {
	order, first, err := tiffHeader(data)
	if err != nil {
		return nil, err
	}
	var pages []uint32
	seen := map[uint32]bool{}
	for offset := first; offset != 0 && len(pages) < MaxTIFFPages; {
		if seen[offset] || int64(offset)+2 > int64(len(data)) {
			break
		}
		seen[offset] = true
		pages = append(pages, offset)
		entries := int64(order.Uint16(data[offset:]))
		next := int64(offset) + 2 + entries*12
		if next+4 > int64(len(data)) {
			break
		}
		offset = order.Uint32(data[next:])
	}
	if len(pages) == 0 {
		return nil, ErrTIFF
	}
	return &TIFF{data: data, pages: pages}, nil
}

// Pages returns how many pages the TIFF has, at most MaxTIFFPages.
func (t *TIFF) Pages() int { return len(t.pages) }

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
