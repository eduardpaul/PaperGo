package documentsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/dms"
	"papergo/internal/model"
)

// BenchmarkDocuments builds the search documents of 100 items, each with
// two leaf keywords of a 10×10×10 keyword tree and two leaves of a managed
// tree in a field that applies parents.
func BenchmarkDocuments(b *testing.B) {
	ctx := context.Background()
	s := dms.NewService(daltest.Database(b).SQL)
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		b.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		b.Fatal(err)
	}
	group, err := s.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Bench"})
	if err != nil {
		b.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "places", Name: "Places"})
	if err != nil {
		b.Fatal(err)
	}
	leaves := func(setID, prefix string) []string {
		var out []string
		for i := range 10 {
			root, err := s.CreateTerm(ctx, "alice", setID, dms.TermInput{Name: fmt.Sprintf("%s%d", prefix, i)})
			if err != nil {
				b.Fatal(err)
			}
			for j := range 10 {
				mid, err := s.CreateTerm(ctx, "alice", setID, dms.TermInput{Name: fmt.Sprintf("%s%d.%d", prefix, i, j), ParentID: &root.ID})
				if err != nil {
					b.Fatal(err)
				}
				for k := range 10 {
					leaf, err := s.CreateTerm(ctx, "alice", setID, dms.TermInput{Name: fmt.Sprintf("%s%d.%d.%d", prefix, i, j, k), ParentID: &mid.ID})
					if err != nil {
						b.Fatal(err)
					}
					out = append(out, leaf.ID)
				}
			}
		}
		return out
	}
	keywords, places := leaves(taxonomy.KeywordsSetID, "k"), leaves(set.ID, "p")
	// Decoded at run time so the benchmark builds where the option is unknown.
	var options model.FieldOptions
	if err = json.Unmarshal([]byte(`{"term_set_id":"`+set.ID+`","multiple":true,"apply_parents":true}`), &options); err != nil {
		b.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", list.ID, dms.CreateField{Key: "places", Label: "Places", Type: "term", Indexed: true, Options: options}); err != nil {
		b.Fatal(err)
	}
	ids := []string{}
	for i := range 100 {
		r, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: fmt.Sprintf("Item %d", i),
			Keywords: []string{keywords[i*7%1000], keywords[(i*13+5)%1000]},
			Values:   map[string]any{"places": []any{places[i*11%1000], places[(i*17+3)%1000]}}})
		if err != nil {
			b.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := documents(ctx, s, ids); err != nil {
			b.Fatal(err)
		}
	}
}
