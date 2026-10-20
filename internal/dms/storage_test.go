package dms

import (
	"context"
	"database/sql"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/resources"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"papergo/internal/calendar"
	"papergo/internal/dal/resultcache"
	"papergo/internal/identity"
)

func TestStorageStatusReconcilesAndSeparatesContent(t *testing.T) {
	s, w, _ := fixture(t)
	ctx := context.Background()
	grantStorage(t, s)
	library := create(t, s, w.ID, "library", "Files", nil)
	item := create(t, s, library.ID, "item", "Scan", nil)
	const size = int64(2_000_000)
	sha := strings.Repeat("ab", 32)
	for i := 0; i < 2; i++ {
		daltest.Insert(t, s.DAL, "blobs", daltest.Row{"id": uuid.NewString(), "created_at": time.Now().UTC(), "version": i + 1, "object_key": uuid.NewString(), "filename": "scan.pdf", "content_type": "application/pdf", "size": size, "sha256": sha, "item_id": item.ID, "availability": "available"})
	}
	past := time.Now().UTC().Add(-8 * 24 * time.Hour)
	change := func(at time.Time) {
		if err := s.DAL.Resources().Touch(ctx, item.ID, 0, "alice"); err != nil {
			t.Fatal(err)
		}
		daltest.Update(t, s.DAL, "live_changes", daltest.Row{"created_at": at}, daltest.Row{"entity_id": item.ID})
	}
	change(past)
	daltest.Insert(t, s.DAL, "cached_results", daltest.Row{"id": uuid.NewString(), "namespace": "answers", "scope": "scope", "key": "key", "payload": `{}`, "created_at": past, "used_at": past, "expires_at": past.Add(time.Hour)})
	before, err := s.StorageStatus(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	var named int64
	for _, table := range before.Tables {
		named += table.Bytes
	}
	sqlite := s.DAL.Dialect() == dal.DialectSQLite
	if pages := int64(before.PageCount) * int64(before.PageSize); sqlite && (before.DatabaseBytes != pages || before.AccountedBytes+before.FreelistBytes != pages || named+before.FreelistBytes != pages || before.DatabaseBytes >= size) {
		t.Fatalf("accounted %d file %d pages %d named %d freelist %d", before.AccountedBytes, before.DatabaseBytes, pages, named, before.FreelistBytes)
	}
	if !sqlite && (before.DatabaseBytes < before.AccountedBytes || before.AccountedBytes != named || named == 0) {
		t.Fatalf("database %d accounted %d named %d", before.DatabaseBytes, before.AccountedBytes, named)
	}
	if before.BlobBytes != size || before.Expired.LiveChanges < 1 || before.Expired.CachedResults < 1 {
		t.Fatalf("%+v", before)
	}
	if before.Retained.AuditEvents < 1 {
		t.Fatal("audit missing", before.Retained)
	}
	audit := before.Retained.AuditEvents
	if _, err = resultcache.New(s.db).PurgeExpired(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = s.CollectExpiredLiveChanges(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	after, err := s.StorageStatus(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if after.Expired.CachedResults != 0 || after.Expired.LiveChanges != 0 || after.Retained.AuditEvents != audit {
		t.Fatalf("after cleanup %+v audit %d", after.Expired, after.Retained.AuditEvents)
	}
	again, err := s.StorageStatus(ctx, "alice")
	if err != nil || again.Expired.CachedResults != 0 || again.Expired.LiveChanges != 0 {
		t.Fatal(again, err)
	}
	if !sqlite {
		return
	}
	// A reader's snapshot keeps later writes in the journal.
	reader, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	if n, err := s.DAL.WithTx(reader).Resources().Count(ctx, resources.Filter{}); err != nil || n < 1 {
		t.Fatal(n, err)
	}
	change(time.Now().UTC())
	stalled, err := s.StorageStatus(ctx, "alice")
	if err != nil || !stalled.Incomplete || stalled.JournalBytes == 0 {
		t.Fatalf("%+v %v", stalled.Checkpoint, err)
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	change(time.Now().UTC())
	settled, err := s.StorageStatus(ctx, "alice")
	if err != nil || settled.Incomplete {
		t.Fatalf("%+v %v", settled.Checkpoint, err)
	}
}

func TestUnchangedCalendarRefreshAddsNoRevisionPublicationOrRun(t *testing.T) {
	s, _, list, body := integrationFixture(t)
	ctx := context.Background()
	*body = sourceFeed(sourceEvent)
	source := addIntegration(t, s, list.ID, "https://example.com/feed")
	if err := s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	itemID := integrationItems(t, s, source.ID)[0].ItemID
	item, err := s.Get(ctx, "alice", itemID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", itemID, item.Version); err != nil {
		t.Fatal(err)
	}
	before := contentCounts(t, s, itemID)
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	same := contentCounts(t, s, itemID)
	if same != before {
		t.Fatalf("unchanged refresh %v from %v", same, before)
	}
	s.integrations.ical.download = func(context.Context, string, string, string) (calendar.Download, error) {
		return calendar.Download{NotModified: true, ETag: "version"}, nil
	}
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if again := contentCounts(t, s, itemID); again != before {
		t.Fatalf("not-modified refresh %v from %v", again, before)
	}
	*body = sourceFeed(strings.ReplaceAll(sourceEvent, "Meeting", "Moved"))
	s.integrations.ical.download = func(context.Context, string, string, string) (calendar.Download, error) {
		return calendar.Download{Body: []byte(*body), ETag: "moved", NotModified: false}, nil
	}
	if _, err = s.RefreshIntegration(ctx, "alice", list.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(ctx); err != nil {
		t.Fatal(err)
	}
	changed := contentCounts(t, s, itemID)
	if changed.revisions <= before.revisions || changed.publications != before.publications || changed.runs != before.runs || changed.updates <= before.updates {
		t.Fatalf("changed refresh %v from %v", changed, before)
	}
}

type contentCount struct{ revisions, publications, runs, updates int }

func contentCounts(t *testing.T, s *Service, itemID string) contentCount {
	t.Helper()
	return contentCount{
		revisions:    daltest.Count(t, s.DAL, "item_revisions", daltest.Row{"item_id": itemID}),
		publications: daltest.Count(t, s.DAL, "publications", daltest.Row{"item_id": itemID}),
		runs:         daltest.Count(t, s.DAL, "workflow_runs", nil),
		updates:      daltest.Count(t, s.DAL, "domain_events", daltest.Row{"resource_id": itemID, "type": "item.updated"}),
	}
}

func grantStorage(t *testing.T, s *Service) {
	t.Helper()
	role, err := s.Directory().SaveRole(context.Background(), "organization-admin", "", 0, identity.RoleInput{Name: "Storage administrator", Scopes: []string{"organization.manage", "document.write"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().SetAssignment(context.Background(), "organization-admin", role.ID, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
}
