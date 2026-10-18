package dms

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"papergo/internal/calendar"
	"papergo/internal/identity"
	"papergo/internal/resultcache"
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
		if _, err := s.db.ExecContext(ctx, `INSERT INTO blobs(id, created_at, version, object_key, filename, content_type, size, sha256, item_id, availability) VALUES(?,?,?,?,?,?,?,?,?,'available')`,
			uuid.NewString(), time.Now().UTC(), i+1, uuid.NewString(), "scan.pdf", "application/pdf", size, sha, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO runner_step_results(run_id, step_id, step, output) VALUES('missing-run', 1, '1', '{}')`); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-8 * 24 * time.Hour)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO live_changes(created_at, kind, entity_id, action) VALUES(?, 'item', 'item-1', 'updated')`, past); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO cached_results(id, namespace, scope, key, payload, created_at, used_at, expires_at) VALUES(?,?,?,?,?,?,?,?)`,
		uuid.NewString(), "answers", "scope", "key", `{}`, past, past, past.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	before, err := s.StorageStatus(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	pages := int64(before.PageCount) * int64(before.PageSize)
	var named int64
	for _, table := range before.Tables {
		named += table.Bytes
	}
	if before.DatabaseBytes != pages || before.AccountedBytes+before.FreelistBytes != pages || named+before.FreelistBytes != pages {
		t.Fatalf("accounted %d file %d pages %d named %d freelist %d", before.AccountedBytes, before.DatabaseBytes, pages, named, before.FreelistBytes)
	}
	if before.BlobBytes != size || before.DatabaseBytes >= size || before.OrphanStepResults != 1 || before.Expired.LiveChanges < 1 || before.Expired.CachedResults < 1 {
		t.Fatalf("%+v", before)
	}
	t.Logf("database %d blob %d accounted %d freelist %d slack %d pages %d orphans %d", before.DatabaseBytes, before.BlobBytes, before.AccountedBytes, before.FreelistBytes, before.UnusedBytes, before.PageCount, before.OrphanStepResults)
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
	if _, err = s.db.ExecContext(ctx, `DELETE FROM runner_step_results WHERE run_id='missing-run'`); err != nil {
		t.Fatal(err)
	}
	after, err := s.StorageStatus(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if after.Expired.CachedResults != 0 || after.Expired.LiveChanges != 0 || after.OrphanStepResults != 0 || after.Retained.AuditEvents != audit {
		t.Fatalf("after cleanup %+v audit %d", after.Expired, after.Retained.AuditEvents)
	}
	again, err := s.StorageStatus(ctx, "alice")
	if err != nil || again.Expired.CachedResults != 0 || again.Expired.LiveChanges != 0 || again.OrphanStepResults != 0 {
		t.Fatal(again, err)
	}
	reader, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var pinned int
	if err = reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM live_changes").Scan(&pinned); err != nil || pinned < 1 {
		t.Fatal(pinned, err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO live_changes(created_at, kind, entity_id, action) VALUES(?, 'item', 'item-2', 'updated')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stalled, err := s.StorageStatus(ctx, "alice")
	if err != nil || !stalled.Incomplete || stalled.JournalBytes == 0 {
		t.Fatalf("%+v %v", stalled.Checkpoint, err)
	}
	if err = reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO live_changes(created_at, kind, entity_id, action) VALUES(?, 'item', 'item-3', 'updated')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
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
	var out contentCount
	ctx := context.Background()
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM item_revisions WHERE item_id=?`, itemID).Scan(&out.revisions); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM publications WHERE item_id=?`, itemID).Scan(&out.publications); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs`).Scan(&out.runs); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE resource_id=? AND type='item.updated'`, itemID).Scan(&out.updates); err != nil {
		t.Fatal(err)
	}
	return out
}

func grantStorage(t *testing.T, s *Service) {
	t.Helper()
	role, err := s.Directory().SaveRole(context.Background(), "organization-admin", "", 0, identity.RoleInput{Name: "Storage administrator", Scopes: []string{"organization.manage", "document.write"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().SetAssignment(context.Background(), "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
}
