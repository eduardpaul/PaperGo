package dms

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	"papergo/internal/identity"
	"papergo/internal/storage"
	"strings"
	"testing"
	"time"
)

func emptyPackageTarget(t *testing.T) *Service {
	t.Helper()
	s := NewService(daltest.EmptyDatabase(t).SQL)
	if err := s.Directory().Initialize(t.Context(), "organization-admin", ""); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestCorePackageConfigurationContentDryRunAndAtomicImport(t *testing.T) {
	s, w, list := fixture(t)
	ctx := t.Context()
	admin := "organization-admin"
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateField(ctx, "alice", list.ID, CreateField{Key: "amount", Label: "Amount", Type: "integer", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, list.ID, "item", "Invoice", map[string]any{"amount": json.Number("7")})
	if _, err = s.Publish(ctx, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	library := create(t, s, w.ID, "library", "Documents", nil)
	object, err := store.Put(ctx, strings.NewReader("%PDF-1.7\nDocument\n%%EOF"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreateDocument(ctx, "alice", library.ID, DocumentMetadata{Name: "Receipt"}, BlobInput{ObjectKey: object.Key, Filename: "receipt.pdf", ContentType: "application/pdf", Size: object.Size, SHA256: object.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Link(ctx, "alice", item.ID, CreateRelationship{TargetID: doc.Item.ID}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Directory().SetUserActive(ctx, admin, "bob", false); err != nil {
		t.Fatal(err)
	}
	home, err := s.Home(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.Directory().SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Package Inbox"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", group.ID, GroupInboxInput{LibraryID: home.InboxLibraryID}); err != nil {
		t.Fatal(err)
	}
	// A credential exists in the source, but never in the portable graph.
	if _, err = s.Directory().CreateAccessToken(ctx, admin, identity.AccessTokenInput{Name: "Export source", Scopes: []string{"organization.manage"}}); err != nil {
		t.Fatal(err)
	}
	// Moving a live folder can leave a retained deleted child deeper than
	// the live hierarchy limit. Packages preserve that tombstone unchanged.
	parent := list
	for i := 0; i < MaxFolderDepth-1; i++ {
		parent = create(t, s, parent.ID, "folder", fmt.Sprintf("depth-%d", i), nil)
	}
	moving := create(t, s, list.ID, "folder", "Retained parent", nil)
	deleted := create(t, s, moving.ID, "folder", "Retained child", nil)
	if err = s.Delete(ctx, "alice", deleted.ID, deleted.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Update(ctx, "alice", moving.ID, moving.Version, UpdateResource{ParentID: &parent.ID}); err != nil {
		t.Fatal(err)
	}
	create(t, s, list.ID, "item", strings.Repeat("漢", 1024), nil)
	export, err := s.StartExport(ctx, admin, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := s.ExportPackage(ctx, admin, export.ID)
	if err != nil {
		t.Fatal(err)
	}
	file, err := store.Open(ctx, result.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	// The manifest and the file bytes once.
	if len(z.File) != 2 {
		t.Fatal("identical physical content should be packaged once", len(z.File))
	}
	for _, entry := range z.File {
		if entry.Name == "manifest.json" {
			f, _ := entry.Open()
			manifest, _ := io.ReadAll(f)
			f.Close()
			if bytes.Contains(manifest, []byte("secret_hash")) || bytes.Contains(manifest, []byte("password_hash")) || bytes.Contains(manifest, []byte("access_tokens")) {
				t.Fatal("credential leaked")
			}
		}
	}
	target := emptyPackageTarget(t)
	targetStore, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = target.RegisterPortability(targetStore); err != nil {
		t.Fatal(err)
	}
	var corrupted bytes.Buffer
	writer := zip.NewWriter(&corrupted)
	for _, entry := range z.File {
		reader, _ := entry.Open()
		data, _ := io.ReadAll(reader)
		reader.Close()
		if strings.HasPrefix(entry.Name, "objects/") {
			data[0] ^= 1
		}
		out, _ := writer.Create(entry.Name)
		_, _ = out.Write(data)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	badObject, err := targetStore.Put(ctx, &corrupted, PackageLimit)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := target.StartImport(ctx, admin, badObject.Key, false, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = target.runOperations(ctx)
	bad, _ = findOperation(target, bad.ID)
	if bad.Status != operations.StatusFailed || !strings.Contains(bad.Error, "checksum") {
		t.Fatal(bad.Status, bad.Error)
	}
	if n, _ := countResources(target, resources.Filter{}); n != 0 {
		t.Fatal("corrupt package wrote content", n)
	}
	upload, err := targetStore.Put(ctx, bytes.NewReader(raw), PackageLimit)
	if err != nil {
		t.Fatal(err)
	}
	dry, err := target.StartImport(ctx, admin, upload.Key, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = target.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	dry, err = findOperation(target, dry.ID)
	if err != nil {
		t.Fatal(err)
	}
	var plan PackageResult
	if err = json.Unmarshal(dry.Result, &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Valid || !plan.DryRun || dry.Status != operations.StatusSucceeded {
		t.Fatal(string(dry.Result), dry.Error)
	}
	if n, _ := countResources(target, resources.Filter{}); n != 0 {
		t.Fatal("dry run wrote content", n)
	}
	imported := 0
	target.OnImport(func(context.Context, *Service) error {
		imported++
		return nil
	})
	hash, err := auth.HashPassword("destination-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = target.Directory().SetInitialPasswordHash(ctx, admin, hash); err != nil {
		t.Fatal(err)
	}
	real, err := target.StartImport(ctx, admin, upload.Key, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = target.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	real, err = findOperation(target, real.ID)
	if err != nil || real.Status != operations.StatusSucceeded {
		t.Fatal(err, real.Error)
	}
	if _, err = (auth.Local{DAL: target.DAL}).Check(ctx, admin, "destination-password"); err != nil {
		t.Fatalf("import removed the destination administrator password: %v", err)
	}
	restoredHome, err := target.Home(ctx, "alice")
	if err != nil || restoredHome != home {
		t.Fatal("Home identity not restored", restoredHome, err)
	}
	restoredInbox, err := target.GroupInbox(ctx, "alice", group.ID)
	if err != nil || restoredInbox.LibraryID != home.InboxLibraryID {
		t.Fatal("Inbox binding not restored", restoredInbox, err)
	}
	loaded, err := target.Get(ctx, admin, item.ID)
	if err != nil || loaded.Name != "Invoice" || fmt.Sprint(loaded.Values["amount"]) != "7" {
		t.Fatal(loaded, err)
	}
	blob, err := target.GetBlob(ctx, admin, doc.Item.ID, doc.File.ID)
	if err != nil {
		t.Fatal(err)
	}
	content, err := targetStore.Open(ctx, blob.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := io.ReadAll(content)
	content.Close()
	if !bytes.Equal(actual, []byte("%PDF-1.7\nDocument\n%%EOF")) {
		t.Fatal(string(actual))
	}
	if n, _ := target.DAL.Relationships().Count(ctx, relationships.Filter{}); n != 1 {
		t.Fatal("relationship not restored", n)
	}
	// Extensions learn of the import, which raised no change events.
	if imported != 1 {
		t.Fatal("import hooks ran", imported, "times")
	}
	if n, _ := countHistory(target); n == 0 {
		t.Fatal("history not restored")
	}
	if credentials := daltest.Count(t, target.DAL, "access_tokens", nil); credentials != 0 {
		t.Fatal(credentials)
	}
	if row, err := target.DAL.Resources().Resource(ctx, deleted.ID); err != nil || row.DeletedAt == nil {
		t.Fatal("tombstone not restored", row, err)
	}
	if _, err := target.RestoreDeleted(ctx, admin, list.ID, deleted.ID, false); err == nil {
		t.Fatal("overdeep deleted folder restored")
	}
	// Applying to a populated app must fail without changing its graph.
	before, _ := countResources(target, resources.Filter{})
	again, err := target.StartImport(ctx, admin, upload.Key, false, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = target.runOperations(ctx)
	again, _ = findOperation(target, again.ID)
	after, _ := countResources(target, resources.Filter{})
	if again.Status != operations.StatusFailed || before != after {
		t.Fatal(again.Status, before, after)
	}
	// Export bytes remain pinned past the ordinary orphan grace period.
	if err = s.CollectObjects(ctx, store, time.Now().UTC().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if file, err = store.Open(ctx, result.ObjectKey); err != nil {
		t.Fatal(err)
	}
	file.Close()
}
