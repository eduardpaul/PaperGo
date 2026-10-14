package paddle

import (
	"math"
	"slices"
	"sort"
)

// The DBNet post-processing follows RapidOcrNet's TextDetector (Apache-2.0,
// adapted from RapidOCR), which PaperDotNet uses with these models:
// threshold the probability map, dilate it by one pixel, take each region's
// minimum-area rectangle, score it by the mean probability inside the
// region, expand it by the unclip distance, and drop boxes whose short side
// is under 3 pixels before and 5 pixels after the expansion.

type point struct{ X, Y float64 }

// dbBox is a text box in network pixels: top left, top right, bottom right,
// bottom left, ordered as RapidOcrNet's GetMiniBox orders them.
type dbBox struct {
	pts   [4]point
	score float64
}

const (
	minBoxSide     = 3.0
	minClippedSide = minBoxSide + 2
)

// dbBoxes post-processes a rows×cols probability map.
func dbBoxes(pred []float32, rows, cols int, minScore, thresh, unclipRatio float64) []dbBox {
	n := rows * cols
	scores := make([]uint8, n)
	text := make([]bool, n)
	for i, p := range pred[:n] {
		scores[i] = uint8(math.Round(math.Max(0, math.Min(1, float64(p))) * 255))
		text[i] = float64(p) > thresh
	}
	mask := dilate(text, rows, cols)
	var boxes []dbBox
	for _, r := range components(mask, rows, cols) {
		hull := convexHull(r.extremes())
		if len(hull) < 3 {
			continue
		}
		rect, w, h := minAreaRect(hull)
		if math.Min(w, h) < minBoxSide {
			continue
		}
		score := r.meanScore(scores, cols)
		if score < minScore {
			continue
		}
		clipped, ok := unclip(rect, w, h, unclipRatio)
		if !ok {
			continue
		}
		final, w2, h2 := minAreaRect(clipped[:])
		if math.Min(w2, h2) < minClippedSide {
			continue
		}
		boxes = append(boxes, dbBox{pts: miniBoxOrder(final), score: score})
	}
	return boxes
}

// dilate grows the mask by one pixel in all eight directions.
func dilate(mask []bool, rows, cols int) []bool {
	out := make([]bool, len(mask))
	for y := range rows {
		for x := range cols {
			if !mask[y*cols+x] {
				continue
			}
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if yy, xx := y+dy, x+dx; yy >= 0 && yy < rows && xx >= 0 && xx < cols {
						out[yy*cols+xx] = true
					}
				}
			}
		}
	}
	return out
}

// region is an 8-connected component as the leftmost and rightmost pixel of
// each of its rows, which is all the outer contour's hull and fill need.
type region struct {
	top    int
	lo, hi []int
}

func (r region) extremes() []point {
	pts := make([]point, 0, 2*len(r.lo))
	for i := range r.lo {
		y := float64(r.top + i)
		pts = append(pts, point{float64(r.lo[i]), y}, point{float64(r.hi[i]), y})
	}
	return pts
}

// meanScore is the mean probability, 0 to 1, of the pixels inside the
// region's outer contour.
func (r region) meanScore(scores []uint8, cols int) float64 {
	var sum, count int
	for i := range r.lo {
		row := scores[(r.top+i)*cols:]
		for x := r.lo[i]; x <= r.hi[i]; x++ {
			sum += int(row[x])
		}
		count += r.hi[i] - r.lo[i] + 1
	}
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count) / 255
}

func components(mask []bool, rows, cols int) []region {
	seen := make([]bool, len(mask))
	var out []region
	var stack, pixels []int
	for start := range mask {
		if !mask[start] || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		pixels = pixels[:0]
		top, bottom := rows, -1
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			pixels = append(pixels, p)
			y, x := p/cols, p%cols
			top, bottom = min(top, y), max(bottom, y)
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					yy, xx := y+dy, x+dx
					if yy < 0 || yy >= rows || xx < 0 || xx >= cols {
						continue
					}
					if q := yy*cols + xx; mask[q] && !seen[q] {
						seen[q] = true
						stack = append(stack, q)
					}
				}
			}
		}
		r := region{top: top, lo: make([]int, bottom-top+1), hi: make([]int, bottom-top+1)}
		for i := range r.lo {
			r.lo[i], r.hi[i] = cols, -1
		}
		for _, p := range pixels {
			i, x := p/cols-top, p%cols
			r.lo[i], r.hi[i] = min(r.lo[i], x), max(r.hi[i], x)
		}
		out = append(out, r)
	}
	return out
}

