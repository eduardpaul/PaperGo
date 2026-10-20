package dms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
	viewstore "papergo/internal/dal/views"
	"slices"
	"strings"
)

type SmartFolderTermRef struct {
	TermSetKey string   `json:"term_set_key"`
	Path       []string `json:"path"`
}
type PortableSmartFolderDefinition struct {
	Mode           string               `json:"mode"`
	CollectionKind string               `json:"collection_kind,omitempty"`
	Collections    []string             `json:"collections,omitempty"`
	Templates      []string             `json:"templates,omitempty"`
	ContentTypes   []string             `json:"content_types,omitempty"`
	Terms          []SmartFolderTermRef `json:"terms,omitempty"`
	TermMatch      string               `json:"term_match,omitempty"`
	Filter         *FilterExpr          `json:"filter,omitempty"`
	GroupBy        []SmartFolderGroupBy `json:"group_by,omitempty"`
	IncludeFolders bool                 `json:"include_folders,omitempty"`
}
type PortableSmartFolder struct {
	Name        string                        `json:"name"`
	Description string                        `json:"description,omitempty"`
	Definition  PortableSmartFolderDefinition `json:"definition"`
}
type SmartFolderPackage struct {
	Folders []PortableSmartFolder `json:"folders"`
}
type SmartFolderImportResult struct {
	Created          int                      `json:"created"`
	Updated          int                      `json:"updated"`
	WorkspaceVersion int                      `json:"workspace_version"`
	Data             []*viewstore.SmartFolder `json:"data"`
}

func (s *Service) smartTermPaths(ctx context.Context, ids []string) (map[string]SmartFolderTermRef, error) {
	out := map[string]SmartFolderTermRef{}
	if len(ids) == 0 {
		return out, nil
	}
	type node struct{ parent, name, set string }
	nodes := map[string]node{}
	setKeys := map[string]string{}
	for pending := ids; len(pending) > 0; {
		terms, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: pending})
		if err != nil {
			return nil, err
		}
		pending = nil
		for _, t := range terms {
			key, ok := setKeys[t.SetID]
			if !ok {
				set, err := s.DAL.Taxonomy().Set(ctx, t.SetID)
				if err != nil {
					return nil, err
				}
				key, setKeys[t.SetID] = set.Key, set.Key
			}
			nodes[t.ID] = node{t.ParentID, t.Name, key}
			if _, seen := nodes[t.ParentID]; t.ParentID != "" && !seen && !slices.Contains(pending, t.ParentID) {
				pending = append(pending, t.ParentID)
			}
		}
		if len(nodes) > 32*len(ids) {
			return nil, invalid("term hierarchy exceeds 32 levels")
		}
	}
	for _, id := range ids {
		n, ok := nodes[id]
		if !ok {
			return nil, ErrNotFound
		}
		ref := SmartFolderTermRef{TermSetKey: n.set, Path: []string{}}
		for current := id; current != ""; {
			n, ok := nodes[current]
			if !ok {
				return nil, ErrNotFound
			}
			ref.Path = append(ref.Path, n.name)
			current = n.parent
			if len(ref.Path) > 32 {
				return nil, invalid("term hierarchy exceeds 32 levels")
			}
		}
		slices.Reverse(ref.Path)
		out[id] = ref
	}
	return out, nil
}

func (s *Service) ExportSmartFolders(ctx context.Context, subject, workspace string) (SmartFolderPackage, int, error) {
	type result struct {
		pkg     SmartFolderPackage
		version int
	}
	out, err := read(ctx, s, func(t *Service) (result, error) {
		w, e := t.workspace(ctx, subject, workspace, "manage")
		if e != nil {
			return result{}, e
		}
		folders, e := t.DAL.Views().SmartFolders(ctx, viewstore.FolderFilter{WorkspaceID: workspace, Shared: true, Order: viewstore.FoldersByName, Limit: MaxSharedSmartFolders + 1})
		if e != nil {
			return result{}, e
		}
		if len(folders) > MaxSharedSmartFolders {
			return result{}, invalid("export exceeds 100 shared smart folders")
		}
		defs := make([]SmartFolderDefinition, len(folders))
		ids := []string{}
		for i, f := range folders {
			defs[i], e = smartDefinition(&f)
			if e != nil {
				return result{}, e
			}
			ids = append(ids, defs[i].Terms...)
		}
		refs, e := t.smartTermPaths(ctx, ids)
		if e != nil {
			return result{}, e
		}
		pkg := SmartFolderPackage{Folders: []PortableSmartFolder{}}
		for i, f := range folders {
			d := defs[i]
			terms := []SmartFolderTermRef{}
			for _, id := range d.Terms {
				terms = append(terms, refs[id])
			}
			pkg.Folders = append(pkg.Folders, PortableSmartFolder{f.Name, f.Description, PortableSmartFolderDefinition{d.Mode, d.CollectionKind, d.Collections, d.Templates, d.ContentTypes, terms, d.TermMatch, d.Filter, d.GroupBy, d.IncludeFolders}})
		}
		return result{pkg, w.Version}, nil
	})
	return out.pkg, out.version, err
}

