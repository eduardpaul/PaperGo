package content

import (
	"context"
	"strings"
	"time"

	"papergo/internal/dal/access"
)

// seekPlan pages a collection query in sort-index order, so a page reads
// about as many index entries as it returns (plus the rows its conditions
// reject) and a cursor seeks to where the previous page ended. Sorting the
// eligible set instead costs the whole collection on every page.
type seekPlan struct {
	// column is the item_surfaces sort column of a built-in field; empty
	// for a custom field.
	column string
}

// Ranked is an eligible row's ID with its sort value, or its sort values
// when a query has several keys.
type Ranked struct {
	ID   string
	Rank any
}

// Position is where a page starts: after the row with this ID and sort
// value, or values.
type Position struct {
	ID   string
	Rank any
}

// seekArm reads one surface: an item appears on the head arm when the reader
// may read drafts there, otherwise on the published arm.
type seekArm struct {
	surface string
	acl     string
	aclArgs []any
}

// arms lists the surfaces that can hold matches. The collection's ACL scopes
// decide which rows the reader reads with drafts (head arm) and which
// without (published arm), so a page tests each row's scope against a list
// instead of probing grants for it; an arm that cannot match would scan its
// whole index to return nothing, and is left out. A collection with too
// many scopes checks each row.
func (r *Repo) arms(ctx context.Context, q *Query) ([]seekArm, error) {
	scopes, err := r.Scopes(ctx, q.collection, q.reader)
	if err != nil {
		return nil, err
	}
	var draft, read, rest string
	var draftArgs, readArgs, restArgs []any
	heads, others := true, true
	if scopes == nil {
		draft, draftArgs = r.acl.Resource("r.id", q.reader, access.ReadDraft)
		read, readArgs = r.acl.Resource("r.id", q.reader, access.Read)
		rest, restArgs = read+" AND NOT "+draft, append(append([]any{}, readArgs...), draftArgs...)
		if q.publishes && q.surface != "head" && q.surface != "published" {
			if heads, others, err = r.armsHeld(ctx, q); err != nil {
				return nil, err
			}
		}
	} else {
		draft, draftArgs = r.drafts(scopes)
		read, readArgs = r.readable(scopes)
		rest, restArgs = r.published(scopes)
	}
	arms := []seekArm{}
	add := func(surface, acl string, args []any) {
		if acl != "FALSE" {
			arms = append(arms, seekArm{surface, acl, args})
		}
	}
	switch {
	case !q.publishes && q.surface == "head":
		// One surface: head readers need drafts, everyone else read access.
		add("published", draft, draftArgs)
	case !q.publishes || q.surface == "published":
		add("published", read, readArgs)
	case q.surface == "head":
		add("head", draft, draftArgs)
	default:
		if heads {
			add("head", draft, draftArgs)
		}
		if others {
			add("published", rest, restArgs)
		}
	}
	return arms, nil
}

