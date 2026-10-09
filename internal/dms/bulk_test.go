package dms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"papergo/ent/itemrevision"
	"papergo/ent/resource"
	"strings"
	"sync"
	"testing"
)

func bulkOK(t *testing.T, s *Service, subject, collection string, operations ...BulkOperation) []BulkResult {
	t.Helper()
	out, err := s.Bulk(testContext, subject, collection, BulkRequest{Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != len(operations) {
		t.Fatalf("results: %+v", out)
	}
	return out.Data
}

func bulkFails(t *testing.T, s *Service, subject, collection string, index int, want error, operations ...BulkOperation) {
	t.Helper()
	out, err := s.Bulk(testContext, subject, collection, BulkRequest{Operations: operations})
	var failure *BulkError
	if !errors.As(err, &failure) || failure.Index != index || (want != nil && !errors.Is(err, want)) || len(out.Data) != 0 {
		t.Fatalf("expected failure %d/%v with no results, got %+v/%v", index, want, out, err)
	}
}

func TestBulkItemLifecycle(t *testing.T) {
	s, w, list := fixture(t)
	_, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "amount", Label: "Amount", Type: "integer", Indexed: true, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	folder := create(t, s, list.ID, "folder", "Archive", nil)
	created := bulkOK(t, s, "alice", list.ID,
		BulkOperation{Action: "create", Create: &BulkCreate{Name: "First", Values: map[string]any{"amount": json.Number("9007199254740993")}}},
		BulkOperation{Action: "create", ParentID: folder.ID, Create: &BulkCreate{Name: "Second", Values: map[string]any{"amount": json.Number("2")}}},
	)
	if created[0].Version != 1 || created[1].Version != 1 || created[0].ID == created[1].ID {
		t.Fatal(created)
	}
	name := "Changed"
	values := map[string]any{"amount": json.Number("9007199254740994")}
	updated := bulkOK(t, s, "alice", list.ID,
		BulkOperation{Action: "update", ID: created[0].ID, Version: 1, Update: &BulkUpdate{Name: &name, Values: &values, ParentID: &folder.ID}},
		BulkOperation{Action: "publish", ID: created[1].ID, Version: 1},
	)
	for _, r := range updated {
		if r.Version != 2 {
			t.Fatal(updated)
		}
	}
	item, err := s.Get(testContext, "alice", created[0].ID)
	if err != nil || item.Name != name || *item.ParentID != folder.ID || item.Values["amount"] != json.Number("9007199254740994") {
		t.Fatalf("update/move: %+v %v", item, err)
	}
	page, err := s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Search: name, Surface: "published"})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("draft leaked: %+v %v", page, err)
	}
	bulkOK(t, s, "alice", list.ID, BulkOperation{Action: "publish", ID: item.ID, Version: 2})
	query, err := s.Query(testContext, "alice", list.ID, QueryRequest{IncludeTotal: true, Surface: "published", Query: QuerySpec{Filter: &FilterExpr{Field: "amount", Op: "eq", Value: json.RawMessage("9007199254740994")}}})
	if err != nil || counted(query.Total) != 1 || query.Data[0].ID != item.ID {
		t.Fatalf("projection: %+v %v", query, err)
	}
	bulkOK(t, s, "alice", list.ID,
		BulkOperation{Action: "unpublish", ID: item.ID, Version: 3},
		BulkOperation{Action: "delete", ID: created[1].ID, Version: 2},
	)
	if _, err = s.GetSurface(testContext, "alice", item.ID, "published"); !errors.Is(err, ErrNotFound) {
		t.Fatal("unpublished item visible", err)
	}
	if _, err = s.Get(testContext, "alice", created[1].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted item visible", err)
	}
	revisions, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(created[1].ID)).Count(testContext)
	if err != nil || revisions != 1 {
		t.Fatal("delete changed history", revisions, err)
	}
}

