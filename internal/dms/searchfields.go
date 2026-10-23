package dms

import (
	"cmp"
	"context"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/taxonomy"
	"slices"
	"strings"
)

// SearchField is a field searches and smart folders can filter on: a key with a type and
// scale, as the readable lists and libraries define it.
type SearchField struct {
	Queryable         bool     `json:"queryable"`
	LookupContainerID string   `json:"lookup_container_id,omitempty"`
	Key               string   `json:"key"`
	Label             string   `json:"label"`
	Type              string   `json:"type"`
	Scale             int      `json:"scale,omitempty"`
	Multiple          bool     `json:"multiple,omitempty"`
	Choices           []string `json:"choices,omitempty"`
	TermSetID         string   `json:"term_set_id,omitempty"`
	// Operators are the filter operators the field takes.
	Operators []string `json:"operators"`
}

// searchOperators are the filter operators of each kind of field.
func searchOperators(typ catalog.FieldType) []string {
	switch typ {
	case catalog.FieldTerm, catalog.FieldKeywords:
		return []string{"eq", "ne", "in", "under", "present", "missing"}
	case catalog.FieldBoolean, catalog.FieldChoice, catalog.FieldLookup, catalog.FieldPerson:
		return []string{"eq", "ne", "in", "present", "missing"}
	default:
		return []string{"eq", "ne", "gt", "gte", "lt", "lte", "in", "present", "missing"}
	}
}

// searchFieldDefinitions are the field definitions of the lists and
// libraries the subject may read, optionally of one workspace or
// collection, by key. Notes are text, not filters.
func (s *Service) searchFieldDefinitions(ctx context.Context, subject, workspaceID, collectionID string) (map[string][]*catalog.FieldDefinition, error) {
	types, err := s.readableContentTypes(ctx, subject, workspaceID, collectionID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, typ := range types {
		for _, id := range typ.FieldIds {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return map[string][]*catalog.FieldDefinition{}, nil
	}
	defs, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{IDs: append([]string{}, ids...), ExcludeType: "note"})
	if err != nil {
		return nil, err
	}
	out := map[string][]*catalog.FieldDefinition{}
	for _, d := range defs {
		// Preserve the scopes, available choices and index readiness needed by typed rule editors.
		if !slices.ContainsFunc(out[d.Key], func(x *catalog.FieldDefinition) bool {
			return x.Type == d.Type && x.Scale == d.Scale && x.Options.Multiple == d.Options.Multiple && x.Options.TermSetID == d.Options.TermSetID && x.Options.LookupContainerID == d.Options.LookupContainerID && queryable(x) == queryable(d) && slices.Equal(x.Choices, d.Choices)
		}) {
			out[d.Key] = append(out[d.Key], d)
		}
	}
	return out, nil
}

// SearchFields lists the fields search can filter on for the subject, of
// the lists and libraries they may read, optionally narrowed to a workspace
// or a collection.
func (s *Service) SearchFields(ctx context.Context, subject, workspaceID, collectionID string) ([]SearchField, error) {
	if err := s.RequireScope(ctx, subject, "item.read"); err != nil {
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
					Choices: d.Choices, TermSetID: d.Options.TermSetID, Operators: searchOperators(d.Type), Queryable: queryable(d), LookupContainerID: d.Options.LookupContainerID})
			}
		}
		return out, nil
	})
}

