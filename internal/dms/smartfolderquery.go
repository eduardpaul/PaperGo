package dms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	"papergo/ent"
	"papergo/ent/contenttype"
	"papergo/ent/fielddefinition"
	"papergo/ent/resource"
	"papergo/ent/term"
	"papergo/ent/termset"
	"strconv"
	"strings"
	"time"
)

type smartCandidate struct {
	collection *ent.Resource
	defs       []*ent.FieldDefinition
	types      []*ent.ContentType
}

type smartQueryBranch struct {
	query           compiledQuery
	collectionIndex int
	collections     []any
}

// Equivalent schema/predicate branches differ only in their collection ID.
// Coalescing them reduces SQL preparation and repeats ACL/surface predicates
// once per distinct shape rather than once per collection.
func addSmartBranch(branches *[]smartQueryBranch, byShape map[string]int, q compiledQuery, collectionIndex int) {
	otherArgs := append([]any{}, q.args[:collectionIndex]...)
	otherArgs = append(otherArgs, q.args[collectionIndex+1:]...)
	raw, _ := json.Marshal(struct {
		SQL  string
		Args []any
	}{q.sql, otherArgs})
	key := string(raw)
	if i, ok := byShape[key]; ok {
		(*branches)[i].collections = append((*branches)[i].collections, q.args[collectionIndex])
		return
	}
	byShape[key] = len(*branches)
	*branches = append(*branches, smartQueryBranch{q, collectionIndex, []any{q.args[collectionIndex]}})
}

func smartBranchSQL(branch smartQueryBranch) (string, []any) {
	marks := make([]string, len(branch.collections))
	for i := range marks {
		marks[i] = "?"
	}
	text := strings.Replace(branch.query.sql, "r.container_id=?", "r.container_id IN ("+strings.Join(marks, ",")+")", 1)
	args := append([]any{}, branch.query.args[:branch.collectionIndex]...)
	args = append(args, branch.collections...)
	args = append(args, branch.query.args[branch.collectionIndex+1:]...)
	return text, args
}

