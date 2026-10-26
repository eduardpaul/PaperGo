package dms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"slices"
	"strconv"
	"strings"
	"time"
)

type FilterExpr struct {
	And      []FilterExpr    `json:"and,omitempty"`
	Or       []FilterExpr    `json:"or,omitempty"`
	Not      *FilterExpr     `json:"not,omitempty"`
	Field    string          `json:"field,omitempty"`
	Op       string          `json:"op,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	ValueRef string          `json:"value_ref,omitempty"`
}
type SortSpec struct {
	Field     string `json:"field,omitempty"`
	Direction string `json:"direction,omitempty"`
}
type QuerySpec struct {
	Archive       refs.ArchiveSelection `json:"archive,omitempty"`
	ContentTypeID string                `json:"content_type_id,omitempty"`
	Filter        *FilterExpr           `json:"filter,omitempty"`
	Sort          []SortSpec            `json:"sort,omitempty"`
	GroupBy       string                `json:"group_by,omitempty"`
	ParentID      string                `json:"parent_id,omitempty"`
	// AncestorID keeps what is anywhere below a folder: in it or its subfolders.
	AncestorID string `json:"ancestor_id,omitempty"`
	Search     string `json:"search,omitempty"`
	Keyword    string `json:"keyword,omitempty"`
}
type QueryRequest struct {
	Select  []string  `json:"select,omitempty"`
	Query   QuerySpec `json:"query"`
	Surface string    `json:"surface,omitempty"`
	After   string    `json:"-"`
	Limit   int       `json:"-"`
	// IncludeTotal counts every authorized match; omitted, total is absent.
	IncludeTotal bool `json:"include_total,omitempty"`
	// TotalLimit stops counting there and reports total_capped when more
	// match, so a total costs at most that many rows. It implies a total.
	TotalLimit int `json:"total_limit,omitempty"`
	// rowACL checks permissions row by row, so queries over several
	// collections share one SQL shape (smart folders).
	rowACL bool
	// itemsOnly leaves folders out (navigation views).
	itemsOnly bool
}
type QueryResult struct {
	Data       []*resources.Resource `json:"value"`
	NextCursor string                `json:"-"`
	Total      *int                  `json:"total,omitempty"`
	// TotalCapped means more than total items match (see TotalLimit).
	TotalCapped bool `json:"total_capped,omitempty"`
	// SearchCapped means the search text matched more entries than a query
	// considers; narrow the words or filters for complete results.
	SearchCapped bool `json:"search_capped,omitempty"`
}
type QueryGroup struct {
	Value any `json:"value"`
	// Label names the term of a term or keywords group when the caller can
	// read the workspace's taxonomy.
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

// compiledQuery is a validated query with what its results show.
type compiledQuery struct {
	q           *content.Query
	fingerprint string
	// projection limits item values to these keys; nil returns them all.
	projection      []string
	includeKeywords bool
	// searchCapped means the search text matched more entries than queries
	// consider (searchMatchLimit).
	searchCapped bool
}
type queryCursor struct {
	Fingerprint string            `json:"fingerprint"`
	ID          string            `json:"id,omitempty"`
	Values      []json.RawMessage `json:"values"`
}
type queryCompiler struct {
	defs  map[string]*catalog.FieldDefinition
	nodes int
}

// systemField is the definition of a built-in field, or nil.
func systemField(key string) *catalog.FieldDefinition {
	typ, ok := content.SystemField(key)
	if !ok {
		return nil
	}
	return &catalog.FieldDefinition{Type: typ}
}

// column checks that a field can sort or group queries.
func (q *queryCompiler) column(field string) (content.Field, error) {
	if systemField(field) != nil {
		return content.Field{Key: field}, nil
	}
	d := q.defs[field]
	if d == nil || !queryable(d) {
		return content.Field{}, unqueryable(field, d)
	}
	if d.Options.Multiple {
		return content.Field{}, invalid("sorting and grouping require a scalar field")
	}
	return content.Field{Key: field, Def: d}, nil
}
func queryValue(d *catalog.FieldDefinition, v any) (any, error) {
	// Query literals must be representable, but current write constraints must
	// not prevent searching retained values from older schema revisions.
	queryDefinition := *d
	queryDefinition.Options.Minimum = nil
	queryDefinition.Options.Maximum = nil
	queryDefinition.Options.MaxLength = nil
	if queryDefinition.Type == catalog.FieldChoice {
		queryDefinition.Type = "text"
	}
	// Retained values can exceed today's write limits. Query representability
	// is separate from both configured and default text constraints.
	if _, ceiling := stringLimits(queryDefinition.Type); ceiling > 0 {
		queryDefinition.Options.MaxLength = &ceiling
	}
	x, e := scalarValue(&queryDefinition, v)
	if e != nil {
		return nil, e
	}
	switch string(d.Type) {
	case "integer":
		return integer(x)
	case "decimal":
		_, n, e := decimal(x, d.Scale)
		return n, e
	case "number", "currency":
		return numberKey(x)
	case "datetime":
		t, e := time.Parse(time.RFC3339Nano, x.(string))
		if e != nil {
			return nil, e
		}
		return t.UTC().Format("2006-01-02T15:04:05.000000000Z"), nil
	default:
		return x, nil
	}
}

// filter validates a filter tree and converts its literals to their
// indexed representation.
func (q *queryCompiler) filter(f *FilterExpr, depth int) (*content.Filter, error) {
	q.nodes++
	if depth > 6 || q.nodes > 32 {
		return nil, invalid("filter is limited to 32 nodes and depth 6")
	}
	groups := 0
	if f.And != nil {
		groups++
	}
	if f.Or != nil {
		groups++
	}
	if f.Not != nil {
		groups++
	}
	if groups > 0 {
		if groups != 1 || f.Field != "" || f.Op != "" || len(f.Value) > 0 {
			return nil, invalid("filter node must be a group or a condition")
		}
		if f.Not != nil {
			inner, e := q.filter(f.Not, depth+1)
			if e != nil {
				return nil, e
			}
			return &content.Filter{Not: inner}, nil
		}
		list := f.And
		if f.Or != nil {
			list = f.Or
		}
		if len(list) == 0 {
			return nil, invalid("filter groups cannot be empty")
		}
		parts := make([]content.Filter, 0, len(list))
		for i := range list {
			x, e := q.filter(&list[i], depth+1)
			if e != nil {
				return nil, e
			}
			parts = append(parts, *x)
		}
		if f.Or != nil {
			return &content.Filter{Or: parts}, nil
		}
		return &content.Filter{And: parts}, nil
	}
	op := content.FilterOp(f.Op)
	if op == "" {
		op = content.FilterEQ
	}
	d := systemField(f.Field)
	builtin := d != nil
	field := content.Field{Key: f.Field}
	if builtin {
		// System metadata belongs to the selected immutable content surface.
	} else if f.Field == "$keywords" {
		if op == content.FilterMissing || op == content.FilterPresent {
			if len(f.Value) > 0 {
				return nil, invalid("missing and present take no value")
			}
			return &content.Filter{Cond: &content.Condition{Field: field, Op: op}}, nil
		}
		if op != content.FilterEQ && op != content.FilterNE {
			return nil, invalid("keywords support eq, ne, missing and present")
		}
		v, e := decodeValue(f.Value)
		if e != nil {
			return nil, invalid("invalid keyword filter")
		}
		keyword, ok := v.(string)
		if !ok {
			return nil, invalid("keyword must be text")
		}
		return &content.Filter{Cond: &content.Condition{Field: field, Op: op, Values: []any{keyword}}}, nil
	} else {
		d = q.defs[f.Field]
		if d == nil || !queryable(d) {
			return nil, unqueryable(f.Field, d)
		}
		field.Def = d
	}
	cond := func(values ...any) (*content.Filter, error) {
		return &content.Filter{Cond: &content.Condition{Field: field, Op: op, Values: values}}, nil
	}
	if op == content.FilterMissing || op == content.FilterPresent {
		if len(f.Value) > 0 {
			return nil, invalid("missing/present do not accept a value")
		}
		return cond()
	}
	v, e := decodeValue(f.Value)
	if e != nil || v == nil {
		return nil, invalid("filter value is required")
	}
	if !builtin && (d.Type == catalog.FieldTerm || d.Type == catalog.FieldKeywords) {
		return termFilter(d, field, op, v)
	}
	if op == content.FilterUnder {
		return nil, invalid("under requires a term or keywords field")
	}
	valueForFilter := func(value any) (any, error) {
		literal, err := queryValue(d, value)
		if err != nil {
			return nil, err
		}
		if builtin && d.Type == catalog.FieldDatetime {
			stamp, err := time.Parse(time.RFC3339Nano, literal.(string))
			if err != nil {
				return nil, err
			}
			return stamp.UTC(), nil
		}
		return literal, nil
	}
	switch op {
	case content.FilterIn:
		list, ok := v.([]any)
		if !ok || len(list) < 1 || len(list) > 50 {
			return nil, invalid("in requires 1..50 values")
		}
		values := []any{}
		for _, value := range list {
			x, e := valueForFilter(value)
			if e != nil {
				return nil, e
			}
			values = append(values, x)
		}
		return cond(values...)
	case content.FilterContains:
		if d.Type != catalog.FieldText && d.Type != catalog.FieldNote && d.Type != catalog.FieldEmail && d.Type != catalog.FieldURL {
			return nil, invalid("contains requires text")
		}
	case content.FilterEQ, content.FilterNE:
	case content.FilterGT, content.FilterGTE, content.FilterLT, content.FilterLTE:
		if d.Type == catalog.FieldBoolean || d.Type == catalog.FieldLookup || d.Type == catalog.FieldChoice {
			return nil, invalid("field supports equality only")
		}
	default:
		return nil, invalid("unsupported filter operator")
	}
	x, e := valueForFilter(v)
	if e != nil {
		return nil, e
	}
	return cond(x)
}
func (s *Service) compileQuery(ctx context.Context, subject, containerID string, in QueryRequest, grouped bool) (compiledQuery, error) {
	c, e := s.authorize(ctx, subject, containerID, "read")
	if e != nil {
		return compiledQuery{}, e
	}
	if c.Kind != "list" && c.Kind != "library" {
		return compiledQuery{}, invalid("queries require a collection")
	}
	defs, e := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: c.ID})
	if e != nil {
		return compiledQuery{}, e
	}
	return s.compileCollectionQuery(ctx, subject, c, defs, in, grouped)
}

// matchQueryText matches the search text of a query of a collection the
// subject may read before its read transaction opens.
func (s *Service) matchQueryText(ctx context.Context, subject, collectionID, text string, archive refs.ArchiveSelection) (context.Context, error) {
	if s.transaction || strings.TrimSpace(text) == "" {
		return ctx, nil
	}
	c, err := s.Authorize(ctx, subject, collectionID, "read")
	if err != nil || c.Kind != refs.KindList && c.Kind != refs.KindLibrary {
		// The query reports what is wrong.
		return ctx, nil
	}
	return s.matchFirst(ctx, subject, collectionID, "", text, archive)
}

// The caller has authorized c in the current snapshot.
func (s *Service) compileCollectionQuery(ctx context.Context, subject string, c *resources.Resource, defs []*catalog.FieldDefinition, in QueryRequest, grouped bool) (compiledQuery, error) {
	plan, q, err := s.collectionPlan(ctx, subject, c, defs, in, grouped)
	if err != nil {
		return compiledQuery{}, err
	}
	q.q = s.DAL.Content().Compile(plan)
	return q, nil
}

// collectionPlan validates a query of collection c. Smart folders load the
// collection catalog in batches and extend the same plan.
func (s *Service) collectionPlan(ctx context.Context, subject string, c *resources.Resource, defs []*catalog.FieldDefinition, in QueryRequest, grouped bool) (content.Plan, compiledQuery, error) {
	if in.Surface == "" {
		in.Surface = "auto"
	}
	if in.Surface != "auto" && in.Surface != "head" && in.Surface != "published" {
		return content.Plan{}, compiledQuery{}, invalid("invalid query surface")
	}
	if len(in.Query.Search) > 256 || len(in.Query.Keyword) > 255 {
		return content.Plan{}, compiledQuery{}, invalid("search or keyword exceeds size limit")
	}
	var keywordErr error
	in.Query.Keyword, keywordErr = s.CanonicalKeyword(ctx, in.Query.Keyword)
	if keywordErr != nil {
		return content.Plan{}, compiledQuery{}, keywordErr
	}
	in.Query.Filter, keywordErr = s.keywordFilters(ctx, in.Query.Filter)
	if keywordErr != nil {
		return content.Plan{}, compiledQuery{}, keywordErr
	}
	resolved, e := resolveFilter(in.Query.Filter, subject, time.Now().UTC(), definitionMap(defs))
	if e != nil {
		return content.Plan{}, compiledQuery{}, e
	}
	in.Query.Filter = resolved
	compiler := queryCompiler{defs: map[string]*catalog.FieldDefinition{}}
	for _, d := range defs {
		compiler.defs[d.Key] = d
	}
	if !in.Query.Archive.Valid() {
		return content.Plan{}, compiledQuery{}, invalid("archive must be active, archived or all")
	}
	plan := content.Plan{Archive: in.Query.Archive.Default(), Reader: principal(ctx, subject), CollectionID: c.ID, Publishes: c.PublishingEnabled, Surface: in.Surface, RowACL: in.rowACL}
	if in.Query.ContentTypeID != "" {
		// A type matches the collection's types deriving from it, on its
		// fields that they all share.
		typ, family, e := s.typeFamily(ctx, c.ID, in.Query.ContentTypeID)
		if e != nil {
			return content.Plan{}, compiledQuery{}, e
		}
		shared := []*catalog.FieldDefinition{}
		for _, d := range selectTypeDefinitions(defs, typ) {
			if !slices.ContainsFunc(family, func(t *catalog.ContentType) bool { return !slices.Contains(t.Effective.FieldIds, d.ID) }) {
				shared = append(shared, d)
			}
		}
		compiler.defs = definitionMap(shared)
		for _, t := range family {
			plan.ContentTypes = append(plan.ContentTypes, t.ID)
		}
	}
	if len(in.Query.Sort) > 8 {
		return content.Plan{}, compiledQuery{}, invalid("at most 8 sort keys")
	}
	orders := append([]SortSpec{}, in.Query.Sort...)
	if len(orders) == 0 {
		orders = []SortSpec{{Field: "$id"}}
	}
	seen := map[string]bool{}
	for _, order := range orders {
		if order.Field == "" || seen[order.Field] {
			return content.Plan{}, compiledQuery{}, invalid("sort keys must be unique known fields")
		}
		seen[order.Field] = true
		if order.Direction != "" && order.Direction != "asc" && order.Direction != "desc" {
			return content.Plan{}, compiledQuery{}, invalid("sort direction must be asc or desc")
		}
		field, err := compiler.column(order.Field)
		if err != nil {
			return content.Plan{}, compiledQuery{}, err
		}
		plan.Orders = append(plan.Orders, content.Order{Field: field, Descending: order.Direction == "desc"})
	}
	if in.Query.GroupBy != "" {
		field, err := compiler.column(in.Query.GroupBy)
		if err != nil {
			return content.Plan{}, compiledQuery{}, err
		}
		if grouped {
			plan.Group = &content.GroupBy{Field: field}
		}
	}
	if grouped {
		if in.Query.GroupBy == "" {
			return content.Plan{}, compiledQuery{}, invalid("group_by is required")
		}
		if in.Select != nil {
			return content.Plan{}, compiledQuery{}, invalid("select belongs to item queries")
		}
	}
	projection, includeKeywords, err := querySelection(compiler.defs, in.Select)
	if err != nil {
		return content.Plan{}, compiledQuery{}, err
	}
	if in.Query.ParentID != "" {
		parent, e := s.authorize(ctx, subject, in.Query.ParentID, "read")
		if e != nil {
			return content.Plan{}, compiledQuery{}, e
		}
		if parent.ID != c.ID && (parent.Kind != "folder" || parent.ContainerID == nil || *parent.ContainerID != c.ID) {
			return content.Plan{}, compiledQuery{}, invalid("parent_id is outside this collection")
		}
		plan.ParentID = parent.ID
	}
	if in.Query.AncestorID != "" {
		if in.Query.ParentID != "" {
			return content.Plan{}, compiledQuery{}, invalid("use parent_id or ancestor_id, not both")
		}
		ancestor, e := s.authorize(ctx, subject, in.Query.AncestorID, "read")
		if e != nil {
			return content.Plan{}, compiledQuery{}, e
		}
		if ancestor.ID != c.ID && (ancestor.Kind != "folder" || ancestor.ContainerID == nil || *ancestor.ContainerID != c.ID) {
			return content.Plan{}, compiledQuery{}, invalid("ancestor_id is outside this collection")
		}
		// The collection itself is every item's ancestor.
		if ancestor.ID != c.ID {
			plan.AncestorID = ancestor.ID
		}
	}
	if in.Query.Filter != nil {
		if plan.Filter, e = compiler.filter(in.Query.Filter, 0); e != nil {
			return content.Plan{}, compiledQuery{}, e
		}
	}
	plan.Keyword = in.Query.Keyword
	plan.ItemsOnly = in.itemsOnly
	searchCapped := false
	if strings.TrimSpace(in.Query.Search) != "" {
		if plan.Search, searchCapped, e = s.matchText(ctx, subject, c.ID, "", in.Query.Search, in.Query.Archive.Default()); e != nil {
			return content.Plan{}, compiledQuery{}, e
		}
	}
	fingerprint, _ := json.Marshal(struct {
		Subject, Collection, Surface, Schema string
		Query                                QuerySpec
		Grouped, ItemsOnly                   bool
		Select                               []string
	}{subject, c.ID, in.Surface, *c.SchemaHeadID, in.Query, grouped, in.itemsOnly, in.Select})
	sum := sha256.Sum256(fingerprint)
	return plan, compiledQuery{fingerprint: hex.EncodeToString(sum[:]), projection: projection, includeKeywords: includeKeywords, searchCapped: searchCapped}, nil
}
func cursorEncode(q compiledQuery, id string, value any) string {
	values := []any{value}
	if len(q.q.Orders) > 1 {
		values = value.([]any)
	}
	raw := make([]json.RawMessage, len(values))
	for i, v := range values {
		raw[i], _ = json.Marshal(v)
	}
	b, _ := json.Marshal(queryCursor{q.fingerprint, id, raw})
	return "q2." + base64.RawURLEncoding.EncodeToString(b)
}
func cursorDecodeValues(q compiledQuery, after string) (queryCursor, []any, error) {
	if len(after) > 128*1024 || !strings.HasPrefix(after, "q2.") {
		return queryCursor{}, nil, invalid("invalid query cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(after, "q2."))
	if err != nil {
		return queryCursor{}, nil, invalid("invalid query cursor")
	}
	var c queryCursor
	if err = json.Unmarshal(raw, &c); err != nil || c.Fingerprint != q.fingerprint {
		return c, nil, invalid("cursor does not match this query, schema or caller")
	}
	if len(c.Values) != len(q.q.Orders) {
		return c, nil, invalid("cursor sort key count differs")
	}
	values := make([]any, len(q.q.Orders))
	for i, order := range q.q.Orders {
		v, e := decodeValue(c.Values[i])
		if e != nil {
			return c, nil, invalid("invalid cursor value")
		}
		if v != nil {
			switch order.Kind {
			case content.KindInteger:
				v, e = integer(v)
			case content.KindBoolean:
				if _, ok := v.(bool); !ok {
					var n int64
					n, e = integer(v)
					if e == nil && n != 0 && n != 1 {
						e = invalid("expected boolean cursor")
					}
					v = n == 1
				}
			case content.KindTime:
				text, ok := v.(string)
				if !ok {
					e = invalid("expected timestamp cursor")
				} else {
					var stamp time.Time
					stamp, e = time.Parse(time.RFC3339Nano, text)
					v = stamp.UTC()
				}
			case content.KindExact:
				key, ok := v.(string)
				if !ok {
					e = invalid("expected exact numeric cursor")
				} else {
					_, e = numberFromKey(key)
				}
			default:
				if _, ok := v.(string); !ok {
					e = invalid("expected text cursor")
				}
			}
		}
		if e != nil {
			return c, nil, invalid("invalid cursor value")
		}
		values[i] = v
	}
	return c, values, nil
}

// position decodes a page cursor of q's rows.
func position(q compiledQuery, after string) (*content.Position, error) {
	if after == "" {
		return nil, nil
	}
	c, values, err := cursorDecodeValues(q, after)
	if err != nil {
		return nil, err
	}
	if c.ID == "" {
		return nil, invalid("cursor has no item ID")
	}
	var rank any = values
	if len(values) == 1 {
		rank = values[0]
	}
	return &content.Position{ID: c.ID, Rank: rank}, nil
}

func (s *Service) Query(ctx context.Context, subject, containerID string, in QueryRequest) (QueryResult, error) {
	if in.Surface == "" {
		in.Surface = "auto"
	}
	ctx, err := s.matchQueryText(ctx, subject, containerID, in.Query.Search, in.Query.Archive.Default())
	if err != nil {
		return QueryResult{}, err
	}
	return read(ctx, s, func(t *Service) (QueryResult, error) {
		q, e := t.compileQuery(ctx, subject, containerID, in, false)
		if e != nil {
			return QueryResult{}, e
		}
		out, err := t.queryCompiled(ctx, subject, in, q, q.projection)
		if in.Select != nil && !q.includeKeywords {
			for _, r := range out.Data {
				r.Keywords = []string{}
			}
		}
		return out, err
	})
}

// queryCompiled returns one page of q. A non-nil fields projects item values
// to those keys (see overlayPage); nil returns complete values.
func (s *Service) queryCompiled(ctx context.Context, subject string, in QueryRequest, q compiledQuery, fields []string) (QueryResult, error) {
	if in.TotalLimit < 0 || in.TotalLimit > 1_000_000 {
		return QueryResult{}, invalid("total_limit must be between 1 and 1000000")
	}
	return read(ctx, s, func(t *Service) (QueryResult, error) {
		limit := pageSize(in.Limit)
		after, e := position(q, in.After)
		if e != nil {
			return QueryResult{}, e
		}
		page, e := t.DAL.Content().Page(ctx, q.q, after, limit+1)
		if e != nil {
			return QueryResult{}, e
		}
		ids := make([]string, len(page))
		ranks := make([]any, len(page))
		for i, r := range page {
			ids[i], ranks[i] = r.ID, r.Rank
		}
		out := QueryResult{Data: []*resources.Resource{}, SearchCapped: q.searchCapped}
		if len(ids) > limit {
			out.NextCursor = cursorEncode(q, ids[limit-1], ranks[limit-1])
			ids = ids[:limit]
		}
		if len(ids) > 0 {
			// Item values come from the selected surface, so the resource's copy
			// of the head values is never loaded.
			summaries, e := t.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids, Summary: true})
			if e != nil {
				return out, e
			}
			lookup := map[string]*resources.Resource{}
			for _, r := range summaries {
				r.Values = map[string]any{}
				lookup[r.ID] = r
			}
			for _, id := range ids {
				out.Data = append(out.Data, lookup[id])
			}
			if _, e = t.overlayPage(ctx, subject, in.Surface, out.Data, fields); e != nil {
				return out, e
			}
		}
		if !in.IncludeTotal && in.TotalLimit == 0 {
			return out, nil
		}
		// An exact total evaluates every eligible row, so callers opt in.
		countLimit := 0
		if in.TotalLimit > 0 {
			countLimit = in.TotalLimit + 1
		}
		total, e := t.DAL.Content().Count(ctx, q.q, countLimit)
		if e != nil {
			return out, e
		}
		if in.TotalLimit > 0 && total > in.TotalLimit {
			total, out.TotalCapped = in.TotalLimit, true
		}
		out.Total = &total
		return out, nil
	})
}
func (s *Service) QueryGroups(ctx context.Context, subject, containerID string, in QueryRequest) (Page[QueryGroup], error) {
	ctx, err := s.matchQueryText(ctx, subject, containerID, in.Query.Search, in.Query.Archive.Default())
	if err != nil {
		return Page[QueryGroup]{}, err
	}
	return read(ctx, s, func(t *Service) (Page[QueryGroup], error) {
		q, e := t.compileQuery(ctx, subject, containerID, in, true)
		if e != nil {
			return Page[QueryGroup]{}, e
		}
		if groups, ok, e := t.countedGroups(ctx, subject, containerID, in, q.q.RankDef); e != nil || ok {
			return Page[QueryGroup]{Data: groups, SearchCapped: q.searchCapped}, e
		}
		out, e := t.queryGroupsCompiled(ctx, in, q)
		out.SearchCapped = q.searchCapped
		if e != nil || q.q.RankDef == nil || q.q.RankDef.Type != catalog.FieldTerm && q.q.RankDef.Type != catalog.FieldKeywords {
			return out, e
		}
		ids := make([]string, 0, len(out.Data))
		for _, g := range out.Data {
			if id, ok := g.Value.(string); ok {
				ids = append(ids, id)
			}
		}
		names, e := t.termNames(ctx, subject, ids)
		for i := range out.Data {
			if id, ok := out.Data[i].Value.(string); ok {
				out.Data[i].Label = names[id]
			}
		}
		return out, e
	})
}

func (s *Service) queryGroupsCompiled(ctx context.Context, in QueryRequest, q compiledQuery) (Page[QueryGroup], error) {
	return read(ctx, s, func(t *Service) (Page[QueryGroup], error) {
		var after *any
		if in.After != "" {
			_, values, e := cursorDecodeValues(q, in.After)
			if e != nil {
				return Page[QueryGroup]{}, e
			}
			after = &values[0]
		}
		rows, e := t.DAL.Content().Groups(ctx, q.q, after, pageSize(in.Limit)+1)
		if e != nil {
			return Page[QueryGroup]{}, e
		}
		groups := make([]QueryGroup, len(rows))
		for i, g := range rows {
			groups[i] = QueryGroup{Value: g.Value, Count: g.Count}
		}
		out := Page[QueryGroup]{Data: groups}
		if len(groups) > pageSize(in.Limit) {
			last := groups[pageSize(in.Limit)-1]
			out.NextCursor = cursorEncode(q, "", last.Value)
			out.Data = groups[:pageSize(in.Limit)]
		}
		// Integer/decimal group values are returned as exact strings, decimal units
		// converted back using the configured scale rather than float arithmetic.
		kind := q.q.Orders[0].Kind
		for i := range out.Data {
			g := &out.Data[i]
			if g.Value == nil {
				continue
			}
			switch kind {
			case content.KindInteger:
				n, ok := g.Value.(int64)
				if ok {
					value := strconv.FormatInt(n, 10)
					if d := q.q.RankDef; d != nil && d.Type == catalog.FieldDecimal {
						negative := n < 0
						digits := strings.TrimPrefix(value, "-")
						for len(digits) <= d.Scale {
							digits = "0" + digits
						}
						if d.Scale > 0 {
							digits = digits[:len(digits)-d.Scale] + "." + digits[len(digits)-d.Scale:]
						}
						if negative {
							digits = "-" + digits
						}
						value = digits
					}
					g.Value = value
				}
			case content.KindExact:
				key, ok := g.Value.(string)
				if !ok {
					return Page[QueryGroup]{}, invalid("invalid exact numeric group")
				}
				g.Value, e = numberFromKey(key)
				if e != nil {
					return Page[QueryGroup]{}, e
				}
			case content.KindBoolean:
				if n, ok := g.Value.(int64); ok {
					g.Value = n != 0
				}
			}
		}
		return out, nil
	})
}

// Used when saving views: compilation validates references and bounded input
// without evaluating content or granting the view owner additional access.
func (s *Service) validateQuery(ctx context.Context, subject, containerID string, spec QuerySpec) error {
	if _, e := s.compileQuery(ctx, subject, containerID, QueryRequest{Query: spec}, false); e != nil {
		return e
	}
	return nil
}

// termFilter validates a term condition. A value that holds a merged term
// counts as the term it was merged into; under also matches the
// descendants of a term.
func termFilter(d *catalog.FieldDefinition, field content.Field, op content.FilterOp, v any) (*content.Filter, error) {
	var ids []any
	switch op {
	case content.FilterEQ, content.FilterNE, content.FilterUnder:
		x, e := queryValue(d, v)
		if e != nil {
			return nil, e
		}
		ids = []any{x}
	case content.FilterIn:
		list, ok := v.([]any)
		if !ok || len(list) < 1 || len(list) > 50 {
			return nil, invalid("in requires 1..50 values")
		}
		for _, value := range list {
			x, e := queryValue(d, value)
			if e != nil {
				return nil, e
			}
			ids = append(ids, x)
		}
	default:
		return nil, invalid("term fields support eq, ne, in and under")
	}
	return &content.Filter{Cond: &content.Condition{Field: field, Op: op, Values: ids}}, nil
}

func querySelection(defs map[string]*catalog.FieldDefinition, selection []string) ([]string, bool, error) {
	if selection == nil {
		return nil, true, nil
	}
	if len(selection) > 50 {
		return nil, false, invalid("at most 50 selected fields")
	}
	fields := []string{}
	keywords := false
	seen := map[string]bool{}
	for _, key := range selection {
		if seen[key] || !builtinColumn(key) && defs[key] == nil {
			return nil, false, invalid("select must contain unique known fields")
		}
		seen[key] = true
		if key == "$keywords" {
			keywords = true
		} else if !builtinColumn(key) {
			fields = append(fields, key)
		}
	}
	return fields, keywords, nil
}
