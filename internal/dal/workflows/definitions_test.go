package workflows_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/workflows"
	"papergo/internal/jsonutil"
)

func TestWorkflowDefinitions_SQLite(t *testing.T) { testWorkflowDefinitions(t, daltest.SQLiteStore(t)) }
func TestWorkflowDefinitions_Postgres(t *testing.T) {
	testWorkflowDefinitions(t, daltest.PostgresStore(t))
}
func testWorkflowDefinitions(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	original := daltest.Script
	original.Definition = []byte(`{"triggers":[{"type":"schedule","cron":"0 * * * *"}]}`)
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "review", Name: "Review", AutomaticEnabled: true, ManualEnabled: true, Content: original, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.Workflows().ReplaceTriggers(ctx, wf.ID, []workflows.TriggerInput{{CollectionID: f.Source, Type: "schedule", Position: 0, NextAt: &now}}); err != nil {
		t.Fatal(err)
	}
	due, err := s.Workflows().Triggers(ctx, workflows.TriggerFilter{DueAt: &now, Limit: 10})
	if err != nil || len(due) != 1 || due[0].CollectionID != f.Source {
		t.Fatalf("due schedule: %+v %v", due, err)
	}
	name := "Renamed"
	parameters := map[string]any{"exact": "9007199254740993"}
	same := original
	same.Definition = []byte(`{ "triggers": [ {"cron":"0 * * * *", "type":"schedule"} ] }`)
	edited, version, err := s.Workflows().Revise(ctx, wf.ID, wf.Version, "bob", same, workflows.Changes{Name: &name, Parameters: &parameters})
	if err != nil || edited.Version != 2 || version.Number != 1 || version.CreatedBy != "alice" || edited.Name != name {
		t.Fatalf("metadata-only edit: %+v %+v %v", edited, version, err)
	}
	if _, _, err := s.Workflows().Revise(ctx, wf.ID, wf.Version, "carol", original, workflows.Changes{}); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	changed := original
	changed.Source, changed.Bundle, changed.BundleHash = "export default { triggers: [] }", "changed", strings.Repeat("1", 64)
	edited, version, err = s.Workflows().Revise(ctx, wf.ID, edited.Version, "bob", changed, workflows.Changes{})
	if err != nil || edited.CurrentVersion != 2 || version.Number != 2 || version.CreatedBy != "bob" {
		t.Fatalf("new immutable version: %+v %+v %v", edited, version, err)
	}
	history, err := s.Workflows().Versions(ctx, wf.ID)
	if err != nil || len(history) != 2 || history[1].Number != 1 || !jsonutil.Equal(history[1].Definition, original.Definition) || history[1].Source != original.Source ||
		history[0].Source != changed.Source || history[0].Bundle != changed.Bundle || history[0].BundleHash != changed.BundleHash {
		t.Fatalf("immutable history changed: %+v %v", history, err)
	}
	tx, err := s.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.WithTx(tx).Workflows().Delete(ctx, wf.ID, edited.Version, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Workflows().Find(ctx, wf.ID)
	if err != nil || loaded.DeletedAt != nil {
		t.Fatalf("deletion escaped rollback: %+v %v", loaded, err)
	}
	if err := s.Workflows().Delete(ctx, wf.ID, edited.Version, "bob"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Workflows().List(ctx, workflows.Filter{LiveOnly: true, IDs: []string{wf.ID}}); err != nil || len(rows) != 0 {
		t.Fatalf("deleted workflow listed: %+v %v", rows, err)
	}
	if rows, err := s.Workflows().Triggers(ctx, workflows.TriggerFilter{WorkspaceID: f.Workspace}); err != nil || len(rows) != 0 {
		t.Fatalf("deleted workflow still scheduled: %+v %v", rows, err)
	}
}