type SmartFolderEntry struct {
	WorkspaceID    string        `json:"workspace_id"`
	CollectionID   string        `json:"collection_id"`
	CollectionName string        `json:"collection_name"`
	Item           *ent.Resource `json:"item"`
}
type SmartFolderResult struct {
	Data       []SmartFolderEntry `json:"data"`
	NextCursor string             `json:"next_cursor,omitempty"`
	Total      *int               `json:"total,omitempty"`
}
type SmartFolderGroup struct {
	Value any    `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}
type SmartFolderGroupsResult struct {
	Data       []SmartFolderGroup `json:"data"`
	NextCursor string             `json:"next_cursor,omitempty"`
	Field      string             `json:"field,omitempty"`
	By         string             `json:"by,omitempty"`
}

func namedSelector(values []string, column string) (string, []any) {
	marks := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		marks[i] = "?"
		args[i] = strings.ToLower(v)
	}
	return "unicode_lower(" + column + ") IN (" + strings.Join(marks, ",") + ")", args
}

func (s *Service) smartCandidates(ctx context.Context, subject string, f *ent.SmartFolder, d SmartFolderDefinition) ([]smartCandidate, error) {
	q := s.Client.Resource.Query().Where(resource.KindIn(resource.KindList, resource.KindLibrary), permissionPredicate(subject, "read"))
	if f.WorkspaceID != nil {
		q.Where(resource.WorkspaceIDEQ(*f.WorkspaceID))
	}
	if len(d.Collections) > 0 {
		text, args := namedSelector(d.Collections, "name")
		q.Where(func(sel *entsql.Selector) { sel.Where(entsql.ExprP(text, args...)) })
	}
	if len(d.Templates) > 0 {
		text, args := namedSelector(d.Templates, "value")
		q.Where(func(sel *entsql.Selector) {
			sel.Where(entsql.ExprP("EXISTS(SELECT 1 FROM json_each(template_keys) WHERE "+text+")", args...))
		})
	}
	if len(d.ContentTypes) > 0 {
		key, keyArgs := namedSelector(d.ContentTypes, "ct.key")
		name, nameArgs := namedSelector(d.ContentTypes, "ct.name")
		q.Where(func(sel *entsql.Selector) {
			sel.Where(entsql.ExprP("EXISTS(SELECT 1 FROM content_types ct WHERE ct.container_id="+sel.C(resource.FieldID)+" AND ("+key+" OR "+name+"))", append(keyArgs, nameArgs...)...))
		})
	}
	collections, err := q.Order(ent.Asc(resource.FieldID)).Limit(MaxSmartCollections + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(collections) > MaxSmartCollections {
		return nil, invalid("smart folders cover at most 100 readable collections; narrow the selectors")
	}
	if len(collections) == 0 {
		return []smartCandidate{}, nil
	}
	ids := make([]string, len(collections))
	for i, c := range collections {
		ids[i] = c.ID
	}
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	types, err := s.Client.ContentType.Query().Where(contenttype.ContainerIDIn(ids...)).Order(ent.Asc(contenttype.FieldKey)).All(ctx)
	if err != nil {
		return nil, err
	}
	fields := map[string][]*ent.FieldDefinition{}
	for _, fd := range defs {
		fields[fd.ContainerID] = append(fields[fd.ContainerID], fd)
	}
	byCollection := map[string][]*ent.ContentType{}
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
		byCollection[typ.ContainerID] = append(byCollection[typ.ContainerID], typ)
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

func (s *Service) smartTerms(ctx context.Context, subject string, ids []string) ([]*ent.Term, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	terms, err := s.Client.Term.Query().Where(term.IDIn(ids...)).WithTermSet().Order(ent.Asc(term.FieldID)).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(terms) != len(ids) {
		return nil, ErrNotFound
	}
	checked := map[string]bool{}
	for _, t := range terms {
		set, err := t.Edges.TermSetOrErr()
		if err != nil {
			return nil, err
		}
		if !checked[set.WorkspaceID] {
			if _, err = s.workspace(ctx, subject, set.WorkspaceID, "read"); err != nil {
				return nil, err
			}
			checked[set.WorkspaceID] = true
		}
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

func smartGroupColumn(compiler *queryCompiler, g SmartFolderGroupBy) (string, []any, string, *ent.FieldDefinition, error) {
	_, d := systemField(g.Field)
	if d == nil {
		d = compiler.defs[g.Field]
	}
	if d == nil {
		return "NULL", nil, "text", nil, nil
	}
	column, args, kind, err := compiler.column(g.Field)
	if err != nil {
		return "", nil, "", nil, err
	}
	if g.By != "" {
		if d.Type != "date" && d.Type != "datetime" {
			return "", nil, "", nil, invalid("year/month navigation requires a date field")
		}
		size := 4
		if g.By == "month" {
			size = 7
		}
		column = "substr(" + column + ",1," + strconv.Itoa(size) + ")"
		kind = "text"
		d = nil
	}
	return column, args, kind, d, nil
}

func smartGroupValue(g SmartFolderGroupBy, d *ent.FieldDefinition, value string) (any, error) {
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
	case "integer", "number":
		v = json.Number(value)
	case "boolean":
		b, err := strconv.ParseBool(value)
		if err != nil || (value != "true" && value != "false") {
			return nil, invalid("boolean paths use true or false")
		}
		v = b
	}
	return queryValue(d, v)
}

func appendSmartPath(q *compiledQuery, compiler *queryCompiler, d SmartFolderDefinition, path []*string) error {
	for i, value := range path {
		col, args, _, fd, err := smartGroupColumn(compiler, d.GroupBy[i])
		if err != nil {
			return err
		}
		q.sql += " AND (" + col
		q.args = append(q.args, args...)
		if value == nil {
			q.sql += " IS NULL)"
		} else {
			v, err := smartGroupValue(d.GroupBy[i], fd, *value)
			if err != nil {
				return err
			}
			q.sql += "=?)"
			q.args = append(q.args, v)
		}
	}
	return nil
}

// Terms are matched through indexed surface values and one recursive CTE shared
// by every collection branch. Descendants are never expanded into parameter lists.
func appendSmartTerms(q *compiledQuery, c smartCandidate, d SmartFolderDefinition, roots []*ent.Term) bool {
	if len(roots) == 0 {
		return true
	}
	parts := []string{}
	args := []any{}
	for _, root := range roots {
		eligible := false
		for _, fd := range c.defs {
			if fd.Type == "term" && queryable(fd) && fd.Options.TermSetID == root.TermSetID {
				eligible = true
				break
			}
		}
		if !eligible {
			if d.TermMatch != "any" {
				return false
			}
			continue
		}
		parts = append(parts, `EXISTS(SELECT 1 FROM field_values f JOIN field_definitions fd ON fd.container_id=f.container_id AND fd.key=f.field_key JOIN smart_terms st ON st.id=f.value_text WHERE f.surface_id=p.id AND fd.type='term' AND fd.indexed=1 AND json_extract(fd.options,'$.term_set_id')=? AND st.root=?)`)
		args = append(args, root.TermSetID, root.ID)
	}
	if len(parts) == 0 {
		return false
	}
	operator := " AND "
	if d.TermMatch == "any" {
		operator = " OR "
	}
	q.sql += " AND (" + strings.Join(parts, operator) + ")"
	q.args = append(q.args, args...)
	return true
}

// Physical folders have system metadata but no item values or publication
// surfaces. Reuse the item predicate with a system-metadata projection; indexed
// custom-value predicates naturally see no values for these resource IDs.
func (s *Service) smartPhysicalQuery(subject string, c smartCandidate, item compiledQuery, surface string) (compiledQuery, error) {
	marker := " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface="
	start := strings.Index(item.sql, marker)
	where := strings.Index(item.sql, " WHERE r.container_id=? AND r.kind='item'")
	if start < 0 || where < 0 {
		return compiledQuery{}, fmt.Errorf("invalid compiled item query")
	}
	_, surfaceArgs := surfaceSQL("r.id", subject, surface)
	// Rank arguments precede the surface selector. The rest of the predicate
	// has the same argument order after replacing the JOIN.
	rankArgs := strings.Count(item.sql[:start], "?")
	args := append([]any{}, item.args[:rankArgs]...)
	args = append(args, item.args[rankArgs+len(surfaceArgs):]...)
	projection := ` FROM resources r JOIN (SELECT id,item_created_at,item_created_by,modified_at,modified_by,name,tags FROM (SELECT id,papergo_time(created_at) item_created_at,created_by item_created_by,papergo_time(updated_at) modified_at,updated_by modified_by,name,tags FROM resources WHERE kind='folder')) p ON p.id=r.id`
	text := item.sql[:start] + projection + strings.Replace(item.sql[where:], "r.kind='item'", "r.kind='folder' AND r.deleted_at IS NULL", 1)
	text = strings.ReplaceAll(text, "FROM item_surface_tags", "FROM (SELECT f.id surface_id,j.value tag FROM resources f,json_each(f.tags) j WHERE f.kind='folder')")
	return compiledQuery{sql: text, args: args}, nil
}

func (s *Service) compileSmartFolder(ctx context.Context, subject string, f *ent.SmartFolder, d SmartFolderDefinition, in SmartFolderQueryRequest, grouped bool) (compiledQuery, []smartCandidate, error) {
	if err := smartPath(d, in.Path); err != nil {
		return compiledQuery{}, nil, err
	}
	if in.Surface == "" {
		in.Surface = "auto"
	}
	if in.Surface != "auto" && in.Surface != "head" && in.Surface != "published" {
		return compiledQuery{}, nil, invalid("invalid query surface")
	}
	candidates, err := s.smartCandidates(ctx, subject, f, d)
	if err != nil {
		return compiledQuery{}, nil, err
	}
	roots, err := s.smartTerms(ctx, subject, d.Terms)
	if err != nil {
		return compiledQuery{}, nil, err
	}
	branches := []smartQueryBranch{}
	byShape := map[string]int{}
	_, surfaceArgs := surfaceSQL("r.id", subject, in.Surface)
	schemas := []string{}
	var firstError error
	var rankDef *ent.FieldDefinition
	kind := "text"
	rankIdentity := ""
	usable := []smartCandidate{}
	for _, c := range candidates {
		spec := QuerySpec{Filter: d.Filter, Sort: SortSpec{Field: "$modified_at", Direction: "desc"}}
		q, err := s.compileCollectionQuery(ctx, subject, c.collection, c.defs, QueryRequest{Query: spec, Surface: in.Surface}, false)
		if err != nil {
			var validation *ValidationError
			if errors.As(err, &validation) {
				if firstError == nil {
					firstError = err
				}
				continue
			}
			return compiledQuery{}, nil, err
		}
		compiler := queryCompiler{defs: definitionMap(c.defs)}
		if len(d.ContentTypes) > 0 {
			marks := []string{}
			for _, typ := range c.types {
				marks = append(marks, "?")
				q.args = append(q.args, typ.ID)
			}
			q.sql += " AND r.content_type_id IN (" + strings.Join(marks, ",") + ")"
		}
		if !appendSmartTerms(&q, c, d, roots) {
			continue
		}
		if err = appendSmartPath(&q, &compiler, d, in.Path); err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		if grouped {
			g := d.GroupBy[len(in.Path)]
			col, rankArgs, k, fd, err := smartGroupColumn(&compiler, g)
			if err != nil {
				if firstError == nil {
					firstError = err
				}
				continue
			}
			if col != "NULL" {
				identity := k
				if fd != nil {
					identity = string(fd.Type) + ":" + strconv.Itoa(fd.Scale) + ":" + fd.Options.TermSetID + ":" + fd.Options.LookupContainerID
				}
				if rankIdentity != "" && rankIdentity != identity {
					return compiledQuery{}, nil, invalid("grouping fields must have compatible types and scopes across collections")
				}
				rankIdentity = identity
				kind = k
				rankDef = fd
			}
			q.sql = strings.Replace(q.sql, "SELECT r.id,p.modified_at AS sort_value", "SELECT r.id,"+col+" AS sort_value", 1)
			q.args = append(rankArgs, q.args...)
		}
		rankArgumentCount := strings.Count(q.sql[:strings.Index(q.sql, " FROM resources r")], "?")
		addSmartBranch(&branches, byShape, q, rankArgumentCount+len(surfaceArgs))
		if d.IncludeFolders && len(roots) == 0 && len(d.ContentTypes) == 0 {
			folderQuery, err := s.smartPhysicalQuery(subject, c, q, in.Surface)
			if err != nil {
				return compiledQuery{}, nil, err
			}
			addSmartBranch(&branches, byShape, folderQuery, rankArgumentCount)
		}
		schemas = append(schemas, *c.collection.SchemaHeadID)
		usable = append(usable, c)
	}
	parts := []string{}
	args := []any{}
	for _, branch := range branches {
		sql, values := smartBranchSQL(branch)
		parts = append(parts, sql)
		args = append(args, values...)
	}
	if len(parts) == 0 {
		if firstError != nil {
			return compiledQuery{}, nil, firstError
		}
		parts = []string{"SELECT NULL id,NULL sort_value WHERE 0"}
	}
	text := strings.Join(parts, " UNION ALL ")
	if len(roots) > 0 {
		marks := []string{}
		rootArgs := []any{}
		for _, root := range roots {
			marks = append(marks, "?")
			rootArgs = append(rootArgs, root.ID)
		}
		text = "WITH RECURSIVE smart_terms(root,id) AS (SELECT id,id FROM terms WHERE id IN (" + strings.Join(marks, ",") + ") UNION ALL SELECT st.root,t.id FROM terms t JOIN smart_terms st ON t.parent_id=st.id) " + text
		args = append(rootArgs, args...)
	}
	if len(args) > 30000 {
		return compiledQuery{}, nil, invalid("smart folder query exceeds the SQL parameter budget")
	}
	fingerprint, _ := json.Marshal(struct {
		Folder, Subject, Surface string
		Version                  int
		Path                     []*string
		Grouped                  bool
		Schemas                  []string
		SQL                      string
		Args                     []any
	}{f.ID, subject, in.Surface, f.Version, in.Path, grouped, schemas, text, args})
	sum := sha256.Sum256(fingerprint)
	return compiledQuery{sql: text, args: args, fingerprint: hex.EncodeToString(sum[:]), descending: !grouped, kind: kind, rankDef: rankDef}, usable, nil
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
		q, candidates, err := t.compileSmartFolder(ctx, subject, f, d, in, false)
		if err != nil {
			return SmartFolderResult{}, err
		}
		if in.Surface == "" {
			in.Surface = "auto"
		}
		out, err := t.queryCompiled(ctx, subject, QueryRequest{Surface: in.Surface, After: in.After, Limit: in.Limit, IncludeTotal: in.IncludeTotal}, q, nil)
		if err != nil {
			return SmartFolderResult{}, err
		}
		byID := map[string]*ent.Resource{}
		for _, c := range candidates {
			byID[c.collection.ID] = c.collection
		}
		result := SmartFolderResult{Data: []SmartFolderEntry{}, NextCursor: out.NextCursor, Total: out.Total}
		for _, item := range out.Data {
			c := byID[*item.ContainerID]
			result.Data = append(result.Data, SmartFolderEntry{item.WorkspaceID, c.ID, c.Name, item})
		}
		return result, nil
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
		if q.rankDef != nil && q.rankDef.Type == "term" {
			set, err := t.Client.TermSet.Query().Where(termset.IDEQ(q.rankDef.Options.TermSetID)).Only(ctx)
			if err != nil {
				return SmartFolderGroupsResult{}, err
			}
			if _, err = t.workspace(ctx, subject, set.WorkspaceID, "read"); err != nil {
				if errors.Is(err, ErrForbidden) {
					return out, nil
				}
				return SmartFolderGroupsResult{}, err
			}
			ids := []string{}
			for _, g := range out.Data {
				if id, ok := g.Value.(string); ok {
					ids = append(ids, id)
				}
			}
			names, err := t.Client.Term.Query().Where(term.IDIn(ids...), term.TermSetIDEQ(set.ID)).All(ctx)
			if err != nil {
				return SmartFolderGroupsResult{}, err
			}
			byID := map[string]string{}
			for _, term := range names {
				byID[term.ID] = term.Name
			}
			for i := range out.Data {
				if id, ok := out.Data[i].Value.(string); ok && byID[id] != "" {
					out.Data[i].Label = byID[id]
				}
			}
		}
		return out, nil
	})
}
