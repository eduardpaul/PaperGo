package dms

import (
	"errors"
	"testing"
)

func TestRelationshipsRequireWritableEndpointsAndCanMutateFromEitherEnd(t *testing.T) {
	s, workspace, list := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", workspace.ID, WorkspaceMember{"bob", "member"}); err != nil {
		t.Fatal(err)
	}
	source := create(t, s, list.ID, "item", "Source", nil)
	target := create(t, s, list.ID, "item", "Target", nil)
	typ, err := s.CreateRelationshipType(testContext, "alice", workspace.ID, RelationshipTypeInput{Key: "owns", Label: "Owns", Directed: true, MaxOutgoing: ptr(1), Attributes: []CreateField{{Key: "note", Label: "Note", Type: "text", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	edge, err := s.Link(testContext, "bob", source.ID, CreateRelationship{TypeID: typ.ID, TargetID: target.ID, Metadata: map[string]any{"note": "original"}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Client.AuditEvent.Query().Count(testContext)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Link(testContext, "bob", source.ID, CreateRelationship{TypeID: typ.ID, TargetID: target.ID})
	if err != nil || retry.ID != edge.ID || retry.Version != edge.Version || retry.Metadata["note"] != "original" {
		t.Fatal("retry at full cardinality mutated edge", retry, err)
	}
	if after, err := s.Client.AuditEvent.Query().Count(testContext); err != nil || after != before {
		t.Fatal("no-op retry audited a mutation", after, err)
	}
	// An inbound directed edge can be updated from its target by an authorized editor.
	edge, err = s.UpdateRelationship(testContext, "bob", target.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"note": "updated"}})
	if err != nil {
		t.Fatal("target could not update directed edge", err)
	}
	target, err = s.SetPermissions(testContext, "alice", target.ID, target.Version, Permissions{Grants: []Permission{{"bob", "read", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Link(testContext, "bob", source.ID, CreateRelationship{TypeID: typ.ID, TargetID: target.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("read-only target allowed link", err)
	}
	if _, err = s.UpdateRelationship(testContext, "bob", source.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"note": "denied"}}); !errors.Is(err, ErrForbidden) {
		t.Fatal("read-only target allowed patch", err)
	}
	if err = s.UnlinkVersion(testContext, "bob", source.ID, edge.ID, edge.Version); !errors.Is(err, ErrForbidden) {
		t.Fatal("read-only target allowed unlink", err)
	}
	if _, err = s.SetPermissions(testContext, "alice", target.ID, target.Version, Permissions{Grants: []Permission{{"bob", "write", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.UnlinkVersion(testContext, "bob", target.ID, edge.ID, edge.Version); err != nil {
		t.Fatal("target could not delete directed edge", err)
	}
}
