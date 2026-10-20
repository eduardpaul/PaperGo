package content

import (
	"context"
	"strings"

	"papergo/internal/dal/access"
)

// LinkFilter is a filter tree over relationships: exactly one of And, Or,
// Not, Const and Cmp.
type LinkFilter struct {
	And, Or []LinkFilter
	Not     *LinkFilter
	// Const is a literal true or false.
	Const *bool
	Cmp   *LinkComparison
}

// LinkProperty is what a link comparison reads.
type LinkProperty string

const (
	LinkID       LinkProperty = "id"
	LinkDirected LinkProperty = "directed"
	// LinkAttribute reads an attribute of the link's metadata.
	LinkAttribute LinkProperty = "attribute"
)

// LiteralKind is the kind of a comparison's literal.
type LiteralKind string

const (
	LiteralText    LiteralKind = "text"
	LiteralNumber  LiteralKind = "number"
	LiteralBoolean LiteralKind = "boolean"
	LiteralNull    LiteralKind = "null"
)

// LinkComparison compares a property of a link with a literal: eq, ne, gt,
// ge, lt or le. Attributes compare a number only with numbers or numeric
// text, and other literals with values of their kind; null tests that an
// attribute is missing.
type LinkComparison struct {
	Property LinkProperty
	Key      string
	Op       string
	Kind     LiteralKind
	Value    any
}

var linkOperators = map[string]string{"eq": "=", "ne": "<>", "gt": ">", "ge": ">=", "lt": "<", "le": "<="}

// linkFilter is the SQL of a filter over relationships aliased l.
func (r *Repo) linkFilter(f *LinkFilter) (string, []any) {
	switch {
	case f.Not != nil:
		text, args := r.linkFilter(f.Not)
		return "NOT (" + text + ")", args
	case f.And != nil || f.Or != nil:
		list, op := f.And, " AND "
		if f.Or != nil {
			list, op = f.Or, " OR "
		}
		parts := []string{}
		args := []any{}
		for i := range list {
			text, a := r.linkFilter(&list[i])
			parts = append(parts, "("+text+")")
			args = append(args, a...)
		}
		return strings.Join(parts, op), args
	case f.Const != nil:
		if *f.Const {
			return "TRUE", nil
		}
		return "FALSE", nil
	}
	return r.linkComparison(f.Cmp)
}

func (r *Repo) linkComparison(c *LinkComparison) (string, []any) {
	operator := linkOperators[c.Op]
	switch c.Property {
	case LinkID:
		return "l.id" + operator + "?", []any{c.Value}
	case LinkDirected:
		return "l.directed" + operator + "?", []any{c.Value}
	}
	if r.d.Postgres {
		value := "l.metadata->?"
		typ := "jsonb_typeof(" + value + ")"
		switch c.Kind {
		case LiteralNull:
			if c.Op == "ne" {
				return typ + " IS NOT NULL", []any{c.Key}
			}
			return typ + " IS NULL", []any{c.Key}
		case LiteralNumber:
			// CASE orders the checks, so only numeric text is cast.
			numeric := `(` + typ + `='number' OR ` + typ + `='string' AND l.metadata->>? ~ '^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$')`
			return "CASE WHEN " + numeric + " THEN CAST(l.metadata->>? AS DOUBLE PRECISION)" + operator + "? ELSE FALSE END", []any{c.Key, c.Key, c.Key, c.Key, c.Value}
		case LiteralBoolean:
			return "CASE WHEN " + typ + "='boolean' THEN CAST(l.metadata->>? AS BOOLEAN)" + operator + "? ELSE FALSE END", []any{c.Key, c.Key, c.Value}
		}
		return "(" + typ + "='string' AND l.metadata->>?" + operator + "?)", []any{c.Key, c.Key, c.Value}
	}
	path := `$."` + strings.ReplaceAll(c.Key, `"`, `\"`) + `"`
	switch c.Kind {
	case LiteralNull:
		if c.Op == "ne" {
			return "json_type(l.metadata,?) IS NOT NULL", []any{path}
		}
		return "json_type(l.metadata,?) IS NULL", []any{path}
	case LiteralNumber:
		return "((json_type(l.metadata,?) IN ('integer','real') OR (json_type(l.metadata,?)='text' AND CASE WHEN json_valid(json_extract(l.metadata,?)) THEN json_type(json_extract(l.metadata,?)) IN ('integer','real') ELSE 0 END)) AND CAST(json_extract(l.metadata,?) AS REAL)" + operator + "?)", []any{path, path, path, path, path, c.Value}
	case LiteralBoolean:
		return "(json_type(l.metadata,?) IN ('true','false') AND json_extract(l.metadata,?)" + operator + "?)", []any{path, path, c.Value}
	}
	return "(json_type(l.metadata,?)='text' AND json_extract(l.metadata,?)" + operator + "?)", []any{path, path, c.Value}
}

// Links selects the relationships a reader sees: both endpoints are
// readable and show a surface the reader reads.
type Links struct {
	Reader access.Principal
	// Endpoint selects links at either end of an item; with Direction
	// outgoing or incoming, only directed links from or to it.
	Endpoint, Direction string
	// WorkspaceID selects links with an end in the workspace.
	WorkspaceID string
	TypeID      string
	Directed    *bool
	Filter      *LinkFilter
	AfterID     string
	Limit       int
}

// LinkIDs returns the IDs of the links l selects, in ID order.
func (r *Repo) LinkIDs(ctx context.Context, l Links) ([]string, error) {
	where := []string{}
	args := []any{}
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	switch l.Direction {
	case "outgoing":
		add("l.directed AND l.source_id=?", l.Endpoint)
	case "incoming":
		add("l.directed AND l.target_id=?", l.Endpoint)
	default:
		if l.Endpoint != "" {
			add("(l.source_id=? OR l.target_id=?)", l.Endpoint, l.Endpoint)
		}
	}
	if l.WorkspaceID != "" {
		add("EXISTS(SELECT 1 FROM resources peer WHERE peer.workspace_id=? AND peer.id IN (l.source_id,l.target_id))", l.WorkspaceID)
	}
	if l.TypeID != "" {
		add("l.type_id=?", l.TypeID)
	}
	if l.Directed != nil {
		add("l.directed=?", *l.Directed)
	}
	if l.Filter != nil {
		text, a := r.linkFilter(l.Filter)
		add("("+text+")", a...)
	}
	if l.AfterID != "" {
		add("l.id>?", l.AfterID)
	}
	for _, end := range []string{"l.source_id", "l.target_id"} {
		allowed, allowedArgs := r.acl.Resource(end, l.Reader, access.Read)
		visible, visibleArgs := r.acl.Visible(end, l.Reader, "auto")
		add(allowed+" AND "+visible, append(allowedArgs, visibleArgs...)...)
	}
	query := "SELECT l.id FROM relationships l WHERE " + strings.Join(where, " AND ") + " ORDER BY l.id"
	if l.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, l.Limit)
	}
	return r.strings(ctx, query, args...)
}
