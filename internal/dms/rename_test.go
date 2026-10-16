package dms

import (
	"errors"
	"testing"

	"papergo/ent/domainevent"
)

func TestRenameFileFreezesDisplayNamesAndPreservesBytes(t *testing.T) {
	s, ws, _ := fixture(t)
	library, err := s.Create(testContext, "alice", ws.ID, CreateResource{Kind: "library", Name: "Files", Versioning: ptr("major"), PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", ws.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreateDocument(testContext, "alice", library.ID, DocumentMetadata{Name: "Original.pdf"}, content("original.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(testContext, "alice", doc.Item.ID, doc.Item.Version); err != nil {
		t.Fatal(err)
	}
	item := latest(t, s, doc.Item.ID)
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Quarterly")})
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.GetBlob(testContext, "alice", item.ID, "")
	if err != nil || file.Filename != "Quarterly.pdf" || file.ID != doc.File.ID {
		t.Fatal("rename changed bytes or lost extension", file, err)
	}
	published, err := s.GetBlob(testContext, "reader", item.ID, "")
	if err != nil || published.Filename != "original.pdf" {
		t.Fatal("draft rename changed publication", published, err)
	}
	stored, err := s.Client.Blob.Get(testContext, doc.File.ID)
	if err != nil || stored.Filename != "original.pdf" {
		t.Fatal("rename mutated uploaded version", stored, err)
	}
	first, err := s.Revision(testContext, "alice", item.ID, 1)
	if err != nil || first.Filename != "original.pdf" {
		t.Fatal("rename mutated history", first, err)
	}
	versions, err := s.FileVersions(testContext, "alice", item.ID, 0, 10)
	if err != nil || len(versions.Data) != 1 || versions.Data[0].Filename != "Quarterly.pdf" {
		t.Fatal(versions, err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	published, err = s.GetBlob(testContext, "reader", item.ID, "")
	if err != nil || published.Filename != "Quarterly.pdf" {
		t.Fatal(published, err)
	}
	other := create(t, s, library.ID, "item", "Taken.pdf", nil)
	item = latest(t, s, item.ID)
	before := revisionCount(t, s, item.ID)
	if _, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: &other.Name}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate name accepted", err)
	}
	if revisionCount(t, s, item.ID) != before {
		t.Fatal("failed rename recorded a revision")
	}
	if n, err := s.Client.DomainEvent.Query().Where(domainevent.ResourceIDEQ(item.ID), domainevent.TypeEQ(EventFileUpdated)).Count(testContext); err != nil || n != 0 {
		t.Fatal("rename triggered file processing", n, err)
	}
}

func TestUploadConflictDisclosesOnlyWritableItems(t *testing.T) {
	s, ws, _ := fixture(t)
	lib := create(t, s, ws.ID, "library", "Documents", nil)
	if err := s.SetWorkspaceMember(testContext, "alice", ws.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreateDocument(testContext, "alice", lib.ID, DocumentMetadata{Name: "Private.pdf"}, content("private.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(testContext, "alice", doc.Item.ID, doc.Item.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateDocument(testContext, "bob", lib.ID, DocumentMetadata{Name: "private.pdf"}, content("private.pdf"))
	var disclosed *DocumentNameConflict
	if !errors.Is(err, ErrConflict) || errors.As(err, &disclosed) {
		t.Fatal("conflict disclosed a private replacement target", err)
	}
}
