package appicon

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type point struct{ x, y float64 }

// polyline is a run of straight segments; closed joins its end to its start.
type polyline struct {
	points []point
	closed bool
}

// curveSteps is how many segments stand for a Bézier curve; an arc gets one
// per arcStep of its sweep.
const (
	curveSteps = 16
	arcStep    = math.Pi / 24
)

func disc(c point, r float64) []point {
	return ellipse(c, r, r)
}

func ellipse(c point, rx, ry float64) []point {
	n := int(2 * math.Pi / arcStep)
	points := make([]point, n)
	for i := range points {
		a := float64(i) * 2 * math.Pi / float64(n)
		points[i] = point{c.x + rx*math.Cos(a), c.y + ry*math.Sin(a)}
	}
	return points
}

func roundedRect(x, y, w, h, rx, ry float64) string {
	rx, ry = min(rx, w/2), min(ry, h/2)
	if rx <= 0 || ry <= 0 {
		return fmt.Sprintf("M%g %gh%gv%gh%gz", x, y, w, h, -w)
	}
	return fmt.Sprintf("M%g %gh%ga%g %g 0 0 1 %g %gv%ga%g %g 0 0 1 %g %gh%ga%g %g 0 0 1 %g %gv%ga%g %g 0 0 1 %g %gz",
		x+rx, y, w-2*rx, rx, ry, rx, ry, h-2*ry, rx, ry, -rx, ry, -(w - 2*rx), rx, ry, -rx, -ry, -(h - 2*ry), rx, ry, rx, -ry)
}

// outline returns the lines of one SVG element of a glyph.
func outline(e element) ([]polyline, error) {
	var failed error
	number := func(name string) float64 {
		text, ok := e.attrs[name]
		if !ok {
			return 0
		}
		v, err := strconv.ParseFloat(text, 64)
		if err != nil && failed == nil {
			failed = fmt.Errorf("%s %s=%q", e.tag, name, text)
		}
		return v
	}
	var lines []polyline
	var err error
	switch e.tag {
	case "path":
		lines, err = flatten(e.attrs["d"])
	case "circle":
		lines = []polyline{{points: disc(point{number("cx"), number("cy")}, number("r")), closed: true}}
	case "ellipse":
		lines = []polyline{{points: ellipse(point{number("cx"), number("cy")}, number("rx"), number("ry")), closed: true}}
	case "rect":
		rx, ry := number("rx"), number("ry")
		if _, ok := e.attrs["ry"]; !ok {
			ry = rx
		}
		if _, ok := e.attrs["rx"]; !ok {
			rx = ry
		}
		lines, err = flatten(roundedRect(number("x"), number("y"), number("width"), number("height"), rx, ry))
	case "line":
		lines = []polyline{{points: []point{{number("x1"), number("y1")}, {number("x2"), number("y2")}}}}
	case "polyline", "polygon":
		s := scanner{text: e.attrs["points"]}
		var points []point
		for {
			x, ok := s.number()
			if !ok {
				break
			}
			y, ok := s.number()
			if !ok {
				return nil, fmt.Errorf("%s points %q", e.tag, e.attrs["points"])
			}
			points = append(points, point{x, y})
		}
		lines = []polyline{{points: points, closed: e.tag == "polygon"}}
	default:
		return nil, fmt.Errorf("unsupported element %q", e.tag)
	}
	if err != nil {
		return nil, err
	}
	return lines, failed
}

// scanner reads the numbers, flags and command letters of SVG path data.
type scanner struct {
	text string
	at   int
}

func (s *scanner) skip() {
	for s.at < len(s.text) && strings.IndexByte(" ,\t\r\n", s.text[s.at]) >= 0 {
		s.at++
	}
}

func (s *scanner) done() bool {
	s.skip()
	return s.at == len(s.text)
}

