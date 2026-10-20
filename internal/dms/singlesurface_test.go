package dms

import (
	"encoding/json"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
	"slices"
	"testing"
)

// Collections without publishing keep one surface per item and folder, the
// published one, and every read of head content uses it.
func TestCollectionsWithoutPublishingKeepOneSurface(t *testing.T) {
	s, w, _ := fixture(t)
	list, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Plain"})
	if err != nil || list.PublishingEnabled {
		t.Fatal(list, err)
	}
	fieldOK(t, s, list.ID, CreateField{Key: "stage", Label: "Stage", Type: "text", Indexed: true})
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	item := create(t, s, list.ID, "item", "Alpha report", map[string]any{"stage": "todo"})
	folder := create(t, s, list.ID, "folder", "Archive", nil)
	surfaceNames := func(id string) []string {
		t.Helper()
		rows, err := s.DAL.Surfaces().Surfaces(testContext, surfaces.Filter{ItemID: id})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, row := range rows {
			out = append(out, string(row.Surface))
		}
		slices.Sort(out)
		return out
	}
	one := []string{"published"}
	if got := surfaceNames(item.ID); !slices.Equal(got, one) {
		t.Fatal("item surfaces", got)
	}
	if got := surfaceNames(folder.ID); !slices.Equal(got, one) {
		t.Fatal("folder surfaces", got)
	}
	stage := func(subject, surface, value string) []*resources.Resource {
		t.Helper()
		q, err := s.Query(testContext, subject, list.ID, QueryRequest{Surface: surface, IncludeTotal: true, Query: QuerySpec{Filter: &FilterExpr{Field: "stage", Value: json.RawMessage(`"` + value + `"`)}}})
		if err != nil {
			t.Fatal(subject, surface, err)
		}
		if counted(q.Total) != len(q.Data) {
			t.Fatal("total", counted(q.Total), len(q.Data))
		}
		return q.Data
	}
	for _, read := range []struct{ subject, surface string }{{"alice", "auto"}, {"alice", "head"}, {"alice", "published"}, {"reader", "auto"}, {"reader", "published"}} {
		found := stage(read.subject, read.surface, "todo")
		if len(found) != 1 || found[0].ID != item.ID || found[0].Values["stage"] != "todo" {
			t.Fatal("read", read, found)
		}
		// Draft readers still see the item's head pointer; readers do not.
		if (found[0].HeadRevisionID != nil) != (read.subject == "alice" && read.surface != "published") {
			t.Fatal("head pointer", read, found[0].HeadRevisionID)
		}
	}
	if q, err := s.Query(testContext, "reader", list.ID, QueryRequest{Surface: "head"}); err != nil || len(q.Data) != 0 {
		t.Fatal("reader read head content", q, err)
	}
	for _, subject := range []string{"alice", "reader"} {
		indexSearch(t, s)
		page, err := s.Browse(testContext, subject, Browse{ParentID: list.ID, Search: "alph"})
		if err != nil || len(page.Data) != 1 || page.Data[0].ID != item.ID {
			t.Fatal("search", subject, page, err)
		}
	}
	patch := map[string]any{"stage": "done"}
	if _, err = s.Update(testContext, "alice", item.ID, latest(t, s, item.ID).Version, UpdateResource{Values: &patch}); err != nil {
		t.Fatal(err)
	}
	if len(stage("reader", "auto", "todo")) != 0 || len(stage("reader", "auto", "done")) != 1 || !slices.Equal(surfaceNames(item.ID), one) {
		t.Fatal("update")
	}

	// Starting to publish gives drafts their own surface.
	on, off := true, false
	list, err = s.Update(testContext, "alice", list.ID, latest(t, s, list.ID).Version, UpdateResource{PublishingEnabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	both := []string{"head", "published"}
	if !slices.Equal(surfaceNames(item.ID), both) || !slices.Equal(surfaceNames(folder.ID), both) {
		t.Fatal("publishing surfaces", surfaceNames(item.ID), surfaceNames(folder.ID))
	}
	patch = map[string]any{"stage": "draft"}
	if _, err = s.Update(testContext, "alice", item.ID, latest(t, s, item.ID).Version, UpdateResource{Values: &patch}); err != nil {
		t.Fatal(err)
	}
	if len(stage("alice", "head", "draft")) != 1 || len(stage("reader", "auto", "done")) != 1 || len(stage("reader", "auto", "draft")) != 0 {
		t.Fatal("draft kept from readers")
	}

	// Stopping publishes the drafts and keeps one surface again.
	list, err = s.Update(testContext, "alice", list.ID, latest(t, s, list.ID).Version, UpdateResource{PublishingEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(surfaceNames(item.ID), one) || !slices.Equal(surfaceNames(folder.ID), one) || len(stage("reader", "auto", "draft")) != 1 {
		t.Fatal("unpublishing collection", surfaceNames(item.ID))
	}
	if heads, err := countFieldValues(s, surfaces.ValueFilter{ContainerID: list.ID, Surface: refs.SurfaceHead}); err != nil || heads != 0 {
		t.Fatal("head field values left", heads, err)
	}

	// Restore and filtered bulk updates use the same single surface.
	if err = s.Delete(testContext, "alice", item.ID, latest(t, s, item.ID).Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreDeleted(testContext, "alice", list.ID, item.ID, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(surfaceNames(item.ID), one) || len(stage("reader", "auto", "draft")) != 1 {
		t.Fatal("restore", surfaceNames(item.ID))
	}
	op, err := s.StartBulkUpdate(testContext, "alice", list.ID, FilteredBulkUpdate{Query: QuerySpec{Filter: &FilterExpr{Field: "stage", Value: json.RawMessage(`"draft"`)}}, Values: map[string]any{"stage": "final"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	if got := bulkStatus(t, s, op.ID); got.Updated != 1 || len(stage("reader", "auto", "final")) != 1 || !slices.Equal(surfaceNames(item.ID), one) {
		t.Fatal("bulk update", got)
	}
}

// Moving an item between collections with and without publishing gives it the
// surfaces of its new collection.
func TestMovesFollowTheDestinationSurfaces(t *testing.T) {
	s, w, publishing := fixture(t)
	item := create(t, s, publishing.ID, "item", "Traveller", nil)
	plain, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Plain", ContentTypeIDs: []string{*item.ContentTypeID}})
	if err != nil {
		t.Fatal(err)
	}
	count := func() int {
		n, err := countSurfaces(s, surfaces.Filter{ItemID: item.ID})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 { // head only, not yet published
		t.Fatal("publishing collection", count())
	}
	if _, err = s.MoveItem(testContext, "alice", item.ID, latest(t, s, item.ID).Version, MoveItem{CollectionID: plain.ID}); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("into plain", count())
	}
	if _, err = s.MoveItem(testContext, "alice", item.ID, latest(t, s, item.ID).Version, MoveItem{CollectionID: publishing.ID}); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatal("back into publishing", count())
	}
}
