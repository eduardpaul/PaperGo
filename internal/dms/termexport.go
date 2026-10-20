package dms

import (
	"context"
	"slices"
	"strings"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
)

// A taxonomy package carries the organization's term groups, sets and terms by
// name, so the taxonomy can be set up again in another
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

func (s *Service) ExportTaxonomy(ctx context.Context, subject string) (TaxonomyPackage, error) {
	return read(ctx, s, func(t *Service) (TaxonomyPackage, error) {
		if e := t.requireTaxonomy(ctx, subject, "read"); e != nil {
			return TaxonomyPackage{}, e
		}
		store := t.DAL.Taxonomy()
		groups, e := store.Groups(ctx, taxonomy.GroupFilter{})
		if e != nil {
			return TaxonomyPackage{}, e
		}
		slices.SortFunc(groups, func(a, b taxonomy.Group) int {
			switch {
			case a.System && !b.System:
				return -1
			case b.System && !a.System:
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		out := TaxonomyPackage{Groups: []PortableTermGroup{}}
		for _, g := range groups {
			pg := PortableTermGroup{Name: g.Name, Description: g.Description, System: g.System, Sets: []PortableTermSet{}}
			sets, e := store.Sets(ctx, taxonomy.SetFilter{GroupID: g.ID})
			if e != nil {
				return TaxonomyPackage{}, e
			}
			slices.SortFunc(sets, func(a, b taxonomy.Set) int { return strings.Compare(a.Key, b.Key) })
			for _, set := range sets {
				terms, e := store.Terms(ctx, taxonomy.TermFilter{SetID: set.ID, Unmerged: true})
				if e != nil {
					return TaxonomyPackage{}, e
				}
				children := map[string][]taxonomy.Term{}
				for _, v := range terms {
					children[v.ParentID] = append(children[v.ParentID], v)
				}
				var tree func(parent string) []PortableTerm
				tree = func(parent string) []PortableTerm {
					var nodes []PortableTerm
					for _, v := range children[parent] {
						nodes = append(nodes, PortableTerm{Name: v.Name, Description: v.Description, Color: optional(v.Color), SortOrder: v.SortOrder, Labels: v.Labels,
							Synonyms: v.Synonyms, Deprecated: v.Deprecated, AvailableAsKeyword: v.AvailableAsKeyword, Children: tree(v.ID)})
					}
					return nodes
				}
				pg.Sets = append(pg.Sets, PortableTermSet{Key: set.Key, Name: set.Name, Description: set.Description, IsOpen: set.Open, Terms: tree("")})
			}
			out.Groups = append(out.Groups, pg)
		}
		return out, nil
	})
}

func (s *Service) ImportTaxonomy(ctx context.Context, subject string, pkg TaxonomyPackage) (out TaxonomyImportResult, err error) {
	if e := pkg.validate(); e != nil {
		return out, e
	}
	err = s.write(ctx, func(t *Service) error {
		out = TaxonomyImportResult{}
		if e := t.requireTaxonomy(ctx, subject, "manage"); e != nil {
			return e
		}
		for _, pg := range pkg.Groups {
			g, e := t.importGroup(ctx, pg, &out)
			if e != nil {
				return e
			}
			for _, ps := range pg.Sets {
				set, e := t.DAL.Taxonomy().SetByKey(ctx, ps.Key)
				switch {
				case dberr.IsNotFound(e):
					if g.System {
						return invalid("set " + ps.Key + ": the system group holds only the keywords set")
					}
					if set, e = t.DAL.Taxonomy().CreateSet(ctx, taxonomy.Set{GroupID: g.ID, Key: ps.Key, Name: ps.Name, Description: ps.Description, Open: ps.IsOpen}); e != nil {
						return taxonomyError(e)
					}
					out.SetsCreated++
				case e != nil:
					return e
				case set.GroupID != g.ID:
					return invalid("set " + ps.Key + " belongs to another group here")
				}
				existing, e := t.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{SetID: set.ID, Unmerged: true})
				if e != nil {
					return e
				}
				children := map[string]taxonomy.Term{}
				for _, v := range existing {
					children[termChildKey(v.ParentID, v.NormalizedName)] = v
				}
				if e = t.importTerms(ctx, set, nil, ps.Terms, children, &out); e != nil {
					return invalid("set " + ps.Key + ": " + e.Error())
				}
			}
		}
		return t.auditTaxonomy(ctx, subject, "taxonomy.import", map[string]any{"groups_created": out.GroupsCreated, "sets_created": out.SetsCreated, "terms_created": out.TermsCreated})
	})
	return
}
func (s *Service) importGroup(ctx context.Context, pg PortableTermGroup, out *TaxonomyImportResult) (taxonomy.Group, error) {
	store := s.DAL.Taxonomy()
	if pg.System {
		return store.Group(ctx, taxonomy.SystemGroupID)
	}
	groups, e := store.Groups(ctx, taxonomy.GroupFilter{Name: pg.Name, Limit: 1})
	if e != nil {
		return taxonomy.Group{}, e
	}
	if len(groups) > 0 {
		if groups[0].System {
			return taxonomy.Group{}, invalid("group " + pg.Name + " is the system group here")
		}
		return groups[0], nil
	}
	out.GroupsCreated++
	return store.CreateGroup(ctx, taxonomy.Group{Name: pg.Name, Description: pg.Description})
}
func (s *Service) importTerms(ctx context.Context, set taxonomy.Set, parent *taxonomy.Term, nodes []PortableTerm, children map[string]taxonomy.Term, out *TaxonomyImportResult) error {
	parentID := ""
	if parent != nil {
		parentID = parent.ID
	}
	for _, n := range nodes {
		key := termChildKey(parentID, strings.ToLower(n.Name))
		v, ok := children[key]
		if !ok {
			if parent != nil && parent.Deprecated {
				return invalid("cannot add " + n.Name + " below the deprecated term " + parent.Name)
			}
			var e error
			in := TermInput{Name: n.Name, ParentID: optional(parentID), Description: n.Description, Color: n.Color, SortOrder: n.SortOrder, Labels: n.Labels, Synonyms: n.Synonyms,
				Deprecated: n.Deprecated, AvailableAsKeyword: n.AvailableAsKeyword && !set.Keywords}
			if e = normalizeTerm(&in); e != nil {
				return invalid(n.Name + ": " + e.Error())
			}
			if v, e = s.insertTerm(ctx, set.ID, in); e != nil {
				return e
			}
			children[key] = v
			out.TermsCreated++
		}
		if e := s.importTerms(ctx, set, &v, n.Children, children, out); e != nil {
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
		if depth > taxonomy.MaxDepth && len(nodes) > 0 {
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
