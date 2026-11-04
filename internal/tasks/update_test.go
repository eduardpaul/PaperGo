package tasks

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"papergo/internal/dal/daltest"
	taskstore "papergo/internal/dal/tasks"
	"papergo/internal/dms"
)

// An open task's details change at its version, by those who may reassign
// it, while its form stays: a change raises no new version when nothing
// changes, and an answered task changes no more.
func TestOpenTasksChangeTheirDetails(t *testing.T) {
	ctx := t.Context()
	s := dms.NewService(daltest.Database(t).SQL)
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", ws.ID, dms.WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{DMS: s}
	form := map[string]any{"type": "object", "properties": map[string]any{"liters": map[string]any{"type": "number", "minimum": 0}}}
	task, err := svc.Create(ctx, "alice", "", Input{WorkspaceID: ws.ID, Key: "diesel_refill", Title: "Fill the barrel: 40 L left", Assignees: []string{"bob"}, InputSchema: form})
	if err != nil {
		t.Fatal(err)
	}
	title, high := "Fill the barrel: 12 L left", taskstore.PriorityHigh
	due := time.Date(2030, 1, 2, 8, 0, 0, 0, time.UTC)
	change := Patch{Title: &title, Priority: &high, SetDueAt: true, DueAt: &due, SetValues: true, Values: map[string]any{"liters": 200}}

	if _, err = svc.Update(ctx, "bob", task.ID, int64(task.Version), change); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("an assignee changed the task: %v", err)
	}
	if _, err = svc.Update(ctx, "alice", task.ID, int64(task.Version)+1, change); !errors.Is(err, dms.ErrPreconditionFailed) {
		t.Fatalf("a stale version: %v", err)
	}
	bad := Patch{SetValues: true, Values: map[string]any{"liters": -1}}
	var invalid *dms.ValidationError
	if _, err = svc.Update(ctx, "alice", task.ID, int64(task.Version), bad); !errors.As(err, &invalid) {
		t.Fatalf("a prefill the form refuses: %v", err)
	}
	updated, err := svc.Update(ctx, "alice", task.ID, int64(task.Version), change)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != title || updated.Priority != high || updated.DueAt == nil || !updated.DueAt.Equal(due) || fmt.Sprint(updated.Values["liters"]) != "200" || updated.Version != task.Version+1 {
		t.Fatalf("the change was not applied: %+v", updated)
	}
	same, err := svc.Update(ctx, "alice", task.ID, int64(updated.Version), Patch{Title: &title})
	if err != nil || same.Version != updated.Version {
		t.Fatalf("a change to the same title made a version: %+v %v", same, err)
	}
	cleared, err := svc.Update(ctx, "alice", task.ID, int64(updated.Version), Patch{SetDueAt: true, SetValues: true})
	if err != nil || cleared.DueAt != nil || cleared.Values != nil || cleared.Title != title {
		t.Fatalf("clearing the due date and prefill: %+v %v", cleared, err)
	}
	if _, err = svc.Respond(ctx, "bob", task.ID, Response{Outcome: "done", Input: map[string]any{"liters": 50}}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Update(ctx, "alice", task.ID, int64(cleared.Version)+1, Patch{Title: &title}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("an answered task changed: %v", err)
	}
}
