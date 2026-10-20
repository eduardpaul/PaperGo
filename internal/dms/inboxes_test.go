package dms

import (
	"errors"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/identity"
	"testing"
)

func TestGroupInboxPermissionsNestedDiscoveryAndCommitChecks(t *testing.T) {
	s, w, _ := fixture(t)
	ctx := t.Context()
	admin := "organization-admin"
	lib := create(t, s, w.ID, "library", "Shared Inbox", nil)
	other := create(t, s, w.ID, "library", "Replacement", nil)
	d := s.Directory()
	parent, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Child"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, admin, parent.ID, identity.Principal{Type: directory.PrincipalGroup, ID: child.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, admin, child.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", child.ID, GroupInboxInput{LibraryID: lib.ID}); err != nil {
		t.Fatal(err)
	}
	hidden, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Hidden"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, admin, hidden.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	personal, err := s.Home(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", hidden.ID, GroupInboxInput{LibraryID: personal.DocumentsLibraryID}); err != nil {
		t.Fatal(err)
	}
	// Alice owns the library but has no group administration role or membership.
	if _, err = s.SetGroupInbox(ctx, "alice", parent.ID, GroupInboxInput{LibraryID: lib.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InboxUploadDestination(ctx, "alice", parent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("nonmember upload", err)
	}
	if _, err = s.InboxUploadDestination(ctx, "bob", parent.ID); err == nil {
		t.Fatal("membership granted library access")
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	inboxes, err := s.Inboxes(ctx, "bob")
	if err != nil || len(inboxes) != 3 || inboxes[0].Kind != "personal" || *inboxes[1].GroupID != child.ID || *inboxes[2].GroupID != parent.ID || inboxes[1].LibraryID != inboxes[2].LibraryID {
		t.Fatal(inboxes, err)
	}
	if _, err = s.InboxUploadDestination(ctx, "bob", parent.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("read-only upload", err)
	}
	if _, err = s.SetGroupInbox(ctx, "bob", parent.ID, GroupInboxInput{LibraryID: other.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("read-only binding change", err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	destination, err := s.InboxUploadDestination(ctx, "bob", parent.ID)
	if err != nil || destination != lib.ID {
		t.Fatal(destination, err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", parent.ID, GroupInboxInput{LibraryID: other.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateInboxDocument(ctx, "bob", parent.ID, destination, DocumentMetadata{Name: "Wrong destination"}, BlobInput{}); !errors.Is(err, ErrConflict) {
		t.Fatal("rebinding not detected", err)
	}
	if err = d.SetMember(ctx, admin, child.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateInboxDocument(ctx, "bob", parent.ID, other.ID, DocumentMetadata{Name: "Revoked member"}, BlobInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatal("membership revocation not detected", err)
	}
	if err = s.Delete(ctx, "alice", other.ID, other.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GroupInbox(ctx, "alice", parent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted Inbox visible", err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", parent.ID, GroupInboxInput{LibraryID: lib.ID}); err != nil {
		t.Fatal("replace deleted Inbox", err)
	}
	if err = s.RemoveGroupInbox(ctx, "alice", parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DAL.Resources().Resource(ctx, lib.ID); err != nil {
		t.Fatal("unbinding deleted library", err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", parent.ID, GroupInboxInput{LibraryID: lib.ID}); err != nil {
		t.Fatal(err)
	}
	if err = d.DeleteGroup(ctx, admin, parent.ID, parent.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DAL.Directory().Inbox(ctx, parent.ID); !dberr.IsNotFound(err) {
		t.Fatal("group binding survived deletion", err)
	}
}

func TestGroupInboxReplacementRequiresOldLibraryManage(t *testing.T) {
	s, w, _ := fixture(t)
	ctx := t.Context()
	first := create(t, s, w.ID, "library", "First", nil)
	bw, err := s.Create(ctx, "bob", "", CreateResource{Kind: "workspace", Name: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, "bob", bw.ID, CreateResource{Kind: "library", Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.Directory().SaveGroup(ctx, "organization-admin", "", 0, identity.GroupInput{Name: "Managed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", group.ID, GroupInboxInput{LibraryID: first.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetGroupInbox(ctx, "bob", group.ID, GroupInboxInput{LibraryID: second.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("old library not authorized", err)
	}
	if err = s.RemoveGroupInbox(ctx, "bob", group.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("unmanaged removal", err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", group.ID, GroupInboxInput{LibraryID: w.ID}); err == nil {
		t.Fatal("workspace accepted as Inbox")
	}
}
