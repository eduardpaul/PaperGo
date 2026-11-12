// Package appicon draws the home-screen icons of PaperGo and of its apps as
// PNG images: a glyph or the app's initials in white on the app's colour.
// An icon is a pure function of its inputs, so it needs no stored image.
package appicon

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// glyphs.json holds the outlines an app may choose from, taken from the
// Lucide icons the web app draws: 24-unit boxes stroked two units wide.
//
//go:embed glyphs.json
var glyphData []byte

// fallback is the glyph of an app that chose none and has no initials the
// icon's font can draw.
const fallback = "app-window"

type element struct {
	tag   string
	attrs map[string]string
}

var glyphs = sync.OnceValue(func() map[string][]element {
	var raw map[string][][2]json.RawMessage
	if err := json.Unmarshal(glyphData, &raw); err != nil {
		panic(fmt.Errorf("app glyphs: %w", err))
	}
	out := make(map[string][]element, len(raw))
	for name, nodes := range raw {
		for _, node := range nodes {
			var e element
			if err := json.Unmarshal(node[0], &e.tag); err != nil {
				panic(fmt.Errorf("app glyph %s: %w", name, err))
			}
			if err := json.Unmarshal(node[1], &e.attrs); err != nil {
				panic(fmt.Errorf("app glyph %s: %w", name, err))
			}
			out[name] = append(out[name], e)
		}
	}
	return out
})

// Names returns the glyphs an app may choose, in order.
func Names() []string {
	names := make([]string, 0, len(glyphs()))
	for name := range glyphs() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ValidGlyph reports whether name is a glyph an app may choose.
func ValidGlyph(name string) bool { return glyphs()[name] != nil }

var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// ValidColor reports whether c is a colour as apps store it: #rrggbb in
// lower case.
func ValidColor(c string) bool { return colorPattern.MatchString(c) }

var bold = sync.OnceValue(func() *sfnt.Font {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		panic(err)
	}
	return f
})

