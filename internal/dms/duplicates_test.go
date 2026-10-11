package dms

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"papergo/ent/blob"
)

func TestDuplicatePolicyAcrossWorkspacesAndVisibility(t *testing.T) {
	s, w, _ := fixture(t)
	lib := create(t, s, w.ID, "library", "Files", nil)
	a := create(t, s, lib.ID, "item", "Original", nil)
	file := func() BlobInput {
		return BlobInput{ObjectKey: uuid.NewString(), Filename: "file.txt", ContentType: "application/pdf", Size: 1, SHA256: emptySHA256}
	}
	if _, err := s.AttachBlob(testContext, "alice", a.ID, a.Version, file()); err != nil {
		t.Fatal(err)
	}
	other := create(t, s, "", "workspace", "Other", nil)
	target := create(t, s, other.ID, "library", "Target", nil)
	b := create(t, s, target.ID, "item", "Copy", nil)
	out, err := s.AttachBlob(testContext, "alice", b.ID, b.Version, file())
	if err != nil || len(out.Duplicates) != 1 || out.Duplicates[0].ItemID != a.ID || out.Duplicates[0].WorkspaceID != w.ID {
		t.Fatal(out, err)
	}
	policy := "block"
	target, err = s.Update(testContext, "alice", target.ID, target.Version, UpdateResource{DuplicatePolicy: &policy})
	if err != nil {
		t.Fatal(err)
	}
	c := create(t, s, target.ID, "item", "Blocked", nil)
	_, err = s.AttachBlob(testContext, "alice", c.ID, c.Version, file())
	var duplicate *DuplicateError
	if !errors.As(err, &duplicate) || len(duplicate.Documents) != 2 {
		t.Fatal("duplicate accepted", err)
	}
	if n, e := s.Client.Blob.Query().Where(blob.ItemIDEQ(c.ID)).Count(testContext); e != nil || n != 0 || latest(t, s, c.ID).Version != c.Version {
		t.Fatal("blocked upload mutated item", n, e)
	}
	// An editor in a different workspace cannot discover or be blocked by
	// inaccessible documents, even though their bytes have the same hash.
	hidden := create(t, s, "", "workspace", "Hidden", nil)
	if err = s.SetWorkspaceMember(testContext, "alice", hidden.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	hlib := create(t, s, hidden.ID, "library", "Private", nil)
	if _, err = s.Update(testContext, "alice", hlib.ID, hlib.Version, UpdateResource{DuplicatePolicy: &policy}); err != nil {
		t.Fatal(err)
	}
	hitem, err := s.Create(testContext, "bob", hlib.ID, CreateResource{Kind: "item", Name: "Private copy"})
	if err != nil {
		t.Fatal(err)
	}
	out, err = s.AttachBlob(testContext, "bob", hitem.ID, hitem.Version, file())
	if err != nil || len(out.Duplicates) != 0 {
		t.Fatal("private file disclosed", out, err)
	}
	// Replacing one's own file is allowed; current duplicates in other visible
	// items still matter. Allow policy suppresses warnings completely.
	policy = "allow"
	if _, err = s.Update(testContext, "alice", target.ID, target.Version, UpdateResource{DuplicatePolicy: &policy}); err != nil {
		t.Fatal(err)
	}
	out, err = s.AttachBlob(testContext, "alice", c.ID, c.Version, file())
	if err != nil || len(out.Duplicates) != 0 {
		t.Fatal(out, err)
	}
}

func TestDuplicatePolicyPublishedVisibilityAndWebDAV(t *testing.T) {
	s, w, _ := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	source := create(t, s, w.ID, "library", "Source", nil)
	item := create(t, s, source.ID, "item", "Source file", nil)
	file := func(hash string) BlobInput {
		return BlobInput{ObjectKey: uuid.NewString(), Filename: "file.txt", ContentType: "application/pdf", Size: 1, SHA256: strings.Repeat(hash, 64)}
	}
	if _, err := s.AttachBlob(testContext, "alice", item.ID, 1, file("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(testContext, "alice", item.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachBlob(testContext, "alice", item.ID, 3, file("b")); err != nil {
		t.Fatal(err)
	}
	destination := create(t, s, "", "workspace", "Destination", nil)
	if err := s.SetWorkspaceMember(testContext, "alice", destination.ID, WorkspaceMember{Subject: "bob", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	policy := "block"
	lib, err := s.Create(testContext, "bob", destination.ID, CreateResource{Kind: "library", Name: "Files", DuplicatePolicy: &policy, WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Create(testContext, "bob", lib.ID, CreateResource{Kind: "item", Name: "Published copy"})
	if err != nil {
		t.Fatal(err)
	}
	var duplicate *DuplicateError
	if _, err = s.AttachBlob(testContext, "bob", a.ID, 1, file("a")); !errors.As(err, &duplicate) {
		t.Fatal("published content did not block", err)
	}
	if _, err = s.AttachBlob(testContext, "bob", a.ID, 1, file("b")); err != nil {
		t.Fatal("private draft disclosed", err)
	}
	// WebDAV creation uses the same transactional policy and rolls back the
	// newly inserted metadata when the bytes duplicate a visible file.
	before, err := s.Client.Resource.Query().Count(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.PutFile(testContext, "bob", lib.ID, []string{"copy.txt"}, PutFile{Blob: file("b")}); !errors.As(err, &duplicate) {
		t.Fatal("WebDAV bypassed blocking", err)
	}
	if n, err := s.Client.Resource.Query().Count(testContext); err != nil || n != before {
		t.Fatal("WebDAV duplicate left metadata", n, err)
	}
	if err = s.Delete(testContext, "alice", item.ID, 4, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(testContext, "bob", lib.ID, CreateResource{Kind: "item", Name: "Deleted copy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachBlob(testContext, "bob", b.ID, 1, file("a")); err != nil {
		t.Fatal("deleted content blocked", err)
	}
}

func TestDuplicateBlockSerializesConcurrentUploads(t *testing.T) {
	s, w, _ := fixture(t)
	policy := "block"
	lib, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "library", Name: "Files", DuplicatePolicy: &policy})
	if err != nil {
		t.Fatal(err)
	}
	a := create(t, s, lib.ID, "item", "A", nil)
	b := create(t, s, lib.ID, "item", "B", nil)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AttachBlob(testContext, "alice", id, 1, BlobInput{ObjectKey: uuid.NewString(), Filename: "f", ContentType: "application/pdf", Size: 1, SHA256: emptySHA256})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, blocked := 0, 0
	for err := range results {
		var d *DuplicateError
		if err == nil {
			ok++
		} else if errors.As(err, &d) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || blocked != 1 {
		t.Fatal(ok, blocked)
	}
}
