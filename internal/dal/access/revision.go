package access

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"github.com/stephenafamo/bob"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/dialect"
)

// Changed replaces the access revision's token. Every DAL write to what a
// permission decision reads besides content calls it in its transaction:
// grants, the directory's users, groups, roles and assignments, and the
// permission scopes of resources. Decisions cached under the previous token
// are then no longer served. The token is random, so one a rolled-back
// transaction read never names a later state.
func Changed(ctx context.Context, exec bob.Executor, d dialect.Dialect) error {
	_, err := exec.ExecContext(ctx, d.Rebind(`INSERT INTO access_revisions(singleton, token) VALUES (TRUE, ?) ON CONFLICT(singleton) DO UPDATE SET token=excluded.token`), uuid.NewString())
	return dberr.Translate(err)
}

// Revision reads the access revision's token: empty until the first change.
func Revision(ctx context.Context, exec bob.Executor, d dialect.Dialect) (string, error) {
	rows, err := exec.QueryContext(ctx, `SELECT token FROM access_revisions`)
	if err != nil {
		return "", dberr.Translate(err)
	}
	defer rows.Close()
	var token sql.NullString
	if rows.Next() {
		if err := rows.Scan(&token); err != nil {
			return "", dberr.Translate(err)
		}
	}
	return token.String, dberr.Translate(errors.Join(rows.Err(), rows.Close()))
}
