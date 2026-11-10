package sqlite

import (
	"context"

	"papergo/internal/dal/access"
	"papergo/internal/dal/dialect"
)

// accessChanged replaces the organization's access revision. Every statement
// that changes what a permission decision reads from the directory (user
// status, memberships, roles and assignments) runs with it in the same
// transaction.
func (q Queries) accessChanged(ctx context.Context) error {
	return access.Changed(ctx, q.exec, dialect.SQLite)
}

// accessChangedIf replaces the organization's access revision when
// condition holds before the write.
func (q Queries) accessChangedIf(ctx context.Context, condition string, args ...any) error {
	return access.ChangedIf(ctx, q.exec, dialect.SQLite, condition, args...)
}

// workspaceAccessChanged replaces the access revision of the workspaces the
// query selects. Every statement that changes a workspace's grants or the
// permission scope of one of its resources runs with it in the same
// transaction.
func (q Queries) workspaceAccessChanged(ctx context.Context, workspaces string, args ...any) error {
	return access.WorkspacesChanged(ctx, q.exec, dialect.SQLite, workspaces, args...)
}
