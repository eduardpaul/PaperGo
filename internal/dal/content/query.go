package content

import (
	"encoding/json"
	"strconv"
	"strings"

	"papergo/internal/dal/access"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/refs"
)

// Kind is how a sort column's values compare.
type Kind string

const (
	KindText    Kind = "text"
	KindTime    Kind = "time"
	KindInteger Kind = "integer"
	// KindExact compares the order-preserving text keys of exact numbers.
	KindExact   Kind = "exact"
	KindBoolean Kind = "boolean"
)

// systemFields maps the built-in fields of an item surface p and its
// resource r to their columns.
var systemFields = map[string]struct {
	column string
	typ    catalog.FieldType
}{
	"$archived_at": {"r.archived_at", catalog.FieldDatetime},
	"$archived_by": {"r.archived_by", catalog.FieldText},
	"$file_type":   {"(SELECT b.content_type FROM item_revisions fr JOIN blobs b ON b.id=fr.blob_id WHERE fr.id=p.revision_id)", catalog.FieldChoice},
	"$id":          {"r.id", catalog.FieldText},
	"$name":        {"p.name", catalog.FieldText},
	"$created_at":  {"p.item_created_at", catalog.FieldDatetime},
	"$created_by":  {"p.item_created_by", catalog.FieldText},
	"$modified_at": {"p.modified_at", catalog.FieldDatetime},
	"$modified_by": {"p.modified_by", catalog.FieldText},
}

// SystemField reports the type of a built-in field.
func SystemField(key string) (catalog.FieldType, bool) {
	f, ok := systemFields[key]
	return f.typ, ok
}

// IndexColumn is the field_values column a custom field is indexed in and
// how its values compare.
func IndexColumn(d *catalog.FieldDefinition) (ValueColumn, Kind) {
	switch d.Type {
	case catalog.FieldInteger, catalog.FieldDecimal:
		return ValueInteger, KindInteger
	case catalog.FieldNumber, catalog.FieldCurrency:
		return ValueText, KindExact
	case catalog.FieldBoolean:
		return ValueBoolean, KindBoolean
	default:
		return ValueText, KindText
	}
}

// Field names a built-in field by key, or a custom field by its definition.
type Field struct {
	Key string
	Def *catalog.FieldDefinition
}

// Filter is a validated filter tree: exactly one of And, Or, Not and Cond.
type Filter struct {
	And, Or []Filter
	Not     *Filter
	Cond    *Condition
}

// FilterOp compares a field with values.
type FilterOp string

const (
	FilterEQ       FilterOp = "eq"
	FilterNE       FilterOp = "ne"
	FilterGT       FilterOp = "gt"
	FilterGTE      FilterOp = "gte"
	FilterLT       FilterOp = "lt"
	FilterLTE      FilterOp = "lte"
	FilterIn       FilterOp = "in"
	FilterContains FilterOp = "contains"
	FilterMissing  FilterOp = "missing"
	FilterPresent  FilterOp = "present"
	// FilterUnder matches a term or any term below it.
	FilterUnder FilterOp = "under"
)

var comparisons = map[FilterOp]string{FilterEQ: "=", FilterNE: "<>", FilterGT: ">", FilterGTE: ">=", FilterLT: "<", FilterLTE: "<="}

// Condition compares a field with literals already converted to their
// indexed representation. Keyword conditions use the field $keywords and
// one canonical keyword ID.
type Condition struct {
	Field  Field
	Op     FilterOp
	Values []any
}

// Order sorts by a field.
type Order struct {
	Field      Field
	Descending bool
}

// OrderInfo is how a compiled query sorts by one of its keys.
type OrderInfo struct {
	Kind       Kind
	Descending bool
}

// GroupBy groups by a field, or by the year or month of a date field.
type GroupBy struct {
	Field Field
	// By is year, month or empty.
	By string
}

// PathStep keeps the items whose group column holds Value, or no value
// when Value is nil.
type PathStep struct {
	Group GroupBy
	Value any
}

// TermRoot matches a term and what lies below it or was merged into those.
type TermRoot struct {
	ID, SetID string
	// Keyword also matches keyword fields and an item's own keywords.
	Keyword bool
}

