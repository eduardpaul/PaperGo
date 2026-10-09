package dms

import (
	"context"
	"strings"
)

// seekPlan pages a collection query in sort-index order, so a page reads about
// as many index entries as it returns (plus the rows its predicates reject) and
// a cursor seeks to where the previous page ended. Sorting the eligible set
// instead costs the whole collection on every page.
type seekPlan struct {
	collection, subject, surface string
	// conditions are compileCollectionQuery's predicates over r and p.
	conditions string
	args       []any
	// column is the item_surfaces sort column of a system field; empty for a custom field.
	column string
}

type rankedID struct {
	id   string
	rank any
}

// seekArm reads one surface: an item appears on the head arm when the caller
// may read drafts there, otherwise on the published arm.
type seekArm struct {
	surface string
	acl     string
	aclArgs []any
}

// arms lists the surfaces that can hold matches. For auto, a collection's few
// ACL scopes, found by a loose index scan, decide whether any item is draft-
// readable (head arm) or readable without drafts (published arm); an arm that
// cannot match would otherwise scan its whole index to return nothing.
func (s *Service) arms(ctx context.Context, p *seekPlan) ([]seekArm, error) {
	draft, draftArgs := permissionSQL("r.id", p.subject, "read_draft")
	read, readArgs := permissionSQL("r.id", p.subject, "read")
	switch p.surface {
	case "head":
		return []seekArm{{"head", draft, draftArgs}}, nil
	case "published":
		return []seekArm{{"published", read, readArgs}}, nil
	}
	scopeDraft, scopeDraftArgs := scopeGrantSQL("scopes.id", p.subject, "read_draft")
	scopeRead, scopeReadArgs := scopeGrantSQL("scopes.id", p.subject, "read")
	args := append([]any{p.collection, p.collection}, scopeDraftArgs...)
	args = append(append(args, scopeReadArgs...), scopeDraftArgs...)
	var heads, published bool
	if err := s.scan(ctx, []any{&heads, &published}, `WITH RECURSIVE scopes(id) AS (
 SELECT min(scope_id) FROM resources WHERE container_id=?
 UNION ALL SELECT (SELECT min(scope_id) FROM resources WHERE container_id=? AND scope_id>scopes.id) FROM scopes WHERE scopes.id IS NOT NULL)
SELECT coalesce(max(`+scopeDraft+`),0),coalesce(max(`+scopeRead+` AND NOT `+scopeDraft+`),0) FROM scopes WHERE id IS NOT NULL`, args...); err != nil {
		return nil, err
	}
	arms := []seekArm{}
	if heads {
		arms = append(arms, seekArm{"head", draft, draftArgs})
	}
	if published {
		arms = append(arms, seekArm{"published", read + " AND NOT " + draft, append(append([]any{}, readArgs...), draftArgs...)})
	}
	return arms, nil
}

