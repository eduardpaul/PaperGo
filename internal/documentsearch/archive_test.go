package documentsearch

import (
	"context"
	"papergo/internal/dal/refs"
	"papergo/internal/dms"
	"testing"
)

func TestArchiveSearchChecksCurrentStateAndReindexes(t *testing.T) {
	_, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", c.ID, dms.CreateResource{Kind: "item", Name: "Invoice"})
	if err != nil {
		t.Fatal(err)
	}
	indexSearch(t, s)
	archived, err := s.SetArchived(ctx, "alice", item.ID, item.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := extension(t, s).Search(ctx, s, "alice", Request{Q: "invoice", Facets: true})
	if err != nil || len(result.Hits) != 0 || result.Facets != nil || !result.TotalEstimated {
		t.Fatal("stale active hit leaked", result, err)
	}
	page, err := s.ArchiveItems(ctx, "alice", dms.ArchiveQuery{})
	if err != nil || len(page.Data) != 1 {
		t.Fatal("archive not immediately browsable", page, err)
	}
	indexSearch(t, s)
	result, err = extension(t, s).Search(ctx, s, "alice", Request{Archive: refs.ArchiveArchived, Facets: true})
	if err != nil || len(result.Hits) != 1 || !result.Hits[0].Archived || result.Hits[0].ArchivedBy != "alice" || result.Hits[0].ArchivedAt == nil {
		t.Fatal("archived search", result, err)
	}
	if _, err = s.SetArchived(ctx, "alice", item.ID, archived.Version, false); err != nil {
		t.Fatal(err)
	}
	result, err = extension(t, s).Search(ctx, s, "alice", Request{Archive: refs.ArchiveArchived, Q: "invoice", Facets: true})
	if err != nil || len(result.Hits) != 0 || result.Facets != nil {
		t.Fatal("stale archived hit leaked", result, err)
	}
	indexSearch(t, s)
	result, err = extension(t, s).Search(ctx, s, "alice", Request{Q: "invoice"})
	if err != nil || len(result.Hits) != 1 {
		t.Fatal("unarchived search", result, err)
	}
}
