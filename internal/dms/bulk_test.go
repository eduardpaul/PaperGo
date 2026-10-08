package dms

import (
	"encoding/json"
	"errors"
	"papergo/ent/itemrevision"
	"papergo/ent/resource"
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
	query, err := s.Query(testContext, "alice", list.ID, QueryRequest{Surface: "published", Query: QuerySpec{Filter: &FilterExpr{Field: "amount", Op: "eq", Value: json.RawMessage("9007199254740994")}}})
	if err != nil || query.Total != 1 || query.Data[0].ID != item.ID {
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
