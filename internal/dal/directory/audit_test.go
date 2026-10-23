package directory_test

import (
	"context"
	"papergo/internal/dal"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
	"reflect"
	"testing"
)

func TestDirectoryAuditState_SQLite(t *testing.T) { testDirectoryAuditState(t, daltest.SQLiteStore(t)) }
func TestDirectoryAuditState_Postgres(t *testing.T) {
	testDirectoryAuditState(t, daltest.PostgresStore(t))
}
func testDirectoryAuditState(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	if state, err := s.Directory().AuditState(ctx, directory.EntityUser, "missing"); err != nil || state != nil {
		t.Fatalf("missing state: %v %v", state, err)
	}
	for _, id := range []string{"z", "a"} {
		if _, err := s.Directory().CreateUser(ctx, id, id, "", true, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Directory().CreateGroup(ctx, "g", "Group", "", directory.SourceLocal); err != nil {
		t.Fatal(err)
	}
	before, err := s.Directory().AuditState(ctx, directory.EntityGroup, "g")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"z", "a"} {
		if err := s.Directory().SetMember(ctx, "g", directory.Principal{Type: directory.PrincipalUser, ID: id}, false); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.Directory().AuditState(ctx, directory.EntityGroup, "g")
	if err != nil || reflect.DeepEqual(before, after) || !reflect.DeepEqual(after["members"], []string{"user:a", "user:z"}) {
		t.Fatalf("membership state: %v %v", after, err)
	}
	user, err := s.Directory().AuditState(ctx, directory.EntityUser, "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password_hash", "sync_hash", "created_at"} {
		if _, exists := user[key]; exists {
			t.Fatalf("private property in audit state: %s", key)
		}
	}
	if _, err := s.Directory().CreateAccessToken(ctx, directory.AccessToken{ID: "t", Subject: "a", Name: "Token", Prefix: "prefix", SecretHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", CreatedBy: "a", Scopes: []string{"item.read"}}); err != nil {
		t.Fatal(err)
	}
	token, err := s.Directory().AuditState(ctx, directory.EntityAccessToken, "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 7 {
		t.Fatalf("token audit shape: %v", token)
	}
	if _, exists := token["secret_hash"]; exists {
		t.Fatal("token secret in audit state")
	}
	event, err := s.Audit().Append(ctx, audit.Event{Subject: "a", Action: directory.EntityGroup.AuditAction(directory.ChangeMember), EntityType: directory.EntityGroup.AuditType(), EntityID: "g", TraceID: "trace"})
	if err != nil {
		t.Fatal(err)
	}
	if access := daltest.Count(t, s, "live_changes", daltest.Row{"kind": "access", "entity_id": event.ID}); access != 1 {
		t.Fatalf("access notification: %d", access)
	}
}