func smartTermRefKey(ref SmartFolderTermRef) string {
	path := make([]string, len(ref.Path))
	for i, v := range ref.Path {
		path[i] = strings.ToLower(strings.TrimSpace(v))
	}
	raw, _ := json.Marshal(SmartFolderTermRef{ref.TermSetKey, path})
	return string(raw)
}

// Term names are unique among the active children of a parent. Fetch the
// active terms named like the requested leaves, then build their paths in one
// recursive query and keep the terms whose paths were asked for. Import cost
// does not multiply by the depth of each term path.
func (s *Service) resolveSmartPackageTerms(ctx context.Context, pkg SmartFolderPackage) (map[string]string, error) {
	type leaf struct {
		Set  string `json:"set"`
		Name string `json:"name"`
	}
	requested := map[string]SmartFolderTermRef{}
	leaves := []leaf{}
	seen := map[leaf]bool{}
	for _, f := range pkg.Folders {
		if len(f.Definition.Terms) > 20 {
			return nil, invalid("at most 20 terms per smart folder")
		}
		for _, ref := range f.Definition.Terms {
			if !fieldKey.MatchString(ref.TermSetKey) || len(ref.Path) < 1 || len(ref.Path) > 32 {
				return nil, invalid("term references require a term_set_key and 1..32 path names")
			}
			for _, name := range ref.Path {
				if err := validateName(name); err != nil {
					return nil, err
				}
			}
			requested[smartTermRefKey(ref)] = ref
			v := leaf{ref.TermSetKey, strings.ToLower(strings.TrimSpace(ref.Path[len(ref.Path)-1]))}
			if !seen[v] {
				seen[v] = true
				leaves = append(leaves, v)
			}
		}
	}
	out := map[string]string{}
	if len(leaves) == 0 {
		return out, nil
	}
	ids := []string{}
	for _, l := range leaves {
		set, err := s.DAL.Taxonomy().SetByKey(ctx, l.Set)
		if dberr.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		terms, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{SetID: set.ID, Unmerged: true, Match: l.Name, In: taxonomy.MatchName})
		if err != nil {
			return nil, err
		}
		for _, t := range terms {
			ids = append(ids, t.ID)
		}
	}
	paths, err := s.smartTermPaths(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, ref := range paths {
		out[smartTermRefKey(ref)] = id
	}
	for key, ref := range requested {
		if out[key] == "" {
			return nil, invalid("unknown target term path in " + ref.TermSetKey)
		}
	}
	return out, nil
}

// Import merges shared definitions by exact name. It checks the workspace ETag
// before resolving taxonomy and rolls back the entire package on any error.
func (s *Service) ImportSmartFolders(ctx context.Context, subject, workspace string, version int, pkg SmartFolderPackage) (out SmartFolderImportResult, err error) {
	if len(pkg.Folders) > MaxSharedSmartFolders {
		return out, invalid("at most 100 smart folders per package")
	}
	err = s.write(ctx, func(t *Service) error {
		w, e := t.workspace(ctx, subject, workspace, "manage")
		if e != nil {
			return e
		}
		if w.Version != version {
			return ErrPreconditionFailed
		}
		existing, e := t.DAL.Views().SmartFolders(ctx, viewstore.FolderFilter{WorkspaceID: workspace, Shared: true})
		if e != nil {
			return e
		}
		byName := map[string]*viewstore.SmartFolder{}
		for i := range existing {
			byName[existing[i].Name] = &existing[i]
		}
		seen := map[string]bool{}
		refs, e := t.resolveSmartPackageTerms(ctx, pkg)
		if e != nil {
			return e
		}
		out = SmartFolderImportResult{Data: []*viewstore.SmartFolder{}}
		for _, portable := range pkg.Folders {
			if seen[portable.Name] {
				return invalid("package folder names must be unique")
			}
			seen[portable.Name] = true
			p := portable.Definition
			if len(p.Terms) > 20 {
				return invalid("at most 20 terms per smart folder")
			}
			ids := []string{}
			for _, ref := range p.Terms {
				ids = append(ids, refs[smartTermRefKey(ref)])
			}
			d := SmartFolderDefinition{p.Mode, p.CollectionKind, p.Collections, p.Templates, p.ContentTypes, ids, p.TermMatch, p.Filter, p.GroupBy, p.IncludeFolders}
			in := SmartFolderInput{Name: portable.Name, Description: portable.Description, WorkspaceID: &workspace, Definition: d}
			if e = t.validateSmartFolder(ctx, subject, in); e != nil {
				return e
			}
			old := byName[in.Name]
			var f *viewstore.SmartFolder
			if old == nil {
				f, e = t.createSmartFolder(ctx, subject, in)
				if e != nil {
					return e
				}
				out.Created++
			} else {
				raw, e := json.Marshal(d)
				if e != nil {
					return e
				}
				if old.Description == in.Description && sameJSON(json.RawMessage(old.Definition), json.RawMessage(raw)) {
					f = old
				} else {
					f, e = t.updateSmartFolder(ctx, subject, old, in)
					if e != nil {
						return e
					}
					out.Updated++
				}
			}
			out.Data = append(out.Data, f)
		}
		current, e := t.DAL.Resources().Resource(ctx, w.ID)
		if e != nil {
			return e
		}
		out.WorkspaceVersion = current.Version
		return nil
	})
	if err != nil {
		return SmartFolderImportResult{}, err
	}
	return
}