// Initials returns the one or two letters that stand for name on an icon:
// the first letter or digit of its first two words, in upper case. It is
// empty when the icon's font cannot draw them.
func Initials(name string) string {
	var out []rune
	var buf sfnt.Buffer
	for word := range strings.FieldsSeq(name) {
		for _, r := range word {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				continue
			}
			r = unicode.ToUpper(r)
			if index, err := bold().GlyphIndex(&buf, r); err != nil || index == 0 {
				return ""
			}
			out = append(out, r)
			break
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// DefaultColor is the colour of an app that chose none: one that stays the
// same for the same name.
func DefaultColor(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	r, g, b := hsl(float64(h.Sum32()%360), 0.55, 0.45)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func hsl(h, s, l float64) (r, g, b uint8) {
	channel := func(n float64) uint8 {
		k := n + h/30
		for k >= 12 {
			k -= 12
		}
		a := s * min(l, 1-l)
		return uint8((l-a*max(-1, min(k-3, 9-k, 1)))*255 + 0.5)
	}
	return channel(0), channel(8), channel(4)
}

func parseColor(c string) color.RGBA {
	var r, g, b uint8
	fmt.Sscanf(c, "#%02x%02x%02x", &r, &g, &b)
	return color.RGBA{R: r, G: g, B: b, A: 0xff}
}

// App draws an app's icon, size pixels square. An empty glyph draws the
// initials of name, and an empty colour the one DefaultColor gives name.
// bleed fills the whole square, for platforms that cut the icon's shape
// themselves; otherwise the corners are rounded and transparent.
func App(glyph, name, colour string, size int, bleed bool) ([]byte, error) {
	if glyph != "" && !ValidGlyph(glyph) {
		return nil, fmt.Errorf("unknown glyph %q", glyph)
	}
	if colour == "" {
		colour = DefaultColor(name)
	}
	if !ValidColor(colour) {
		return nil, fmt.Errorf("invalid colour %q", colour)
	}
	c := newCanvas(size)
	c.background(parseColor(colour), bleed)
	initials := ""
	if glyph == "" {
		if initials = Initials(name); initials == "" {
			glyph = fallback
		}
	}
	if glyph != "" {
		// The glyph takes the middle half, inside the circle a maskable icon keeps.
		scale := float64(size) / 2 / 24
		at := func(p point) point { return point{float64(size)/4 + p.x*scale, float64(size)/4 + p.y*scale} }
		for _, e := range glyphs()[glyph] {
			lines, err := outline(e)
			if err != nil {
				return nil, fmt.Errorf("glyph %s: %w", glyph, err)
			}
			if e.attrs["fill"] == "currentColor" {
				c.fill(lines, at)
			}
			c.stroke(lines, at, scale)
		}
		c.paint(color.White)
	} else {
		c.text(initials)
	}
	return c.png()
}

// Brand draws PaperGo's own icon, size pixels square; bleed is as for App.
func Brand(size int, bleed bool) ([]byte, error) {
	indigo := color.RGBA{R: 0x4f, G: 0x46, B: 0xe5, A: 0xff}
	c := newCanvas(size)
	c.background(indigo, bleed)
	scale := float64(size) / 32
	at := func(p point) point { return point{p.x * scale, p.y * scale} }
	for _, part := range []struct {
		d      string
		colour color.Color
		width  float64
	}{
		{"M10 7h8l5 5v13a1 1 0 0 1-1 1H10a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1z", color.White, 0},
		{"M18 7v5h5z", color.RGBA{R: 0xc7, G: 0xd2, B: 0xfe, A: 0xff}, 0},
		{"M12 17h8M12 21h6", indigo, 1.8},
	} {
		lines, err := flatten(part.d)
		if err != nil {
			return nil, err
		}
		if part.width == 0 {
			c.fill(lines, at)
		} else {
			c.stroke(lines, at, scale*part.width/2)
		}
		c.paint(part.colour)
	}
	return c.png()
}

// canvas is an image being drawn: shapes gather in the rasterizer until
// paint lays them down in one colour.
type canvas struct {
	size int
	img  *image.RGBA
	z    *vector.Rasterizer
}

func newCanvas(size int) *canvas {
	return &canvas{size: size, img: image.NewRGBA(image.Rect(0, 0, size, size)), z: vector.NewRasterizer(size, size)}
}

func (c *canvas) background(colour color.Color, bleed bool) {
	if bleed {
		draw.Draw(c.img, c.img.Bounds(), image.NewUniform(colour), image.Point{}, draw.Src)
		return
	}
	s := float64(c.size)
	lines, _ := flatten(roundedRect(0, 0, s, s, s*7/32, s*7/32))
	c.fill(lines, func(p point) point { return p })
	c.paint(colour)
}

func (c *canvas) paint(colour color.Color) {
	c.z.Draw(c.img, c.img.Bounds(), image.NewUniform(colour), image.Point{})
	c.z.Reset(c.size, c.size)
}

// polygon adds a closed shape. Every shape turns the same way, so shapes
// that overlap cover their union.
func (c *canvas) polygon(points []point) {
	if len(points) < 3 {
		return
	}
	area := 0.0
	for i, p := range points {
		q := points[(i+1)%len(points)]
		area += p.x*q.y - q.x*p.y
	}
	if area < 0 {
		points = slices.Clone(points)
		slices.Reverse(points)
	}
	c.z.MoveTo(float32(points[0].x), float32(points[0].y))
	for _, p := range points[1:] {
		c.z.LineTo(float32(p.x), float32(p.y))
	}
	c.z.ClosePath()
}

func (c *canvas) fill(lines []polyline, at func(point) point) {
	for _, l := range lines {
		points := make([]point, len(l.points))
		for i, p := range l.points {
			points[i] = at(p)
		}
		c.polygon(points)
	}
}

// stroke draws lines with round caps and joins, half pixels to each side: a
// disc at every point and a bar along every segment.
func (c *canvas) stroke(lines []polyline, at func(point) point, half float64) {
	for _, l := range lines {
		points := make([]point, len(l.points))
		for i, p := range l.points {
			points[i] = at(p)
		}
		if l.closed && len(points) > 1 {
			points = append(points, points[0])
		}
		for i, p := range points {
			c.polygon(disc(p, half))
			if i == 0 {
				continue
			}
			q := points[i-1]
			dx, dy := p.x-q.x, p.y-q.y
			length := math.Hypot(dx, dy)
			if length == 0 {
				continue
			}
			nx, ny := -dy/length*half, dx/length*half
			c.polygon([]point{{q.x + nx, q.y + ny}, {p.x + nx, p.y + ny}, {p.x - nx, p.y - ny}, {q.x - nx, q.y - ny}})
		}
	}
}

// text draws initials in white, centred on what they ink.
func (c *canvas) text(initials string) {
	size := 0.5 * float64(c.size)
	if len([]rune(initials)) > 1 {
		size = 0.42 * float64(c.size)
	}
	face, err := opentype.NewFace(bold(), &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return
	}
	defer face.Close()
	d := font.Drawer{Dst: c.img, Src: image.NewUniform(color.White), Face: face}
	bounds, _ := d.BoundString(initials)
	side := fixed.I(c.size)
	d.Dot = fixed.Point26_6{
		X: (side-(bounds.Max.X-bounds.Min.X))/2 - bounds.Min.X,
		Y: (side-(bounds.Max.Y-bounds.Min.Y))/2 - bounds.Min.Y,
	}
	d.DrawString(initials)
}

func (c *canvas) png() ([]byte, error) {
	var out bytes.Buffer
	if err := png.Encode(&out, c.img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
