package dms

import (
	"errors"
	"testing"
)

func TestMoveSelectionMovesFoldersAndItemsAtomically(t *testing.T) {
	s, _, list := fixture(t)
	folder := create(t, s, list.ID, "folder", "Folder", nil)
	child := create(t, s, folder.ID, "item", "Child", nil)
	item := create(t, s, list.ID, "item", "Loose", nil)
	target := create(t, s, list.ID, "folder", "Archive", nil)
	moved, err := s.MoveSelection(testContext, "alice", list.ID, MoveSelection{ParentID: target.ID, Items: []MoveSelectionItem{{folder.ID, folder.Version}, {item.ID, item.Version}}})
	if err != nil || len(moved) != 2 {
		t.Fatal(moved, err)
	}
	for _, entry := range moved {
		current := latest(t, s, entry.ID)
		if *current.ParentID != target.ID || current.Version != entry.Version {
			t.Fatal("wrong move", current)
		}
	}
	if *latest(t, s, child.ID).ParentID != folder.ID {
		t.Fatal("folder subtree was flattened")
	}
}

func TestMoveSelectionRollsBackCyclesAndRejectsOverlappingSelections(t *testing.T) {
	s, _, list := fixture(t)
	folder := create(t, s, list.ID, "folder", "Folder", nil)
	child := create(t, s, folder.ID, "folder", "Child", nil)
	item := create(t, s, list.ID, "item", "Loose", nil)
	for _, request := range []MoveSelection{
		{ParentID: child.ID, Items: []MoveSelectionItem{{item.ID, item.Version}, {folder.ID, folder.Version}}},
		{Items: []MoveSelectionItem{{folder.ID, folder.Version}, {child.ID, child.Version}}},
		{ParentID: folder.ID, Items: []MoveSelectionItem{{item.ID, item.Version + 1}}},
	} {
		if _, err := s.MoveSelection(testContext, "alice", list.ID, request); err == nil {
			t.Fatal("invalid move accepted", request)
		}
		if got := latest(t, s, item.ID); *got.ParentID != list.ID || got.Version != item.Version {
			t.Fatal("failed selection partially moved", got)
		}
	}
	if _, err := s.MoveSelection(testContext, "bob", list.ID, MoveSelection{ParentID: folder.ID, Items: []MoveSelectionItem{{item.ID, item.Version}}}); err == nil {
		t.Fatal("unauthorized move accepted")
	}
}

func TestMoveSelectionConflictDisclosesOnlyWritableFiles(t *testing.T) {
	s, workspace, _ := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", workspace.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	library := create(t, s, workspace.ID, "library", "Files", nil)
	target := create(t, s, library.ID, "folder", "Archive", nil)
	source := create(t, s, library.ID, "item", "Report.pdf", nil)
	existing := create(t, s, target.ID, "item", "report.PDF", nil)
	request := MoveSelection{ParentID: target.ID, Items: []MoveSelectionItem{{source.ID, source.Version}}}
	_, err := s.MoveSelection(testContext, "bob", library.ID, request)
	var conflict *MoveNameConflict
	if !errors.As(err, &conflict) || conflict.Existing == nil || conflict.Existing.ItemID != existing.ID {
		t.Fatal("writable conflict not identified", err)
	}
	if _, err = s.SetPermissions(testContext, "alice", existing.ID, existing.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.MoveSelection(testContext, "bob", library.ID, request)
	conflict = nil
	if !errors.As(err, &conflict) || conflict.SourceID != source.ID || conflict.Existing != nil {
		t.Fatal("private replacement target leaked", err)
	}
	if *latest(t, s, source.ID).ParentID != library.ID {
		t.Fatal("conflicting move changed source")
	}
}

func TestMoveConflictReplacementIsAtomic(t *testing.T) {
	s, workspace, _ := fixture(t)
	library := create(t, s, workspace.ID, "library", "Files", nil)
	folder := create(t, s, library.ID, "folder", "Archive", nil)
	source := create(t, s, library.ID, "item", "Report.pdf", nil)
	existing := create(t, s, folder.ID, "item", "report.PDF", nil)
	ops := BulkRequest{Operations: []BulkOperation{
		{Action: "delete", ID: existing.ID, Version: existing.Version},
		{Action: "update", ID: source.ID, Version: source.Version + 1, Update: &BulkUpdate{ParentID: &folder.ID}},
	}}
	if _, err := s.Bulk(testContext, "alice", library.ID, ops); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal(err)
	}
	if _, err := s.Get(testContext, "alice", existing.ID); err != nil {
		t.Fatal("failed replacement deleted destination", err)
	}
	ops.Operations[1].Version = source.Version
	if _, err := s.Bulk(testContext, "alice", library.ID, ops); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(testContext, "alice", existing.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("destination not recycled", err)
	}
	if *latest(t, s, source.ID).ParentID != folder.ID {
		t.Fatal("source not moved")
	}
}