func TestBulkRollbackEveryDerivedEffect(t *testing.T) {
	s, _, list := fixture(t)
	first := create(t, s, list.ID, "item", "Original", nil)
	second := create(t, s, list.ID, "item", "Second", nil)
	beforeAudit, _ := s.Client.AuditEvent.Query().Count(testContext)
	beforeRevisions, _ := s.Client.ItemRevision.Query().Count(testContext)
	name := "Rolled back"
	bulkFails(t, s, "alice", list.ID, 2, ErrConflict,
		BulkOperation{Action: "update", ID: first.ID, Version: 1, Update: &BulkUpdate{Name: &name}},
		BulkOperation{Action: "create", Create: &BulkCreate{Name: "Also rolled back"}},
		BulkOperation{Action: "publish", ID: second.ID, Version: 99},
	)
	got, err := s.Get(testContext, "alice", first.ID)
	if err != nil || got.Name != first.Name || got.Version != first.Version {
		t.Fatal("update survived rollback", got, err)
	}
	count, _ := s.Client.Resource.Query().Where(resource.ContainerIDEQ(list.ID), resource.KindEQ(resource.KindItem)).Count(testContext)
	audit, _ := s.Client.AuditEvent.Query().Count(testContext)
	revisions, _ := s.Client.ItemRevision.Query().Count(testContext)
	if count != 2 || audit != beforeAudit || revisions != beforeRevisions {
		t.Fatal("transaction effects survived", count, audit, revisions)
	}
	bulkFails(t, s, "alice", list.ID, 1, ErrConflict,
		BulkOperation{Action: "publish", ID: first.ID, Version: 1},
		BulkOperation{Action: "delete", ID: second.ID, Version: 99},
	)
	got, _ = s.Get(testContext, "alice", first.ID)
	publications, _ := s.Client.Publication.Query().Count(testContext)
	if got.PublishedRevisionID != nil || got.Version != 1 || publications != 0 {
		t.Fatal("publication survived rollback", got, publications)
	}
}

func TestBulkPermissionsAndCollectionBoundary(t *testing.T) {
	s, w, list := fixture(t)
	first := create(t, s, list.ID, "item", "Allowed", nil)
	blocked := create(t, s, list.ID, "item", "Blocked", nil)
	_, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage"}, {Subject: "bob", Action: "write"}}})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err = s.SetPermissions(testContext, "alice", blocked.ID, blocked.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage"}}})
	if err != nil {
		t.Fatal(err)
	}
	name := "Attempt"
	bulkFails(t, s, "bob", list.ID, 1, ErrForbidden,
		BulkOperation{Action: "update", ID: first.ID, Version: 1, Update: &BulkUpdate{Name: &name}},
		BulkOperation{Action: "delete", ID: blocked.ID, Version: blocked.Version},
	)
	got, _ := s.Get(testContext, "alice", first.ID)
	if got.Name != first.Name || got.Version != 1 {
		t.Fatal("authorization failure did not roll back", got)
	}
	other := create(t, s, w.ID, "list", "Other", nil)
	foreign := create(t, s, other.ID, "item", "Foreign", nil)
	for _, op := range []BulkOperation{
		{Action: "delete", ID: foreign.ID, Version: 1},
		{Action: "delete", ID: list.ID, Version: list.Version},
		{Action: "create", ParentID: other.ID, Create: &BulkCreate{Name: "Foreign"}},
		{Action: "update", ID: first.ID, Version: 1, Update: &BulkUpdate{ParentID: &other.ID}},
	} {
		bulkFails(t, s, "alice", list.ID, 0, nil, op)
	}
}

