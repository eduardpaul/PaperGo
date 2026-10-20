// Package daltest creates database fixtures through the DAL for application tests.
package daltest

import (
	"context"
	"papergo/internal/dal"
	runstore "papergo/internal/dal/runs"
)

// WorkflowRun seeds an owning run without scheduling durable execution.
func WorkflowRun(ctx context.Context, store *dal.Store, id, workflowID, workspaceID, actor string, version int64) error {
	_, err := store.Runs().Create(ctx, runstore.CreateInput{ID: id, WorkflowID: workflowID, WorkspaceID: workspaceID, Actor: actor, WorkflowVersion: int(version), EventID: id, EventType: "manual"})
	return err
}
