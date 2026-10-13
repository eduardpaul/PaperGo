package dms

import (
	"encoding/json"
	"testing"
)

func TestContainsFilterIgnoresCase(t *testing.T) {
	s, _, c := fixture(t)
	fieldOK(t, s, c.ID, CreateField{Key: "city", Label: "City", Type: "text", Indexed: true})
	acme := create(t, s, c.ID, "item", "Acme Travel 12", map[string]any{"city": "Zürich"})
	create(t, s, c.ID, "item", "Globex Software 7", map[string]any{"city": "Bern"})
	for _, f := range []FilterExpr{
		{Field: "$name", Op: "contains", Value: json.RawMessage(`"acm"`)},
		{Field: "$name", Op: "contains", Value: json.RawMessage(`"TRAVEL 1"`)},
		{Field: "city", Op: "contains", Value: json.RawMessage(`"ZÜR"`)},
	} {
		q, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{Filter: &f}})
		if err != nil || len(q.Data) != 1 || q.Data[0].ID != acme.ID {
			t.Fatal("contains", string(f.Value), q, err)
		}
	}
}
