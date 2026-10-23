package dms

import (
	"context"
	"encoding/json"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
	viewstore "papergo/internal/dal/views"
)

// ViewSettings configure a view's layout. A navigation view browses the
// terms of each of Fields as a tree of folders.
type ViewSettings struct {
	Fields []NavigationFieldSetting `json:"fields,omitempty"`
	// Match combines the terms opened in different fields: the items of
	// all of them, or of any.
	Match NavigationMatch `json:"match,omitempty"`
	// HideEmpty leaves out the terms no item of the view holds at or below
	// them.
	HideEmpty bool `json:"hide_empty,omitempty"`
}

// NavigationFieldSetting is a field a navigation view browses: $keywords or
// a term or keywords field.
type NavigationFieldSetting struct {
	Field string `json:"field"`
	// IncludeSubterms lists, at a term, the items holding a term below it
	// too. Unset follows the field: item keywords and fields that apply
	// parents always do, other fields list the items holding the term.
	IncludeSubterms *bool `json:"include_subterms,omitempty"`
}

// NavigationMatch is how a navigation view combines the terms opened in
// different fields.
type NavigationMatch string

const (
	// MatchAll lists the items of every opened term; each field's counts
	// follow the terms opened in the others. It is the default.
	MatchAll NavigationMatch = "all"
	// MatchAny lists the items of any opened term; counts cover the view.
	MatchAny NavigationMatch = "any"
)

// maxNavigationFields bounds the fields a navigation view browses.
const maxNavigationFields = 5

func (s ViewSettings) empty() bool {
	return len(s.Fields) == 0 && s.Match == "" && !s.HideEmpty
}

// navigationField is a field a navigation view browses: def is nil for
// item keywords.
type navigationField struct {
	key      string
	def      *catalog.FieldDefinition
	setID    string
	subterms bool
}

// validateViewSettings checks settings for layout against the
// collection's fields.
func validateViewSettings(layout viewstore.Layout, settings ViewSettings, defs []*catalog.FieldDefinition) error {
	if layout != viewstore.LayoutNavigation {
		if !settings.empty() {
			return invalid("settings belong to navigation views")
		}
		return nil
	}
	_, err := resolveNavigation(settings, defs)
	return err
}

func resolveNavigation(settings ViewSettings, defs []*catalog.FieldDefinition) ([]navigationField, error) {
	if len(settings.Fields) == 0 || len(settings.Fields) > maxNavigationFields {
		return nil, invalid("a navigation view browses 1 to 5 settings.fields")
	}
	switch settings.Match {
	case "", MatchAll, MatchAny:
	default:
		return nil, invalid("settings.match must be all or any")
	}
	out := make([]navigationField, 0, len(settings.Fields))
	seen := map[string]bool{}
	for _, f := range settings.Fields {
		if seen[f.Field] {
			return nil, invalid("settings.fields must be unique")
		}
		seen[f.Field] = true
		field, err := resolveNavigationField(f, defs)
		if err != nil {
			return nil, err
		}
		out = append(out, field)
	}
	return out, nil
}

func resolveNavigationField(setting NavigationFieldSetting, defs []*catalog.FieldDefinition) (navigationField, error) {
	out := navigationField{key: setting.Field, setID: taxonomy.KeywordsSetID, subterms: true}
	if setting.Field != "$keywords" {
		for _, d := range defs {
			if d.Key == setting.Field {
				out.def = d
			}
		}
		if out.def == nil || !out.def.Type.Terms() || !queryable(out.def) {
			return navigationField{}, invalid("settings.fields must be $keywords or indexed term or keywords fields")
		}
		if out.def.Type == catalog.FieldTerm {
			out.setID = out.def.Options.TermSetID
		}
		out.subterms = out.def.Options.ApplyParents
	}
	if setting.IncludeSubterms != nil {
		// Items holding a term below hold the term itself where parents apply.
		if !*setting.IncludeSubterms && out.subterms {
			return navigationField{}, invalid("item keywords and fields that apply parents always include subterms")
		}
		out.subterms = *setting.IncludeSubterms
	}
	return out, nil
}