// armsHeld reports, for a collection whose rows are checked one by one,
// whether any scope is read with drafts and whether any is read without.
func (r *Repo) armsHeld(ctx context.Context, q *Query) (heads, others bool, err error) {
	draft, draftArgs := r.acl.Content("scopes.id", q.collection, q.reader, access.ReadDraft)
	read, readArgs := r.acl.Content("scopes.id", q.collection, q.reader, access.Read)
	args := append([]any{q.collection, q.collection}, draftArgs...)
	args = append(append(args, readArgs...), draftArgs...)
	err = r.scan(ctx, []any{&heads, &others}, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources WHERE container_id=?
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE container_id=? AND scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL)
SELECT `+r.d.AnyTrue(draft)+`,`+r.d.AnyTrue(read+` AND NOT `+draft)+` FROM scopes WHERE id IS NOT NULL`, args...)
	return heads, others, err
}

// seekPage returns up to n rows after the cursor in the order of
// eligiblePage: values by the sort direction, missing values last, ID ties
// in the sort direction. Every statement scans one index in order and stops
// at its LIMIT.
func (r *Repo) seekPage(ctx context.Context, q *Query, after *Position, n int) ([]Ranked, error) {
	p := q.seek
	order, op := "ASC", ">"
	if q.Descending() {
		order, op = "DESC", "<"
	}
	arms, err := r.arms(ctx, q)
	if err != nil {
		return nil, err
	}
	out := []Ranked{}
	// Phase 1: rows holding a sort value, unless the cursor already passed
	// them.
	if after == nil || after.Rank != nil {
		var driver, key, id string
		var driverArgs []any
		if p.column != "" {
			driver, key, id = "item_surfaces p "+r.d.OrderedJoin()+" resources r ON r.id=p.item_id WHERE p.container_id=? AND p.surface=?", p.column, "p.item_id"
		} else {
			col, _ := IndexColumn(q.RankDef)
			// Ties use sk.item_id: the planner cannot use the index for p.item_id.
			key, id = "sk."+string(col), "sk.item_id"
			driver = "field_values sk " + r.d.OrderedJoin() + " item_surfaces p ON p.id=sk.surface_id " + r.d.OrderedJoin() + " resources r ON r.id=sk.item_id WHERE sk.container_id=? AND sk.surface=? AND sk.field_key=? AND sk.field_type=? AND sk.scale=? AND sk.ordinal=0"
			// The integer and boolean value indexes are partial: naming the
			// column's NOT NULL lets the planner scan the one that holds them.
			if col != ValueText {
				driver += " AND " + key + " IS NOT NULL"
			}
			driverArgs = []any{q.RankDef.Key, string(q.RankDef.Type), q.RankDef.Scale}
		}
		var seek string
		var seekArgs []any
		switch {
		case after == nil:
		case key == id:
			seek, seekArgs = " AND "+id+op+"?", []any{after.ID}
		default:
			seek, seekArgs = " AND ("+key+","+id+")"+op+"(?,?)", []any{after.Rank, after.ID}
		}
		orderBy := " ORDER BY " + key + " " + order
		if key != id {
			orderBy += "," + id + " " + order
		}
		rows, err := r.seekArms(ctx, q, arms, n, func(a seekArm) (string, []any) {
			args := append([]any{q.collection, a.surface}, driverArgs...)
			return "SELECT " + id + "," + key + " FROM " + driver + seek, append(args, seekArgs...)
		}, orderBy)
		if err != nil {
			return nil, err
		}
		out = rows
	}
	if p.column != "" || len(out) >= n {
		return out, nil
	}
	// Phase 2: rows with no value for a custom sort field, by item ID.
	var seek string
	var seekArgs []any
	if after != nil && after.Rank == nil {
		seek, seekArgs = " AND p.item_id"+op+"?", []any{after.ID}
	}
	d := q.RankDef
	rows, err := r.seekArms(ctx, q, arms, n-len(out), func(a seekArm) (string, []any) {
		args := append([]any{q.collection, a.surface, d.Key, string(d.Type), d.Scale}, seekArgs...)
		return "SELECT p.item_id,NULL FROM item_surfaces p " + r.d.OrderedJoin() + " resources r ON r.id=p.item_id WHERE p.container_id=? AND p.surface=? AND NOT EXISTS(SELECT 1 FROM field_values sk WHERE sk.surface_id=p.id AND sk.field_key=? AND sk.field_type=? AND sk.scale=? AND sk.ordinal=0)" + seek, args
	}, " ORDER BY p.item_id "+order)
	if err != nil {
		return nil, err
	}
	return append(out, rows...), nil
}

// seekArms runs one ordered statement per arm and merges their rows.
func (r *Repo) seekArms(ctx context.Context, q *Query, arms []seekArm, n int, head func(seekArm) (string, []any), orderBy string) ([]Ranked, error) {
	var merged []Ranked
	for _, a := range arms {
		text, args := head(a)
		var b strings.Builder
		b.WriteString(text)
		b.WriteString(" AND r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL")
		b.WriteString(q.conds)
		b.WriteString(" AND ")
		b.WriteString(a.acl)
		b.WriteString(orderBy)
		b.WriteString(" LIMIT ?")
		args = append(args, q.collection)
		args = append(args, q.condArgs...)
		args = append(args, a.aclArgs...)
		rows, err := r.ranked(ctx, 1, b.String(), append(args, n)...)
		if err != nil {
			return nil, err
		}
		merged = mergeRanked(merged, rows, q.Descending())
	}
	if len(merged) > n {
		merged = merged[:n]
	}
	return merged, nil
}

// mergeRanked merges two runs ordered by (rank, id) in the given direction.
func mergeRanked(a, b []Ranked, descending bool) []Ranked {
	if len(a) == 0 {
		return b
	}
	out := make([]Ranked, 0, len(a)+len(b))
	for len(a) > 0 && len(b) > 0 {
		c := compareRank(a[0].Rank, b[0].Rank)
		if c == 0 {
			c = strings.Compare(a[0].ID, b[0].ID)
		}
		if descending {
			c = -c
		}
		if c <= 0 {
			out, a = append(out, a[0]), a[1:]
		} else {
			out, b = append(out, b[0]), b[1:]
		}
	}
	return append(append(out, a...), b...)
}

// compareRank orders typed ranks consistently across query branches.
func compareRank(a, b any) int {
	switch x := a.(type) {
	case time.Time:
		if y, ok := b.(time.Time); ok {
			return x.Compare(y)
		}
	case int64:
		switch y := b.(type) {
		case int64:
			return compareOrdered(x, y)
		case float64:
			return compareOrdered(float64(x), y)
		}
	case float64:
		switch y := b.(type) {
		case int64:
			return compareOrdered(x, float64(y))
		case float64:
			return compareOrdered(x, y)
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	case bool:
		if y, ok := b.(bool); ok {
			return compareOrdered(boolInt(x), boolInt(y))
		}
	}
	return 0
}

func compareOrdered[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
