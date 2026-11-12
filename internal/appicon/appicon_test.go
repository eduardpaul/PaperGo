package appicon

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func decode(t *testing.T, data []byte, err error) image.Image {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// white counts the pixels drawn in white, the colour of a glyph or initials.
func white(img image.Image) int {
	n := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if r, g, b, a := img.At(x, y).RGBA(); r == 0xffff && g == 0xffff && b == 0xffff && a == 0xffff {
				n++
			}
		}
	}
	return n
}

func TestEveryGlyphDraws(t *testing.T) {
	if len(Names()) == 0 || !ValidGlyph(fallback) {
		t.Fatal("the glyph set must hold the fallback glyph")
	}
	for _, name := range Names() {
		data, err := App(name, "", "#4f46e5", 192, true)
		img := decode(t, data, err)
		if img.Bounds().Dx() != 192 || img.Bounds().Dy() != 192 {
			t.Fatalf("%s: %v", name, img.Bounds())
		}
		// A glyph is thin lines in the middle half: some white, never most of it.
		if n := white(img); n < 150 || n > 192*192/4 {
			t.Fatalf("%s: %d white pixels", name, n)
		}
		// The corners stay the app's colour, inside what a mask keeps.
		if r, g, b, _ := img.At(20, 20).RGBA(); r>>8 != 0x4f || g>>8 != 0x46 || b>>8 != 0xe5 {
			t.Fatalf("%s: background %x %x %x", name, r>>8, g>>8, b>>8)
		}
	}
}

func TestShapeInitialsAndColour(t *testing.T) {
	rounded := icon(t, "", "Diesel dashboard", "", 192, false)
	if _, _, _, a := rounded.At(0, 0).RGBA(); a != 0 {
		t.Fatal("a rounded icon has transparent corners")
	}
	if white(rounded) < 500 {
		t.Fatal("initials must be drawn")
	}
	full := icon(t, "", "Diesel dashboard", "", 192, true)
	if _, _, _, a := full.At(0, 0).RGBA(); a != 0xffff {
		t.Fatal("a bleeding icon fills its corners")
	}
	for name, want := range map[string]string{"Diesel dashboard": "DD", "receipts": "R", "  3d printing queue now": "3P", "¡Órdenes!": "Ó", "文書": "", "": ""} {
		if got := Initials(name); got != want {
			t.Fatalf("Initials(%q) = %q, want %q", name, got, want)
		}
	}
	// A name the font cannot draw gets the fallback glyph.
	if white(icon(t, "", "文書", "", 192, true)) < 150 {
		t.Fatal("the fallback glyph must be drawn")
	}
	if a, b := DefaultColor("Diesel"), DefaultColor("Receipts"); a == b || !ValidColor(a) || a != DefaultColor("Diesel") {
		t.Fatalf("default colours: %s %s", a, b)
	}
	for _, c := range []string{"4f46e5", "#4F46E5", "#4f46e", "red", "#4f46e5 "} {
		if ValidColor(c) {
			t.Fatalf("%q must not be a colour", c)
		}
		if _, err := App("fuel", "", c, 64, true); err == nil {
			t.Fatalf("%q must be refused", c)
		}
	}
	if _, err := App("no-such-glyph", "", "#4f46e5", 64, true); err == nil {
		t.Fatal("an unknown glyph must be refused")
	}
}

func icon(t *testing.T, glyph, name, colour string, size int, bleed bool) image.Image {
	t.Helper()
	data, err := App(glyph, name, colour, size, bleed)
	return decode(t, data, err)
}

func TestBrand(t *testing.T) {
	data, err := Brand(512, false)
	img := decode(t, data, err)
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatal("the rounded brand icon has transparent corners")
	}
	// The page of the logo is white at the centre left, above its lines.
	if r, g, b, _ := img.At(512*12/32, 512*14/32).RGBA(); r != 0xffff || g != 0xffff || b != 0xffff {
		t.Fatal("the page of the logo must be white")
	}
}

func TestPathData(t *testing.T) {
	lines, err := flatten("M2 2l4 0 0 4zm10 10a1 1 0 011-1")
	if err != nil || len(lines) != 2 || !lines[0].closed || lines[1].closed {
		t.Fatalf("%v %v", lines, err)
	}
	if end := lines[1].points[len(lines[1].points)-1]; end != (point{13, 11}) {
		t.Fatalf("arc ends at %v", end)
	}
	for _, bad := range []string{"M1", "5 5", "M1 1 A1 1 0 2 0 3 3", "M1 1 L x"} {
		if _, err := flatten(bad); err == nil {
			t.Fatalf("%q must fail", bad)
		}
	}
}
