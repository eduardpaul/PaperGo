package operations_test

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/daltest"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
)

func TestOperations_SQLite(t *testing.T)   { testOperations(t, daltest.SQLiteStore(t)) }
func TestOperations_Postgres(t *testing.T) { testOperations(t, daltest.PostgresStore(t)) }
func testOperations(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	ops := s.Operations()
	if _, err := ops.Next(ctx); !dberr.IsNotFound(err) {
		t.Fatalf("no pending operation: %v", err)
	}
	refresh, err := ops.Enqueue(ctx, operations.Operation{ContainerID: f.Source, Kind: "refresh", CreatedBy: "alice", Payload: json.RawMessage(`{"integration_id":"one"}`)})
	if err != nil {
		t.Fatal(err)
	}
	index, err := ops.Enqueue(ctx, operations.Operation{ContainerID: f.Source, Kind: "field_index", FieldID: "field", CreatedBy: "bob", CredentialID: "token", CredentialScopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := ops.Next(ctx)
	if err != nil || next.ID != refresh.ID || next.Status != operations.StatusPending || string(next.Result) != "{}" {
		t.Fatalf("oldest pending: %+v %v", next, err)
	}
	stored, err := ops.Find(ctx, index.ID)
	if err != nil || stored.CredentialID != "token" || len(stored.CredentialScopes) != 1 || stored.FieldID != "field" {
		t.Fatalf("credential: %+v %v", stored, err)
	}
	// Owners see their operations and everyone sees shared kinds.
	visible, err := ops.List(ctx, operations.Filter{ContainerID: f.Source, OwnedBy: "carol", SharedKinds: []string{"field_index"}})
	if err != nil || len(visible) != 1 || visible[0].ID != index.ID {
		t.Fatalf("visible operations: %+v %v", visible, err)
	}
	if ok, err := ops.Exists(ctx, operations.Filter{PayloadField: "integration_id", PayloadValue: "one", Statuses: []operations.Status{operations.StatusPending}}); err != nil || !ok {
		t.Fatalf("payload match: %v %v", ok, err)
	}
	if err := ops.Supersede(ctx, operations.Filter{Kinds: []string{"refresh"}, PayloadField: "integration_id", PayloadValue: "one"}); err != nil {
		t.Fatal(err)
	}
	if superseded, err := ops.Find(ctx, refresh.ID); err != nil || superseded.Status != operations.StatusSuperseded || superseded.CompletedAt == nil {
		t.Fatalf("supersede: %+v %v", superseded, err)
	}
	stored.Processed, stored.Progress, stored.Result = 3, 50, json.RawMessage(`{"done":3}`)
	if err := ops.Save(ctx, &stored); err != nil {
		t.Fatal(err)
	}
	if saved, err := ops.Find(ctx, index.ID); err != nil || saved.Processed != 3 || saved.Progress != 50 {
		t.Fatalf("save: %+v %v", saved, err)
	}
	if n, err := ops.Count(ctx, operations.Filter{Finished: true}); err != nil || n != 1 {
		t.Fatalf("finished count: %d %v", n, err)
	}
	if n, err := ops.CollectExpired(ctx, time.Now().Add(time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("expired operations: %d %v", n, err)
	}
	if _, err := ops.Find(ctx, refresh.ID); !dberr.IsNotFound(err) {
		t.Fatalf("expired operation retained: %v", err)
	}
}
