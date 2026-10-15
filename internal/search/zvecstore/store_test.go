package zvecstore

import (
	"context"
	"path/filepath"
	"testing"

	"papergo/internal/search"
	"papergo/internal/search/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) search.Store {
		s, err := Open(context.Background(), filepath.Join(t.TempDir(), "zvec"), Options{Languages: []string{"english"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		return s
	})
}
