package dms

import (
	"context"
	"strconv"
	"testing"
)

// BenchmarkBrowse lists a collection's children a page at a time, as the
// web client's table does.
func BenchmarkBrowse(b *testing.B) {
	s, _, list, _ := benchCollection(b)
	ctx := context.Background()
	for _, top := range []int{50, 500} {
		b.Run(strconv.Itoa(top), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := s.Browse(ctx, "alice", Browse{ParentID: list, Limit: top}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