// TermMatch keeps the items holding all, or with Any any, of Roots.
type TermMatch struct {
	Roots []TermRoot
	Any   bool
}

// Plan is a query over one collection's items and folders, read by Reader
// on Surface: head, published or auto. The caller has authorized the
// collection, parent and ancestor and validated every field.
type Plan struct {
	Archive      refs.ArchiveSelection
	Reader       access.Principal
	CollectionID string
	// Publishes is whether the collection keeps a head surface apart from
	// the published one.
	Publishes     bool
	Surface       string
	ContentTypeID string
	ParentID      string
	AncestorID    string
	Filter        *Filter
	Keyword       string
	Search        *SearchMatch
	Orders        []Order
	// Group sorts by a group column instead of Orders, for group counts.
	Group *GroupBy
	// RowACL checks permissions row by row, so queries over several
	// collections share one statement shape.
	RowACL bool
	// ItemsOnly leaves folders out; ContentTypes keeps items of these types.
	ItemsOnly    bool
	ContentTypes []string
	Terms        *TermMatch
	Path         []PathStep
}

// Query is a compiled plan: its eligible rows with their sort values.
type Query struct {
	Orders []OrderInfo
	// RankDef is the definition behind the first sort column; nil for
	// built-in columns.
	RankDef *catalog.FieldDefinition

	reader     access.Principal
	collection string
	surface    string
	publishes  bool
	rowACL     bool
	ranks      string
	rankArgs   []any
	conds      string
	condArgs   []any
	seek       *seekPlan
	// sql and args are the eligible rows once decided: from the start for
	// row-checked and union queries, on first use for collection queries.
	sql   string
	args  []any
	built bool
}

// Descending reports whether the first sort key descends.
func (q *Query) Descending() bool { return q.Orders[0].Descending }

// Shape identifies what a query selects apart from its collection and
// reader, so equal shapes of several collections share one statement.
func (q *Query) Shape() string {
	raw, _ := json.Marshal(struct {
		Ranks, Conds       string
		RankArgs, CondArgs []any
		Surface            string
	}{q.ranks, q.conds, q.rankArgs, q.condArgs, q.surface})
	return string(raw)
}

func sortAlias(i int) string {
	if i == 0 {
		return "sort_value"
	}
	return "sort_value_" + strconv.Itoa(i)
}

// column is the SQL of a field's scalar value on surface p.
func (r *Repo) column(f Field) (string, []any, Kind) {
	if s, ok := systemFields[f.Key]; ok && f.Def == nil {
		kind := KindText
		if s.typ == catalog.FieldDatetime {
			kind = KindTime
		}
		return s.column, nil, kind
	}
	col, kind := IndexColumn(f.Def)
	return "(SELECT f." + string(col) + " FROM field_values f WHERE f.surface_id=p.id AND f.field_key=? AND f.field_type=? AND f.scale=? AND f.ordinal=0)", []any{f.Def.Key, string(f.Def.Type), f.Def.Scale}, kind
}

// groupColumn is the SQL of a group column; a field the collection lacks
// groups as missing.
func (r *Repo) groupColumn(g GroupBy) (string, []any, Kind) {
	if g.Field.Def == nil {
		if _, ok := systemFields[g.Field.Key]; !ok {
			return "NULL", nil, KindText
		}
	}
	column, args, kind := r.column(g.Field)
	if g.By != "" {
		size := 4
		if g.By == "month" {
			size = 7
		}
		column, kind = r.d.Prefix(column, size, kind == KindTime), KindText
	}
	return column, args, kind
}

// filter is the SQL of a filter tree over r and p.
func (r *Repo) filter(f *Filter) (string, []any) {
	switch {
	case f.Not != nil:
		text, args := r.filter(f.Not)
		return "NOT (" + text + ")", args
	case f.And != nil || f.Or != nil:
		list, op := f.And, " AND "
		if f.Or != nil {
			list, op = f.Or, " OR "
		}
		parts := []string{}
		args := []any{}
		for i := range list {
			text, a := r.filter(&list[i])
			parts = append(parts, "("+text+")")
			args = append(args, a...)
		}
		return strings.Join(parts, op), args
	}
	return r.condition(f.Cond)
}

