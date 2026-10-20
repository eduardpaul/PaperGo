// Package content runs the queries readers browse and search content with:
// listings of a hierarchy, typed field filters, keyword and text matches,
// all filtered by what the reader may see on the surface they read. The
// statements are built as text for the store's engine and return resource
// IDs; package resources loads the rows.
package content

import (
	"context"
	"database/sql"
	"errors"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/scan"
	"papergo/internal/dal/access"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/dialect"
)

// Repo runs content queries on one connection or transaction.
type Repo struct {
	exec bob.Executor
	d    dialect.Dialect
	acl  access.Dialect
}

func NewRepo(exec bob.Executor, d dialect.Dialect) *Repo {
	return &Repo{exec: exec, d: d, acl: access.For(d)}
}

// strings runs a query of one text column.
func (r *Repo) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := r.exec.QueryContext(ctx, r.d.Rebind(query), args...)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			return nil, dberr.Translate(err)
		}
		if v.Valid {
			out = append(out, v.String)
		}
	}
	return out, dberr.Translate(rows.Err())
}

// scan reads the single row of a query.
func (r *Repo) scan(ctx context.Context, dest []any, query string, args ...any) error {
	rows, err := r.exec.QueryContext(ctx, r.d.Rebind(query), args...)
	if err != nil {
		return dberr.Translate(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return dberr.Translate(err)
		}
		return dberr.ErrNotFound
	}
	if err := rows.Scan(dest...); err != nil {
		return dberr.Translate(err)
	}
	return dberr.Translate(rows.Close())
}

// each runs a query and calls fn for every row.
func (r *Repo) each(ctx context.Context, fn func(scan.Rows) error, query string, args ...any) error {
	rows, err := r.exec.QueryContext(ctx, r.d.Rebind(query), args...)
	if err != nil {
		return dberr.Translate(err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return errors.Join(err, rows.Close())
		}
	}
	return dberr.Translate(rows.Err())
}
