package imaging

import (
	"bytes"
	"encoding/binary"
)

// Orientation returns the EXIF orientation (1 to 8) of an encoded image, or
// 1 when it has none: from a JPEG's APP1 segment, a PNG's eXIf chunk or a
// WebP's EXIF chunk. Only the orientation tag of the first image directory
// is read.
func Orientation(data []byte, media string) int {
	var exif []byte
	switch media {
	case "image/jpeg":
		exif = jpegExif(data)
	case "image/png":
		for _, c := range pngChunks(data) {
			if c.typ == "eXIf" {
				exif = c.data
				break
			}
			if c.typ == "IDAT" {
				break
			}
		}
	case "image/webp":
		exif = webpExif(data)
	}
	return tiffOrientation(bytes.TrimPrefix(exif, []byte("Exif\x00\x00")))
}

// jpegExif finds the Exif APP1 segment before the image data.
func jpegExif(data []byte) []byte {
	for at := 2; at+4 <= len(data) && data[at] == 0xFF; {
		marker := data[at+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan, end of image
			return nil
		}
		n := int(binary.BigEndian.Uint16(data[at+2:]))
		if n < 2 || at+2+n > len(data) {
			return nil
		}
		segment := data[at+4 : at+2+n]
		if marker == 0xE1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return segment
		}
		at += 2 + n
	}
	return nil
}

// webpExif finds the EXIF chunk of an extended WebP file.
func webpExif(data []byte) []byte {
	for at := 12; at+8 <= len(data); {
		typ := string(data[at : at+4])
		n := int(binary.LittleEndian.Uint32(data[at+4:]))
		if n < 0 || at+8+n > len(data) {
			return nil
		}
		if typ == "EXIF" {
			return data[at+8 : at+8+n]
		}
		at += 8 + n + n%2
	}
	return nil
}

// tiffOrientation reads tag 0x0112 of the first IFD of TIFF-structured EXIF.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:4]) {
	case "II*\x00":
		order = binary.LittleEndian
	case "MM\x00*":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	entries := int(order.Uint16(t[ifd:]))
	for i := range entries {
		e := ifd + 2 + i*12
		if e+12 > len(t) {
			return 1
		}
		// SHORT (type 3), count 1: the value is in the entry.
		if order.Uint16(t[e:]) == 0x0112 && order.Uint16(t[e+2:]) == 3 && order.Uint32(t[e+4:]) == 1 {
			if v := int(order.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
