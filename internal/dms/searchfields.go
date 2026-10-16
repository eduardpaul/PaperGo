package dms

import (
	"context"
	"errors"
	"slices"
	"strings"

	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/internal/search"
)

// SearchField is a field search can filter on: a key with a type and
// scale, as the readable lists and libraries define it.
type SearchField struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Type      string   `json:"type"`
	Scale     int      `json:"scale,omitempty"`
	Multiple  bool     `json:"multiple,omitempty"`
	Choices   []string `json:"choices,omitempty"`
	TermSetID string   `json:"term_set_id,omitempty"`
	// Operators are the filter operators the field takes.
	Operators []string `json:"operators"`
}

// searchOperators are the filter operators of each kind of field.
func searchOperators(typ string) []string {
	switch typ {
	case "term", "keywords":
		return []string{"eq", "ne", "in", "under", "present", "missing"}
	case "boolean", "choice", "lookup", "person":
		return []string{"eq", "ne", "in", "present", "missing"}
	default:
		return []string{"eq", "ne", "gt", "gte", "lt", "lte", "in", "present", "missing"}
	}
}

// searchFieldDefinitions are the field definitions of the lists and
// libraries the subject may read, optionally of one workspace or
// collection, by key. Notes are text, not filters.
func (s *Service) searchFieldDefinitions(ctx context.Context, subject, workspaceID, collectionID string) (map[string][]*ent.FieldDefinition, error) {
	readable, args := permissionSQL(ctx, "r.id", subject, "read")
	conds := []string{"r.kind IN ('list','library')", "r.deleted_at IS NULL", readable}
	if workspaceID != "" {
		conds, args = append(conds, "r.workspace_id=?"), append(args, workspaceID)
	}
	if collectionID != "" {
		conds, args = append(conds, "r.id=?"), append(args, collectionID)
	}
	rows, err := s.Client.QueryContext(ctx, `SELECT DISTINCT f.value FROM resources r JOIN collection_types a ON a.container_id=r.id
 JOIN content_types t ON t.id=a.content_type_id, json_each(t.field_ids) f WHERE `+strings.Join(conds, " AND "), args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil || len(ids) == 0 {
		return map[string][]*ent.FieldDefinition{}, err
	}
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.IDIn(ids...), fielddefinition.TypeNEQ(fielddefinition.TypeNote)).
		Order(ent.Asc(fielddefinition.FieldKey), ent.Asc(fielddefinition.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]*ent.FieldDefinition{}
	for _, d := range defs {
		// One definition per key, type and scale: they share index rows.
		if !slices.ContainsFunc(out[d.Key], func(x *ent.FieldDefinition) bool { return x.Type == d.Type && x.Scale == d.Scale }) {
			out[d.Key] = append(out[d.Key], d)
		}
	}
	return out, nil
}

// SearchFields lists the fields search can filter on for the subject, of
// the lists and libraries they may read, optionally narrowed to a workspace
// or a collection.
func (s *Service) SearchFields(ctx context.Context, subject, workspaceID, collectionID string) ([]SearchField, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	return read(ctx, s, func(t *Service) ([]SearchField, error) {
		defs, err := t.searchFieldDefinitions(ctx, subject, workspaceID, collectionID)
		if err != nil {
			return nil, err
		}
		out := []SearchField{}
		for _, key := range slices.Sorted(func(yield func(string) bool) {
			for k := range defs {
				if !yield(k) {
					return
				}
			}
		}) {
			for _, d := range defs[key] {
				out = append(out, SearchField{Key: d.Key, Label: d.Label, Type: string(d.Type), Scale: d.Scale, Multiple: d.Options.Multiple,
					Choices: d.Choices, TermSetID: d.Options.TermSetID, Operators: searchOperators(string(d.Type))})
			}
		}
		return out, nil
	})
}

// SearchContentType is a named type attached to a collection the caller can read.
type SearchContentType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) SearchContentTypes(ctx context.Context, subject, workspaceID, collectionID string) ([]SearchContentType, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	return read(ctx, s, func(t *Service) ([]SearchContentType, error) {
		readable, args := permissionSQL(ctx, "r.id", subject, "read")
		conditions := []string{"r.kind IN ('list','library')", "r.deleted_at IS NULL", readable}
		if workspaceID != "" {
			conditions, args = append(conditions, "r.workspace_id=?"), append(args, workspaceID)
		}
		if collectionID != "" {
			conditions, args = append(conditions, "r.id=?"), append(args, collectionID)
		}
		rows, err := t.Client.QueryContext(ctx, `SELECT DISTINCT t.id,t.name FROM resources r JOIN collection_types a ON a.container_id=r.id JOIN content_types t ON t.id=a.content_type_id WHERE `+strings.Join(conditions, " AND ")+` ORDER BY t.name,t.id`, args...)
		if err != nil {
			return nil, err
		}
		out := []SearchContentType{}
		for rows.Next() {
			var typ SearchContentType
			if err = rows.Scan(&typ.ID, &typ.Name); err != nil {
				break
			}
			out = append(out, typ)
		}
		return out, errors.Join(err, rows.Err(), rows.Close())
	})
}

