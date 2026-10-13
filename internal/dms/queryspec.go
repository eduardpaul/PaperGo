package dms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/resource"
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
	ContentTypeID string      `json:"content_type_id,omitempty"`
	Filter        *FilterExpr `json:"filter,omitempty"`
	Sort          []SortSpec  `json:"sort,omitempty"`
	GroupBy       string      `json:"group_by,omitempty"`
	ParentID      string      `json:"parent_id,omitempty"`
	// AncestorID keeps what is anywhere below a folder: in it or its subfolders.
	AncestorID string `json:"ancestor_id,omitempty"`
	Search     string `json:"search,omitempty"`
	Tag        string `json:"tag,omitempty"`
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
}
type QueryResult struct {
	Data       []*ent.Resource `json:"value"`
	NextCursor string          `json:"-"`
	Total      *int            `json:"total,omitempty"`
	// TotalCapped means more than total items match (see TotalLimit).
	TotalCapped bool `json:"total_capped,omitempty"`
}
type QueryGroup struct {
	Value any `json:"value"`
	// Label names the term of a term or keywords group when the caller can
	// read the workspace's taxonomy.
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}
type compiledQuery struct {
	sql         string
	args        []any
	fingerprint string
	descending  bool
	kind        string
	// rankDef is the definition behind the rank column; nil for built-in columns.
	rankDef *ent.FieldDefinition
	// seek, set for ungrouped collection queries, pages from the sort index
	// instead of sorting every eligible row.
	seek        *seekPlan
	orders      []queryOrder
	projection  []string
	includeTags bool
}
type queryCursor struct {
	Fingerprint string            `json:"fingerprint"`
	ID          string            `json:"id,omitempty"`
	Values      []json.RawMessage `json:"values"`
}
type queryCompiler struct {
	defs  map[string]*ent.FieldDefinition
	nodes int
}

func queryTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

func systemField(key string) (string, *ent.FieldDefinition) {
	columns := map[string]string{"$id": "r.id", "$name": "p.name", "$created_at": "p.item_created_at", "$created_by": "p.item_created_by", "$modified_at": "p.modified_at", "$modified_by": "p.modified_by"}
	column, ok := columns[key]
	if !ok {
		return "", nil
	}
	typ := fielddefinition.TypeText
	if key == "$created_at" || key == "$modified_at" {
		typ = fielddefinition.TypeDatetime
	}
	return column, &ent.FieldDefinition{Type: typ}
}

