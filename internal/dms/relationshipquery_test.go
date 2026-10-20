package dms

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRelationshipDirectAndWorkspaceQueries(t *testing.T) {
	s, w, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	c := create(t, s, list.ID, "item", "C", nil)
	typ, err := s.CreateRelationshipType(testContext, "alice", RelationshipTypeInput{Name: "Line", Directed: true, Attributes: []CreateField{{Key: "amount", Label: "Amount", Type: "integer"}, {Key: "note", Label: "Note", Type: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	e1, err := s.Link(testContext, "alice", a.ID, CreateRelationship{Type: typ.ID, TargetID: b.ID, Metadata: map[string]any{"amount": json.Number("120"), "note": "Bob's"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Link(testContext, "alice", a.ID, CreateRelationship{Type: typ.ID, TargetID: c.ID, Metadata: map[string]any{"amount": json.Number("20")}}); err != nil {
		t.Fatal(err)
	}
	edge, err := s.Relationship(testContext, "alice", b.ID, e1.ID)
	if err != nil || edge.RelatedItem.ID != a.ID || edge.RelatedItem.WorkspaceName != w.Name || edge.RelatedItem.CollectionName != list.Name || edge.Version != 1 {
		t.Fatal(edge, err)
	}
	if _, err = s.Relationship(testContext, "alice", c.ID, e1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, filter := range []string{"attributes/amount gt 100", "100 lt attributes/amount", "attributes/note eq 'Bob''s'", "directed eq true and (attributes/amount ge 100 or false)", "not (attributes/amount le 100)", "id eq " + e1.ID} {
		page, err := s.WorkspaceRelationships(testContext, "alice", w.ID, WorkspaceRelationshipsQuery{Type: "Line", Directed: ptr(true), Filter: filter, After: "", Limit: 1})
		if err != nil || len(page.Data) != 1 || page.Data[0].ID != e1.ID || page.Data[0].TargetItem.ID != b.ID {
			t.Fatal(filter, page, err)
		}
	}
	page, err := s.WorkspaceRelationships(testContext, "alice", w.ID, WorkspaceRelationshipsQuery{Type: "", Directed: nil, Filter: "", After: "", Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	next, err := s.WorkspaceRelationships(testContext, "alice", w.ID, WorkspaceRelationshipsQuery{Type: "", Directed: nil, Filter: "", After: page.NextCursor, Limit: 1})
	if err != nil || len(next.Data) != 1 || next.Data[0].ID == page.Data[0].ID || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{"bob", "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(testContext, "alice", b.ID, latest(t, s, b.ID).Version, Permissions{Grants: []Permission{{"user", "alice", "write", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Relationship(testContext, "bob", a.ID, e1.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("hidden peer", err)
	}
	page, err = s.WorkspaceRelationships(testContext, "bob", w.ID, WorkspaceRelationshipsQuery{Type: "", Directed: nil, Filter: "", After: "", Limit: 50})
	if err != nil || len(page.Data) != 1 || page.Data[0].TargetID != c.ID {
		t.Fatal("hidden edge leaked", page, err)
	}
	for _, filter := range []string{"attributes/a/b eq 1", "directed gt true", "attributes/note lt 'a'", "id eq 'anything'", "attributes/amount eq 1; DELETE FROM resources", "attributes/note eq 'unterminated"} {
		if _, err = s.WorkspaceRelationships(testContext, "alice", w.ID, WorkspaceRelationshipsQuery{Type: "", Directed: nil, Filter: filter, After: "", Limit: 50}); err == nil {
			t.Fatal("invalid filter accepted", filter)
		}
	}
}