// Search filters are bounded like collection query filters.
const (
	maxSearchFilterNodes = 32
	maxSearchFilterDepth = 6
)

// searchFilter compiles a filter on search fields: each condition applies
// to every type and scale its key has among the definitions, with the value
// encoded as the field index encodes it.
func (s *Service) searchFilter(ctx context.Context, f *FilterExpr, defs map[string][]*ent.FieldDefinition, depth int, nodes *int) (*search.FieldFilter, error) {
	if *nodes++; depth > maxSearchFilterDepth || *nodes > maxSearchFilterNodes {
		return nil, invalid("filter is limited to 32 nodes and depth 6")
	}
	groups := 0
	for _, set := range []bool{f.And != nil, f.Or != nil, f.Not != nil} {
		if set {
			groups++
		}
	}
	if groups > 0 {
		if groups != 1 || f.Field != "" || f.Op != "" || len(f.Value) > 0 || f.ValueRef != "" {
			return nil, invalid("filter node must be a group or a condition")
		}
		if f.Not != nil {
			inner, err := s.searchFilter(ctx, f.Not, defs, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			return &search.FieldFilter{Not: inner}, nil
		}
		list := f.And
		if f.Or != nil {
			list = f.Or
		}
		if len(list) == 0 {
			return nil, invalid("filter groups cannot be empty")
		}
		parts := make([]search.FieldFilter, len(list))
		for i := range list {
			part, err := s.searchFilter(ctx, &list[i], defs, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			parts[i] = *part
		}
		if f.Or != nil {
			return &search.FieldFilter{Or: parts}, nil
		}
		return &search.FieldFilter{And: parts}, nil
	}
	if f.ValueRef != "" {
		return nil, invalid("search filters take values, not value_ref")
	}
	variants := defs[f.Field]
	if len(variants) == 0 {
		return nil, invalid("unknown search field: " + f.Field + " (GET /v1/search/fields lists them)")
	}
	op := f.Op
	if op == "" {
		op = "eq"
	}
	var conds []search.FieldFilter
	var first error
	for _, d := range variants {
		c, err := s.searchCondition(ctx, d, op, f)
		if err != nil {
			first = cmpOr(first, err)
			continue
		}
		conds = append(conds, c)
	}
	if len(conds) == 0 {
		return nil, first
	}
	if len(conds) == 1 {
		return &conds[0], nil
	}
	// Missing means missing in every type the key has; the others hold
	// when any type matches.
	if op == "missing" {
		return &search.FieldFilter{And: conds}, nil
	}
	return &search.FieldFilter{Or: conds}, nil
}

func cmpOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// searchCondition compiles a condition on one field definition.
func (s *Service) searchCondition(ctx context.Context, d *ent.FieldDefinition, op string, f *FilterExpr) (search.FieldFilter, error) {
	c := search.FieldFilter{Key: d.Key, Type: string(d.Type), Scale: d.Scale, Op: op}
	if !slices.Contains(searchOperators(c.Type), op) {
		return c, invalid(f.Field + " does not support " + op)
	}
	if op == "present" || op == "missing" {
		if len(f.Value) > 0 {
			return c, invalid("missing/present do not accept a value")
		}
		return c, nil
	}
	v, err := decodeValue(f.Value)
	if err != nil || v == nil {
		return c, invalid("filter value is required")
	}
	list := []any{v}
	if op == "in" {
		var ok bool
		if list, ok = v.([]any); !ok || len(list) < 1 || len(list) > 50 {
			return c, invalid("in requires 1..50 values")
		}
	}
	for _, x := range list {
		encoded, err := queryValue(d, x)
		if err != nil {
			return c, err
		}
		c.Values = append(c.Values, searchQueryValue(encoded))
	}
	if d.Type != "term" && d.Type != "keywords" {
		return c, nil
	}
	// A term matches the terms merged into it; under also its descendants.
	var ids []string
	for _, v := range c.Values {
		expand := s.SearchTermAliases
		if op == "under" {
			expand = s.SearchTerms
		}
		expanded, err := expand(ctx, v.Text)
		if err != nil {
			return c, err
		}
		ids = append(ids, expanded...)
	}
	c.Values = c.Values[:0]
	for _, id := range ids {
		c.Values = append(c.Values, search.FieldValue{Text: id})
	}
	if op != "ne" {
		c.Op = "in"
	}
	return c, nil
}

// searchQueryValue turns an encoded query value into a search value.
func searchQueryValue(v any) search.FieldValue {
	switch x := v.(type) {
	case int64:
		return search.FieldValue{Int: x}
	case bool:
		if x {
			return search.FieldValue{Int: 1}
		}
		return search.FieldValue{}
	case string:
		return search.FieldValue{Text: x}
	}
	return search.FieldValue{}
}

// SearchTermAliases is a term with the terms merged into it: retained values
// may hold any of these IDs while displaying the target's words.
func (s *Service) SearchTermAliases(ctx context.Context, id string) ([]string, error) {
	rows, err := s.Client.QueryContext(ctx, "SELECT ? UNION SELECT id FROM terms WHERE merged_into_id=?", id, id)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			break
		}
		out = append(out, v)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}
