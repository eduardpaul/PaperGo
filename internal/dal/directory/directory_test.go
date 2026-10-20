package directory_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
)

func TestDirectory_SQLite(t *testing.T)   { testDirectory(t, daltest.SQLiteStore(t)) }
func TestDirectory_Postgres(t *testing.T) { testDirectory(t, daltest.PostgresStore(t)) }
func testDirectory(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	for _, u := range []struct {
		id     string
		active bool
	}{{"alice", true}, {"bob", false}, {"carol", true}} {
		if _, err := s.Directory().CreateUser(ctx, u.id, u.id, "", u.active, false); err != nil {
			t.Fatal(err)
		}
	}
	d := s.Directory()
	if active, err := d.Users(ctx, directory.UserFilter{Active: true}); err != nil || len(active) != 2 || active[0].ID != "alice" || active[1].ID != "carol" {
		t.Fatalf("active users: %+v %v", active, err)
	}
	if some, err := d.Users(ctx, directory.UserFilter{IDs: []string{"bob", "missing"}}); err != nil || len(some) != 1 || some[0].Active {
		t.Fatalf("users by ID: %+v %v", some, err)
	}
	if none, err := d.Users(ctx, directory.UserFilter{IDs: []string{}}); err != nil || len(none) != 0 {
		t.Fatalf("empty ID list: %+v %v", none, err)
	}
	if ok, err := d.ActiveUser(ctx, "bob"); err != nil || ok {
		t.Fatalf("inactive user active: %v %v", ok, err)
	}
	if ok, err := d.ActiveUser(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing user active: %v %v", ok, err)
	}
	group, err := s.Directory().CreateGroup(ctx, "staff", "Staff", "", directory.SourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := d.GroupExists(ctx, group.ID); err != nil || !ok {
		t.Fatalf("group exists: %v %v", ok, err)
	}
	if _, err := d.BindInbox(ctx, group.ID, f.Item); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("item accepted as inbox: %v", err)
	}
	if changed, err := d.BindInbox(ctx, group.ID, f.Source); err != nil || !changed {
		t.Fatalf("bind inbox: %v %v", changed, err)
	}
	if changed, err := d.BindInbox(ctx, group.ID, f.Source); err != nil || changed {
		t.Fatalf("rebinding the same library changed it: %v %v", changed, err)
	}
	if changed, err := d.BindInbox(ctx, group.ID, f.Destination); err != nil || !changed {
		t.Fatalf("move inbox: %v %v", changed, err)
	}
	if inbox, err := d.Inbox(ctx, group.ID); err != nil || inbox.LibraryID != f.Destination {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	if err := d.UnbindInbox(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Inbox(ctx, group.ID); !dberr.IsNotFound(err) {
		t.Fatalf("unbound inbox: %v", err)
	}
}
