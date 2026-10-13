package imaging

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

var (
	red    = color.RGBA{255, 0, 0, 255}
	green  = color.RGBA{0, 255, 0, 255}
	blue   = color.RGBA{0, 0, 255, 255}
	yellow = color.RGBA{255, 255, 0, 255}
)

// corners returns a 2×3 image with distinct corners, as in PaperDotNet's test.
func corners() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	img.Set(0, 0, red)
	img.Set(1, 0, green)
	img.Set(0, 2, blue)
	img.Set(1, 2, yellow)
	return img
}

func TestOrientationsPreservePixelsIncludingMirrors(t *testing.T) {
	// For each EXIF orientation: the upright size and where the red corner
	// lands (1 top left, 2 top right, 3 bottom left, 4 bottom right).
	for o, want := range map[int]struct{ w, h, corner int }{
		1: {2, 3, 1}, 2: {2, 3, 2}, 3: {2, 3, 4}, 4: {2, 3, 3},
		5: {3, 2, 1}, 6: {3, 2, 2}, 7: {3, 2, 4}, 8: {3, 2, 3},
	} {
		up := Orient(corners(), o)
		b := up.Bounds()
		if b.Dx() != want.w || b.Dy() != want.h {
			t.Fatalf("orientation %d: %v", o, b)
		}
		at := []image.Point{{0, 0}, {want.w - 1, 0}, {0, want.h - 1}, {want.w - 1, want.h - 1}}[want.corner-1]
		if c := color.RGBAModel.Convert(up.At(at.X, at.Y)); c != red {
			t.Errorf("orientation %d: red is not at corner %d (%v)", o, want.corner, c)
		}
	}
	// Orientation 6 is a quarter turn clockwise: green moves to the bottom right.
	if c := color.RGBAModel.Convert(Orient(corners(), 6).At(2, 1)); c != green {
		t.Errorf("orientation 6 moved green to %v", c)
	}
}

type byteOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// exifJPEG encodes img as JPEG with an EXIF orientation.
func exifJPEG(t *testing.T, img image.Image, orientation uint16, order byteOrder) []byte {
	t.Helper()
	var plain bytes.Buffer
	if err := jpeg.Encode(&plain, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	tiff := []byte("II*\x00")
	if order == binary.BigEndian {
		tiff = []byte("MM\x00*")
	}
	tiff = order.AppendUint32(tiff, 8)
	tiff = order.AppendUint16(tiff, 1)
	tiff = order.AppendUint16(tiff, 0x0112)
	tiff = order.AppendUint16(tiff, 3)
	tiff = order.AppendUint32(tiff, 1)
	tiff = order.AppendUint16(tiff, orientation)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	app1 := binary.BigEndian.AppendUint16([]byte{0xFF, 0xE1}, uint16(len(segment)+2))
	data := plain.Bytes()
	return append(append(append([]byte{}, data[:2]...), append(app1, segment...)...), data[2:]...)
}

func TestDecodeAppliesExifOrientation(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for _, order := range []byteOrder{binary.LittleEndian, binary.BigEndian} {
		data := exifJPEG(t, src, 6, order)
		if o := Orientation(data, "image/jpeg"); o != 6 {
			t.Fatalf("orientation %d", o)
		}
		img, err := Decode(data, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 40 || img.Orientation != 6 || img.MediaType != "image/jpeg" {
			t.Fatalf("decoded %v, %+v", b, img)
		}
	}
}

func TestAnimatedPNGIsDetected(t *testing.T) {
	// PaperDotNet's two one-pixel frames (red and blue), valid APNG.
	apng, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAACGFjVEwAAAACAAAAAPONk3AAAAAaZmNUTAAAAAAAAAABAAAAAQAAAAAAAAAAAAEACgAAWn8w0AAAAA1JREFUeJxj+M/A8B8ABQAB/4mZPR0AAAAaZmNUTAAAAAEAAAABAAAAAQAAAAAAAAAAAAEACgAAwQzaBAAAABFmZEFUAAAAAnicY2Bg+P8fAAMCAf/1e6XXAAAAAElFTkSuQmCC")
	if _, err := Decode(apng, 1<<20); !errors.Is(err, ErrAnimated) {
		t.Fatalf("APNG: %v", err)
	}
	var still bytes.Buffer
	_ = png.Encode(&still, corners())
	if _, err := Decode(still.Bytes(), 1<<20); err != nil {
		t.Fatalf("still PNG: %v", err)
	}
	// An extended WebP with the animation flag.
	vp8x := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00"), 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if !animated(vp8x, "image/webp") {
		t.Fatal("animated WebP not detected")
	}
}

func TestDecodeRejectsMalformedAndPixelBombs(t *testing.T) {
	if _, err := Decode([]byte("broken image"), 1<<20); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("not an image: %v", err)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	if _, err := Decode(buf.Bytes(), 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("16 pixels above 10: %v", err)
	}
	truncated := buf.Bytes()[:len(buf.Bytes())-20]
	if _, err := Decode(truncated, 1<<20); err == nil {
		t.Fatal("a truncated PNG decoded")
	}
}

func TestWebPRoundTripAndResize(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for i := range src.Pix {
		src.Pix[i] = uint8(i)
	}
	small := Resize(src, 32, 16)
	if small.Rect.Dx() != 32 || small.Rect.Dy() != 16 {
		t.Fatalf("resized %v", small.Rect)
	}
	data, err := EncodeWebP(small, 80)
	if err != nil {
		t.Fatal(err)
	}
	img, err := Decode(data, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if img.MediaType != "image/webp" || img.Bounds().Dx() != 32 {
		t.Fatalf("decoded %+v", img)
	}
}

func FuzzOrientation(f *testing.F) {
	f.Add(exifJPEG(&testing.T{}, image.NewRGBA(image.Rect(0, 0, 2, 2)), 6, binary.LittleEndian), "image/jpeg")
	f.Add([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x08eXIfII*\x00"), "image/png")
	f.Add([]byte("RIFF\x00\x00\x00\x00WEBPEXIF\x10\x00\x00\x00MM\x00*"), "image/webp")
	f.Fuzz(func(t *testing.T, data []byte, media string) {
		if o := Orientation(data, media); o < 1 || o > 8 {
			t.Fatalf("orientation %d", o)
		}
	})
}
