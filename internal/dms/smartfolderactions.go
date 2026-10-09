package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/resource"
	"slices"
	"sort"
	"strings"
	"time"
)

type SmartFolderDrop struct {
	FolderVersion int         `json:"folder_version"`
	CollectionID  string      `json:"collection_id"`
	ItemID        string      `json:"item_id,omitempty"`
	Version       int         `json:"version,omitempty"`
	Create        *BulkCreate `json:"create,omitempty"`
	ParentID      string      `json:"parent_id,omitempty"`
	Path          []*string   `json:"path,omitempty"`
}

type SmartFolderUnclassify struct {
	FolderVersion int `json:"folder_version"`
}

func smartEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Only positive equality conditions joined by AND imply assignments. OR/NOT
// subtrees cannot identify a unique classification and must already match.
func smartEqualities(f *FilterExpr) []FilterExpr {
	if f == nil || f.Or != nil || f.Not != nil {
		return nil
	}
	if f.And != nil {
		out := []FilterExpr{}
		for i := range f.And {
			out = append(out, smartEqualities(&f.And[i])...)
		}
		return out
	}
	if f.Op == "" || f.Op == "eq" {
		return []FilterExpr{*f}
	}
	return nil
}

func smartArray(v any) []any {
	switch x := v.(type) {
	case []any:
		return append([]any{}, x...)
	case []string:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = v
		}
		return out
	}
	return []any{}
}

func (s *Service) smartActionCandidate(ctx context.Context, subject string, f *ent.SmartFolder, d SmartFolderDefinition, collection string) (smartCandidate, error) {
	candidates, err := s.smartCandidates(ctx, subject, f, d)
	if err != nil {
		return smartCandidate{}, err
	}
	for _, c := range candidates {
		if c.collection.ID == collection {
			return c, nil
		}
	}
	return smartCandidate{}, invalid("collection is outside the smart folder selectors or is not readable")
}

