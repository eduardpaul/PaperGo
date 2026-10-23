package dms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/taxonomy"
	viewstore "papergo/internal/dal/views"
	"strconv"
	"strings"
	"time"
)

type smartCandidate struct {
	collection *resources.Resource
	defs       []*catalog.FieldDefinition
	types      []*catalog.ContentType
}

type SmartFolderEntry struct {
	CollectionKind string              `json:"collection_kind"`
	WorkspaceID    string              `json:"workspace_id"`
	CollectionID   string              `json:"collection_id"`
	CollectionName string              `json:"collection_name"`
	Item           *resources.Resource `json:"item"`
}
type SmartFolderResult struct {
	TotalCapped bool               `json:"total_capped,omitempty"`
	Data        []SmartFolderEntry `json:"value"`
	NextCursor  string             `json:"-"`
	Total       *int               `json:"total,omitempty"`
}
type SmartFolderGroup struct {
	Value any    `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}
type SmartFolderGroupsResult struct {
	Data       []SmartFolderGroup `json:"value"`
	NextCursor string             `json:"-"`
	Field      string             `json:"field,omitempty"`
	By         string             `json:"by,omitempty"`
}

func (s *Service) smartCandidates(ctx context.Context, subject string, f *viewstore.SmartFolder, d SmartFolderDefinition) ([]smartCandidate, error) {
	scope := content.Collections{Reader: principal(ctx, subject), Kind: refs.ResourceKind(d.CollectionKind), Names: d.Collections, Templates: d.Templates,
		ContentTypes: d.ContentTypes, Limit: MaxSmartCollections + 1}
	if f.WorkspaceID != nil {
		scope.WorkspaceID = *f.WorkspaceID
	}
	ids, err := s.DAL.Content().CollectionIDs(ctx, scope)
	if err != nil {
		return nil, err
	}
	collections, err := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids})
	if err != nil {
		return nil, err
	}
	if len(collections) > MaxSmartCollections {
		return nil, invalid("smart folders cover at most 100 readable collections; narrow the selectors")
	}
	if len(collections) == 0 {
		return []smartCandidate{}, nil
	}
	fields := map[string][]*catalog.FieldDefinition{}
	byCollection := map[string][]*catalog.ContentType{}
	for _, c := range collections {
		defs, e := s.collectionFields(ctx, c.ID)
		if e != nil {
			return nil, e
		}
		fields[c.ID] = defs
		types, e := s.collectionTypes(ctx, c.ID)
		if e != nil {
			return nil, e
		}
		for _, typ := range types {
			if len(d.ContentTypes) > 0 {
				match := false
				for _, key := range d.ContentTypes {
					match = match || strings.EqualFold(key, typ.Key) || strings.EqualFold(key, typ.Name)
				}
				if !match {
					continue
				}
			}
			byCollection[c.ID] = append(byCollection[c.ID], typ)
		}
	}
	out := []smartCandidate{}
	for _, c := range collections {
		if len(byCollection[c.ID]) == 0 {
			continue
		}
		out = append(out, smartCandidate{c, fields[c.ID], byCollection[c.ID]})
	}
	return out, nil
}

// smartTerms loads a definition's terms; a merged term stands for the term
// it was merged into.
func (s *Service) smartTerms(ctx context.Context, subject string, ids []string) ([]taxonomy.Term, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	store := s.DAL.Taxonomy()
	terms, err := store.Terms(ctx, taxonomy.TermFilter{IDs: ids, Order: taxonomy.OrderID})
	if err != nil {
		return nil, err
	}
	if len(terms) != len(ids) {
		return nil, ErrNotFound
	}
	for i, t := range terms {
		if t.Merged() {
			if terms[i], err = store.Term(ctx, t.MergedIntoID); err != nil {
				return nil, err
			}
		}
	}
	if err := s.requireTaxonomy(ctx, subject, "read"); err != nil {
		return nil, err
	}
	return terms, nil
}

func smartPath(d SmartFolderDefinition, path []*string) error {
	if len(path) > len(d.GroupBy) {
		return invalid("path is deeper than group_by")
	}
	for _, value := range path {
		if value != nil && len(*value) > 65536 {
			return invalid("metadata path value exceeds 65536 bytes")
		}
	}
	return nil
}

// smartGroup checks a grouping level. A field the collection lacks groups
// as missing; fd is the definition values convert with, nil for year and
// month.
func smartGroup(compiler *queryCompiler, g SmartFolderGroupBy) (group content.GroupBy, fd *catalog.FieldDefinition, missing bool, err error) {
	fd = systemField(g.Field)
	if fd == nil {
		fd = compiler.defs[g.Field]
	}
	if fd == nil {
		return content.GroupBy{Field: content.Field{Key: g.Field}}, nil, true, nil
	}
	field, err := compiler.column(g.Field)
	if err != nil {
		return content.GroupBy{}, nil, false, err
	}
	if g.By != "" {
		if fd.Type != catalog.FieldDate && fd.Type != catalog.FieldDatetime {
			return content.GroupBy{}, nil, false, invalid("year/month navigation requires a date field")
		}
		fd = nil
	}
	return content.GroupBy{Field: field, By: g.By}, fd, false, nil
}

func smartGroupValue(g SmartFolderGroupBy, d *catalog.FieldDefinition, value string) (any, error) {
	if g.By != "" {
		layout := "2006"
		if g.By == "month" {
			layout = "2006-01"
		}
		date, err := time.Parse(layout, value)
		if err != nil || date.Format(layout) != value {
			return nil, invalid("invalid year/month path value")
		}
		return value, nil
	}
	if d == nil {
		return value, nil
	}
	var v any = value
	switch d.Type {
	case catalog.FieldInteger, catalog.FieldNumber, catalog.FieldCurrency:
		v = json.Number(value)
	case catalog.FieldBoolean:
		b, err := strconv.ParseBool(value)
		if err != nil || (value != "true" && value != "false") {
			return nil, invalid("boolean paths use true or false")
		}
		v = b
	}
	return queryValue(d, v)
}

// smartPathSteps keeps the items on a navigation path.
func smartPathSteps(compiler *queryCompiler, d SmartFolderDefinition, path []*string) ([]content.PathStep, error) {
	steps := []content.PathStep{}
	for i, value := range path {
		group, fd, _, err := smartGroup(compiler, d.GroupBy[i])
		if err != nil {
			return nil, err
		}
		step := content.PathStep{Group: group}
		if value != nil {
			if step.Value, err = smartGroupValue(d.GroupBy[i], fd, *value); err != nil {
				return nil, err
			}
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// smartTermField reports whether fd can hold root: a term field of root's
// set, or a keywords field when root is a keyword.
func smartTermField(fd *catalog.FieldDefinition, root taxonomy.Term) bool {
	if !queryable(fd) {
		return false
	}
	if fd.Type == catalog.FieldTerm {
		return fd.Options.TermSetID == root.SetID
	}
	return fd.Type == catalog.FieldKeywords && (root.AvailableAsKeyword || root.SetID == taxonomy.KeywordsSetID)
}

// smartTermMatch matches the selected terms in a collection through its
// indexed surface values. It reports false when the collection cannot hold
// the terms the folder requires.
func smartTermMatch(c smartCandidate, d SmartFolderDefinition, roots []taxonomy.Term) (*content.TermMatch, bool) {
	if len(roots) == 0 {
		return nil, true
	}
	match := &content.TermMatch{Any: d.TermMatch == "any"}
	for _, root := range roots {
		keyword := smartTermField(&catalog.FieldDefinition{Type: "keywords", Indexed: true, IndexStatus: "ready"}, root)
		eligible := keyword
		for _, fd := range c.defs {
			if smartTermField(fd, root) {
				eligible = true
				break
			}
		}
		if !eligible {
			if !match.Any {
				return nil, false
			}
			continue
		}
		match.Roots = append(match.Roots, content.TermRoot{ID: root.ID, SetID: root.SetID, Keyword: keyword})
	}
	return match, len(match.Roots) > 0
}

func (s *Service) compileSmartFolder(ctx context.Context, subject string, f *viewstore.SmartFolder, d SmartFolderDefinition, in SmartFolderQueryRequest, grouped bool) (compiledQuery, []smartCandidate, error) {
	if err := smartPath(d, in.Path); err != nil {
		return compiledQuery{}, nil, err
	}
	if in.Surface == "" {
		in.Surface = "auto"
	}
	if in.Surface != "auto" && in.Surface != "head" && in.Surface != "published" {
		return compiledQuery{}, nil, invalid("invalid query surface")
	}
	order := SortSpec{Field: "$modified_at", Direction: "desc"}
	if in.Sort != nil && !grouped {
		order = *in.Sort
	}
	if (order.Field != "$name" && order.Field != "$created_at" && order.Field != "$modified_at") || (order.Direction != "asc" && order.Direction != "desc") {
		return compiledQuery{}, nil, invalid("sort by name, created or modified in ascending or descending order")
	}
	candidates, err := s.smartCandidates(ctx, subject, f, d)
	if err != nil {
		return compiledQuery{}, nil, err
	}
	roots, err := s.smartTerms(ctx, subject, d.Terms)
	if err != nil {
		return compiledQuery{}, nil, err
	}
	var firstError error
	keep := func(err error) {
		if firstError == nil {
			firstError = err
		}
	}
	rankIdentity := ""
	var ranked *content.Query
	usable := []smartCandidate{}
	queries := []*content.Query{}
	shapes := []string{}
	collections := []string{}
	schemas := []string{}
	for _, c := range candidates {
		spec := QuerySpec{Archive: d.Archive, Filter: d.Filter, Sort: []SortSpec{order}}
		plan, _, err := s.collectionPlan(ctx, subject, c.collection, c.defs, QueryRequest{Query: spec, Surface: in.Surface, rowACL: true}, false)
		if err != nil {
			var validation *ValidationError
			if errors.As(err, &validation) {
				keep(err)
				continue
			}
			return compiledQuery{}, nil, err
		}
		plan.ItemsOnly = !d.IncludeFolders
		if len(d.ContentTypes) > 0 {
			for _, typ := range c.types {
				plan.ContentTypes = append(plan.ContentTypes, typ.ID)
			}
			if len(plan.ContentTypes) == 0 {
				continue
			}
		}
		terms, ok := smartTermMatch(c, d, roots)
		if !ok {
			continue
		}
		plan.Terms = terms
		compiler := queryCompiler{defs: definitionMap(c.defs)}
		if plan.Path, err = smartPathSteps(&compiler, d, in.Path); err != nil {
			keep(err)
			continue
		}
		if grouped {
			g := d.GroupBy[len(in.Path)]
			group, fd, missing, err := smartGroup(&compiler, g)
			if err != nil {
				keep(err)
				continue
			}
			if !missing {
				identity := "text"
				if fd != nil {
					identity = string(fd.Type) + ":" + strconv.Itoa(fd.Scale) + ":" + fd.Options.TermSetID + ":" + fd.Options.LookupContainerID
				}
				if rankIdentity != "" && rankIdentity != identity {
					return compiledQuery{}, nil, invalid("grouping fields must have compatible types and scopes across collections")
				}
				rankIdentity = identity
			}
			plan.Group = &group
		}
		q := s.DAL.Content().Compile(plan)
		if ranked == nil && (!grouped || rankIdentity != "") {
			ranked = q
		}
		queries = append(queries, q)
		shapes = append(shapes, q.Shape())
		collections = append(collections, c.collection.ID)
		schemas = append(schemas, *c.collection.SchemaHeadID)
		usable = append(usable, c)
	}
	if len(queries) == 0 && firstError != nil {
		return compiledQuery{}, nil, firstError
	}
	rootIDs := make([]string, len(roots))
	for i, root := range roots {
		rootIDs[i] = root.ID
	}
	union := s.DAL.Content().Union(queries, rootIDs)
	if union.Arguments() > 30000 {
		return compiledQuery{}, nil, invalid("smart folder query exceeds the SQL parameter budget")
	}
	if ranked != nil {
		union.Orders, union.RankDef = ranked.Orders, ranked.RankDef
	}
	if len(queries) == 0 {
		kind := content.KindText
		if !grouped && (order.Field == "$created_at" || order.Field == "$modified_at") {
			kind = content.KindTime
		}
		union.Orders = []content.OrderInfo{{Kind: kind, Descending: !grouped && order.Direction == "desc"}}
	}
	fingerprint, _ := json.Marshal(struct {
		Folder, Subject, Surface string
		Version                  int
		Path                     []*string
		Grouped                  bool
		Sort                     SortSpec
		Collections, Schemas     []string
		Shapes, Roots            []string
	}{f.ID, subject, in.Surface, f.Version, in.Path, grouped, order, collections, schemas, shapes, rootIDs})
	sum := sha256.Sum256(fingerprint)
	return compiledQuery{q: union, fingerprint: hex.EncodeToString(sum[:])}, usable, nil
}

func (s *Service) QuerySmartFolder(ctx context.Context, subject, id string, in SmartFolderQueryRequest) (SmartFolderResult, error) {
	return read(ctx, s, func(t *Service) (SmartFolderResult, error) {
		f, err := t.smartFolder(ctx, subject, id, false)
		if err != nil {
			return SmartFolderResult{}, err
		}
		d, err := smartDefinition(f)
		if err != nil {
			return SmartFolderResult{}, err
		}
		return t.querySmartFolder(ctx, subject, f, d, in)
	})
}

func (s *Service) querySmartFolder(ctx context.Context, subject string, f *viewstore.SmartFolder, d SmartFolderDefinition, in SmartFolderQueryRequest) (SmartFolderResult, error) {
	q, candidates, err := s.compileSmartFolder(ctx, subject, f, d, in, false)
	if err != nil {
		return SmartFolderResult{}, err
	}
	if in.Surface == "" {
		in.Surface = "auto"
	}
	out, err := s.queryCompiled(ctx, subject, QueryRequest{Surface: in.Surface, After: in.After, Limit: in.Limit, IncludeTotal: in.IncludeTotal, TotalLimit: in.TotalLimit}, q, nil)
	if err != nil {
		return SmartFolderResult{}, err
	}
	byID := map[string]*resources.Resource{}
	for _, c := range candidates {
		byID[c.collection.ID] = c.collection
	}
	result := SmartFolderResult{Data: []SmartFolderEntry{}, NextCursor: out.NextCursor, Total: out.Total, TotalCapped: out.TotalCapped}
	for _, item := range out.Data {
		c := byID[*item.ContainerID]
		result.Data = append(result.Data, SmartFolderEntry{string(c.Kind), item.WorkspaceID, c.ID, c.Name, item})
	}
	return result, nil
}

type SmartFolderPreview struct {
	WorkspaceID *string               `json:"workspace_id,omitempty"`
	Definition  SmartFolderDefinition `json:"definition"`
}

// Preview evaluates an unsaved definition against exactly the caller's visible item surfaces.
func (s *Service) PreviewSmartFolder(ctx context.Context, subject string, in SmartFolderPreview) (SmartFolderResult, error) {
	if err := s.RequireScope(ctx, subject, "item.read"); err != nil {
		return SmartFolderResult{}, err
	}
	return read(ctx, s, func(t *Service) (SmartFolderResult, error) {
		if err := t.validateSmartFolder(ctx, subject, SmartFolderInput{Name: "Preview", Personal: true, WorkspaceID: in.WorkspaceID, Definition: in.Definition}); err != nil {
			return SmartFolderResult{}, err
		}
		f := &viewstore.SmartFolder{ID: "preview", WorkspaceID: in.WorkspaceID, OwnerID: &subject, Version: 1}
		out, err := t.querySmartFolder(ctx, subject, f, in.Definition, SmartFolderQueryRequest{Limit: 5, IncludeTotal: true, TotalLimit: 10000})
		out.NextCursor = ""
		return out, err
	})
}

func (s *Service) SmartFolderGroups(ctx context.Context, subject, id string, in SmartFolderQueryRequest) (SmartFolderGroupsResult, error) {
	return read(ctx, s, func(t *Service) (SmartFolderGroupsResult, error) {
		f, err := t.smartFolder(ctx, subject, id, false)
		if err != nil {
			return SmartFolderGroupsResult{}, err
		}
		d, err := smartDefinition(f)
		if err != nil {
			return SmartFolderGroupsResult{}, err
		}
		if err = smartPath(d, in.Path); err != nil {
			return SmartFolderGroupsResult{}, err
		}
		if len(in.Path) == len(d.GroupBy) {
			if in.After != "" {
				return SmartFolderGroupsResult{}, invalid("there is no next grouping level")
			}
			return SmartFolderGroupsResult{Data: []SmartFolderGroup{}}, nil
		}
		q, _, err := t.compileSmartFolder(ctx, subject, f, d, in, true)
		if err != nil {
			return SmartFolderGroupsResult{}, err
		}
		groups, err := t.queryGroupsCompiled(ctx, QueryRequest{After: in.After, Limit: in.Limit}, q)
		if err != nil {
			return SmartFolderGroupsResult{}, err
		}
		level := d.GroupBy[len(in.Path)]
		out := SmartFolderGroupsResult{Data: []SmartFolderGroup{}, NextCursor: groups.NextCursor, Field: level.Field, By: level.By}
		for _, g := range groups.Data {
			label := "(empty)"
			if g.Value != nil {
				switch value := g.Value.(type) {
				case string:
					label = value
				default:
					raw, _ := json.Marshal(value)
					label = string(raw)
				}
			}
			out.Data = append(out.Data, SmartFolderGroup{g.Value, label, g.Count})
		}
		if q.q.RankDef != nil && (q.q.RankDef.Type == catalog.FieldTerm || q.q.RankDef.Type == catalog.FieldKeywords) {
			ids := []string{}
			for _, g := range out.Data {
				if id, ok := g.Value.(string); ok {
					ids = append(ids, id)
				}
			}
			names, err := t.termNames(ctx, subject, ids)
			if err != nil {
				return SmartFolderGroupsResult{}, err
			}
			for i := range out.Data {
				if id, ok := out.Data[i].Value.(string); ok && names[id] != "" {
					out.Data[i].Label = names[id]
				}
			}
		}
		return out, nil
	})
}
