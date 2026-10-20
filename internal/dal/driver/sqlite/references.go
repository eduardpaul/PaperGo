package sqlite

import (
	"context"
	"fmt"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/driver/sqlite/models"
	"papergo/internal/dal/refs"
)

// Containment and ownership checks domains run before they reference
// resources, workflows, runs and directory principals.

// LockWorkspace does nothing: the transaction already holds the writer.
func (Queries) LockWorkspace(context.Context, string) error { return nil }

func (q Queries) Resource(ctx context.Context, id string) (refs.Resource, error) {
	row, err := models.FindResource(ctx, q.exec, id, "kind", "workspace_id", "container_id", "deleted_at", "created_at", "created_by")
	if err != nil {
		return refs.Resource{}, dberr.Translate(err)
	}
	return refs.Resource{Kind: refs.ResourceKind(row.Kind), WorkspaceID: row.WorkspaceID, ContainerID: row.ContainerID.GetOrZero(), Deleted: row.DeletedAt.IsValue(),
		CreatedAt: row.CreatedAt.UTC(), CreatedBy: row.CreatedBy}, nil
}

func (q Queries) Workflow(ctx context.Context, id string) (refs.Workflow, error) {
	row, err := models.FindWorkflow(ctx, q.exec, id, "workspace_id", "deleted_at")
	if err != nil {
		return refs.Workflow{}, dberr.Translate(err)
	}
	return refs.Workflow{WorkspaceID: row.WorkspaceID, Deleted: row.DeletedAt.IsValue()}, nil
}

func (q Queries) RunInWorkflow(ctx context.Context, runID, workflowID, workspaceID string) (bool, error) {
	w := models.SelectWhere.WorkflowRuns
	ok, err := models.WorkflowRuns.Query(w.ID.EQ(runID), w.WorkflowID.EQ(workflowID), w.WorkspaceID.EQ(workspaceID)).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) RunInWorkspace(ctx context.Context, runID, workspaceID string) (bool, error) {
	w := models.SelectWhere.WorkflowRuns
	ok, err := models.WorkflowRuns.Query(w.ID.EQ(runID), w.WorkspaceID.EQ(workspaceID)).Exists(ctx, q.exec)
	return ok, dberr.Translate(err)
}

func (q Queries) PrincipalExists(ctx context.Context, kind, id string) (bool, error) {
	switch directory.PrincipalType(kind) {
	case directory.PrincipalUser:
		ok, err := models.IdentityUserExists(ctx, q.exec, id)
		return ok, dberr.Translate(err)
	case directory.PrincipalGroup:
		ok, err := models.IdentityGroupExists(ctx, q.exec, id)
		return ok, dberr.Translate(err)
	}
	return false, fmt.Errorf("%w: invalid principal_type %q", dberr.ErrCheckViolation, kind)
}
