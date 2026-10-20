package directory_test

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"reflect"
	"sync"
	"testing"
)

func TestDirectorySettings_SQLite(t *testing.T)   { testDirectorySettings(t, daltest.SQLiteStore(t)) }
func TestDirectorySettings_Postgres(t *testing.T) { testDirectorySettings(t, daltest.PostgresStore(t)) }
func testDirectorySettings(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	if _, err := s.Directory().CreateUser(ctx, "alice", "Alice", "", true, false); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"language": "es", "description": "quote \" and newline\n日本語"}
	if err := s.Directory().SavePreferences(ctx, directory.OwnerUser, "alice", 0, values); err != nil {
		t.Fatal(err)
	}
	current, err := s.Directory().Preferences(ctx, directory.OwnerUser, "alice")
	if err != nil || current.Version != 1 || !reflect.DeepEqual(values, current.Values) {
		t.Fatalf("preferences round trip: %+v %v", current, err)
	}
	if err := s.Directory().SavePreferences(ctx, directory.OwnerUser, "alice", 0, map[string]string{}); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale insert: %v", err)
	}
	if err := s.Directory().SavePreferences(ctx, directory.OwnerUser, "alice", 9, map[string]string{}); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale update: %v", err)
	}
	updated, err := s.Directory().Preferences(ctx, directory.OwnerUser, "alice")
	if err != nil || !reflect.DeepEqual(updated, current) {
		t.Fatalf("stale writer changed preferences: %+v %v", updated, err)
	}
	if err := s.Directory().SavePreferences(ctx, directory.OwnerOrganization, "", 0, map[string]string{"language": "en"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, hash := range []string{"first", "second"} {
		wg.Go(func() { errs[i] = s.Directory().InitializeLocalPassword(ctx, "alice", hash) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	hash, err := s.Directory().LocalPasswordHash(ctx, "alice")
	if err != nil || hash != "first" && hash != "second" {
		t.Fatalf("first password: %q %v", hash, err)
	}
	if err := s.Directory().InitializeLocalPassword(ctx, "alice", "third"); err != nil {
		t.Fatal(err)
	}
	kept, err := s.Directory().LocalPasswordHash(ctx, "alice")
	if err != nil || kept != hash {
		t.Fatalf("password overwritten: %q %v", kept, err)
	}
	if err := s.Directory().SetUserActive(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if user, err := s.Directory().User(ctx, "alice"); err != nil || user.Active {
		t.Fatalf("status: %v %v", user.Active, err)
	}
	if err := s.Directory().SetUserActive(ctx, "missing", false); !dberr.IsNotFound(err) {
		t.Fatalf("missing status: %v", err)
	}
	if err := s.Directory().EnsureOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	org, err := s.Directory().Organization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Directory().SaveOrganization(ctx, directory.Organization{ID: org.ID, Identifier: "records", DisplayName: "Records", Description: "Description"}, org.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.Directory().SaveOrganization(ctx, directory.Organization{ID: org.ID, Identifier: "stale", DisplayName: "Stale"}, org.Version); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale organization update: %v", err)
	}
	org, err = s.Directory().Organization(ctx)
	if err != nil || org.Version != 2 || org.Identifier != "records" {
		t.Fatalf("organization: %+v %v", org, err)
	}
}