func (q *queryCompiler) column(field string) (string, []any, string, error) {
	if column, d := systemField(field); d != nil {
		return column, nil, "text", nil
	}
	d := q.defs[field]
	if d == nil || !queryable(d) {
		return "", nil, "", unqueryable(field, d)
	}
	if d.Options.Multiple {
		return "", nil, "", invalid("sorting and grouping require a scalar field")
	}
	col, kind := indexColumn(d)
	return "(SELECT f." + col + " FROM field_values f WHERE f.surface_id=p.id AND f.field_key=? AND f.field_type=? AND f.scale=? AND f.ordinal=0)", []any{d.Key, string(d.Type), d.Scale}, kind, nil
}
func indexColumn(d *ent.FieldDefinition) (string, string) {
	switch string(d.Type) {
	case "integer", "decimal":
		return "value_integer", "integer"
	case "number", "currency":
		return "value_text", "exact"
	case "boolean":
		return "value_boolean", "boolean"
	default:
		return "value_text", "text"
	}
}
func queryValue(d *ent.FieldDefinition, v any) (any, error) {
	// Query literals must be representable, but current write constraints must
	// not prevent searching retained values from older schema revisions.
	queryDefinition := *d
	queryDefinition.Options.Minimum = nil
	queryDefinition.Options.Maximum = nil
	queryDefinition.Options.MaxLength = nil
	if queryDefinition.Type == "choice" {
		queryDefinition.Type = "text"
	}
	// Retained values can exceed today's write limits. Query representability
	// is separate from both configured and default text constraints.
	if _, ceiling := stringLimits(string(queryDefinition.Type)); ceiling > 0 {
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
func (q *queryCompiler) filter(f *FilterExpr, depth int) (string, []any, error) {
	q.nodes++
	if depth > 6 || q.nodes > 32 {
		return "", nil, invalid("filter is limited to 32 nodes and depth 6")
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
			return "", nil, invalid("filter node must be a group or a condition")
		}
		if f.Not != nil {
			text, args, e := q.filter(f.Not, depth+1)
			return "NOT (" + text + ")", args, e
		}
		list, op := f.And, " AND "
		if f.Or != nil {
			list = f.Or
			op = " OR "
		}
		if len(list) == 0 {
			return "", nil, invalid("filter groups cannot be empty")
		}
		parts := []string{}
		args := []any{}
		for _, x := range list {
			text, a, e := q.filter(&x, depth+1)
			if e != nil {
				return "", nil, e
			}
			parts = append(parts, "("+text+")")
			args = append(args, a...)
		}
		return strings.Join(parts, op), args, nil
	}
	op := f.Op
	if op == "" {
		op = "eq"
	}
	col, d := systemField(f.Field)
	builtin := d != nil
	if builtin {
		// System metadata belongs to the selected immutable content surface.
	} else if f.Field == "$tags" {
		if op != "eq" && op != "ne" {
			return "", nil, invalid("tags support eq and ne")
		}
		v, e := decodeValue(f.Value)
		if e != nil {
			return "", nil, invalid("invalid tag filter")
		}
		tag, ok := v.(string)
		if !ok {
			return "", nil, invalid("tag must be text")
		}
		text := "EXISTS (SELECT 1 FROM item_surface_tags WHERE surface_id=p.id AND tag=?)"
		if op == "ne" {
			text = "NOT (" + text + ")"
		}
		return text, []any{tag}, nil
	} else {
		d = q.defs[f.Field]
		if d == nil || !queryable(d) {
			return "", nil, unqueryable(f.Field, d)
		}
	}
	prefix := ""
	args := []any{}
	if !builtin {
		col, _ = indexColumn(d)
		prefix = "f.surface_id=p.id AND f.field_key=? AND f.field_type=? AND f.scale=?"
		args = []any{f.Field, string(d.Type), d.Scale}
	}
	if op == "missing" || op == "present" {
		if len(f.Value) > 0 {
			return "", nil, invalid("missing/present do not accept a value")
		}
		if builtin {
			if op == "missing" {
				return "0", nil, nil
			}
			return "1", nil, nil
		}
		text := "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + ")"
		if op == "missing" {
			text = "NOT (" + text + ")"
		}
		return text, args, nil
	}
	v, e := decodeValue(f.Value)
	if e != nil || v == nil {
		return "", nil, invalid("filter value is required")
	}
	if !builtin && (d.Type == "term" || d.Type == "keywords") {
		return termFilter(d, op, v, prefix, args)
	}
	if op == "under" {
		return "", nil, invalid("under requires a term or keywords field")
	}
	compare := ""
	bind := []any{}
	if op == "in" {
		list, ok := v.([]any)
		if !ok || len(list) < 1 || len(list) > 50 {
			return "", nil, invalid("in requires 1..50 values")
		}
		marks := []string{}
		for _, value := range list {
			x, e := queryValue(d, value)
			if e != nil {
				return "", nil, e
			}
			bind = append(bind, x)
			marks = append(marks, "?")
		}
		compare = col + " IN (" + strings.Join(marks, ",") + ")"
	} else {
		x, e := queryValue(d, v)
		if e != nil {
			return "", nil, e
		}
		bind = append(bind, x)
		ops := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}
		if op == "contains" {
			if d.Type != "text" && d.Type != "note" && d.Type != "email" && d.Type != "url" {
				return "", nil, invalid("contains requires text")
			}
			// Case-insensitive, as people type names in pickers and filters.
			compare = "instr(unicode_lower(" + col + "),unicode_lower(?))>0"
		} else {
			operator := ops[op]
			if operator == "" {
				return "", nil, invalid("unsupported filter operator")
			}
			if (d.Type == "boolean" || d.Type == "lookup" || d.Type == "choice") && op != "eq" && op != "ne" {
				return "", nil, invalid("field supports equality only")
			}
			compare = col + operator + "?"
		}
	}
	if builtin {
		return compare, bind, nil
	}
	// For a multi-valued field, ne means present with no equal member.
	if op == "ne" && d.Options.Multiple {
		return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + ") AND NOT EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + col + "=?)", append(append(append([]any{}, args...), args...), bind...), nil
	}
	return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + compare + ")", append(args, bind...), nil
}
func (s *Service) compileQuery(ctx context.Context, subject, containerID string, in QueryRequest, grouped bool) (compiledQuery, error) {
	c, e := s.authorize(ctx, subject, containerID, "read")
	if e != nil {
		return compiledQuery{}, e
	}
	if c.Kind != "list" && c.Kind != "library" {
		return compiledQuery{}, invalid("queries require a collection")
	}
	defs, e := s.Client.FieldDefinition.Query().Where(collectionFieldPredicate(c.ID)).All(ctx)
	if e != nil {
		return compiledQuery{}, e
	}
	return s.compileCollectionQuery(ctx, subject, c, defs, in, grouped)
}