func TestBulkBoundsShapesAndDuplicateTargets(t *testing.T) {
	s, _, list := fixture(t)
	for _, operations := range [][]BulkOperation{nil, make([]BulkOperation, MaxBulkOperations+1)} {
		out, err := s.Bulk(testContext, "alice", list.ID, BulkRequest{Operations: operations})
		var validation *ValidationError
		var indexed *BulkError
		if !errors.As(err, &validation) || errors.As(err, &indexed) || len(out.Data) != 0 {
			t.Fatalf("batch size error: %+v %v", out, err)
		}
	}
	for _, op := range []BulkOperation{
		{Action: "unknown"},
		{Action: "create"},
		{Action: "create", ID: "id", Create: &BulkCreate{Name: "Bad"}},
		{Action: "create", Version: 1, Create: &BulkCreate{Name: "Bad"}},
		{Action: "create", Create: &BulkCreate{Name: "Bad"}, Update: &BulkUpdate{}},
		{Action: "update", ID: "id", Version: 1},
		{Action: "delete", ID: "id"},
		{Action: "publish", ID: "id", Version: -1},
		{Action: "delete", ID: "id", Version: 1, ParentID: list.ID},
		{Action: "unpublish", ID: "id", Version: 1, Update: &BulkUpdate{}},
	} {
		bulkFails(t, s, "alice", list.ID, 0, nil, op)
	}
	ops := make([]BulkOperation, MaxBulkOperations)
	for i := range ops {
		ops[i] = BulkOperation{Action: "create", Create: &BulkCreate{Name: fmt.Sprintf("Item %03d", i)}}
	}
	created := bulkOK(t, s, "alice", list.ID, ops...)
	seen := map[string]bool{}
	for _, item := range created {
		if seen[item.ID] || item.Version != 1 || item.Action != "create" {
			t.Fatal("invalid ordered result", item)
		}
		seen[item.ID] = true
	}
	bulkFails(t, s, "alice", list.ID, 1, nil,
		BulkOperation{Action: "publish", ID: created[0].ID, Version: 1},
		BulkOperation{Action: "delete", ID: created[0].ID, Version: 2},
	)
	item, err := s.Get(testContext, "alice", created[0].ID)
	if err != nil || item.Version != 1 || item.PublishedRevisionID != nil {
		t.Fatal("duplicate target mutated", item, err)
	}
	folder := create(t, s, list.ID, "folder", "Folder", nil)
	bulkFails(t, s, "alice", list.ID, 0, nil, BulkOperation{Action: "delete", ID: folder.ID, Version: 1})
}

func TestBulkLibraryConflictsAndPublication(t *testing.T) {
	s, w, _ := fixture(t)
	library := create(t, s, w.ID, "library", "Files", nil)
	bulkFails(t, s, "alice", library.ID, 1, ErrConflict,
		BulkOperation{Action: "create", Create: &BulkCreate{Name: "Case.txt"}},
		BulkOperation{Action: "create", Create: &BulkCreate{Name: "CASE.TXT"}},
	)
	count, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(library.ID)).Count(testContext)
	if err != nil || count != 0 {
		t.Fatal("colliding create survived", count, err)
	}
	created := bulkOK(t, s, "alice", library.ID,
		BulkOperation{Action: "create", Create: &BulkCreate{Name: "A.txt"}},
		BulkOperation{Action: "create", Create: &BulkCreate{Name: "B.txt"}},
	)
	attach := func(item BulkResult) {
		t.Helper()
		_, err := s.AttachBlob(testContext, "alice", item.ID, 1, BlobInput{ObjectKey: uuid.NewString(), Filename: "file.txt", ContentType: "text/plain", Size: 5, SHA256: strings.Repeat("a", 64)})
		if err != nil {
			t.Fatal(err)
		}
	}
	attach(created[0])
	bulkFails(t, s, "alice", library.ID, 1, nil,
		BulkOperation{Action: "publish", ID: created[0].ID, Version: 2},
		BulkOperation{Action: "publish", ID: created[1].ID, Version: 1},
	)
	first, err := s.Get(testContext, "alice", created[0].ID)
	if err != nil || first.Version != 2 || first.PublishedRevisionID != nil {
		t.Fatal("partial library publication", first, err)
	}
	attach(created[1])
	bulkOK(t, s, "alice", library.ID,
		BulkOperation{Action: "publish", ID: created[0].ID, Version: 2},
		BulkOperation{Action: "publish", ID: created[1].ID, Version: 2},
	)
	name := "New.txt"
	bulkFails(t, s, "alice", library.ID, 1, ErrConflict,
		BulkOperation{Action: "update", ID: created[0].ID, Version: 3, Update: &BulkUpdate{Name: &name}},
		BulkOperation{Action: "update", ID: created[1].ID, Version: 3, Update: &BulkUpdate{Name: &name}},
	)
	first, err = s.Get(testContext, "alice", created[0].ID)
	if err != nil || first.Name != "A.txt" || first.Version != 3 {
		t.Fatal("rename survived conflict", first, err)
	}
}