// navigationFilter keeps the items a navigation node lists: those holding
// term, or one below it where subterms count, or with an empty term none.
func navigationFilter(field navigationField, term string) FilterExpr {
	if term == "" {
		return FilterExpr{Field: field.key, Op: "missing"}
	}
	value, _ := json.Marshal(term)
	op := "eq"
	// Item keywords and fields that apply parents match below with eq.
	if field.subterms && field.def != nil && !field.def.Options.ApplyParents {
		op = "under"
	}
	return FilterExpr{Field: field.key, Op: op, Value: value}
}

// NavigationSelection is a term opened in one field of a navigation view,
// or with NoValue the items holding none.
type NavigationSelection struct {
	Field   string `json:"field"`
	TermID  string `json:"term_id,omitempty"`
	NoValue bool   `json:"no_value,omitempty"`
}

// selectionFilter keeps what base keeps and the opened terms list,
// combined as match says, leaving out the selection of the field except.
func selectionFilter(base *FilterExpr, match NavigationMatch, fields []navigationField, selected []NavigationSelection, except string) (*FilterExpr, error) {
	byKey := map[string]navigationField{}
	for _, f := range fields {
		byKey[f.key] = f
	}
	seen := map[string]bool{}
	parts := []FilterExpr{}
	for _, s := range selected {
		field, ok := byKey[s.Field]
		if !ok || seen[s.Field] {
			return nil, invalid("selected names each field of the view at most once")
		}
		seen[s.Field] = true
		if (s.TermID == "") == !s.NoValue {
			return nil, invalid("a selection takes term_id or no_value")
		}
		if s.Field != except {
			parts = append(parts, navigationFilter(field, s.TermID))
		}
	}
	var node *FilterExpr
	switch {
	case len(parts) == 1:
		node = &parts[0]
	case len(parts) > 1 && match == MatchAny:
		node = &FilterExpr{Or: parts}
	case len(parts) > 1:
		node = &FilterExpr{And: parts}
	}
	if base == nil || node == nil {
		if node == nil {
			return base, nil
		}
		return node, nil
	}
	return &FilterExpr{And: []FilterExpr{*base, *node}}, nil
}

// viewNavigation reads a navigation view's settings and fields.
func (t *Service) viewNavigation(ctx context.Context, v *viewstore.ListView) (ViewSettings, []navigationField, error) {
	var settings ViewSettings
	if v.Layout != viewstore.LayoutNavigation {
		return settings, nil, invalid("the view is not a navigation view")
	}
	if err := json.Unmarshal(v.Settings, &settings); err != nil {
		return settings, nil, err
	}
	defs, err := t.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: v.ContainerID})
	if err != nil {
		return settings, nil, err
	}
	fields, err := resolveNavigation(settings, defs)
	return settings, fields, err
}

type NavigationRequest struct {
	// Field is the field whose terms are listed.
	Field string `json:"field"`
	// ParentID opens a term; empty lists the roots.
	ParentID string `json:"parent_id,omitempty"`
	Surface  string `json:"surface,omitempty"`
	// Selected are the terms opened in the view's fields. Where the view
	// matches all of them, counts follow those of the other fields.
	Selected []NavigationSelection `json:"selected,omitempty"`
	After    string                `json:"-"`
	Limit    int                   `json:"-"`
}

// NavigationNode is a term of a navigation level. Count is how many items
// the node lists; SubtreeCount how many hold the term or one below it.
// HasChildren says whether opening the node shows terms.
type NavigationNode struct {
	TermID       string `json:"term_id"`
	Name         string `json:"name"`
	HasChildren  bool   `json:"has_children"`
	Count        int    `json:"count"`
	SubtreeCount int    `json:"subtree_count"`
}

// Navigation is one level of a field of a navigation view: the terms below
// a parent, and at the roots how many items hold no term.
type Navigation struct {
	Field           string           `json:"field"`
	TermSetID       string           `json:"term_set_id"`
	IncludeSubterms bool             `json:"include_subterms"`
	HideEmpty       bool             `json:"hide_empty"`
	ParentID        string           `json:"parent_id,omitempty"`
	NoValueCount    *int             `json:"no_value_count,omitempty"`
	Data            []NavigationNode `json:"value"`
	NextCursor      string           `json:"-"`
}