// The caller has authorized c in the current snapshot. Smart folders load the
// collection catalog in batches and reuse this same typed SQL compiler.
func (s *Service) compileCollectionQuery(ctx context.Context, subject string, c *ent.Resource, defs []*ent.FieldDefinition, in QueryRequest, grouped bool) (compiledQuery, error) {
	if in.Surface == "" {
		in.Surface = "auto"
	}
	if in.Surface != "auto" && in.Surface != "head" && in.Surface != "published" {
		return compiledQuery{}, invalid("invalid query surface")
	}
	if len(in.Query.Search) > 256 || len(in.Query.Tag) > 64 {
		return compiledQuery{}, invalid("search or tag exceeds size limit")
	}
	resolved, e := resolveFilter(in.Query.Filter, subject, time.Now().UTC(), definitionMap(defs))
	if e != nil {
		return compiledQuery{}, e
	}
	in.Query.Filter = resolved
	compiler := queryCompiler{defs: map[string]*ent.FieldDefinition{}}
	for _, d := range defs {
		compiler.defs[d.Key] = d
	}
	if in.Query.ContentTypeID != "" {
		typ, e := s.contentType(ctx, c.ID, in.Query.ContentTypeID)
		if e != nil {
			return compiledQuery{}, e
		}
		compiler.defs = definitionMap(selectTypeDefinitions(defs, typ))
	}
	if len(in.Query.Sort) > 8 {
		return compiledQuery{}, invalid("at most 8 sort keys")
	}
	orders := append([]SortSpec{}, in.Query.Sort...)
	if len(orders) == 0 {
		orders = []SortSpec{{Field: "$id"}}
	}
	seen := map[string]bool{}
	for _, order := range orders {
		if order.Field == "" || seen[order.Field] {
			return compiledQuery{}, invalid("sort keys must be unique known fields")
		}
		seen[order.Field] = true
		if order.Direction != "" && order.Direction != "asc" && order.Direction != "desc" {
			return compiledQuery{}, invalid("sort direction must be asc or desc")
		}
		if _, _, _, err := compiler.column(order.Field); err != nil {
			return compiledQuery{}, err
		}
	}
	if in.Query.GroupBy != "" {
		if _, _, _, err := compiler.column(in.Query.GroupBy); err != nil {
			return compiledQuery{}, err
		}
	}
	if grouped {
		if in.Query.GroupBy == "" {
			return compiledQuery{}, invalid("group_by is required")
		}
		if in.Select != nil {
			return compiledQuery{}, invalid("select belongs to item queries")
		}
		orders = []SortSpec{{Field: in.Query.GroupBy}}
	}
	projection, includeTags, err := querySelection(compiler.defs, in.Select)
	if err != nil {
		return compiledQuery{}, err
	}
	rankField := orders[0].Field
	rankParts := []string{}
	rankArgs := []any{}
	compiledOrders := []queryOrder{}
	for i, order := range orders {
		column, bind, kind, err := compiler.column(order.Field)
		if err != nil {
			return compiledQuery{}, err
		}
		alias := sortAlias(i)
		rankParts = append(rankParts, column+" AS "+alias)
		rankArgs = append(rankArgs, bind...)
		compiledOrders = append(compiledOrders, queryOrder{kind: kind, descending: order.Direction == "desc"})
	}
	// Conditions reference only r and p, so the CTE below and the index-driven
	// pages of queryseek.go share them.
	text := ""
	args := []any{}
	if in.Query.ContentTypeID != "" {
		text += " AND r.content_type_id=?"
		args = append(args, in.Query.ContentTypeID)
	}
	if in.Query.ParentID != "" {
		parent, e := s.authorize(ctx, subject, in.Query.ParentID, "read")
		if e != nil {
			return compiledQuery{}, e
		}
		if parent.ID != c.ID && (parent.Kind != "folder" || parent.ContainerID == nil || *parent.ContainerID != c.ID) {
			return compiledQuery{}, invalid("parent_id is outside this collection")
		}
		text += " AND r.parent_id=?"
		args = append(args, parent.ID)
	}
	if in.Query.AncestorID != "" {
		if in.Query.ParentID != "" {
			return compiledQuery{}, invalid("use parent_id or ancestor_id, not both")
		}
		ancestor, e := s.authorize(ctx, subject, in.Query.AncestorID, "read")
		if e != nil {
			return compiledQuery{}, e
		}
		if ancestor.ID != c.ID && (ancestor.Kind != "folder" || ancestor.ContainerID == nil || *ancestor.ContainerID != c.ID) {
			return compiledQuery{}, invalid("ancestor_id is outside this collection")
		}
		// The collection itself is every item's ancestor.
		if ancestor.ID != c.ID {
			text += " AND r.parent_id IN (WITH RECURSIVE below(id) AS (SELECT ? UNION ALL SELECT f.id FROM resources f JOIN below ON f.parent_id=below.id WHERE f.kind='folder' AND f.deleted_at IS NULL) SELECT id FROM below)"
			args = append(args, ancestor.ID)
		}
	}
	if in.Query.Filter != nil {
		filter, a, e := compiler.filter(in.Query.Filter, 0)
		if e != nil {
			return compiledQuery{}, e
		}
		text += " AND (" + filter + ")"
		args = append(args, a...)
	}
	if in.Query.Tag != "" {
		text += " AND EXISTS(SELECT 1 FROM item_surface_tags WHERE surface_id=p.id AND tag=?)"
		args = append(args, in.Query.Tag)
	}
	if strings.TrimSpace(in.Query.Search) != "" {
		phrase := ftsPhrase(in.Query.Search)
		// FTS rowids are item_surfaces rowids; reading the UNINDEXED id column costs a content-table lookup per match.
		// The unary + keeps SQLite from probing the surface index once per match for every row.
		text += " AND +p.rowid IN (SELECT rowid FROM item_surface_search WHERE item_surface_search MATCH ?)"
		args = append(args, phrase)
	}
	conditions, conditionArgs := text, args
	var access *scopeAccess
	if !in.rowACL {
		if access, err = s.collectionScopes(ctx, c.ID, subject); err != nil {
			return compiledQuery{}, err
		}
	}
	if access != nil {
		// Permissions follow ACL scopes, and a collection has few: decide each
		// scope once instead of probing grants for every row.
		selected, readable, scopeArgs := access.sql(in.Surface)
		args = append(append(append([]any{}, rankArgs...), scopeArgs[0]...), c.ID)
		text = "SELECT r.id," + strings.Join(rankParts, ",") + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id=? AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND " + readable + conditions
		args = append(append(args, scopeArgs[1]...), conditionArgs...)
	} else {
		selected, surfaceArgs := surfaceSQL(ctx, "r.id", subject, in.Surface)
		args = append(append(append([]any{}, rankArgs...), surfaceArgs...), c.ID)
		text = "SELECT r.id," + strings.Join(rankParts, ",") + " FROM resources r JOIN item_surfaces p ON p.item_id=r.id AND p.surface=" + selected + " WHERE r.container_id=? AND r.kind IN ('item','folder')" + conditions
		args = append(args, conditionArgs...)
		// SQLite evaluates these terms in order: cheap index filters first, so the
		// ACL probe runs only on rows that already match.
		action := "read"
		if in.Surface == "head" {
			action = "read_draft"
		}
		acl, aclArgs := permissionSQL(ctx, "r.id", subject, action)
		text += " AND " + acl
		args = append(args, aclArgs...)
	}
	fingerprint, _ := json.Marshal(struct {
		Subject, Collection, Surface, Schema string
		Query                                QuerySpec
		Grouped                              bool
		Select                               []string
	}{subject, c.ID, in.Surface, *c.SchemaHeadID, in.Query, grouped, in.Select})
	sum := sha256.Sum256(fingerprint)
	q := compiledQuery{sql: text, args: args, fingerprint: hex.EncodeToString(sum[:]), descending: compiledOrders[0].descending, kind: compiledOrders[0].kind, rankDef: compiler.defs[rankField], orders: compiledOrders, projection: projection, includeTags: includeTags}
	if !grouped && len(compiledOrders) == 1 {
		column, _ := systemField(rankField)
		if column == "r.id" {
			column = "p.item_id"
		}
		q.seek = &seekPlan{collection: c.ID, subject: subject, surface: in.Surface, conditions: conditions, args: conditionArgs, column: column}
	}
	return q, nil
}
func cursorEncode(q compiledQuery, id string, value any) string {
	values := []any{value}
	if len(q.orders) > 1 {
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
	kinds := []string{}
	for _, order := range q.orders {
		kinds = append(kinds, order.kind)
	}
	if len(c.Values) != len(kinds) {
		return c, nil, invalid("cursor sort key count differs")
	}
	values := make([]any, len(kinds))
	for i, kind := range kinds {
		v, e := decodeValue(c.Values[i])
		if e != nil {
			return c, nil, invalid("invalid cursor value")
		}
		if v != nil {
			switch kind {
			case "integer", "boolean":
				v, e = integer(v)
			case "exact":
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
func cursorDecode(q compiledQuery, after string) (queryCursor, any, error) {
	c, values, err := cursorDecodeValues(q, after)
	if err != nil {
		return c, nil, err
	}
	return c, values[0], nil
}
func keyset(q compiledQuery, after string, grouped bool) (string, []any, error) {
	if after == "" {
		return "", nil, nil
	}
	c, values, err := cursorDecodeValues(q, after)
	if err != nil {
		return "", nil, err
	}
	if grouped {
		if values[0] == nil {
			return " WHERE 0", nil, nil
		}
		return " WHERE sort_value IS NULL OR sort_value>? ", []any{values[0]}, nil
	}
	if c.ID == "" {
		return "", nil, invalid("cursor has no item ID")
	}
	conditions := []string{}
	bind := []any{}
	equal := []string{}
	equalArgs := []any{}
	for i, order := range q.orders {
		alias := sortAlias(i)
		v := values[i]
		op := ">"
		if order.descending {
			op = "<"
		}
		if v != nil {
			parts := append(append([]string{}, equal...), "("+alias+" IS NULL OR "+alias+op+"?)")
			conditions = append(conditions, "("+strings.Join(parts, " AND ")+")")
			bind = append(bind, equalArgs...)
			bind = append(bind, v)
		}
		equal = append(equal, alias+" IS ?")
		equalArgs = append(equalArgs, v)
	}
	op := ">"
	if q.descending {
		op = "<"
	}
	conditions = append(conditions, "("+strings.Join(append(equal, "id"+op+"?"), " AND ")+")")
	bind = append(bind, equalArgs...)
	bind = append(bind, c.ID)
	return " WHERE " + strings.Join(conditions, " OR "), bind, nil
}
func (s *Service) Query(ctx context.Context, subject, containerID string, in QueryRequest) (QueryResult, error) {
	if in.Surface == "" {
		in.Surface = "auto"
	}
	return read(ctx, s, func(t *Service) (QueryResult, error) {
		q, e := t.compileQuery(ctx, subject, containerID, in, false)
		if e != nil {
			return QueryResult{}, e
		}
		out, err := t.queryCompiled(ctx, subject, in, q, q.projection)
		if in.Select != nil && !q.includeTags {
			for _, r := range out.Data {
				r.Tags = []string{}
			}
		}
		return out, err
	})
}

// resourceSummaryColumns are every resources column but values.
var resourceSummaryColumns = func() []string {
	columns := []string{}
	for _, c := range resource.Columns {
		if c != resource.FieldValues {
			columns = append(columns, c)
		}
	}
	return columns
}()

// page selects one keyset page of IDs and sort values; where is keyset's clause.
// Missing values sort last; ID ties follow the sort direction, as in queryseek.go.
func (q compiledQuery) page(where string) string {
	columns := []string{}
	ordering := []string{}
	for i, order := range q.orders {
		alias := sortAlias(i)
		direction := "ASC"
		if order.descending {
			direction = "DESC"
		}
		columns = append(columns, alias)
		ordering = append(ordering, "("+alias+" IS NULL) ASC", alias+" "+direction)
	}
	direction := "ASC"
	if q.descending {
		direction = "DESC"
	}
	ordering = append(ordering, "id "+direction)
	return "WITH eligible AS (" + q.sql + ") SELECT id," + strings.Join(columns, ",") + " FROM eligible" + where + " ORDER BY " + strings.Join(ordering, ",") + " LIMIT ?"
}

// eligiblePage sorts every eligible row of the CTE. Smart folders, which
// union several collections, page this way.
func (s *Service) eligiblePage(ctx context.Context, q compiledQuery, after string, n int) ([]rankedID, error) {
	where, afterArgs, e := keyset(q, after, false)
	if e != nil {
		return nil, e
	}
	args := append(append([]any{}, q.args...), afterArgs...)
	return s.queryRankedIDs(ctx, q, q.page(where), append(args, n)...)
}

// rankedIDs reads (id, sort_value) rows.
func (s *Service) rankedIDs(ctx context.Context, query string, args ...any) ([]rankedID, error) {
	rows, e := s.Client.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []rankedID{}
	for rows.Next() {
		var r rankedID
		if e = rows.Scan(&r.id, &r.rank); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queryCompiled returns one page of q. A non-nil fields projects item values
// to those keys (see overlayPage); nil returns complete values.
func (s *Service) queryCompiled(ctx context.Context, subject string, in QueryRequest, q compiledQuery, fields []string) (QueryResult, error) {
	if in.TotalLimit < 0 || in.TotalLimit > 1_000_000 {
		return QueryResult{}, invalid("total_limit must be between 1 and 1000000")
	}
	return read(ctx, s, func(t *Service) (QueryResult, error) {
		limit := pageSize(in.Limit)
		var page []rankedID
		var e error
		if q.seek != nil {
			page, e = t.seekPage(ctx, q, in.After, limit+1)
		} else {
			page, e = t.eligiblePage(ctx, q, in.After, limit+1)
		}
		if e != nil {
			return QueryResult{}, e
		}
		ids := make([]string, len(page))
		ranks := make([]any, len(page))
		for i, r := range page {
			ids[i], ranks[i] = r.id, r.rank
		}
		out := QueryResult{Data: []*ent.Resource{}}
		if len(ids) > limit {
			out.NextCursor = cursorEncode(q, ids[limit-1], ranks[limit-1])
			ids = ids[:limit]
		}
		if len(ids) > 0 {
			// Item values come from the selected surface, so the resource's copy
			// of the head values is never loaded.
			resources, e := t.Client.Resource.Query().Where(resource.IDIn(ids...)).Select(resourceSummaryColumns...).All(ctx)
			if e != nil {
				return out, e
			}
			lookup := map[string]*ent.Resource{}
			for _, r := range resources {
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
		var total int
		if in.TotalLimit > 0 {
			args := append(append([]any{}, q.args...), in.TotalLimit+1)
			if e = t.scan(ctx, []any{&total}, "SELECT count(*) FROM (SELECT 1 FROM ("+q.sql+") LIMIT ?)", args...); e != nil {
				return out, e
			}
			if total > in.TotalLimit {
				total, out.TotalCapped = in.TotalLimit, true
			}
		} else if e = t.scan(ctx, []any{&total}, "WITH eligible AS ("+q.sql+") SELECT count(*) FROM eligible", q.args...); e != nil {
			return out, e
		}
		out.Total = &total
		return out, nil
	})
}
func (s *Service) QueryGroups(ctx context.Context, subject, containerID string, in QueryRequest) (Page[QueryGroup], error) {
	return read(ctx, s, func(t *Service) (Page[QueryGroup], error) {
		q, e := t.compileQuery(ctx, subject, containerID, in, true)
		if e != nil {
			return Page[QueryGroup]{}, e
		}
		out, e := t.queryGroupsCompiled(ctx, in, q)
		if e != nil || q.rankDef == nil || q.rankDef.Type != "term" && q.rankDef.Type != "keywords" {
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
		where, afterArgs, e := keyset(q, in.After, true)
		if e != nil {
			return Page[QueryGroup]{}, e
		}
		args := append(append([]any{}, q.args...), afterArgs...)
		args = append(args, pageSize(in.Limit)+1)
		rows, e := t.Client.QueryContext(ctx, "WITH eligible AS ("+q.sql+"),grouped AS (SELECT sort_value,count(*) n FROM eligible GROUP BY sort_value) SELECT sort_value,n FROM grouped"+where+" ORDER BY (sort_value IS NULL) ASC,sort_value ASC LIMIT ?", args...)
		if e != nil {
			return Page[QueryGroup]{}, e
		}
		groups := []QueryGroup{}
		for rows.Next() {
			var g QueryGroup
			if e = rows.Scan(&g.Value, &g.Count); e != nil {
				break
			}
			groups = append(groups, g)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return Page[QueryGroup]{}, e
		}
		out := Page[QueryGroup]{Data: groups}
		if len(groups) > pageSize(in.Limit) {
			last := groups[pageSize(in.Limit)-1]
			out.NextCursor = cursorEncode(q, "", last.Value)
			out.Data = groups[:pageSize(in.Limit)]
		}
		// Integer/decimal group values are returned as exact strings, decimal units
		// converted back using the configured scale rather than float arithmetic.
		for i := range out.Data {
			g := &out.Data[i]
			if g.Value == nil {
				continue
			}
			if q.kind == "integer" {
				n, ok := g.Value.(int64)
				if ok {
					value := strconv.FormatInt(n, 10)
					if d := q.rankDef; d != nil && d.Type == "decimal" {
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
			} else if q.kind == "exact" {
				key, ok := g.Value.(string)
				if !ok {
					return Page[QueryGroup]{}, invalid("invalid exact numeric group")
				}
				g.Value, e = numberFromKey(key)
				if e != nil {
					return Page[QueryGroup]{}, e
				}
			} else if q.kind == "boolean" {
				if n, ok := g.Value.(int64); ok {
					g.Value = n != 0
				}
			}
		}
		return out, nil
	})
}

// Used when saving views: SQL compilation validates references and bounded input
// without evaluating content or granting the view owner additional access.
func (s *Service) validateQuery(ctx context.Context, subject, containerID string, spec QuerySpec) error {
	if _, e := s.compileQuery(ctx, subject, containerID, QueryRequest{Query: spec}, false); e != nil {
		return e
	}
	return nil
}

// termFilter matches term values. A value that holds a merged term counts as
// the term it was merged into; under also matches the descendants of a term.
func termFilter(d *ent.FieldDefinition, op string, v any, prefix string, args []any) (string, []any, error) {
	var ids []any
	switch op {
	case "eq", "ne", "under":
		x, e := queryValue(d, v)
		if e != nil {
			return "", nil, e
		}
		ids = []any{x}
	case "in":
		list, ok := v.([]any)
		if !ok || len(list) < 1 || len(list) > 50 {
			return "", nil, invalid("in requires 1..50 values")
		}
		for _, value := range list {
			x, e := queryValue(d, value)
			if e != nil {
				return "", nil, e
			}
			ids = append(ids, x)
		}
	default:
		return "", nil, invalid("term fields support eq, ne, in and under")
	}
	var set string
	var bind []any
	if op == "under" {
		// The subtree of r is one range of the path index.
		subtree := "terms r JOIN terms t ON t.path>=r.path AND t.path<substr(r.path,1,length(r.path)-1)||'0' WHERE r.id=?"
		set = "SELECT t.id FROM " + subtree + " UNION ALL SELECT m.id FROM terms m WHERE m.merged_into_id IN (SELECT t.id FROM " + subtree + ")"
		bind = []any{ids[0], ids[0]}
	} else {
		marks := strings.TrimPrefix(strings.Repeat(",?", len(ids)), ",")
		set = "SELECT id FROM terms WHERE id IN (" + marks + ") UNION ALL SELECT id FROM terms WHERE merged_into_id IN (" + marks + ")"
		bind = append(append([]any{}, ids...), ids...)
	}
	member := "f.value_text IN (" + set + ")"
	if op == "ne" {
		return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + ") AND NOT EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + member + ")", append(append(append([]any{}, args...), args...), bind...), nil
	}
	return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + member + ")", append(args, bind...), nil
}