// seekPage returns up to n rows after the cursor in the order of eligiblePage:
// values by the sort direction, missing values last, ID ties in the sort
// direction. Every statement scans one index in order and stops at its LIMIT.
func (s *Service) seekPage(ctx context.Context, q compiledQuery, after string, n int) ([]rankedID, error) {
	p := q.seek
	var cursor *queryCursor
	var value any
	if after != "" {
		c, v, e := cursorDecode(q, after)
		if e != nil {
			return nil, e
		}
		if c.ID == "" {
			return nil, invalid("cursor has no item ID")
		}
		cursor, value = &c, v
	}
	order, op := "ASC", ">"
	if q.descending {
		order, op = "DESC", "<"
	}
	arms, e := s.arms(ctx, p)
	if e != nil {
		return nil, e
	}
	out := []rankedID{}
	// Phase 1: rows holding a sort value, unless the cursor already passed them.
	if cursor == nil || value != nil {
		var driver, key, id string
		var driverArgs []any
		if p.column != "" {
			driver, key, id = "item_surfaces p CROSS JOIN resources r ON r.id=p.item_id WHERE p.container_id=? AND p.surface=?", p.column, "p.item_id"
		} else {
			col, _ := indexColumn(q.rankDef)
			// Ties use sk.item_id: the planner cannot use the index for p.item_id.
			key, id = "sk."+col, "sk.item_id"
			driver = "field_values sk CROSS JOIN item_surfaces p ON p.id=sk.surface_id CROSS JOIN resources r ON r.id=sk.item_id WHERE sk.container_id=? AND sk.surface=? AND sk.field_key=? AND sk.field_type=? AND sk.scale=? AND sk.ordinal=0"
			driverArgs = []any{q.rankDef.Key, string(q.rankDef.Type), q.rankDef.Scale}
		}
		var seek string
		var seekArgs []any
		switch {
		case cursor == nil:
		case key == id:
			seek, seekArgs = " AND "+id+op+"?", []any{cursor.ID}
		default:
			seek, seekArgs = " AND ("+key+","+id+")"+op+"(?,?)", []any{value, cursor.ID}
		}
		orderBy := " ORDER BY " + key + " " + order
		if key != id {
			orderBy += "," + id + " " + order
		}
		rows, e := s.seekArms(ctx, p, arms, q.descending, n, func(a seekArm) (string, []any) {
			args := append([]any{p.collection, a.surface}, driverArgs...)
			args = append(args, seekArgs...)
			return "SELECT " + id + "," + key + " FROM " + driver + seek, args
		}, orderBy)
		if e != nil {
			return nil, e
		}
		out = rows
	}
	if p.column != "" || len(out) >= n {
		return out, nil
	}
	// Phase 2: rows with no value for a custom sort field, by item ID.
	var seek string
	var seekArgs []any
	if cursor != nil && value == nil {
		seek, seekArgs = " AND p.item_id"+op+"?", []any{cursor.ID}
	}
	d := q.rankDef
	rows, e := s.seekArms(ctx, p, arms, q.descending, n-len(out), func(a seekArm) (string, []any) {
		args := append([]any{p.collection, a.surface, d.Key, string(d.Type), d.Scale}, seekArgs...)
		return "SELECT p.item_id,NULL FROM item_surfaces p CROSS JOIN resources r ON r.id=p.item_id WHERE p.container_id=? AND p.surface=? AND NOT EXISTS(SELECT 1 FROM field_values sk WHERE sk.surface_id=p.id AND sk.field_key=? AND sk.field_type=? AND sk.scale=? AND sk.ordinal=0)" + seek, args
	}, " ORDER BY p.item_id "+order)
	if e != nil {
		return nil, e
	}
	return append(out, rows...), nil
}

// seekArms runs one ordered statement per arm and merges their rows.
func (s *Service) seekArms(ctx context.Context, p *seekPlan, arms []seekArm, descending bool, n int, head func(seekArm) (string, []any), orderBy string) ([]rankedID, error) {
	var merged []rankedID
	for _, a := range arms {
		text, args := head(a)
		var b strings.Builder
		b.WriteString(text)
		b.WriteString(" AND r.container_id=? AND r.kind='item'")
		b.WriteString(p.conditions)
		b.WriteString(" AND ")
		b.WriteString(a.acl)
		b.WriteString(orderBy)
		b.WriteString(" LIMIT ?")
		args = append(args, p.collection)
		args = append(args, p.args...)
		args = append(args, a.aclArgs...)
		rows, e := s.rankedIDs(ctx, b.String(), append(args, n)...)
		if e != nil {
			return nil, e
		}
		merged = mergeRanked(merged, rows, descending)
	}
	if len(merged) > n {
		merged = merged[:n]
	}
	return merged, nil
}

// mergeRanked merges two runs ordered by (rank, id) in the given direction.
func mergeRanked(a, b []rankedID, descending bool) []rankedID {
	if len(a) == 0 {
		return b
	}
	out := make([]rankedID, 0, len(a)+len(b))
	for len(a) > 0 && len(b) > 0 {
		c := compareRank(a[0].rank, b[0].rank)
		if c == 0 {
			c = strings.Compare(a[0].id, b[0].id)
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

// compareRank orders scanned SQLite values of one sort column as SQLite does:
// numbers by value, text by bytes (BINARY collation).
func compareRank(a, b any) int {
	switch x := a.(type) {
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