// ViewNavigation lists a level of a navigation view's terms, with how many
// of the view's items each lists. Counts follow the view's query, the
// reader's access and the surface read.
func (s *Service) ViewNavigation(ctx context.Context, subject, id string, in NavigationRequest) (Navigation, error) {
	return read(ctx, s, func(t *Service) (Navigation, error) {
		v, e := t.View(ctx, subject, id)
		if e != nil {
			return Navigation{}, e
		}
		if e = t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return Navigation{}, e
		}
		settings, fields, e := t.viewNavigation(ctx, v)
		if e != nil {
			return Navigation{}, e
		}
		var field navigationField
		for _, f := range fields {
			if f.key == in.Field {
				field = f
			}
		}
		if field.key == "" {
			return Navigation{}, invalid("field must be one of the view's fields")
		}
		var spec QuerySpec
		if e = json.Unmarshal(v.Query, &spec); e != nil {
			return Navigation{}, e
		}
		// Counts read every item the view matches, in no particular order.
		// Matching all opened terms, they follow those of the other fields;
		// matching any, the other fields add items, so counts cover the view.
		spec.Sort, spec.GroupBy = nil, ""
		narrowing := in.Selected
		if settings.Match == MatchAny {
			narrowing = nil
			if _, e = selectionFilter(nil, settings.Match, fields, in.Selected, ""); e != nil {
				return Navigation{}, e
			}
		}
		if spec.Filter, e = selectionFilter(spec.Filter, settings.Match, fields, narrowing, field.key); e != nil {
			return Navigation{}, e
		}
		request := QueryRequest{Query: spec, Surface: in.Surface, itemsOnly: true}
		if request.Surface == "" {
			request.Surface = "auto"
		}
		q, e := t.compileQuery(ctx, subject, v.ContainerID, request, false)
		if e != nil {
			return Navigation{}, e
		}
		level := content.TermLevel{SetID: field.setID, HideEmpty: settings.HideEmpty, Limit: pageSize(in.Limit) + 1}
		if in.ParentID != "" {
			parent, e := t.DAL.Taxonomy().Term(ctx, in.ParentID)
			if dberr.IsNotFound(e) || e == nil && (parent.SetID != field.setID || parent.Merged()) {
				return Navigation{}, invalid("parent_id must be a term of the view's term set")
			}
			if e != nil {
				return Navigation{}, e
			}
			level.ParentID, level.ParentPath = parent.ID, parent.Path
		}
		fingerprint := queryFingerprint(struct {
			View, Subject, Field, Parent, Surface string
			Selected                              []NavigationSelection
		}{v.ID, subject, field.key, in.ParentID, request.Surface, in.Selected})
		if in.After != "" {
			after, ok := decodePageCursor[content.TermNodeKey](in.After, "n1.", fingerprint)
			if !ok {
				return Navigation{}, invalid("invalid cursor")
			}
			level.After = &after
		}
		source := content.TermSource{Field: field.def, Subterms: field.subterms}
		nodes, e := t.DAL.Content().TermNodes(ctx, q.q, source, level)
		if e != nil {
			return Navigation{}, e
		}
		out := Navigation{Field: field.key, TermSetID: field.setID, IncludeSubterms: field.subterms, HideEmpty: settings.HideEmpty, ParentID: in.ParentID, Data: []NavigationNode{}}
		if len(nodes) > pageSize(in.Limit) {
			nodes = nodes[:pageSize(in.Limit)]
			out.NextCursor = encodePageCursor("n1.", fingerprint, nodes[len(nodes)-1].Key)
		}
		for _, n := range nodes {
			children := n.Children
			if settings.HideEmpty {
				children = n.Below > 0
			}
			out.Data = append(out.Data, NavigationNode{TermID: n.Key.ID, Name: n.Name, HasChildren: children, Count: n.Listed, SubtreeCount: n.Subtree})
		}
		if in.ParentID == "" && in.After == "" {
			none, e := t.DAL.Content().Termless(ctx, q.q, source)
			if e != nil {
				return Navigation{}, e
			}
			out.NoValueCount = &none
		}
		return out, nil
	})
}
