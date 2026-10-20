// Package daltest creates database fixtures through the DAL for application tests.
package daltest

import (
	"context"
	"strings"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
)

// Script is placeholder content for workflow versions that DAL tests store
// but never run.
var Script = workflows.Content{Definition: []byte(`{"triggers":[]}`), Source: "export default {}", Bundle: "export default {}", BundleHash: strings.Repeat("0", 64)}

// RunKind is the operation kind of workflow runs.
const RunKind = "script.run"

// WorkflowRun seeds a run: its pending operation and its provenance row.
func WorkflowRun(ctx context.Context, store *dal.Store, id, workflowID, workspaceID, actor string, version int64) error {
	return Run(ctx, store, runstore.CreateInput{ID: id, WorkflowID: workflowID, WorkspaceID: workspaceID, Actor: actor, WorkflowVersion: int(version), EventID: id, EventType: "manual"})
}

// Run seeds the run in: its pending operation and its provenance row.
func Run(ctx context.Context, store *dal.Store, in runstore.CreateInput) error {
	if _, _, err := store.Operations().Accept(ctx, operations.Operation{ID: in.ID, Kind: RunKind, ContainerID: in.WorkspaceID, WorkspaceID: in.WorkspaceID, CreatedBy: in.Actor, TraceID: in.ID}, time.Now()); err != nil {
		return err
	}
	_, err := store.Runs().Create(ctx, in)
	return err
}