// SearchContentType is a named type attached to a collection the caller can read.
type SearchContentType struct {
	Key  string `json:"key"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Service) SearchContentTypes(ctx context.Context, subject, workspaceID, collectionID string) ([]SearchContentType, error) {
	if err := s.RequireScope(ctx, subject, "item.read"); err != nil {
		return nil, err
	}
	return read(ctx, s, func(t *Service) ([]SearchContentType, error) {
		types, err := t.readableContentTypes(ctx, subject, workspaceID, collectionID)
		if err != nil {
			return nil, err
		}
		out := make([]SearchContentType, len(types))
		for i, typ := range types {
			out[i] = SearchContentType{ID: typ.ID, Name: typ.Name, Key: typ.Key}
		}
		slices.SortFunc(out, func(a, b SearchContentType) int {
			return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ID, b.ID))
		})
		return out, nil
	})
}

// readableContentTypes are the content types of the lists and libraries the
// subject may read, optionally of one workspace or collection.
func (s *Service) readableContentTypes(ctx context.Context, subject, workspaceID, collectionID string) ([]*catalog.ContentType, error) {
	scope := content.Collections{Reader: principal(ctx, subject), WorkspaceID: workspaceID}
	if collectionID != "" {
		scope.IDs = []string{collectionID}
	}
	collections, err := s.DAL.Content().CollectionIDs(ctx, scope)
	if err != nil {
		return nil, err
	}
	return s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{CollectionIDs: collections})
}

// IndexFilter is a filter on the fields of the lists and libraries a
// subject may read, for an index kept outside the database: a condition
// on one type and scale of a key, or a group. Values are encoded as the
// field index encodes them, and terms are expanded to the terms merged
// into them (under: also their descendants).
type IndexFilter struct {
	Key    string
	Type   string
	Scale  int
	Op     string
	Values []IndexValue
	And    []IndexFilter
	Or     []IndexFilter
	Not    *IndexFilter
}

// Index filters are bounded like collection query filters.
const (
	maxIndexFilterNodes = 32
	maxIndexFilterDepth = 6
)

// CompileIndexFilter compiles a filter on the fields of the lists and
// libraries the subject may read (SearchFields), optionally of one
// workspace or collection. A condition applies to every type and scale its
// key has among them.
func (s *Service) CompileIndexFilter(ctx context.Context, subject, workspaceID, collectionID string, f *FilterExpr) (*IndexFilter, error) {
	return read(ctx, s, func(t *Service) (*IndexFilter, error) {
		defs, err := t.searchFieldDefinitions(ctx, subject, workspaceID, collectionID)
		if err != nil {
			return nil, err
		}
		nodes := 0
		return t.indexFilter(ctx, f, defs, 0, &nodes)
	})
}

func (s *Service) indexFilter(ctx context.Context, f *FilterExpr, defs map[string][]*catalog.FieldDefinition, depth int, nodes *int) (*IndexFilter, error) {
	if *nodes++; depth > maxIndexFilterDepth || *nodes > maxIndexFilterNodes {
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
			inner, err := s.indexFilter(ctx, f.Not, defs, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			return &IndexFilter{Not: inner}, nil
		}
		list := f.And
		if f.Or != nil {
			list = f.Or
		}
		if len(list) == 0 {
			return nil, invalid("filter groups cannot be empty")
		}
		parts := make([]IndexFilter, len(list))
		for i := range list {
			part, err := s.indexFilter(ctx, &list[i], defs, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			parts[i] = *part
		}
		if f.Or != nil {
			return &IndexFilter{Or: parts}, nil
		}
		return &IndexFilter{And: parts}, nil
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
	var conds []IndexFilter
	var first error
	for _, d := range variants {
		c, err := s.indexCondition(ctx, d, op, f)
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
		return &IndexFilter{And: conds}, nil
	}
	return &IndexFilter{Or: conds}, nil
}

func cmpOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// indexCondition compiles a condition on one field definition.
func (s *Service) indexCondition(ctx context.Context, d *catalog.FieldDefinition, op string, f *FilterExpr) (IndexFilter, error) {
	c := IndexFilter{Key: d.Key, Type: string(d.Type), Scale: d.Scale, Op: op}
	if !slices.Contains(searchOperators(d.Type), op) {
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
		c.Values = append(c.Values, queryIndexValue(encoded))
	}
	if d.Type != catalog.FieldTerm && d.Type != catalog.FieldKeywords {
		return c, nil
	}
	// A term matches the terms merged into it; under also its descendants,
	// and so does every comparison on a field whose values apply parents.
	var ids []string
	for _, v := range c.Values {
		expand := s.TermAliases
		if op == "under" || d.Options.ApplyParents {
			expand = s.TermFamily
		}
		expanded, err := expand(ctx, v.Text)
		if err != nil {
			return c, err
		}
		ids = append(ids, expanded...)
	}
	c.Values = c.Values[:0]
	for _, id := range ids {
		c.Values = append(c.Values, IndexValue{Text: id})
	}
	if op != "ne" {
		c.Op = "in"
	}
	return c, nil
}

// queryIndexValue turns an encoded query value into an index value.
func queryIndexValue(v any) IndexValue {
	switch x := v.(type) {
	case int64:
		return IndexValue{Int: x}
	case bool:
		if x {
			return IndexValue{Int: 1}
		}
		return IndexValue{}
	case string:
		return IndexValue{Text: x}
	}
	return IndexValue{}
}

// TermAliases is a term with the terms merged into it: retained values may
// hold any of these IDs while displaying the target's words.
func (s *Service) TermAliases(ctx context.Context, id string) ([]string, error) {
	merged, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{MergedInto: id})
	if err != nil {
		return nil, err
	}
	out := []string{id}
	for _, t := range merged {
		if t.ID != id {
			out = append(out, t.ID)
		}
	}
	return out, nil
}