func marks(n int) string { return strings.TrimPrefix(strings.Repeat(",?", n), ",") }

func (r *Repo) condition(c *Condition) (string, []any) {
	if c.Field.Key == "$keywords" && c.Field.Def == nil && (c.Op == FilterMissing || c.Op == FilterPresent) {
		text := "(EXISTS(SELECT 1 FROM item_surface_keywords k WHERE k.surface_id=p.id) OR EXISTS(SELECT 1 FROM field_values f WHERE f.surface_id=p.id AND " + keywordValue + "))"
		if c.Op == FilterMissing {
			text = "NOT " + text
		}
		return text, nil
	}
	if c.Field.Key == "$keywords" && c.Field.Def == nil {
		keyword := c.Values[0]
		text := "p.id IN (" + r.keywordSurfaces("coalesce((SELECT merged_into_id FROM terms WHERE id=?),?)") + ")"
		if c.Op == FilterNE {
			text = "NOT (" + text + ")"
		}
		return text, []any{keyword, keyword, keyword, keyword}
	}
	d := c.Field.Def
	builtin := d == nil
	col := ""
	prefix := ""
	args := []any{}
	if builtin {
		col = systemFields[c.Field.Key].column
	} else {
		column, _ := IndexColumn(d)
		col = "f." + string(column)
		prefix = "f.surface_id=p.id AND f.field_key=? AND f.field_type=? AND f.scale=?"
		args = []any{d.Key, string(d.Type), d.Scale}
	}
	switch c.Op {
	case FilterMissing, FilterPresent:
		if builtin {
			if c.Op == FilterMissing {
				return "FALSE", nil
			}
			return "TRUE", nil
		}
		text := "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + ")"
		if c.Op == FilterMissing {
			text = "NOT (" + text + ")"
		}
		return text, args
	}
	if !builtin && (d.Type == catalog.FieldTerm || d.Type == catalog.FieldKeywords) {
		return r.termCondition(c, prefix, args)
	}
	var compare string
	switch c.Op {
	case FilterIn:
		compare = col + " IN (" + marks(len(c.Values)) + ")"
	case FilterContains:
		// Case-insensitive, as people type names in pickers and filters.
		compare = r.d.Contains(r.d.Fold(col), r.d.Fold("?"))
	default:
		compare = col + comparisons[c.Op] + "?"
	}
	if builtin {
		return compare, c.Values
	}
	// For a multi-valued field, ne means present with no equal member.
	if c.Op == FilterNE && d.Options.Multiple {
		return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + ") AND NOT EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + col + "=?)", append(append(append([]any{}, args...), args...), c.Values...)
	}
	return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + compare + ")", append(args, c.Values...)
}

// termCondition matches term values. A value holding a merged term counts as
// the term it was merged into; under also matches the descendants of a term,
// and so does every comparison on a field whose values apply their parents.
func (r *Repo) termCondition(c *Condition, prefix string, args []any) (string, []any) {
	var set string
	var bind []any
	if c.Op == FilterUnder || c.Field.Def.Options.ApplyParents {
		// The subtree of r is one range of the path index.
		m := marks(len(c.Values))
		subtree := "terms r JOIN terms t ON " + r.d.Below("t.path", "r.path") + " WHERE r.id IN (" + m + ")"
		set = "SELECT t.id FROM " + subtree + " UNION ALL SELECT m.id FROM terms m WHERE m.merged_into_id IN (SELECT t.id FROM " + subtree + ")"
		bind = append(append([]any{}, c.Values...), c.Values...)
	} else {
		m := marks(len(c.Values))
		set = "SELECT id FROM terms WHERE id IN (" + m + ") UNION ALL SELECT id FROM terms WHERE merged_into_id IN (" + m + ")"
		bind = append(append([]any{}, c.Values...), c.Values...)
	}
	member := "f.value_text IN (" + set + ")"
	if c.Op == FilterNE {
		return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + ") AND NOT EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + member + ")", append(append(append([]any{}, args...), args...), bind...)
	}
	return "EXISTS (SELECT 1 FROM field_values f WHERE " + prefix + " AND " + member + ")", append(args, bind...)
}

