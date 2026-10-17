package httpapi

import (
	"papergo/internal/dms"
	"strings"
	"testing"
)

func keywordLabels(t *testing.T, s *dms.Service, ids []string) string {
	t.Helper()
	names := []string{}
	for _, id := range ids {
		term, err := s.Term(t.Context(), "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, term.Name)
	}
	return strings.Join(names, ",")
}
