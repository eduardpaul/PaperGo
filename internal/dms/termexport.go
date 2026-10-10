package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/term"
	"papergo/ent/termgroup"
	"papergo/ent/termset"
	"strings"
)

// A taxonomy package carries a workspace's term groups, sets and terms by
// name, so the taxonomy can be set up again in another workspace or
// deployment. Terms nest under their parents; merged terms are left out.
// Import is additive: groups match by name (the system group by system),
// sets by key and terms by name under the same parent; what exists stays as
// it is and what is missing is created.

type TaxonomyPackage struct {
	Groups []PortableTermGroup `json:"groups"`
}
type PortableTermGroup struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	System      bool              `json:"system,omitempty"`
	Sets        []PortableTermSet `json:"sets"`
}
type PortableTermSet struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	IsOpen      bool           `json:"is_open,omitempty"`
	Terms       []PortableTerm `json:"terms,omitempty"`
}
type PortableTerm struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	Color              *string           `json:"color,omitempty"`
	SortOrder          int               `json:"sort_order,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	Synonyms           []string          `json:"synonyms,omitempty"`
	Deprecated         bool              `json:"deprecated,omitempty"`
	AvailableAsKeyword bool              `json:"available_as_keyword,omitempty"`
	Children           []PortableTerm    `json:"children,omitempty"`
}
type TaxonomyImportResult struct {
	GroupsCreated int `json:"groups_created"`
	SetsCreated   int `json:"sets_created"`
	TermsCreated  int `json:"terms_created"`
}

const (
	maxPackageGroups = 100
	maxPackageSets   = 500
	maxPackageTerms  = 20000
)

func (s *Service) ExportTaxonomy(ctx context.Context, subject, workspaceID string) (TaxonomyPackage, error) {
	return read(ctx, s, func(t *Service) (TaxonomyPackage, error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "read"); e != nil {
			return TaxonomyPackage{}, e
		}
		groups, e := t.Client.TermGroup.Query().Where(termgroup.WorkspaceIDEQ(workspaceID)).Order(ent.Desc(termgroup.FieldIsSystem), ent.Asc(termgroup.FieldName)).
			WithTermSets(func(q *ent.TermSetQuery) { q.Order(ent.Asc(termset.FieldKey)) }).All(ctx)
		if e != nil {
			return TaxonomyPackage{}, e
		}
		out := TaxonomyPackage{Groups: []PortableTermGroup{}}
		for _, g := range groups {
			pg := PortableTermGroup{Name: g.Name, Description: g.Description, System: g.IsSystem, Sets: []PortableTermSet{}}
			for _, set := range g.Edges.TermSets {
				terms, e := t.Client.Term.Query().Where(term.TermSetIDEQ(set.ID), term.MergedIntoIDIsNil()).
					Order(ent.Asc(term.FieldSortOrder), ent.Asc(term.FieldNormalizedName), ent.Asc(term.FieldID)).All(ctx)
				if e != nil {
					return TaxonomyPackage{}, e
				}
				children := map[string][]*ent.Term{}
				for _, v := range terms {
					parent := ""
					if v.ParentID != nil {
						parent = *v.ParentID
					}
					children[parent] = append(children[parent], v)
				}
				var tree func(parent string) []PortableTerm
				tree = func(parent string) []PortableTerm {
					var nodes []PortableTerm
					for _, v := range children[parent] {
						nodes = append(nodes, PortableTerm{Name: v.Name, Description: v.Description, Color: v.Color, SortOrder: v.SortOrder, Labels: v.Labels,
							Synonyms: v.Synonyms, Deprecated: v.Deprecated, AvailableAsKeyword: v.AvailableAsKeyword, Children: tree(v.ID)})
					}
					return nodes
				}
				pg.Sets = append(pg.Sets, PortableTermSet{Key: set.Key, Name: set.Name, Description: set.Description, IsOpen: set.IsOpen, Terms: tree("")})
			}
			out.Groups = append(out.Groups, pg)
		}
		return out, nil
	})
}

func (s *Service) ImportTaxonomy(ctx context.Context, subject, workspaceID string, pkg TaxonomyPackage) (out TaxonomyImportResult, err error) {
	if e := pkg.validate(); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		out = TaxonomyImportResult{}
		w, e := t.workspace(ctx, subject, workspaceID, "manage")
		if e != nil {
			return e
		}
		for _, pg := range pkg.Groups {
			g, e := t.importGroup(ctx, w.ID, pg, &out)
			if e != nil {
				return e
			}
			for _, ps := range pg.Sets {
				set, e := t.Client.TermSet.Query().Where(termset.WorkspaceIDEQ(w.ID), termset.KeyEQ(ps.Key)).Only(ctx)
				switch {
				case ent.IsNotFound(e):
					if g.IsSystem {
						return invalid("set " + ps.Key + ": the system group holds only the keywords set")
					}
					if set, e = t.Client.TermSet.Create().SetWorkspaceID(w.ID).SetGroupID(g.ID).SetKey(ps.Key).SetName(ps.Name).SetDescription(ps.Description).SetIsOpen(ps.IsOpen).Save(ctx); e != nil {
						return e
					}
					out.SetsCreated++
				case e != nil:
					return e
				case set.GroupID != g.ID:
					return invalid("set " + ps.Key + " belongs to another group here")
				}
				existing, e := t.Client.Term.Query().Where(term.TermSetIDEQ(set.ID), term.MergedIntoIDIsNil()).All(ctx)
				if e != nil {
					return e
				}
				children := map[string]*ent.Term{}
				for _, v := range existing {
					children[termChildKey(v.ParentID, v.NormalizedName)] = v
				}
				if e = t.importTerms(ctx, set, nil, ps.Terms, children, &out); e != nil {
					return invalid("set " + ps.Key + ": " + e.Error())
				}
			}
		}
		return t.audit(ctx, subject, "taxonomy.import", w, map[string]any{"groups_created": out.GroupsCreated, "sets_created": out.SetsCreated, "terms_created": out.TermsCreated})
	})
	return
}
func (s *Service) importGroup(ctx context.Context, workspaceID string, pg PortableTermGroup, out *TaxonomyImportResult) (*ent.TermGroup, error) {
	q := s.Client.TermGroup.Query().Where(termgroup.WorkspaceIDEQ(workspaceID))
	if pg.System {
		return q.Where(termgroup.IsSystemEQ(true)).Only(ctx)
	}
	g, e := q.Where(termgroup.NameEQ(pg.Name)).Only(ctx)
	if e == nil && g.IsSystem {
		return nil, invalid("group " + pg.Name + " is the system group here")
	}
	if !ent.IsNotFound(e) {
		return g, e
	}
	out.GroupsCreated++
	return s.Client.TermGroup.Create().SetWorkspaceID(workspaceID).SetName(pg.Name).SetDescription(pg.Description).Save(ctx)
}
func (s *Service) importTerms(ctx context.Context, set *ent.TermSet, parent *ent.Term, nodes []PortableTerm, children map[string]*ent.Term, out *TaxonomyImportResult) error {
	var parentID *string
	if parent != nil {
		parentID = &parent.ID
	}
	for _, n := range nodes {
		key := termChildKey(parentID, strings.ToLower(n.Name))
		v := children[key]
		if v == nil {
			if parent != nil && parent.Deprecated {
				return invalid("cannot add " + n.Name + " below the deprecated term " + parent.Name)
			}
			var e error
			in := TermInput{Name: n.Name, Description: n.Description, Color: n.Color, SortOrder: n.SortOrder, Labels: n.Labels, Synonyms: n.Synonyms,
				Deprecated: n.Deprecated, AvailableAsKeyword: n.AvailableAsKeyword && !set.IsKeywords}
			if e = normalizeTerm(&in); e != nil {
				return invalid(n.Name + ": " + e.Error())
			}
			if v, e = s.insertTerm(ctx, set, parent, in); e != nil {
				return e
			}
			children[key] = v
			out.TermsCreated++
		}
		if e := s.importTerms(ctx, set, v, n.Children, children, out); e != nil {
			return e
		}
	}
	return nil
}

// validate checks a package's shape and limits before anything is written.
func (p TaxonomyPackage) validate() error {
	if len(p.Groups) > maxPackageGroups {
		return invalid("at most 100 groups")
	}
	sets, terms := 0, 0
	keys := map[string]bool{}
	var count func([]PortableTerm, int) error
	count = func(nodes []PortableTerm, depth int) error {
		if depth > MaxDepth && len(nodes) > 0 {
			return invalid("term hierarchy depth exceeded")
		}
		names := map[string]bool{}
		for _, n := range nodes {
			if terms++; terms > maxPackageTerms {
				return invalid("at most 20000 terms")
			}
			key := strings.ToLower(strings.TrimSpace(n.Name))
			if names[key] {
				return invalid("duplicate term name " + n.Name)
			}
			names[key] = true
			if e := count(n.Children, depth+1); e != nil {
				return e
			}
		}
		return nil
	}
	system := 0
	for _, g := range p.Groups {
		if g.System {
			system++
		} else {
			if e := validateName(g.Name); e != nil {
				return e
			}
			if e := validateDescription(g.Description); e != nil {
				return e
			}
		}
		for _, set := range g.Sets {
			if sets++; sets > maxPackageSets {
				return invalid("at most 500 term sets")
			}
			if !fieldKey.MatchString(set.Key) || keys[set.Key] {
				return invalid("invalid or duplicate term set key " + set.Key)
			}
			keys[set.Key] = true
			if e := validateName(set.Name); e != nil {
				return e
			}
			if e := validateDescription(set.Description); e != nil {
				return e
			}
			if e := count(set.Terms, 1); e != nil {
				return invalid("set " + set.Key + ": " + e.Error())
			}
		}
	}
	if system > 1 {
		return invalid("at most one system group")
	}
	return nil
}
