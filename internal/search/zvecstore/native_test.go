package zvecstore

import (
	"math"
	"path/filepath"
	"testing"
)

func TestNativeABI(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	h, err := createCollection(filepath.Join(t.TempDir(), "collection"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	r := newRow("probe")
	r.text("row_kind", "doc")
	r.text("resource_id", "b")
	r.text("payload", `{}`)
	r.text("content", "invoice")
	r.text("metadata", "invoice")
	r.array("tokens", []string{"a", "b", "c", "d", "e", "f", "g", "h"})
	if err = writeRows(h, []*row{r}, false); err != nil {
		t.Fatal(err)
	}
	if err = h.Optimize(); err != nil {
		t.Fatal(err)
	}
	hits, err := nativeQuery(h, "content", "invoice", "resource_id > 'a'", nil, 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("STRING range: %+v %v", hits, err)
	}
	for _, dimensions := range []int{4, 768} {
		axis := func(index int, value float32) []float32 { v := make([]float32, dimensions); v[index] = value; return v }
		v, err := createCollection(filepath.Join(t.TempDir(), "vectors"), dimensions)
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		vr := newRow("vector")
		vr.text("row_kind", "passage")
		vr.array("tokens", []string{"a", "b", "c", "d", "e", "f", "g", "h"})
		vr.vector(axis(0, 1))
		if err = writeRows(v, []*row{vr}, false); err != nil {
			t.Fatal(err)
		}
		other := newRow("orthogonal")
		other.text("row_kind", "passage")
		other.array("tokens", []string{"h"})
		other.vector(axis(1, 1))
		opposite := newRow("opposite")
		opposite.text("row_kind", "passage")
		opposite.array("tokens", []string{"h"})
		opposite.vector(axis(0, -1))
		if err = writeRows(v, []*row{other, opposite}, false); err != nil {
			t.Fatal(err)
		}
		assertScores := func() {
			t.Helper()
			hits, err := nativeQuery(v, "embedding", "", "tokens CONTAIN_ANY ('h')", axis(0, 1), 10)
			if err != nil || len(hits) != 3 {
				t.Fatal(hits, err)
			}
			for i, want := range []float32{0, 1, 2} {
				if math.IsNaN(float64(hits[i].score)) || math.Abs(float64(hits[i].score-want)) > 1e-5 {
					t.Fatalf("native cosine distances: %+v", hits)
				}
			}
		}
		assertScores()
		if err = v.Optimize(); err != nil {
			t.Fatal(err)
		}
		assertScores()
	}
	if _, err = nativeQuery(h, "content", "invoice", "resource_id == 'b'", nil, 10); err == nil {
		t.Fatal("invalid filter accepted")
	}
}