func (s *Service) smartMember(ctx context.Context, subject string, f *ent.SmartFolder, d SmartFolderDefinition, path []*string, item string) (bool, error) {
	q, _, err := s.compileSmartFolder(ctx, subject, f, d, SmartFolderQueryRequest{Surface: "head", Path: path}, false)
	if err != nil {
		return false, err
	}
	args := append(append([]any{}, q.args...), item)
	rows, err := s.Client.QueryContext(ctx, "WITH eligible AS ("+q.sql+") SELECT 1 FROM eligible WHERE id=? LIMIT 1", args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	match := rows.Next()
	return match, rows.Err()
}

func (s *Service) ClassifySmartFolder(ctx context.Context, subject, id string, in SmartFolderDrop) (out *ent.Resource, created bool, err error) {
	if in.FolderVersion < 1 || in.CollectionID == "" {
		return nil, false, invalid("folder_version and collection_id are required")
	}
	if (in.ItemID == "") == (in.Create == nil) || (in.ItemID != "" && (in.Version < 1 || in.ParentID != "")) || (in.Create != nil && in.Version != 0) {
		return nil, false, invalid("supply either item_id and its current version, or create with optional parent_id")
	}
	err = s.write(ctx, func(t *Service) error {
		f, e := t.smartFolder(ctx, subject, id, false)
		if e != nil {
			return e
		}
		if f.Version != in.FolderVersion {
			return ErrConflict
		}
		d, e := smartDefinition(f)
		if e != nil {
			return e
		}
		if e = smartPath(d, in.Path); e != nil {
			return e
		}
		c, e := t.smartActionCandidate(ctx, subject, f, d, in.CollectionID)
		if e != nil {
			return e
		}
		var item *ent.Resource
		typID := ""
		name := ""
		tags := []string{}
		values := map[string]any{}
		if in.Create != nil {
			name = in.Create.Name
			tags = append(tags, in.Create.Tags...)
			typID = in.Create.ContentTypeID
			for key, v := range in.Create.Values {
				values[key] = v
			}
		} else {
			item, e = t.authorize(ctx, subject, in.ItemID, "write")
			if e != nil {
				return e
			}
			if item.Kind != resource.KindItem || item.ContainerID == nil || *item.ContainerID != c.collection.ID {
				return invalid("item must belong to the selected collection")
			}
			if item.Version != in.Version {
				return ErrConflict
			}
			if e = t.overlayHead(ctx, item); e != nil {
				return e
			}
			name = item.Name
			tags = append(tags, item.Tags...)
			typID = *item.ContentTypeID
		}
		typ, e := t.contentType(ctx, c.collection.ID, typID)
		if e != nil {
			return e
		}
		if !slices.ContainsFunc(c.types, func(v *ent.ContentType) bool { return v.ID == typ.ID }) {
			return invalid("content type is outside the smart folder selectors")
		}
		defs := selectDefinitions(c.defs, typ.FieldKeys)
		known := definitionMap(defs)
		if item != nil {
			for key, v := range item.Values {
				if known[key] != nil {
					values[key] = v
				}
			}
		}
		// Detect contradictory implied scalar values rather than silently choosing
		// whichever predicate or navigation level was processed last.
		assigned := map[string]any{}
		assign := func(key string, v any) error {
			fd := known[key]
			if fd == nil {
				return invalid("classification field is not assigned to this content type: " + key)
			}
			if v != nil {
				var e error
				v, e = scalarValue(fd, v)
				if e != nil {
					return e
				}
			}
			if fd.Options.Multiple && v != nil {
				list := smartArray(values[key])
				if !slices.ContainsFunc(list, func(x any) bool { return smartEqual(x, v) }) {
					list = append(list, v)
				}
				values[key] = list
			} else {
				if old, ok := assigned[key]; ok && !smartEqual(old, v) {
					return invalid("conflicting classification values for " + key)
				}
				assigned[key] = v
				values[key] = v
			}
			return nil
		}
		filter, e := resolveFilter(d.Filter, subject, time.Now().UTC(), definitionMap(c.defs))
		if e != nil {
			return e
		}
		if filter != nil {
			compiler := queryCompiler{defs: definitionMap(c.defs)}
			if _, _, e = compiler.filter(filter, 0); e != nil {
				return e
			}
		}
		for _, cond := range smartEqualities(filter) {
			value, e := decodeValue(cond.Value)
			if e != nil {
				return e
			}
			switch cond.Field {
			case "$name":
				name = value.(string)
			case "$tags":
				tag, ok := value.(string)
				if !ok {
					return invalid("tag must be text")
				}
				if !slices.Contains(tags, tag) {
					tags = append(tags, tag)
				}
			default:
				if strings.HasPrefix(cond.Field, "$") {
					continue
				}
				if e = assign(cond.Field, value); e != nil {
					return e
				}
			}
		}
		roots, e := t.smartTerms(ctx, subject, d.Terms)
		if e != nil {
			return e
		}
		sort.Slice(defs, func(i, j int) bool { return defs[i].Key < defs[j].Key })
		for _, root := range roots {
			var target *ent.FieldDefinition
			for _, fd := range defs {
				if fd.Type == "term" && queryable(fd) && fd.Options.TermSetID == root.TermSetID {
					target = fd
					break
				}
			}
			if target == nil {
				if d.TermMatch == "any" {
					continue
				}
				return invalid("content type has no indexed field for a selected term set")
			}
			if d.TermMatch == "any" && !target.Options.Multiple {
				if _, ok := assigned[target.Key]; ok {
					continue
				}
			}
			if e = assign(target.Key, root.ID); e != nil {
				return e
			}
		}
		for i, value := range in.Path {
			level := d.GroupBy[i]
			if level.By != "" {
				continue
			}
			if value == nil {
				if strings.HasPrefix(level.Field, "$") {
					return invalid("system metadata cannot be missing")
				}
				if e = assign(level.Field, nil); e != nil {
					return e
				}
				continue
			}
			if level.Field == "$name" {
				name = *value
				continue
			}
			if strings.HasPrefix(level.Field, "$") {
				continue
			}
			fd := known[level.Field]
			if fd == nil {
				return invalid("navigation field is absent from this content type")
			}
			var v any = *value
			if fd.Type == "integer" || fd.Type == "number" {
				v = json.Number(*value)
			}
			if fd.Type == "boolean" {
				if *value != "true" && *value != "false" {
					return invalid("boolean paths use true or false")
				}
				v = *value == "true"
			}
			if e = assign(level.Field, v); e != nil {
				return e
			}
		}
		if item == nil {
			parent := in.ParentID
			if parent == "" {
				parent = c.collection.ID
			}
			p, parentErr := t.authorize(ctx, subject, parent, "write")
			if parentErr != nil {
				return parentErr
			}
			if p.ID != c.collection.ID && (p.Kind != resource.KindFolder || p.ContainerID == nil || *p.ContainerID != c.collection.ID) {
				return invalid("parent_id is outside the selected collection")
			}
			out, e = t.create(ctx, subject, parent, CreateResource{Kind: "item", Name: name, Tags: tags, Values: values, ContentTypeID: typ.ID}, nil)
			created = true
		} else {
			out, e = t.update(ctx, subject, item.ID, item.Version, UpdateResource{Name: &name, Tags: &tags, Values: &values})
		}
		if e != nil {
			return e
		}
		match, e := t.smartMember(ctx, subject, f, d, in.Path, out.ID)
		if e != nil {
			return e
		}
		if !match {
			return invalid("result does not match the smart folder path; supply values for predicates that cannot be inferred")
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return
}

// Fetch only descendant IDs present on this item's active term fields. Large
// taxonomies do not turn into large SQL parameter lists or response payloads.
func (s *Service) smartRemoveTerms(ctx context.Context, roots []string, values map[string]any, defs []*ent.FieldDefinition) error {
	if len(roots) == 0 {
		return nil
	}
	ids := []any{}
	for _, fd := range defs {
		if fd.Type == "term" {
			if fd.Options.Multiple {
				ids = append(ids, smartArray(values[fd.Key])...)
			} else if v := values[fd.Key]; v != nil {
				ids = append(ids, v)
			}
		}
	}
	rootJSON, _ := json.Marshal(roots)
	idsJSON, _ := json.Marshal(ids)
	rows, err := s.Client.QueryContext(ctx, `WITH RECURSIVE descendants(id) AS (SELECT value FROM json_each(?) UNION SELECT t.id FROM terms t JOIN descendants d ON t.parent_id=d.id) SELECT id FROM descendants WHERE id IN (SELECT value FROM json_each(?))`, string(rootJSON), string(idsJSON))
	if err != nil {
		return err
	}
	removed := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		removed[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, fd := range defs {
		if fd.Type != "term" {
			continue
		}
		if values[fd.Key] == nil {
			continue
		}
		if fd.Options.Multiple {
			values[fd.Key] = slices.DeleteFunc(smartArray(values[fd.Key]), func(v any) bool { id, ok := v.(string); return ok && removed[id] })
		} else if id, ok := values[fd.Key].(string); ok && removed[id] {
			delete(values, fd.Key)
		}
	}
	return nil
}

func (s *Service) UnclassifySmartFolder(ctx context.Context, subject, id, itemID string, folderVersion, itemVersion int) (out *ent.Resource, err error) {
	if folderVersion < 1 || itemVersion < 1 {
		return nil, invalid("current folder and item versions are required")
	}
	err = s.write(ctx, func(t *Service) error {
		f, e := t.smartFolder(ctx, subject, id, false)
		if e != nil {
			return e
		}
		if f.Version != folderVersion {
			return ErrConflict
		}
		d, e := smartDefinition(f)
		if e != nil {
			return e
		}
		item, e := t.authorize(ctx, subject, itemID, "write")
		if e != nil {
			return e
		}
		if item.Kind != resource.KindItem || item.ContainerID == nil {
			return invalid("only items can be unclassified")
		}
		if item.Version != itemVersion {
			return ErrConflict
		}
		c, e := t.smartActionCandidate(ctx, subject, f, d, *item.ContainerID)
		if e != nil {
			return e
		}
		match, e := t.smartMember(ctx, subject, f, d, nil, itemID)
		if e != nil {
			return e
		}
		if !match {
			return invalid("item is not a member of this smart folder")
		}
		if e = t.overlayHead(ctx, item); e != nil {
			return e
		}
		typ, e := t.contentType(ctx, c.collection.ID, *item.ContentTypeID)
		if e != nil {
			return e
		}
		defs := selectDefinitions(c.defs, typ.FieldKeys)
		known := definitionMap(defs)
		values := map[string]any{}
		for key, v := range item.Values {
			if known[key] != nil {
				values[key] = v
			}
		}
		tags := append([]string{}, item.Tags...)
		filter, e := resolveFilter(d.Filter, subject, time.Now().UTC(), definitionMap(c.defs))
		if e != nil {
			return e
		}
		for _, cond := range smartEqualities(filter) {
			v, e := decodeValue(cond.Value)
			if e != nil {
				return e
			}
			if cond.Field == "$tags" {
				tags = slices.DeleteFunc(tags, func(x string) bool { return smartEqual(x, v) })
				continue
			}
			fd := known[cond.Field]
			if fd == nil {
				continue
			}
			v, e = scalarValue(fd, v)
			if e != nil {
				return e
			}
			if fd.Options.Multiple {
				values[fd.Key] = slices.DeleteFunc(smartArray(values[fd.Key]), func(x any) bool { return smartEqual(x, v) })
			} else if smartEqual(values[fd.Key], v) {
				delete(values, fd.Key)
			}
		}
		if e = t.smartRemoveTerms(ctx, d.Terms, values, defs); e != nil {
			return e
		}
		out, e = t.update(ctx, subject, item.ID, item.Version, UpdateResource{Tags: &tags, Values: &values})
		if e != nil {
			return e
		}
		match, e = t.smartMember(ctx, subject, f, d, nil, itemID)
		if e != nil {
			return e
		}
		if match {
			return invalid("item still matches; this definition cannot be removed by clearing term and equality classifications")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return
}
