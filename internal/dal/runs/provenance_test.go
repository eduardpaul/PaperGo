package runs_test

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/events"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"testing"
)

func TestExecutionProvenance_SQLite(t *testing.T) { testExecutionProvenance(t, daltest.SQLiteStore(t)) }
func TestExecutionProvenance_Postgres(t *testing.T) {
	testExecutionProvenance(t, daltest.PostgresStore(t))
}
func testExecutionProvenance(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "provenance", Name: "Provenance", Content: daltest.Script, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	run := runstore.CreateInput{ID: "source-run", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, ItemID: f.Item, EventID: "source-event", EventType: "manual", Actor: "alice"}
	if err := daltest.Run(ctx, s, run); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().AddItem(ctx, run.ID, f.Item, 0); err != nil {
		t.Fatal(err)
	}
	recorded, err := s.Audit().Append(ctx, audit.Event{WorkspaceID: f.Workspace, ResourceID: f.Item, Subject: "alice", Action: "resource.purge", EntityType: "item", EntityID: f.Item, TraceID: "trace"})
	if err != nil {
		t.Fatal(err)
	}
	event := events.Event{ID: "event", WorkspaceID: f.Workspace, CollectionID: f.Source, ResourceID: f.Item, CauseRunID: run.ID, Actor: "alice", Type: "item.deleted"}
	if err := s.Events().Append(ctx, []events.Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := daltest.Remove(ctx, s, f.Item, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.Resources().Purge(ctx, f.Item); err != nil {
		t.Fatal(err)
	}
	found, err := s.Runs().Find(ctx, run.ID)
	if err != nil || found.ItemID != f.Item {
		t.Fatalf("run lost purged source: %+v %v", found, err)
	}
	members, err := s.Runs().Items(ctx, []string{run.ID}, "")
	if err != nil || len(members) != 1 || members[0].ItemID != f.Item {
		t.Fatalf("selection lost purged source: %+v %v", members, err)
	}
	event.ID = "after-purge"
	event.Type = "item.purged"
	if err := s.Events().Append(ctx, []events.Event{event}); err != nil {
		t.Fatalf("purge event rejected: %v", err)
	}
	retry := runstore.CreateInput{ID: "retry", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "retry-event", EventType: "manual", Actor: "alice", RetryOf: run.ID}
	if err := daltest.Run(ctx, s, retry); err != nil {
		t.Fatal(err)
	}
	if err := daltest.Run(ctx, s, runstore.CreateInput{ID: "bad-retry", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "bad-retry-event", EventType: "manual", Actor: "alice", RetryOf: "missing"}); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("missing retry origin accepted: %v", err)
	}
	if err := s.Operations().Delete(ctx, []string{run.ID}); err != nil {
		t.Fatal(err)
	}
	found, err = s.Runs().Find(ctx, retry.ID)
	if err != nil || found.RetryOf != run.ID {
		t.Fatalf("retry origin lost to retention: %+v %v", found, err)
	}
	if err := s.Resources().Purge(ctx, f.Workspace); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Events().Find(ctx, event.ID)
	if err != nil || stored.WorkspaceID != f.Workspace || stored.ResourceID != f.Item || stored.CauseRunID != run.ID {
		t.Fatalf("event provenance lost: %+v %v", stored, err)
	}
	audits, err := s.Audit().List(ctx, audit.Filter{WorkspaceID: f.Workspace})
	if err != nil || len(audits) != 1 || audits[0].ID != recorded.ID || audits[0].ResourceID != f.Item {
		t.Fatalf("audit provenance lost: %+v %v", audits, err)
	}
}