func TestBulkDeleteRollbackRestoresRelationshipsAndSurfaces(t *testing.T) {
	s, w, list := fixture(t)
	first := create(t, s, list.ID, "item", "Searchable document", nil)
	second := create(t, s, list.ID, "item", "Other document", nil)
	bulkOK(t, s, "alice", list.ID,
		BulkOperation{Action: "publish", ID: first.ID, Version: 1},
		BulkOperation{Action: "publish", ID: second.ID, Version: 1},
	)
	typ, err := s.CreateRelationshipType(testContext, "alice", w.ID, RelationshipTypeInput{Key: "related", Label: "Related", Directed: true})
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.Link(testContext, "alice", first.ID, CreateRelationship{TypeID: typ.ID, TargetID: second.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"delete", "unpublish"} {
		bulkFails(t, s, "alice", list.ID, 1, ErrConflict,
			BulkOperation{Action: action, ID: first.ID, Version: 2},
			BulkOperation{Action: "delete", ID: second.ID, Version: 99},
		)
		page, err := s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Search: "Searchable", Surface: "published"})
		if err != nil || len(page.Data) != 1 || page.Data[0].ID != first.ID || page.Data[0].Version != 2 {
			t.Fatalf("%s lost published FTS surface: %+v %v", action, page, err)
		}
		if _, err := s.Client.Relationship.Get(testContext, link.ID); err != nil {
			t.Fatal("relationship lost on rollback", err)
		}
	}
	bulkOK(t, s, "alice", list.ID,
		BulkOperation{Action: "delete", ID: first.ID, Version: 2},
		BulkOperation{Action: "delete", ID: second.ID, Version: 2},
	)
	links, _ := s.Client.Relationship.Query().Count(testContext)
	surfaces, _ := s.Client.ItemSurface.Query().Count(testContext)
	revisions, _ := s.Client.ItemRevision.Query().Count(testContext)
	if links != 0 || surfaces != 0 || revisions != 2 {
		t.Fatal("delete effects", links, surfaces, revisions)
	}
}

func TestConcurrentBulkUpdatesHaveOneWholeBatchWinner(t *testing.T) {
	s, _, list := fixture(t)
	first := create(t, s, list.ID, "item", "First", nil)
	second := create(t, s, list.ID, "item", "Second", nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("Writer %d", i)
			_, errs[i] = s.Bulk(testContext, "alice", list.ID, BulkRequest{Operations: []BulkOperation{
				{Action: "update", ID: first.ID, Version: 1, Update: &BulkUpdate{Name: &name}},
				{Action: "update", ID: second.ID, Version: 1, Update: &BulkUpdate{Name: &name}},
			}})
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	for i, err := range errs {
		if err == nil {
			if winner != -1 {
				t.Fatal("both batches won")
			}
			winner = i
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winner == -1 {
		t.Fatal("neither batch won")
	}
	for _, id := range []string{first.ID, second.ID} {
		got, err := s.Get(testContext, "alice", id)
		if err != nil || got.Version != 2 || got.Name != fmt.Sprintf("Writer %d", winner) {
			t.Fatal("mixed batch result", got, err)
		}
	}
}

func TestBulkPublisherAndCancelledWriter(t *testing.T) {
	s, w, list := fixture(t)
	item := create(t, s, list.ID, "item", "Draft", nil)
	_, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage"}, {Subject: "publisher", Action: "publish"}}})
	if err != nil {
		t.Fatal(err)
	}
	bulkOK(t, s, "publisher", list.ID, BulkOperation{Action: "publish", ID: item.ID, Version: 1})
	bulkFails(t, s, "publisher", list.ID, 0, ErrForbidden, BulkOperation{Action: "delete", ID: item.ID, Version: 2})
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	s.writeMu <- struct{}{}
	out, err := s.Bulk(ctx, "alice", list.ID, BulkRequest{Operations: []BulkOperation{{Action: "delete", ID: item.ID, Version: 2}}})
	<-s.writeMu
	if !errors.Is(err, context.Canceled) || len(out.Data) != 0 {
		t.Fatal("cancelled batch", out, err)
	}
	got, err := s.Get(testContext, "alice", item.ID)
	if err != nil || got.Version != 2 {
		t.Fatal("cancelled mutation committed", got, err)
	}
}
