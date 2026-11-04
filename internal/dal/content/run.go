package content

import (
	"context"
	"strings"
	"time"

	"github.com/stephenafamo/scan"
	"papergo/internal/dal/dberr"
)

// ranked reads (id, sort values...) rows of a query with keys sort keys:
// one key gives a scalar rank, several a []any of them.
func (r *Repo) ranked(ctx context.Context, keys int, query string, args ...any) ([]Ranked, error) {
	out := []Ranked{}
	err := r.each(ctx, func(rows scan.Rows) error {
		var row Ranked
		values := make([]any, keys)
		targets := make([]any, keys+1)
		targets[0] = &row.ID
		for i := range values {
			targets[i+1] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		if keys == 1 {
			row.Rank = values[0]
		} else {
			row.Rank = values
		}
		out = append(out, row)
		return nil
	}, query, args...)
	return out, err
}

// Page returns up to n of q's rows after a position, in its order: values
// by the sort direction, missing values last, ID ties in the sort direction.
func (r *Repo) Page(ctx context.Context, q *Query, after *Position, n int) ([]Ranked, error) {
	if q.seek != nil {
		return r.seekPage(ctx, q, after, n)
	}
	where, afterArgs := r.keyset(q, after)
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return nil, err
	}
	columns := []string{}
	ordering := []string{}
	for i, order := range q.Orders {
		alias := sortAlias(i)
		direction := "ASC"
		if order.Descending {
			direction = "DESC"
		}
		columns = append(columns, alias)
		ordering = append(ordering, "("+alias+" IS NULL) ASC", alias+" "+direction)
	}
	direction := "ASC"
	if q.Descending() {
		direction = "DESC"
	}
	ordering = append(ordering, "id "+direction)
	page := "WITH eligible AS (" + query + ") SELECT id," + strings.Join(columns, ",") + " FROM eligible" + where + " ORDER BY " + strings.Join(ordering, ",") + " LIMIT ?"
	args = append(append(args, afterArgs...), n)
	return r.ranked(ctx, len(q.Orders), page, args...)
}

// keyset is the condition of the rows after a position.
func (r *Repo) keyset(q *Query, after *Position) (string, []any) {
	if after == nil {
		return "", nil
	}
	values := []any{after.Rank}
	if len(q.Orders) > 1 {
		values = after.Rank.([]any)
	}
	conditions := []string{}
	bind := []any{}
	equal := []string{}
	equalArgs := []any{}
	for i, order := range q.Orders {
		alias := sortAlias(i)
		v := values[i]
		op := ">"
		if order.Descending {
			op = "<"
		}
		if v != nil {
			parts := append(append([]string{}, equal...), "("+alias+" IS NULL OR "+alias+op+"?)")
			conditions = append(conditions, "("+strings.Join(parts, " AND ")+")")
			bind = append(bind, equalArgs...)
			bind = append(bind, v)
		}
		equal = append(equal, r.d.Same(alias, "?"))
		equalArgs = append(equalArgs, v)
	}
	op := ">"
	if q.Descending() {
		op = "<"
	}
	conditions = append(conditions, "("+strings.Join(append(equal, "id"+op+"?"), " AND ")+")")
	bind = append(bind, equalArgs...)
	bind = append(bind, after.ID)
	return " WHERE " + strings.Join(conditions, " OR "), bind
}

// Count counts q's rows, stopping after limit when limit is positive.
func (r *Repo) Count(ctx context.Context, q *Query, limit int) (int, error) {
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return 0, err
	}
	var n int
	if limit > 0 {
		err = r.scan(ctx, []any{&n}, "SELECT count(*) FROM (SELECT 1 FROM ("+query+") AS eligible LIMIT ?) AS counted", append(args, limit)...)
	} else {
		err = r.scan(ctx, []any{&n}, "WITH eligible AS ("+query+") SELECT count(*) FROM eligible", args...)
	}
	return n, err
}

// Group is the count of q's rows with one sort value.
type Group struct {
	Value any
	Count int
}

// Groups counts q's rows by their sort value, missing values last, up to n
// groups after a value. A nil after starts at the first group; a pointer
// to nil is past every group.
func (r *Repo) Groups(ctx context.Context, q *Query, after *any, n int) ([]Group, error) {
	where, afterArgs := "", []any{}
	if after != nil {
		if *after == nil {
			where = " WHERE FALSE"
		} else {
			where, afterArgs = " WHERE sort_value IS NULL OR sort_value>? ", []any{*after}
		}
	}
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return nil, err
	}
	args = append(append(args, afterArgs...), n)
	out := []Group{}
	err = r.each(ctx, func(rows scan.Rows) error {
		var g Group
		if err := rows.Scan(&g.Value, &g.Count); err != nil {
			return err
		}
		out = append(out, g)
		return nil
	}, "WITH eligible AS ("+query+"),grouped AS (SELECT sort_value,count(*) AS n FROM eligible GROUP BY sort_value) SELECT sort_value,n FROM grouped"+where+" ORDER BY (sort_value IS NULL) ASC,sort_value ASC LIMIT ?", args...)
	return out, err
}

// Contains reports whether q selects a resource.
func (r *Repo) Contains(ctx context.Context, q *Query, id string) (bool, error) {
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return false, err
	}
	ids, err := r.strings(ctx, "WITH eligible AS ("+query+") SELECT id FROM eligible WHERE id=? LIMIT 1", append(args, id)...)
	return len(ids) > 0, err
}

// QueueOperation adds every resource q selects to an operation's items.
func (r *Repo) QueueOperation(ctx context.Context, q *Query, operationID string, at time.Time) error {
	query, args, err := r.eligible(ctx, q)
	if err != nil {
		return err
	}
	_, err = r.exec.ExecContext(ctx, r.d.Rebind(`INSERT INTO operation_items(id,created_at,operation_id,item_id) SELECT CAST(? AS TEXT)||':'||id,?,?,id FROM (`+query+`) AS eligible`), append([]any{operationID, at, operationID}, args...)...)
	return dberr.Translate(err)
}
