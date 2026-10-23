package builtin

import "testing"

func TestMark(t *testing.T) {
	for _, c := range []struct {
		snippet string
		ranges  [][2]int
		want    string
	}{
		{"pay the invoice now", [][2]int{{8, 15}}, "pay the «invoice» now"},
		{"a b c", [][2]int{{0, 1}, {4, 5}}, "«a» b «c»"},
		// Overlapping, empty, out-of-range and mid-character ranges are skipped.
		{"één factuur", [][2]int{{1, 3}, {6, 13}, {0, 0}, {0, 99}}, "één «factuur»"},
	} {
		if got := mark(c.snippet, c.ranges); got != c.want {
			t.Errorf("mark(%q, %v) = %q, want %q", c.snippet, c.ranges, got, c.want)
		}
	}
}
