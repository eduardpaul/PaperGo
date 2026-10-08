package dms

import (
	"context"
	"fmt"
	"papergo/internal/testutil"
	"testing"
)

func TestSmartFolderCollectionBudgetDoesNotTruncate(t *testing.T) {
	s, w, l := fixture(t)
	for i := 0; i < 100; i++ {
		create(t, s, w.ID, "list", fmt.Sprintf("Extra %03d", i), nil)
	}
	f := smartOK(t, s, SmartFolderInput{Name: "Too broad", WorkspaceID: &w.ID})
	if _, err := s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{}); !isValidation(err) {
		t.Fatal("broad folder silently truncated", err)
	}
	in := SmartFolderInput{Name: f.Name, WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Collections: []string{l.Name}}}
	f, err := s.UpdateSmartFolder(testContext, "alice", f.ID, f.Version, in)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{}); err != nil || rows.Total != 0 {
		t.Fatal(rows, err)
	}
}

func BenchmarkSmartFolderQuery100Collections(b *testing.B) {
	db := testutil.Database(b)
	s := NewService(db.Client)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", CreateResource{Kind: "workspace", Name: "Performance"})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		c, err := s.Create(ctx, "alice", w.ID, CreateResource{Kind: "list", Name: fmt.Sprintf("List %03d", i)})
		if err != nil {
			b.Fatal(err)
		}
		if _, err = s.CreateField(ctx, "alice", c.ID, CreateField{Key: "status", Label: "Status", Type: "text", Indexed: true}); err != nil {
			b.Fatal(err)
		}
		ops := []BulkOperation{}
		for j := 0; j < 10; j++ {
			status := "closed"
			if j%2 == 0 {
				status = "open"
			}
			ops = append(ops, BulkOperation{Action: "create", Create: &BulkCreate{Name: fmt.Sprintf("Item %03d", j), Values: map[string]any{"status": status}}})
		}
		if _, err = s.Bulk(ctx, "alice", c.ID, BulkRequest{Operations: ops}); err != nil {
			b.Fatal(err)
		}
	}
	f, err := s.CreateSmartFolder(ctx, "alice", SmartFolderInput{Name: "Open", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Filter: &FilterExpr{Field: "status", Value: []byte(`"open"`)}}})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := s.QuerySmartFolder(ctx, "alice", f.ID, SmartFolderQueryRequest{Limit: 50})
		if err != nil || rows.Total != 500 || len(rows.Data) != 50 {
			b.Fatal(rows, err)
		}
	}
}
