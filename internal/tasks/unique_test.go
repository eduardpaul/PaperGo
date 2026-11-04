package tasks

import (
	"errors"
	"testing"

	"papergo/internal/dal/daltest"
	taskstore "papergo/internal/dal/tasks"
	"papergo/internal/dms"
)

// A unique task is asked once: while one with its key is open, asking
// again names the open task, and once that one ends it can be asked again.
func TestUniqueTasksAreAskedOnce(t *testing.T) {
	ctx := t.Context()
	s := dms.NewService(daltest.Database(t).SQL)
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{DMS: s}
	ask := func(key string) (View, error) {
		return svc.Create(ctx, "alice", "", Input{WorkspaceID: ws.ID, Key: key, Unique: true, Title: "Measure the diesel barrel"})
	}

	var invalid *dms.ValidationError
	if _, err = ask(""); !errors.As(err, &invalid) {
		t.Fatalf("a unique task without a key: %v", err)
	}
	first, err := ask("diesel_measure")
	if err != nil {
		t.Fatal(err)
	}
	var open *taskstore.OpenError
	if _, err = ask("diesel_measure"); !errors.As(err, &open) || open.ID != first.ID {
		t.Fatalf("asking again names the open task: %v", err)
	}
	if _, err = svc.Cancel(ctx, "alice", first.ID, "Measured in the list"); err != nil {
		t.Fatal(err)
	}
	if _, err = ask("diesel_measure"); err != nil {
		t.Fatalf("asked again once the first is cancelled: %v", err)
	}
}