// Compile builds a plan's statements.
func (r *Repo) Compile(p Plan) *Query {
	q := &Query{reader: p.Reader, collection: p.CollectionID, surface: p.Surface, publishes: p.Publishes, rowACL: p.RowACL}
	rankParts := []string{}
	if p.Group != nil {
		column, args, kind := r.groupColumn(*p.Group)
		rankParts = append(rankParts, column+" AS sort_value")
		q.rankArgs = append(q.rankArgs, args...)
		q.Orders = []OrderInfo{{Kind: kind}}
		if column != "NULL" && p.Group.By == "" {
			q.RankDef = p.Group.Field.Def
		}
	} else {
		for i, o := range p.Orders {
			column, args, kind := r.column(o.Field)
			rankParts = append(rankParts, column+" AS "+sortAlias(i))
			q.rankArgs = append(q.rankArgs, args...)
			q.Orders = append(q.Orders, OrderInfo{Kind: kind, Descending: o.Descending})
		}
		q.RankDef = p.Orders[0].Field.Def
	}
	q.ranks = strings.Join(rankParts, ",")
	// Conditions reference only r and p, so the eligible rows and the
	// index-driven pages of seek.go share them.
	var conds strings.Builder
	add := func(text string, args ...any) {
		conds.WriteString(" AND " + text)
		q.condArgs = append(q.condArgs, args...)
	}
	add(p.Archive.Predicate("r"))
	if p.ContentTypeID != "" {
		add("r.content_type_id=?", p.ContentTypeID)
	}
	if p.ParentID != "" {
		add("r.parent_id=?", p.ParentID)
	}
	if p.AncestorID != "" {
		add("r.parent_id IN (WITH RECURSIVE below(id) AS (SELECT id FROM resources WHERE id=? UNION ALL SELECT f.id FROM resources f JOIN below ON f.parent_id=below.id WHERE f.kind='folder' AND f.deleted_at IS NULL) SELECT id FROM below)", p.AncestorID)
	}
	if p.Filter != nil {
		text, args := r.filter(p.Filter)
		add("("+text+")", args...)
	}
	if p.Keyword != "" {
		add("p.id IN ("+r.keywordSurfaces("coalesce((SELECT merged_into_id FROM terms WHERE id=?),?)")+")", p.Keyword, p.Keyword, p.Keyword, p.Keyword)
	}
	if p.Search != nil && p.Search.ByName {
		// p is the surface the reader reads: match its name.
		add(r.d.Contains("p.folded_name", "?"), p.Search.Name)
	} else if p.Search != nil {
		head, _ := json.Marshal(nonNil(p.Search.Head))
		published, _ := json.Marshal(nonNil(p.Search.Published))
		// p is the surface the reader reads: match on that surface.
		add("p.item_id IN (SELECT value FROM "+r.d.Elements("CASE p.surface WHEN 'head' THEN "+r.d.JSONArg()+" ELSE "+r.d.JSONArg()+" END", "matched")+")", string(head), string(published))
	}
	if p.ItemsOnly {
		add("r.kind='item'")
	}
	if len(p.ContentTypes) > 0 {
		args := make([]any, len(p.ContentTypes))
		for i, id := range p.ContentTypes {
			args[i] = id
		}
		add("r.content_type_id IN ("+marks(len(args))+")", args...)
	}
	if p.Terms != nil {
		text, args := r.terms(*p.Terms)
		add(text, args...)
	}
	for _, step := range p.Path {
		column, args, _ := r.groupColumn(step.Group)
		if step.Value == nil {
			add("("+column+" IS NULL)", args...)
		} else {
			add("("+column+"=?)", append(args, step.Value)...)
		}
	}
	q.conds = conds.String()
	if p.RowACL {
		q.sql, q.args = r.rows(q)
		q.built = true
	}
	if p.Group == nil && len(p.Orders) == 1 && !p.RowACL {
		column := ""
		if s, ok := systemFields[p.Orders[0].Field.Key]; ok && p.Orders[0].Field.Def == nil {
			column = s.column
			if column == "r.id" {
				column = "p.item_id"
			}
		}
		q.seek = &seekPlan{column: column}
	}
	return q
}

