package imaging

import (
	"errors"
	"image"
	"image/color"
	"testing"

	"papergo/internal/imaging/imagingtest"
)

func TestTIFFPagesDecodeOneByOne(t *testing.T) {
	data := imagingtest.TIFF(imagingtest.Page(30, 20, 10), imagingtest.Page(40, 50, 200), imagingtest.Page(5, 5, 90))
	doc, err := OpenTIFF(data)
	if err != nil || doc.Pages() != 3 {
		t.Fatal(doc, err)
	}
	for i, want := range []struct {
		w, h  int
		shade uint8
	}{{30, 20, 10}, {40, 50, 200}, {5, 5, 90}} {
		img, err := doc.Decode(i, 1<<20)
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
	if _, err = doc.Decode(3, 1<<20); !errors.Is(err, ErrTIFF) {
		t.Fatal("a page past the end decoded", err)
	}
	if _, err = doc.Decode(1, 100); !errors.Is(err, ErrTooLarge) {
		t.Fatal("a page above the pixel bound decoded", err)
	}
	// The original bytes are untouched.
	if cfg, err := doc.Config(0); err != nil || cfg.Width != 30 {
		t.Fatal("decoding changed the file")
	}
	if _, err = OpenTIFF([]byte("II+\x00........")); err == nil {
		t.Fatal("BigTIFF was read")
	}
}

func TestTIFFPagesStopAtLoops(t *testing.T) {
	data := imagingtest.TIFF(imagingtest.Page(4, 4, 1), imagingtest.Page(4, 4, 2))
	doc, _ := OpenTIFF(data)
	// Point the last page back at the first.
	last := int(doc.pages[1]) + 2 + 9*12
	copy(data[last:], data[4:8])
	if again, err := OpenTIFF(data); err != nil || again.Pages() != 2 {
		t.Fatal(again, err)
	}
}

func TestTIFFPagesReportMoreThanTheyList(t *testing.T) {
	page := imagingtest.Page(1, 1, 7)
	for _, n := range []int{MaxTIFFPages, MaxTIFFPages + 1} {
		pages := make([]*image.Gray, n)
		for i := range pages {
			pages[i] = page
		}
		doc, err := OpenTIFF(imagingtest.TIFF(pages...))
		if err != nil || doc.Pages() != MaxTIFFPages || doc.Truncated() != (n > MaxTIFFPages) {
			t.Fatalf("%d pages: %v pages=%d truncated=%v", n, err, doc.Pages(), doc.Truncated())
		}
	}
	// A chain that loops back is complete at its last new page, not truncated.
	data := imagingtest.TIFF(imagingtest.Page(4, 4, 1), imagingtest.Page(4, 4, 2))
	doc, _ := OpenTIFF(data)
	copy(data[int(doc.pages[1])+2+9*12:], data[4:8])
	if again, err := OpenTIFF(data); err != nil || again.Pages() != 2 || again.Truncated() {
		t.Fatal(again, err)
	}
}
