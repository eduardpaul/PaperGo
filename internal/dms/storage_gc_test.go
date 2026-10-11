package dms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papergo/internal/storage"
)

func TestIdenticalBytesShareStorageAcrossItemsAndPurgeChecksReferences(t *testing.T) {
	s, w, _ := fixture(t)
	lib := create(t, s, w.ID, "library", "Files", nil)
	first := create(t, s, lib.ID, "item", "First", nil)
	otherWorkspace := create(t, s, "", "workspace", "Other", nil)
	otherLibrary := create(t, s, otherWorkspace.ID, "library", "Other files", nil)
	second := create(t, s, otherLibrary.ID, "item", "Second", nil)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	upload := func(id string, version int, filename, media string) *BlobUpload {
		t.Helper()
		object, e := store.Put(testContext, strings.NewReader("identical bytes"), 1024)
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.AttachBlob(testContext, "alice", id, version, BlobInput{ObjectKey: object.Key, Size: object.Size, SHA256: object.SHA256, Filename: filename, ContentType: media})
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	a := upload(first.ID, first.Version, "one.txt", "application/pdf")
	b := upload(second.ID, second.Version, "two.bin", "application/pdf")
	if a.ObjectKey != b.ObjectKey || b.Filename != "two.bin" || b.ContentType != "application/pdf" {
		t.Fatal("bytes or per-item metadata were not preserved", a, b)
	}
	if err = s.CleanupPurgedBlobs(testContext, store); err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{first.ID, second.ID} {
		current := latest(t, s, item)
		if err = s.Delete(testContext, "alice", item, current.Version, DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err = s.PurgeDeleted(testContext, "alice", *current.ContainerID, item, false); err != nil {
			t.Fatal(err)
		}
		if err = s.CleanupPurgedBlobs(testContext, store); err != nil {
			t.Fatal(err)
		}
		f, e := store.Open(testContext, a.ObjectKey)
		if item == first.ID {
			if e != nil {
				t.Fatal("purging one item removed shared bytes", e)
			}
			f.Close()
		} else if !os.IsNotExist(e) {
			if f != nil {
				f.Close()
			}
			t.Fatal("unreferenced shared bytes remain", e)
		}
	}
}

func TestObjectCollectionHonorsGraceAndFileVersions(t *testing.T) {
	s, w, _ := fixture(t)
	lib := create(t, s, w.ID, "library", "Files", nil)
	item := create(t, s, lib.ID, "item", "File", nil)
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	objects := make([]storage.Object, 3)
	for i, bytes := range []string{"referenced", "old orphan", "recent upload"} {
		objects[i], err = store.Put(testContext, strings.NewReader(bytes), 1024)
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			old := now.Add(-2 * ObjectGracePeriod)
			if err = os.Chtimes(filepath.Join(dir, objects[i].Key), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	referenced := objects[0]
	if _, err = s.AttachBlob(testContext, "alice", item.ID, item.Version, BlobInput{ObjectKey: referenced.Key, Size: referenced.Size, SHA256: referenced.SHA256, Filename: "file.txt", ContentType: "application/pdf"}); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectObjects(testContext, store, now); err != nil {
		t.Fatal(err)
	}
	for i, object := range objects {
		f, e := store.Open(testContext, object.Key)
		if i == 1 {
			if !os.IsNotExist(e) {
				t.Fatal("old orphan remains", e)
			}
		} else {
			if e != nil {
				t.Fatal("referenced or recent bytes were removed", e)
			}
			f.Close()
		}
	}
}
