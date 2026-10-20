package revisions_test

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/daltest"
	"slices"
	"strings"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/revisions"
)

func TestRevisions_SQLite(t *testing.T)   { testRevisions(t, daltest.SQLiteStore(t)) }
func TestRevisions_Postgres(t *testing.T) { testRevisions(t, daltest.PostgresStore(t)) }

func testRevisions(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	r := s.Revisions()
	base := revisions.Revision{ItemID: f.Item, ContainerID: f.Source, ContentTypeID: "type", SchemaRevisionID: f.Schema, Name: "Item", CreatedBy: "alice"}
	other := base
	other.ContentTypeID = "missing"
	if _, err := s.Resources().Revise(ctx, other, nil, "alice"); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("revision outside its schema: %v", err)
	}
	other = base
	other.ContainerID = f.Destination
	if _, err := s.Resources().Revise(ctx, other, nil, "alice"); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("revision in another collection: %v", err)
	}
	file, err := s.Blobs().AddBlob(ctx, blobs.Blob{ItemID: f.Item, ObjectKey: "object", Filename: "a.pdf", ContentType: blobs.MediaPDF, Sha256: strings.Repeat("e", 64), Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	second := base
	second.RevisionNumber, second.BlobID, second.Filename, second.Payload = 2, &file.ID, "a.pdf", json.RawMessage(`{"amount": 3}`)
	recorded, err := s.Resources().Revise(ctx, second, nil, "alice")
	if err != nil || recorded.Keywords == nil || recorded.ID == "" {
		t.Fatalf("record: %+v %v", recorded, err)
	}
	if found, err := r.Numbered(ctx, f.Item, 2); err != nil || found.ID != recorded.ID || *found.BlobID != file.ID {
		t.Fatalf("numbered: %+v %v", found, err)
	}
	pinned, err := r.Revisions(ctx, revisions.Filter{ItemID: f.Item, BlobIDs: []string{file.ID}})
	if err != nil || len(pinned) != 1 {
		t.Fatalf("by file: %+v %v", pinned, err)
	}

	// History keeps the newest entries.
	for number := 3; number <= 5; number++ {
		next := base
		next.RevisionNumber = number
		rev, err := s.Resources().Revise(ctx, next, nil, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Retain(ctx, rev, []string{"amount"}, 2); err != nil {
			t.Fatal(err)
		}
		recorded = rev
	}
	entries, err := r.Entries(ctx, revisions.HistoryFilter{ItemID: f.Item})
	numbers := []int{}
	for _, e := range entries {
		numbers = append(numbers, e.Number)
	}
	if err != nil || !slices.Equal(numbers, []int{3, 2}) || entries[0].Revision.ID != recorded.ID {
		t.Fatalf("retained history: %v %v", numbers, err)
	}
	if last, err := r.LastNumber(ctx, f.Item); err != nil || last != 3 {
		t.Fatalf("last number: %d %v", last, err)
	}
	if found, err := r.Recorded(ctx, f.Item, 3); err != nil || found.ID != recorded.ID {
		t.Fatalf("recorded: %+v %v", found, err)
	}

	// Publications belong to items and their own revisions.
	if _, err = r.Publish(ctx, revisions.Publication{ItemID: f.Folder, Version: 1, Action: revisions.ActionPublish, PublishedBy: "alice"}); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("published a folder: %v", err)
	}
	published, err := r.Publish(ctx, revisions.Publication{ItemID: f.Item, RevisionID: &recorded.ID, Version: 7, Action: revisions.ActionPublish, PublishedBy: "alice", Snapshot: json.RawMessage(`{"name": "Item"}`)})
	if err != nil {
		t.Fatal(err)
	}
	found, err := r.Publications(ctx, revisions.PublicationFilter{ItemID: f.Item, RevisionID: recorded.ID, Action: revisions.ActionPublish})
	if err != nil || len(found) != 1 || found[0].ID != published.ID || found[0].Version != 7 {
		t.Fatalf("publications: %+v %v", found, err)
	}
	if later, err := r.Publications(ctx, revisions.PublicationFilter{ItemID: f.Item, AfterVersion: 7}); err != nil || len(later) != 0 {
		t.Fatalf("publications after version: %+v %v", later, err)
	}
}