// command returns the command letter next in the data, if one is.
func (s *scanner) command() (byte, bool) {
	s.skip()
	if s.at < len(s.text) && strings.IndexByte("MmLlHhVvCcSsQqTtAaZz", s.text[s.at]) >= 0 {
		s.at++
		return s.text[s.at-1], true
	}
	return 0, false
}

// number reads one number; numbers may run together, as in "-.5.5".
func (s *scanner) number() (float64, bool) {
	s.skip()
	start := s.at
	if s.at < len(s.text) && (s.text[s.at] == '-' || s.text[s.at] == '+') {
		s.at++
	}
	digits := func() {
		for s.at < len(s.text) && s.text[s.at] >= '0' && s.text[s.at] <= '9' {
			s.at++
		}
	}
	digits()
	if s.at < len(s.text) && s.text[s.at] == '.' {
		s.at++
		digits()
	}
	if s.at < len(s.text) && (s.text[s.at] == 'e' || s.text[s.at] == 'E') {
		mark := s.at
		s.at++
		if s.at < len(s.text) && (s.text[s.at] == '-' || s.text[s.at] == '+') {
			s.at++
		}
		if s.at < len(s.text) && s.text[s.at] >= '0' && s.text[s.at] <= '9' {
			digits()
		} else {
			s.at = mark
		}
	}
	v, err := strconv.ParseFloat(s.text[start:s.at], 64)
	if err != nil {
		s.at = start
		return 0, false
	}
	return v, true
}

// flag reads an arc flag, a single 0 or 1 that may touch what follows.
func (s *scanner) flag() (bool, bool) {
	s.skip()
	if s.at < len(s.text) && (s.text[s.at] == '0' || s.text[s.at] == '1') {
		s.at++
		return s.text[s.at-1] == '1', true
	}
	return false, false
}

// flatten turns SVG path data into straight segments.
func flatten(d string) ([]polyline, error) {
	s := scanner{text: d}
	var lines []polyline
	var current, start, control point
	var command, previous byte
	open := false
	to := func(p point) {
		if !open {
			lines = append(lines, polyline{points: []point{current}})
			open = true
		}
		line := &lines[len(lines)-1]
		line.points = append(line.points, p)
		current = p
	}
	numbers := func(n int) ([]float64, error) {
		out := make([]float64, n)
		for i := range out {
			v, ok := s.number()
			if !ok {
				return nil, fmt.Errorf("path %q: a number is missing at %d", d, s.at)
			}
			out[i] = v
		}
		return out, nil
	}
	for !s.done() {
		if c, ok := s.command(); ok {
			command = c
		} else if command == 0 || command == 'Z' || command == 'z' {
			return nil, fmt.Errorf("path %q: a command is missing at %d", d, s.at)
		} else if command == 'M' {
			command = 'L'
		} else if command == 'm' {
			command = 'l'
		}
		relative := command >= 'a'
		origin := point{}
		if relative {
			origin = current
		}
		switch command {
		case 'M', 'm':
			v, err := numbers(2)
			if err != nil {
				return nil, err
			}
			current = point{origin.x + v[0], origin.y + v[1]}
			start, open = current, false
		case 'L', 'l':
			v, err := numbers(2)
			if err != nil {
				return nil, err
			}
			to(point{origin.x + v[0], origin.y + v[1]})
		case 'H', 'h':
			v, err := numbers(1)
			if err != nil {
				return nil, err
			}
			to(point{origin.x + v[0], current.y})
		case 'V', 'v':
			v, err := numbers(1)
			if err != nil {
				return nil, err
			}
			to(point{current.x, origin.y + v[0]})
		case 'C', 'c', 'S', 's':
			smooth := command == 'S' || command == 's'
			count := 6
			if smooth {
				count = 4
			}
			v, err := numbers(count)
			if err != nil {
				return nil, err
			}
			first := current
			if smooth {
				if strings.IndexByte("CcSs", previous) >= 0 {
					first = point{2*current.x - control.x, 2*current.y - control.y}
				}
			} else {
				first = point{origin.x + v[0], origin.y + v[1]}
				v = v[2:]
			}
			second, end := point{origin.x + v[0], origin.y + v[1]}, point{origin.x + v[2], origin.y + v[3]}
			from := current
			for i := 1; i <= curveSteps; i++ {
				t := float64(i) / curveSteps
				u := 1 - t
				to(point{
					u*u*u*from.x + 3*u*u*t*first.x + 3*u*t*t*second.x + t*t*t*end.x,
					u*u*u*from.y + 3*u*u*t*first.y + 3*u*t*t*second.y + t*t*t*end.y,
				})
			}
			control = second
		case 'Q', 'q', 'T', 't':
			smooth := command == 'T' || command == 't'
			count := 4
			if smooth {
				count = 2
			}
			v, err := numbers(count)
			if err != nil {
				return nil, err
			}
			middle := current
			if smooth {
				if strings.IndexByte("QqTt", previous) >= 0 {
					middle = point{2*current.x - control.x, 2*current.y - control.y}
				}
			} else {
				middle = point{origin.x + v[0], origin.y + v[1]}
				v = v[2:]
			}
			end := point{origin.x + v[0], origin.y + v[1]}
			from := current
			for i := 1; i <= curveSteps; i++ {
				t := float64(i) / curveSteps
				u := 1 - t
				to(point{u*u*from.x + 2*u*t*middle.x + t*t*end.x, u*u*from.y + 2*u*t*middle.y + t*t*end.y})
			}
			control = middle
		case 'A', 'a':
			v, err := numbers(3)
			if err != nil {
				return nil, err
			}
			large, ok := s.flag()
			sweep, ok2 := s.flag()
			if !ok || !ok2 {
				return nil, fmt.Errorf("path %q: an arc flag is missing at %d", d, s.at)
			}
			e, err := numbers(2)
			if err != nil {
				return nil, err
			}
			for _, p := range arc(current, math.Abs(v[0]), math.Abs(v[1]), v[2]*math.Pi/180, large, sweep, point{origin.x + e[0], origin.y + e[1]}) {
				to(p)
			}
		case 'Z', 'z':
			if open {
				lines[len(lines)-1].closed = true
			}
			current, open = start, false
		}
		previous = command
	}
	return lines, nil
}

