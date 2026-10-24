// Package imagingtest makes image files for tests.
package imagingtest

import (
	"encoding/binary"
	"image"
)

// TIFF encodes pages as one uncompressed, 8-bit grey, multi-page TIFF.
func TIFF(pages ...*image.Gray) []byte {
	le := binary.LittleEndian
	out := []byte{'I', 'I', 42, 0, 0, 0, 0, 0}
	next := 4 // where the offset of the next directory goes
	for _, page := range pages {
		w, h := page.Rect.Dx(), page.Rect.Dy()
		strip := uint32(len(out))
		for y := range h {
			row := page.PixOffset(page.Rect.Min.X, page.Rect.Min.Y+y)
			out = append(out, page.Pix[row:row+w]...)
		}
		if len(out)%2 == 1 {
			out = append(out, 0)
		}
		le.PutUint32(out[next:], uint32(len(out)))
		entries := [][3]uint32{{256, 4, uint32(w)}, {257, 4, uint32(h)}, {258, 3, 8}, {259, 3, 1}, {262, 3, 1}, {273, 4, strip}, {277, 3, 1}, {278, 4, uint32(h)}, {279, 4, uint32(w * h)}}
		out = le.AppendUint16(out, uint16(len(entries)))
		for _, e := range entries {
			out = le.AppendUint16(out, uint16(e[0]))
			out = le.AppendUint16(out, uint16(e[1]))
			out = le.AppendUint32(out, 1)
			if e[1] == 3 {
				out = le.AppendUint16(out, uint16(e[2]))
				out = le.AppendUint16(out, 0)
			} else {
				out = le.AppendUint32(out, e[2])
			}
		}
		next = len(out)
		out = le.AppendUint32(out, 0)
	}
	return out
}

// Page is a w×h grey page filled with shade.
func Page(w, h int, shade uint8) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	return img
}
