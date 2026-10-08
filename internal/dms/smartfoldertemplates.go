package dms

import (
	"context"
	"database/sql"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/smartfolder"
	"slices"
	"strings"
)

type SmartFolderTermRef struct {
	TermSetKey string   `json:"term_set_key"`
	Path       []string `json:"path"`
}
type PortableSmartFolderDefinition struct {
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
	Created          int                `json:"created"`
	Updated          int                `json:"updated"`
	WorkspaceVersion int                `json:"workspace_version"`
	Data             []*ent.SmartFolder `json:"data"`
}

func (s *Service) smartTermPaths(ctx context.Context, ids []string) (map[string]SmartFolderTermRef, error) {
	out := map[string]SmartFolderTermRef{}
	if len(ids) == 0 {
		return out, nil
	}
	raw, _ := json.Marshal(ids)
	rows, err := s.Client.QueryContext(ctx, `WITH RECURSIVE ancestors(id) AS (SELECT value FROM json_each(?) UNION SELECT t.parent_id FROM terms t JOIN ancestors a ON t.id=a.id WHERE t.parent_id IS NOT NULL) SELECT t.id,t.parent_id,t.name,ts.key FROM ancestors a JOIN terms t ON t.id=a.id JOIN term_sets ts ON ts.id=t.term_set_id`, string(raw))
	if err != nil {
		return nil, err
	}
	type node struct{ parent, name, set string }
	nodes := map[string]node{}
	for rows.Next() {
		var id, name, set string
		var parent sql.NullString
		if err = rows.Scan(&id, &parent, &name, &set); err != nil {
			rows.Close()
			return nil, err
		}
		nodes[id] = node{parent.String, name, set}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
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
		folders, e := t.Client.SmartFolder.Query().Where(smartfolder.WorkspaceIDEQ(workspace), smartfolder.OwnerIDIsNil()).Order(ent.Asc(smartfolder.FieldName)).Limit(MaxSharedSmartFolders + 1).All(ctx)
		if e != nil {
			return result{}, e
		}
		if len(folders) > MaxSharedSmartFolders {
			return result{}, invalid("export exceeds 100 shared smart folders")
		}
		defs := make([]SmartFolderDefinition, len(folders))
		ids := []string{}
		for i, f := range folders {
			defs[i], e = smartDefinition(f)
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
			pkg.Folders = append(pkg.Folders, PortableSmartFolder{f.Name, f.Description, PortableSmartFolderDefinition{d.Collections, d.Templates, d.ContentTypes, terms, d.TermMatch, d.Filter, d.GroupBy, d.IncludeFolders}})
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

// Term names are unique inside a term set. Fetch requested leaves by their
// indexed (set,name) keys, then fetch all required ancestors in one recursive
// query. Import cost does not multiply by the depth of each term path.
func (s *Service) resolveSmartPackageTerms(ctx context.Context, workspace string, pkg SmartFolderPackage) (map[string]string, error) {
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
	raw, _ := json.Marshal(leaves)
	rows, err := s.Client.QueryContext(ctx, `SELECT t.id FROM json_each(?) j JOIN term_sets ts ON ts.workspace_id=? AND ts.key=json_extract(j.value,'$.set') JOIN terms t ON t.term_set_id=ts.id AND t.normalized_name=json_extract(j.value,'$.name')`, string(raw), workspace)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
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
			return ErrConflict
		}
		existing, e := t.Client.SmartFolder.Query().Where(smartfolder.WorkspaceIDEQ(workspace), smartfolder.OwnerIDIsNil()).All(ctx)
		if e != nil {
			return e
		}
		byName := map[string]*ent.SmartFolder{}
		for _, f := range existing {
			byName[f.Name] = f
		}
		seen := map[string]bool{}
		refs, e := t.resolveSmartPackageTerms(ctx, workspace, pkg)
		if e != nil {
			return e
		}
		out = SmartFolderImportResult{Data: []*ent.SmartFolder{}}
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
			d := SmartFolderDefinition{p.Collections, p.Templates, p.ContentTypes, ids, p.TermMatch, p.Filter, p.GroupBy, p.IncludeFolders}
			in := SmartFolderInput{Name: portable.Name, Description: portable.Description, WorkspaceID: &workspace, Definition: d}
			if e = t.validateSmartFolder(ctx, subject, in); e != nil {
				return e
			}
			old := byName[in.Name]
			var f *ent.SmartFolder
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
				if old.Description == in.Description && smartEqual(json.RawMessage(old.Definition), json.RawMessage(raw)) {
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
		current, e := t.Client.Resource.Get(ctx, w.ID)
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