// terms matches indexed term values and keywords through the smart_terms
// table of the union, which holds each root's subtree and merged terms.
func (r *Repo) terms(m TermMatch) (string, []any) {
	parts := []string{}
	args := []any{}
	for _, root := range m.Roots {
		fields := "fd.type='term' AND " + r.d.JSONText("fd.options", "term_set_id") + "=?"
		if root.Keyword {
			fields = "(fd.type='keywords' OR " + fields + ")"
		}
		part := `EXISTS(SELECT 1 FROM field_values f JOIN field_definitions fd ON fd.key=f.field_key JOIN collection_types ca ON ca.container_id=f.container_id JOIN content_types ct ON ct.id=ca.content_type_id AND EXISTS(SELECT 1 FROM ` + r.d.Elements("ct.field_ids", "fid") + ` WHERE fid.value=fd.id) JOIN smart_terms st ON st.id=f.value_text WHERE f.surface_id=p.id AND fd.indexed AND ` + fields + ` AND st.root=?)`
		args = append(args, root.SetID, root.ID)
		if root.Keyword {
			part = "(" + part + " OR EXISTS(SELECT 1 FROM item_surface_keywords k JOIN smart_terms st ON st.id=k.keyword WHERE k.surface_id=p.id AND st.root=?))"
			args = append(args, root.ID)
		}
		parts = append(parts, part)
	}
	operator := " AND "
	if m.Any {
		operator = " OR "
	}
	return "(" + strings.Join(parts, operator) + ")", args
}

// Union combines the row-checked queries of several collections into one,
// sorted as the first. Queries of one shape share a statement listing their
// collections. Roots are the terms their term matches select.
func (r *Repo) Union(queries []*Query, roots []string) *Query {
	if len(queries) == 0 {
		return &Query{Orders: []OrderInfo{{Kind: KindText}}, sql: "SELECT NULL AS id,NULL AS sort_value WHERE FALSE", built: true}
	}
	type branch struct {
		q           *Query
		collections []any
	}
	branches := []*branch{}
	byShape := map[string]*branch{}
	for _, q := range queries {
		key := q.Shape()
		if b := byShape[key]; b != nil {
			b.collections = append(b.collections, q.collection)
			continue
		}
		b := &branch{q, []any{q.collection}}
		byShape[key] = b
		branches = append(branches, b)
	}
	parts := []string{}
	args := []any{}
	for _, b := range branches {
		text, a := r.rowsIn(b.q, b.collections)
		parts = append(parts, text)
		args = append(args, a...)
	}
	text := strings.Join(parts, " UNION ALL ")
	if len(roots) > 0 {
		rootArgs := make([]any, len(roots))
		for i, id := range roots {
			rootArgs[i] = id
		}
		subtrees := "SELECT r.id AS root,t.id FROM terms r JOIN terms t ON " + r.d.Below("t.path", "r.path") + " WHERE r.id IN (" + marks(len(roots)) + ")"
		text = "WITH smart_subtrees(root,id) AS (" + subtrees + "),smart_terms(root,id) AS (SELECT root,id FROM smart_subtrees UNION ALL SELECT s.root,m.id FROM smart_subtrees s JOIN terms m ON m.merged_into_id=s.id) SELECT * FROM (" + text + ") AS smart_rows"
		args = append(rootArgs, args...)
	}
	first := queries[0]
	return &Query{Orders: first.Orders, RankDef: first.RankDef, sql: text, args: args, built: true}
}

// Arguments counts the statement arguments of a built query.
func (q *Query) Arguments() int { return len(q.args) }

// Sorted returns q sorting its eligible rows on every page instead of
// paging a sort index, which pages the same rows.
func (q *Query) Sorted() *Query {
	sorted := *q
	sorted.seek = nil
	return &sorted
}