// convexHull returns the hull counter-clockwise (Andrew's monotone chain),
// without collinear points.
func convexHull(pts []point) []point {
	pts = slices.Clone(pts)
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].X != pts[j].X {
			return pts[i].X < pts[j].X
		}
		return pts[i].Y < pts[j].Y
	})
	pts = slices.Compact(pts)
	if len(pts) < 3 {
		return pts
	}
	cross := func(o, a, b point) float64 { return (a.X-o.X)*(b.Y-o.Y) - (a.Y-o.Y)*(b.X-o.X) }
	hull := make([]point, 0, 2*len(pts))
	for _, p := range pts {
		for len(hull) >= 2 && cross(hull[len(hull)-2], hull[len(hull)-1], p) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, p)
	}
	for i, lower := len(pts)-2, len(hull)+1; i >= 0; i-- {
		for len(hull) >= lower && cross(hull[len(hull)-2], hull[len(hull)-1], pts[i]) <= 0 {
			hull = hull[:len(hull)-1]
		}
		hull = append(hull, pts[i])
	}
	return hull[:len(hull)-1]
}

// minAreaRect returns the minimum-area rectangle around a convex polygon
// (rotating calipers over its edges), with the lengths of its first and
// second sides.
func minAreaRect(hull []point) ([4]point, float64, float64) {
	if len(hull) == 0 {
		return [4]point{}, 0, 0
	}
	best := math.Inf(1)
	var rect [4]point
	var bw, bh float64
	for i := range hull {
		a, b := hull[i], hull[(i+1)%len(hull)]
		ux, uy := b.X-a.X, b.Y-a.Y
		l := math.Hypot(ux, uy)
		if l == 0 {
			if len(hull) == 1 {
				return [4]point{a, a, a, a}, 0, 0
			}
			continue
		}
		ux, uy = ux/l, uy/l
		vx, vy := -uy, ux
		minU, maxU, minV, maxV := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
		for _, p := range hull {
			u, v := p.X*ux+p.Y*uy, p.X*vx+p.Y*vy
			minU, maxU, minV, maxV = math.Min(minU, u), math.Max(maxU, u), math.Min(minV, v), math.Max(maxV, v)
		}
		if area := (maxU - minU) * (maxV - minV); area < best {
			best, bw, bh = area, maxU-minU, maxV-minV
			at := func(u, v float64) point { return point{u*ux + v*vx, u*uy + v*vy} }
			rect = [4]point{at(minU, minV), at(maxU, minV), at(maxU, maxV), at(minU, maxV)}
		}
	}
	return rect, bw, bh
}

// unclip expands a rectangle by area × ratio / perimeter on every side.
// Offsetting a rectangle with round joins, as PaddleOCR does with Clipper,
// keeps the same minimum-area rectangle as this expansion. Corners are
// rounded to whole pixels as Clipper's integer paths are.
func unclip(rect [4]point, w, h, ratio float64) ([4]point, bool) {
	if w < 1.001 && h < 1.001 {
		return rect, false
	}
	d := w * h * ratio / (2 * (w + h))
	// The rectangle's axes; minAreaRect gives sides of length w and h.
	unit := func(a, b point, l float64) (float64, float64) {
		if l == 0 {
			return 0, 0
		}
		return (b.X - a.X) / l, (b.Y - a.Y) / l
	}
	ux, uy := unit(rect[0], rect[1], w)
	vx, vy := unit(rect[0], rect[3], h)
	cx, cy := (rect[0].X+rect[2].X)/2, (rect[0].Y+rect[2].Y)/2
	var out [4]point
	for i, p := range rect {
		// Move each corner d outward along both axes.
		dx, dy := p.X-cx, p.Y-cy
		su, sv := math.Copysign(d, dx*ux+dy*uy), math.Copysign(d, dx*vx+dy*vy)
		out[i] = point{math.Round(p.X + su*ux + sv*vx), math.Round(p.Y + su*uy + sv*vy)}
	}
	return out, true
}

// miniBoxOrder orders a rectangle's corners as RapidOcrNet's GetMiniBox
// does: sorted by x, the left pair gives top left and bottom left, the right
// pair top right and bottom right, by y.
func miniBoxOrder(rect [4]point) [4]point {
	p := rect
	sort.SliceStable(p[:], func(i, j int) bool { return p[i].X < p[j].X })
	tl, bl := p[0], p[1]
	if p[1].Y <= p[0].Y {
		tl, bl = p[1], p[0]
	}
	tr, br := p[2], p[3]
	if p[3].Y <= p[2].Y {
		tr, br = p[3], p[2]
	}
	return [4]point{tl, tr, br, bl}
}
