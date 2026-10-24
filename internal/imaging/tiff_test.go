package imaging

import (
	"errors"
	"image/color"
	"testing"

	"papergo/internal/imaging/imagingtest"
)

func TestTIFFPagesDecodeOneByOne(t *testing.T) {
	data := imagingtest.TIFF(imagingtest.Page(30, 20, 10), imagingtest.Page(40, 50, 200), imagingtest.Page(5, 5, 90))
	pages, err := TIFFPages(data)
	if err != nil || len(pages) != 3 {
		t.Fatal(pages, err)
	}
	for i, want := range []struct {
		w, h  int
		shade uint8
	}{{30, 20, 10}, {40, 50, 200}, {5, 5, 90}} {
		img, err := DecodeTIFFPage(data, i, 1<<20)
		if err != nil {
			t.Fatal(i, err)
		}
		if b := img.Bounds(); b.Dx() != want.w || b.Dy() != want.h {
			t.Fatalf("page %d is %v", i, b)
		}
		if g := color.GrayModel.Convert(img.At(1, 1)).(color.Gray); g.Y != want.shade {
			t.Fatalf("page %d shade %d", i, g.Y)
		}
	}
	if _, err = DecodeTIFFPage(data, 3, 1<<20); !errors.Is(err, ErrTIFF) {
		t.Fatal("a page past the end decoded", err)
	}
	if _, err = DecodeTIFFPage(data, 1, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatal("a page above the pixel bound decoded", err)
	}
	// The original bytes are untouched.
	if again, _ := TIFFPages(data); again[0] != pages[0] {
		t.Fatal("decoding changed the file")
	}
	if _, err = TIFFPages([]byte("II+\x00........")); err == nil {
		t.Fatal("BigTIFF was read")
	}
}

func TestTIFFPagesStopAtLoops(t *testing.T) {
	data := imagingtest.TIFF(imagingtest.Page(4, 4, 1), imagingtest.Page(4, 4, 2))
	pages, _ := TIFFPages(data)
	// Point the last page back at the first.
	last := int(pages[1]) + 2 + 9*12
	copy(data[last:], data[4:8])
	if again, err := TIFFPages(data); err != nil || len(again) != 2 {
		t.Fatal(again, err)
	}
}
