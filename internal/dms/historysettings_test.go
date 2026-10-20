package dms

import (
	"errors"
	"fmt"
	"os"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/resources"
	"papergo/internal/storage"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHistoryDefaultsAndPolicyValidation(t *testing.T) {
	s, w, _ := fixture(t)
	for _, kind := range []string{"list", "library"} {
		c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: kind, Name: kind})
		if err != nil || c.Versioning != resources.VersioningOff || c.MaxVersions != 50 {
			t.Fatal(c, err)
		}
		item := create(t, s, c.ID, "item", "Item", nil)
		rows, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
		if err != nil || len(rows.Data) != 0 {
			t.Fatal(rows, err)
		}
		if _, err = s.ItemSchema(testContext, "alice", item.ID, "head"); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []CreateResource{
		{Kind: "list", Name: "Invalid", Versioning: ptr("minor")},
		{Kind: "list", Name: "Invalid", MaxVersions: ptr(0)},
		{Kind: "list", Name: "Invalid", MaxVersions: ptr(501)},
		{Kind: "folder", Name: "Invalid", Versioning: ptr("major")},
	} {
		if _, err := s.Create(testContext, "alice", w.ID, in); err == nil {
			t.Fatal("invalid history policy accepted", in)
		}
	}
	templates, err := s.CollectionTemplates(testContext, "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, recipe := range templates {
		if recipe.Versioning != "off" {
			t.Fatal("built-in template enables history", recipe.Key)
		}
		c, err := s.Create(testContext, "alice", w.ID, CreateResource{TemplateKey: recipe.Key, Name: recipe.Name})
		if err != nil || string(c.Versioning) != recipe.Versioning || c.MaxVersions != recipe.MaxVersions {
			t.Fatal(c, err)
		}
	}
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{TemplateKey: "documents", Name: "With history", Versioning: ptr("major"), MaxVersions: ptr(500)})
	if err != nil || c.Versioning != resources.VersioningMajor || c.MaxVersions != 500 {
		t.Fatal(c, err)
	}
}

