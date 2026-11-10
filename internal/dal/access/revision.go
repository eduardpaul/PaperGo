package access

import (
	"context"

	"github.com/google/uuid"
	"github.com/stephenafamo/bob"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/dialect"
)

// Access revisions name the state of everything a permission decision reads
// besides content. The organization's revision covers the directory: users'
// status, groups, memberships, roles and assignments. A workspace's covers
// its grants and the permission scopes of its resources. The DAL replaces a
// token in the transaction of every change to a decision's inputs, and
// decisions are kept under the tokens of the state they were made from. The
// tokens are random, so one a rolled-back transaction read never names a
// later state.

// Changed replaces the organization's access revision, which every kept
// decision is made under.
func Changed(ctx context.Context, exec bob.Executor, d dialect.Dialect) error {
	return ChangedIf(ctx, exec, d, "TRUE")
}

// ChangedIf replaces the organization's access revision when condition, an
// SQL expression with ? placeholders for args, holds. Callers test the state
// before their write, so the revision changes only with what decisions read.
func ChangedIf(ctx context.Context, exec bob.Executor, d dialect.Dialect, condition string, args ...any) error {
	_, err := exec.ExecContext(ctx, d.Rebind(`INSERT INTO access_revisions(singleton, token) SELECT TRUE, ? WHERE `+condition+`
 ON CONFLICT(singleton) DO UPDATE SET token=excluded.token`), append([]any{uuid.NewString()}, args...)...)
	return dberr.Translate(err)
}

// WorkspacesChanged replaces the access revision of the workspaces that
// workspaces, a query of one column of workspace IDs with ? placeholders for
// args, selects. It selects none when nothing a decision reads changes.
func WorkspacesChanged(ctx context.Context, exec bob.Executor, d dialect.Dialect, workspaces string, args ...any) error {
	_, err := exec.ExecContext(ctx, d.Rebind(`INSERT INTO workspace_access_revisions(workspace_id, token) SELECT w.id, ? FROM resources w WHERE w.id IN (`+workspaces+`)
 ON CONFLICT(workspace_id) DO UPDATE SET token=excluded.token`), append([]any{uuid.NewString()}, args...)...)
	return dberr.Translate(err)
}

// Revision reads what a decision about a collection's content is made under:
// the organization's revision and that of the collection's workspace. A
// token never replaced reads as empty.
func Revision(ctx context.Context, exec bob.Executor, d dialect.Dialect, collectionID string) (string, error) {
	rows, err := exec.QueryContext(ctx, d.Rebind(`SELECT COALESCE((SELECT token FROM access_revisions), '') || ':' ||
 COALESCE((SELECT w.token FROM resources c JOIN workspace_access_revisions w ON w.workspace_id=c.workspace_id WHERE c.id=?), '')`), collectionID)
	if err != nil {
		return "", dberr.Translate(err)
	}
	defer rows.Close()
	var token string
	if rows.Next() {
		if err := rows.Scan(&token); err != nil {
			return "", dberr.Translate(err)
		}
	}
	if err := rows.Err(); err != nil {
		return "", dberr.Translate(err)
	}
	return token, dberr.Translate(rows.Close())
}
