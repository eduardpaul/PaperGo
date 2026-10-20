package dms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	viewstore "papergo/internal/dal/views"
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

func (s *Service) smartActionCandidate(ctx context.Context, subject string, f *viewstore.SmartFolder, d SmartFolderDefinition, collection string) (smartCandidate, error) {
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

func (s *Service) smartMember(ctx context.Context, subject string, f *viewstore.SmartFolder, d SmartFolderDefinition, path []*string, item string) (bool, error) {
	q, _, err := s.compileSmartFolder(ctx, subject, f, d, SmartFolderQueryRequest{Surface: "head", Path: path}, false)
	if err != nil {
		return false, err
	}
	return s.DAL.Content().Contains(ctx, q.q, item)
}

func (s *Service) ClassifySmartFolder(ctx context.Context, subject, id string, in SmartFolderDrop) (out *resources.Resource, created bool, err error) {
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
			return ErrPreconditionFailed
		}
		d, e := smartDefinition(f)
		if e != nil {
			return e
		}
		if d.Mode != "classification" {
			return invalid("saved searches cannot classify items; use a classification folder")
		}
		if e = smartPath(d, in.Path); e != nil {
			return e
		}
		c, e := t.smartActionCandidate(ctx, subject, f, d, in.CollectionID)
		if e != nil {
			return e
		}
		var item *resources.Resource
		typID := ""
		name := ""
		keywords := []string{}
		values := map[string]any{}
		if in.Create != nil {
			name = in.Create.Name
			keywords = append(keywords, in.Create.Keywords...)
			typID = in.Create.ContentTypeID
			for key, v := range in.Create.Values {
				values[key] = v
			}
		} else {
			item, e = t.authorize(ctx, subject, in.ItemID, "write")
			if e != nil {
				return e
			}
			if item.Kind != refs.KindItem || item.ContainerID == nil || *item.ContainerID != c.collection.ID {
				return invalid("item must belong to the selected collection")
			}
			if item.Version != in.Version {
				return ErrPreconditionFailed
			}
			if e = t.overlayHead(ctx, item); e != nil {
				return e
			}
			name = item.Name
			keywords = append(keywords, item.Keywords...)
			typID = *item.ContentTypeID
		}
		typ, e := t.contentType(ctx, c.collection.ID, typID)
		if e != nil {
			return e
		}
		if !slices.ContainsFunc(c.types, func(v *catalog.ContentType) bool { return v.ID == typ.ID }) {
			return invalid("content type is outside the smart folder selectors")
		}
		defs := selectTypeDefinitions(c.defs, typ)
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
				if !slices.ContainsFunc(list, func(x any) bool { return sameJSON(x, v) }) {
					list = append(list, v)
				}
				values[key] = list
			} else {
				if old, ok := assigned[key]; ok && !sameJSON(old, v) {
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
		filter, e = t.keywordFilters(ctx, filter)
		if e != nil {
			return e
		}
		if filter != nil {
			compiler := queryCompiler{defs: definitionMap(c.defs)}
			if _, e = compiler.filter(filter, 0); e != nil {
				return e
			}
		}
		filter, e = t.keywordFilters(ctx, filter)
		if e != nil {
			return e
		}
		for _, cond := range smartEqualities(filter) {
			value, e := decodeValue(cond.Value)
			if e != nil {
				return e
			}
			switch cond.Field {
			case "$name":
				name = value.(string)
			case "$keywords":
				keyword, ok := value.(string)
				if !ok {
					return invalid("keyword must be text")
				}
				if !slices.Contains(keywords, keyword) {
					keywords = append(keywords, keyword)
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
			var target *catalog.FieldDefinition
			for _, fd := range defs {
				if smartTermField(fd, root) {
					target = fd
					break
				}
			}
			if target == nil && smartTermField(&catalog.FieldDefinition{Type: "keywords", Indexed: true, IndexStatus: "ready"}, root) {
				if !slices.Contains(keywords, root.ID) {
					keywords = append(keywords, root.ID)
				}
				continue
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
			if fd.Type == catalog.FieldInteger || fd.Type == catalog.FieldNumber {
				v = json.Number(*value)
			}
			if fd.Type == catalog.FieldBoolean {
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
			if p.ID != c.collection.ID && (p.Kind != refs.KindFolder || p.ContainerID == nil || *p.ContainerID != c.collection.ID) {
				return invalid("parent_id is outside the selected collection")
			}
			out, e = t.create(ctx, subject, parent, CreateResource{Kind: "item", Name: name, Keywords: keywords, Values: values, ContentTypeID: typ.ID}, nil)
			created = true
		} else {
			out, e = t.update(ctx, subject, item.ID, item.Version, UpdateResource{Name: &name, Keywords: &keywords, Values: &values})
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

// Fetch only the IDs on this item's term and keywords fields that are in the
// selected terms' subtrees or merged into them. Large taxonomies do not turn
// into large SQL parameter lists or response payloads.
func (s *Service) smartRemoveTerms(ctx context.Context, roots []string, values map[string]any, defs []*catalog.FieldDefinition) error {
	if len(roots) == 0 {
		return nil
	}
	ids := []any{}
	for _, fd := range defs {
		if fd.Type == catalog.FieldTerm || fd.Type == catalog.FieldKeywords {
			if fd.Options.Multiple {
				ids = append(ids, smartArray(values[fd.Key])...)
			} else if v := values[fd.Key]; v != nil {
				ids = append(ids, v)
			}
		}
	}
	family := map[string]bool{}
	for _, root := range roots {
		canonical, err := s.DAL.Taxonomy().Canonical(ctx, root)
		if dberr.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		members, err := s.DAL.Taxonomy().Family(ctx, canonical.ID)
		if err != nil {
			return err
		}
		for _, id := range members {
			family[id] = true
		}
	}
	removed := map[string]bool{}
	for _, v := range ids {
		if id, ok := v.(string); ok && family[id] {
			removed[id] = true
		}
	}
	for _, fd := range defs {
		if fd.Type != catalog.FieldTerm && fd.Type != catalog.FieldKeywords {
			continue
		}
		if values[fd.Key] == nil {
			continue
		}
		if fd.Options.Multiple {
			values[fd.Key] = slices.DeleteFunc(smartArray(values[fd.Key]), func(v any) bool { id, ok := v.(string); return ok && removed[id] })
		} else if id, ok := values[fd.Key].(string); ok && removed[id] {
			values[fd.Key] = nil
		}
	}
	return nil
}

func (s *Service) UnclassifySmartFolder(ctx context.Context, subject, id, itemID string, folderVersion, itemVersion int) (out *resources.Resource, err error) {
	if folderVersion < 1 || itemVersion < 1 {
		return nil, invalid("current folder and item versions are required")
	}
	err = s.write(ctx, func(t *Service) error {
		f, e := t.smartFolder(ctx, subject, id, false)
		if e != nil {
			return e
		}
		if f.Version != folderVersion {
			return ErrPreconditionFailed
		}
		d, e := smartDefinition(f)
		if e != nil {
			return e
		}
		if d.Mode != "classification" {
			return invalid("saved searches cannot remove classifications")
		}
		item, e := t.authorize(ctx, subject, itemID, "write")
		if e != nil {
			return e
		}
		if item.Kind != refs.KindItem || item.ContainerID == nil {
			return invalid("only items can be unclassified")
		}
		if item.Version != itemVersion {
			return ErrPreconditionFailed
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
		defs := selectTypeDefinitions(c.defs, typ)
		known := definitionMap(defs)
		values := map[string]any{}
		for key, v := range item.Values {
			if known[key] != nil {
				values[key] = v
			}
		}
		keywords := append([]string{}, item.Keywords...)
		filter, e := resolveFilter(d.Filter, subject, time.Now().UTC(), definitionMap(c.defs))
		if e != nil {
			return e
		}
		filter, e = t.keywordFilters(ctx, filter)
		if e != nil {
			return e
		}
		for _, cond := range smartEqualities(filter) {
			v, e := decodeValue(cond.Value)
			if e != nil {
				return e
			}
			if cond.Field == "$keywords" {
				keywords = slices.DeleteFunc(keywords, func(x string) bool { return sameJSON(x, v) })
				if id, ok := v.(string); ok {
					if err := t.smartRemoveTerms(ctx, []string{id}, values, defs); err != nil {
						return err
					}
				}
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
				values[fd.Key] = slices.DeleteFunc(smartArray(values[fd.Key]), func(x any) bool { return sameJSON(x, v) })
			} else if sameJSON(values[fd.Key], v) {
				values[fd.Key] = nil
			}
		}
		if e = t.smartRemoveTerms(ctx, d.Terms, values, defs); e != nil {
			return e
		}
		retained := []string{}
		for _, id := range keywords {
			match, err := t.MatchTerms(ctx, d.Terms, []string{id})
			if err != nil {
				return err
			}
			if len(match) == 0 {
				retained = append(retained, id)
			}
		}
		keywords = retained
		out, e = t.update(ctx, subject, item.ID, item.Version, UpdateResource{Keywords: &keywords, Values: &values})
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
