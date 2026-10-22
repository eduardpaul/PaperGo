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
// terms of Field, a term or keywords field or $keywords, as folders.
type ViewSettings struct {
	Field string `json:"field,omitempty"`
	// IncludeSubterms lists, at a term, the items holding a term below it
	// too. Unset follows the field: item keywords and fields that apply
	// parents always do, other fields list the items holding the term.
	IncludeSubterms *bool `json:"include_subterms,omitempty"`
	// HideEmpty leaves out the terms no item of the view holds at or below
	// them.
	HideEmpty bool `json:"hide_empty,omitempty"`
}

// navigationField is the field a navigation view browses: nil for item
// keywords.
type navigationField struct {
	def      *catalog.FieldDefinition
	setID    string
	subterms bool
}

// validateViewSettings checks settings for layout against the
// collection's fields.
func validateViewSettings(layout viewstore.Layout, settings ViewSettings, defs []*catalog.FieldDefinition) error {
	if layout != viewstore.LayoutNavigation {
		if settings != (ViewSettings{}) {
			return invalid("settings belong to navigation views")
		}
		return nil
	}
	_, err := resolveNavigationField(settings, defs)
	return err
}

func resolveNavigationField(settings ViewSettings, defs []*catalog.FieldDefinition) (navigationField, error) {
	if settings.Field == "" {
		return navigationField{}, invalid("a navigation view needs settings.field")
	}
	out := navigationField{setID: taxonomy.KeywordsSetID, subterms: true}
	if settings.Field != "$keywords" {
		for _, d := range defs {
			if d.Key == settings.Field {
				out.def = d
			}
		}
		if out.def == nil || !out.def.Type.Terms() || !queryable(out.def) {
			return navigationField{}, invalid("settings.field must be $keywords or an indexed term or keywords field")
		}
		if out.def.Type == catalog.FieldTerm {
			out.setID = out.def.Options.TermSetID
		}
		out.subterms = out.def.Options.ApplyParents
	}
	if settings.IncludeSubterms != nil {
		// Items holding a term below hold the term itself where parents apply.
		if !*settings.IncludeSubterms && out.subterms {
			return navigationField{}, invalid("item keywords and fields that apply parents always include subterms")
		}
		out.subterms = *settings.IncludeSubterms
	}
	return out, nil
}

// navigationFilter keeps the items a navigation node lists: those holding
// term, or one below it where subterms count, or with an empty term none.
func navigationFilter(settings ViewSettings, field navigationField, term string) FilterExpr {
	if term == "" {
		return FilterExpr{Field: settings.Field, Op: "missing"}
	}
	value, _ := json.Marshal(term)
	op := "eq"
	// Item keywords and fields that apply parents match below with eq.
	if field.subterms && field.def != nil && !field.def.Options.ApplyParents {
		op = "under"
	}
	return FilterExpr{Field: settings.Field, Op: op, Value: value}
}

// viewNavigation reads a navigation view's settings and field.
func (t *Service) viewNavigation(ctx context.Context, v *viewstore.ListView) (ViewSettings, navigationField, error) {
	var settings ViewSettings
	if v.Layout != viewstore.LayoutNavigation {
		return settings, navigationField{}, invalid("the view is not a navigation view")
	}
	if err := json.Unmarshal(v.Settings, &settings); err != nil {
		return settings, navigationField{}, err
	}
	defs, err := t.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: v.ContainerID})
	if err != nil {
		return settings, navigationField{}, err
	}
	field, err := resolveNavigationField(settings, defs)
	return settings, field, err
}

type NavigationRequest struct {
	// ParentID opens a term; empty lists the roots.
	ParentID string `json:"parent_id,omitempty"`
	Surface  string `json:"surface,omitempty"`
	After    string `json:"-"`
	Limit    int    `json:"-"`
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

// Navigation is one level of a navigation view: the terms below a parent,
// and at the roots how many items hold no term.
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
		settings, field, e := t.viewNavigation(ctx, v)
		if e != nil {
			return Navigation{}, e
		}
		var spec QuerySpec
		if e = json.Unmarshal(v.Query, &spec); e != nil {
			return Navigation{}, e
		}
		// Counts read every item the view matches, in no particular order.
		spec.Sort, spec.GroupBy = nil, ""
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
		fingerprint := queryFingerprint(struct{ View, Subject, Parent, Surface string }{v.ID, subject, in.ParentID, request.Surface})
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
		out := Navigation{Field: settings.Field, TermSetID: field.setID, IncludeSubterms: field.subterms, HideEmpty: settings.HideEmpty, ParentID: in.ParentID, Data: []NavigationNode{}}
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
