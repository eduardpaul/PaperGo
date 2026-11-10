package sqlite

import (
	"context"

	"papergo/internal/dal/access"
	"papergo/internal/dal/dialect"
)

// accessChanged replaces the access revision. Every statement that writes
// grants, directory users, memberships, roles or assignments, or the
// permission scope of a resource, runs after it in the same transaction.
func (q Queries) accessChanged(ctx context.Context) error {
	return access.Changed(ctx, q.exec, dialect.SQLite)
}