func TestSnapshotCollectionKeepsHistorySurfacesAndQueuedEvents(t *testing.T) {
	s, w, _ := fixture(t)
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Retention", Versioning: ptr("major"), MaxVersions: ptr(2), PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, c.ID, "item", "Published", nil)
	publication, err := s.Publish(testContext, "alice", item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	item = latest(t, s, item.ID)
	for _, name := range []string{"Second", "Third", "Fourth", "Fifth"} {
		item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.CollectRevisions(testContext); err != nil || revisionCount(t, s, item.ID) != 5 {
		t.Fatal("queued event snapshots were removed", err)
	}
	// The dispatcher consumes term-change snapshots before releasing them.
	if err = dispatchAll(testContext, s.DAL); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectRevisions(testContext); err != nil || revisionCount(t, s, item.ID) != 3 {
		t.Fatal("snapshots exceed history plus published surface", err)
	}
	if current, e := s.GetSurface(testContext, "alice", item.ID, "published"); e != nil || current.Name != "Published" {
		t.Fatal(current, e)
	}
	if _, err = s.Unpublish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	if err = dispatchAll(testContext, s.DAL); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectRevisions(testContext); err != nil || revisionCount(t, s, item.ID) != 2 {
		t.Fatal("unpublished snapshot was retained", err)
	}
	if recorded := findPublication(t, s, publication.ID); !sameDocument(t, recorded.Snapshot, publication.Snapshot) {
		t.Fatal("publication audit snapshot changed", recorded)
	}
	if _, err = s.revision(testContext, *latest(t, s, item.ID).HeadRevisionID); err != nil {
		t.Fatal("live head was collected", err)
	}
}

func TestHistoryOffSnapshotsDoNotAccumulateAfterDispatch(t *testing.T) {
	s, w, _ := fixture(t)
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Off"})
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, c.ID, "item", "First", nil)
	for _, name := range []string{"Second", "Third", "Fourth"} {
		var err error
		item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
		if err = dispatchAll(testContext, s.DAL); err != nil {
			t.Fatal(err)
		}
		if err = s.CollectRevisions(testContext); err != nil || revisionCount(t, s, item.ID) != 1 {
			t.Fatal("history-off snapshots accumulated", err)
		}
	}
}

func TestHistoryRetentionDisableResumeAndPublishedSnapshot(t *testing.T) {
	s, w, _ := fixture(t)
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "History", Versioning: ptr("major"), MaxVersions: ptr(2), PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, c.ID, CreateField{Key: "unchanged", Label: "Unchanged", Type: "text"})
	other := create(t, s, c.ID, "item", "Other", nil)
	item := create(t, s, c.ID, "item", "First", map[string]any{"unchanged": "value"})
	publishItem(t, s, item)
	item = latest(t, s, item.ID)
	for _, name := range []string{"Second", "Third"} {
		item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
	if err != nil || len(rows.Data) != 2 || rows.Data[0].RevisionNumber != 3 || !slices.Equal(rows.Data[0].ChangedFields, []string{"$name"}) {
		t.Fatal(rows, err)
	}
	if otherRows, e := s.RevisionHistory(testContext, "alice", other.ID, 0, 10); e != nil || len(otherRows.Data) != 1 {
		t.Fatal("retention affected another item", otherRows, e)
	}
	for _, fetch := range []func() error{
		func() error { _, e := s.Revision(testContext, "alice", item.ID, 1); return e },
		func() error { _, e := s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version); return e },
	} {
		if e := fetch(); !errors.Is(e, ErrNotFound) {
			t.Fatal("trimmed revision accessible", e)
		}
	}
	c, err = s.Update(testContext, "alice", c.ID, latest(t, s, c.ID).Version, UpdateResource{Versioning: ptr("off"), MaxVersions: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("History off")})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
	if err != nil || len(rows.Data) != 2 || !rows.Data[0].IsCurrent || rows.Data[1].IsCurrent {
		t.Fatal(rows, err)
	}
	if _, err = s.Revision(testContext, "alice", item.ID, 4); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled snapshot exposed", err)
	}
	if _, err = s.RestoreRevision(testContext, "alice", item.ID, 4, item.Version); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled snapshot restored", err)
	}
	if head := latest(t, s, item.ID); head.Name != "History off" {
		t.Fatal(head)
	}
	if published, e := s.GetSurface(testContext, "alice", item.ID, "published"); e != nil || published.Name != "First" {
		t.Fatal(published, e)
	}
	// Historical changes remain readable and restorable while history is off.
	item, err = s.RestoreRevision(testContext, "alice", item.ID, 2, item.Version)
	if err != nil || item.Name != "Second" {
		t.Fatal(item, err)
	}
	_, err = s.Update(testContext, "alice", c.ID, c.Version, UpdateResource{Versioning: ptr("major")})
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Resumed")})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
	if err != nil || len(rows.Data) != 1 || rows.Data[0].RevisionNumber != 4 || rows.Data[0].Name != "Resumed" || !rows.Data[0].IsCurrent {
		t.Fatal(rows, err)
	}
	if _, err = s.Revision(testContext, "alice", item.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestHistoryDisabledKeepsEventAndPublishedSnapshots(t *testing.T) {
	s, w, _ := fixture(t)
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Drafts", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, c.ID, "item", "First", nil)
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Second")})
	if err != nil || revisionCount(t, s, item.ID) != 2 {
		t.Fatal(item, err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	item = latest(t, s, item.ID)
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Third")})
	if err != nil || revisionCount(t, s, item.ID) != 3 {
		t.Fatal(item, err)
	}
	if _, err = s.Unpublish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
}

func TestItemSnapshotCollectionTouchesOnlyTheGivenItems(t *testing.T) {
	s, w, _ := fixture(t)
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Scoped", Versioning: ptr("major"), MaxVersions: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}
	first := create(t, s, c.ID, "item", "First", nil)
	second := create(t, s, c.ID, "item", "Second", nil)
	for _, item := range []*resources.Resource{first, second} {
		for _, name := range []string{"Edited", "Edited again"} {
			if item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: &name}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = dispatchAll(testContext, s.DAL); err != nil {
		t.Fatal(err)
	}
	before := revisionCount(t, s, second.ID)
	if err = s.CollectItemRevisions(testContext, []string{first.ID, first.ID, ""}); err != nil {
		t.Fatal(err)
	}
	if revisionCount(t, s, first.ID) != 1 || revisionCount(t, s, second.ID) != before || before == 1 {
		t.Fatal("collection was not limited to the given item", revisionCount(t, s, first.ID), revisionCount(t, s, second.ID), before)
	}
	if err = s.CollectRevisions(testContext); err != nil || revisionCount(t, s, second.ID) != 1 {
		t.Fatal("the full sweep left unreferenced snapshots", err)
	}
}

func TestLibraryReplacementReclaimsBytesOnlyWhenVersioningOff(t *testing.T) {
	for _, mode := range []string{"", "major"} {
		for _, publishing := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode=%q/publishing=%v", mode, publishing), func(t *testing.T) {
				s, ws, _ := fixture(t)
				ctx := t.Context()
				in := CreateResource{Kind: "library", Name: "Files", PublishingEnabled: publishing}
				if mode != "" {
					in.Versioning = &mode
				}
				library, err := s.Create(ctx, "alice", ws.ID, in)
				if err != nil {
					t.Fatal(err)
				}
				store, err := storage.NewLocal(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				content := func(text string) BlobInput {
					t.Helper()
					object, e := store.Put(ctx, strings.NewReader(text), 1000)
					if e != nil {
						t.Fatal(e)
					}
					return BlobInput{ObjectKey: object.Key, Filename: "file.pdf", ContentType: "application/pdf", Size: object.Size, SHA256: object.SHA256}
				}
				document, err := s.CreateDocument(ctx, "alice", library.ID, DocumentMetadata{}, content("%PDF-1.7\nFirst\n%%EOF"))
				if err != nil {
					t.Fatal(err)
				}
				item := document.Item
				if publishing {
					if _, err = s.Publish(ctx, "alice", item.ID, item.Version); err != nil {
						t.Fatal(err)
					}
					item = latest(t, s, item.ID)
				}
				second, err := s.AttachBlob(ctx, "alice", item.ID, item.Version, content("%PDF-1.7\nSecond\n%%EOF"))
				if err != nil {
					t.Fatal(err)
				}
				old, err := s.DAL.Blobs().Blob(ctx, document.File.ID)
				if err != nil {
					t.Fatal(err)
				}
				history, err := s.RevisionHistory(ctx, "alice", item.ID, 0, 10)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "major" {
					if old.Availability != blobs.AvailabilityAvailable || len(history.Data) != 2 {
						t.Fatal("explicit history did not retain versions", old, history)
					}
				} else if old.Availability != blobs.AvailabilityPending || len(history.Data) != 0 || publishing != (old.ReclaimAfter == nil) {
					t.Fatal("history-off replacement was not queued with publication protection", old, history)
				}
				if err = s.SweepReclaims(ctx, store, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				f, err := store.Open(ctx, old.ObjectKey)
				if err != nil {
					t.Fatal("file bytes removed before grace period", err)
				}
				f.Close()
				future := time.Now().UTC().Add(2 * ObjectGracePeriod)
				if err = s.SweepReclaims(ctx, store, future); err != nil {
					t.Fatal(err)
				}
				if publishing {
					f, err := store.Open(ctx, old.ObjectKey)
					if err != nil {
						t.Fatal("published bytes were reclaimed", err)
					}
					f.Close()
					item = latest(t, s, item.ID)
					if _, err = s.Publish(ctx, "alice", item.ID, item.Version); err != nil {
						t.Fatal(err)
					}
					if err = s.SweepReclaims(ctx, store, future); err != nil {
						t.Fatal(err)
					}
					if err = s.SweepReclaims(ctx, store, future.Add(2*ObjectGracePeriod)); err != nil {
						t.Fatal(err)
					}
				}
				f, err = store.Open(ctx, old.ObjectKey)
				if mode == "major" {
					if err != nil {
						t.Fatal("explicit history lost file bytes", err)
					}
					f.Close()
				} else if !os.IsNotExist(err) {
					if f != nil {
						f.Close()
					}
					t.Fatal("replaced bytes were not reclaimed", err)
				}
				current, err := s.GetBlob(ctx, "alice", item.ID, "")
				if err != nil || current.ID != second.ID || current.Availability != blobs.AvailabilityAvailable {
					t.Fatal("replacement is not current and available", current, err)
				}
				f, err = store.Open(ctx, second.ObjectKey)
				if err != nil {
					t.Fatal("current bytes removed", err)
				}
				f.Close()
				// Re-uploading reclaimed content must use the newly stored object.
				item = latest(t, s, item.ID)
				reuploaded, err := s.AttachBlob(ctx, "alice", item.ID, item.Version, content("%PDF-1.7\nFirst\n%%EOF"))
				if err != nil {
					t.Fatal(err)
				}
				f, err = store.Open(ctx, reuploaded.ObjectKey)
				if err != nil {
					t.Fatal("re-upload reused a reclaimed object", err)
				}
				f.Close()
			})
		}
	}
}