// arc returns the points after from along an SVG elliptical arc ending at to.
func arc(from point, rx, ry, rotation float64, large, sweep bool, to point) []point {
	if rx == 0 || ry == 0 || from == to {
		return []point{to}
	}
	sin, cos := math.Sincos(rotation)
	dx, dy := (from.x-to.x)/2, (from.y-to.y)/2
	x, y := cos*dx+sin*dy, -sin*dx+cos*dy
	if scale := x*x/(rx*rx) + y*y/(ry*ry); scale > 1 {
		rx, ry = rx*math.Sqrt(scale), ry*math.Sqrt(scale)
	}
	factor := math.Sqrt(max(0, (rx*rx*ry*ry-rx*rx*y*y-ry*ry*x*x)/(rx*rx*y*y+ry*ry*x*x)))
	if large == sweep {
		factor = -factor
	}
	cx, cy := factor*rx*y/ry, -factor*ry*x/rx
	centre := point{cos*cx - sin*cy + (from.x+to.x)/2, sin*cx + cos*cy + (from.y+to.y)/2}
	angle := func(ux, uy, vx, vy float64) float64 { return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy) }
	first := angle(1, 0, (x-cx)/rx, (y-cy)/ry)
	delta := angle((x-cx)/rx, (y-cy)/ry, (-x-cx)/rx, (-y-cy)/ry)
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	} else if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	steps := max(2, int(math.Ceil(math.Abs(delta)/arcStep)))
	points := make([]point, steps)
	for i := range points {
		a := first + delta*float64(i+1)/float64(steps)
		ex, ey := rx*math.Cos(a), ry*math.Sin(a)
		points[i] = point{centre.x + cos*ex - sin*ey, centre.y + sin*ex + cos*ey}
	}
	points[steps-1] = to
	return points
}
