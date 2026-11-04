package dms

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
)

// AggregateOp is what an aggregate works out of a field's values.
type AggregateOp string

const (
	AggregateCount AggregateOp = "count"
	AggregateSum   AggregateOp = "sum"
	AggregateAvg   AggregateOp = "avg"
	AggregateMin   AggregateOp = "min"
	AggregateMax   AggregateOp = "max"
)

// AggregateInterval groups a date or date-time field by the period that
// holds its value.
type AggregateInterval string

const (
	IntervalDay   AggregateInterval = "day"
	IntervalWeek  AggregateInterval = "week"
	IntervalMonth AggregateInterval = "month"
	IntervalYear  AggregateInterval = "year"
)

// Aggregate is one figure of each group: Op of Field's values, named As
// (op_field by default).
type Aggregate struct {
	Op    AggregateOp `json:"op"`
	Field string      `json:"field"`
	As    string      `json:"as,omitempty"`
}

// AggregateGroupBy is one field of a group's key: its values, or for a date
// or date-time field the period of Interval holding them, in TimeZone for
// date-times.
type AggregateGroupBy struct {
	Field    string            `json:"field"`
	Interval AggregateInterval `json:"interval,omitempty"`
	TimeZone string            `json:"time_zone,omitempty"`
}

// AggregateSpec is what aggregates of any set of items ask for: figures of
// all of them, or in groups by the values of Groups.
type AggregateSpec struct {
	Groups     []AggregateGroupBy `json:"groups,omitempty"`
	Aggregates []Aggregate        `json:"aggregates"`
	// Sort orders the groups by a figure's name, $count or a field of Groups,
	// then by their keys.
	Sort  []SortSpec `json:"sort,omitempty"`
	After string     `json:"-"`
	Limit int        `json:"-"`
}

// AggregateRequest asks for figures of the items a collection query matches.
type AggregateRequest struct {
	Query   QuerySpec `json:"query"`
	Surface string    `json:"surface,omitempty"`
	AggregateSpec
}

// ViewAggregateRequest asks for figures of the items a saved view lists.
// Selected names the terms a navigation view lists the items of.
type ViewAggregateRequest struct {
	Surface  string                `json:"surface,omitempty"`
	Selected []NavigationSelection `json:"selected,omitempty"`
	AggregateSpec
}

// SmartFolderAggregateRequest asks for figures of the items at a path of a
// smart folder.
type SmartFolderAggregateRequest struct {
	Path    []*string `json:"path,omitempty"`
	Surface string    `json:"surface,omitempty"`
	AggregateSpec
}

// AggregateKey is a group's value of one of the request's groups. Label
// names the term of a term or keywords field when the caller can read the
// workspace's taxonomy.
type AggregateKey struct {
	Value any    `json:"value"`
	Label string `json:"label,omitempty"`
}

// AggregateGroup is a group's key, one entry for each of the request's
// groups; its items; and its figures, by name.
type AggregateGroup struct {
	Keys   []AggregateKey `json:"keys"`
	Count  int            `json:"count"`
	Values map[string]any `json:"values"`
}

// AggregateResult is one page of the groups.
type AggregateResult struct {
	Data []AggregateGroup `json:"value"`
	// Total counts the groups of every page.
	Total      int    `json:"total"`
	NextCursor string `json:"-"`
}

// APIPage exposes the page to the HTTP layer.
func (p AggregateResult) APIPage() (map[string]any, string) {
	return map[string]any{"value": p.Data, "total": p.Total}, p.NextCursor
}

// Aggregate limits: figures a request asks for, groups it holds to sort and
// page them, and items it reads. A request past the items or groups fails
// rather than answering about part of them.
const (
	maxAggregates      = 20
	maxAggregateGroups = 100_000
	maxAggregateItems  = 1_000_000
	// maxAggregateGroupBy is the fields a group's key can hold.
	maxAggregateGroupBy = 4
)

var aggregateName = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// divide rounds num/den half to even.
func divide(num, den *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Abs(r)
	twice.Lsh(twice, 1)
	if c := twice.Cmp(new(big.Int).Abs(den)); c > 0 || c == 0 && q.Bit(0) == 1 {
		if num.Sign()*den.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return q
}

func pow10(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }

func numericType(t catalog.FieldType) bool {
	return t == catalog.FieldInteger || t == catalog.FieldDecimal || t == catalog.FieldNumber || t == catalog.FieldCurrency
}

func temporalType(t catalog.FieldType) bool {
	return t == catalog.FieldDate || t == catalog.FieldDatetime
}

// fieldType is the type of a validated query field.
func fieldType(f content.Field) catalog.FieldType {
	if f.Def != nil {
		return f.Def.Type
	}
	if d := systemField(f.Key); d != nil {
		return d.Type
	}
	return ""
}

// compareRaw orders raw column values: numbers, text keys (exact numbers
// and ISO dates sort as text) and times.
func compareRaw(a, b any) int {
	switch x := a.(type) {
	case int64:
		if y, ok := b.(int64); ok {
			return cmp.Compare(x, y)
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	case time.Time:
		if y, ok := b.(time.Time); ok {
			return x.Compare(y)
		}
	case bool:
		if y, ok := b.(bool); ok {
			return cmp.Compare(boolInt(x), boolInt(y))
		}
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rawTime reads a date or date-time column value.
func rawTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x, nil
	case []byte:
		return rawTime(string(x))
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("invalid date value %v", v)
}

// period is the first day of the interval holding t, as YYYY-MM-DD.
func period(t time.Time, interval AggregateInterval) string {
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	switch interval {
	case IntervalWeek:
		// Weeks start on Monday, as ISO weeks do.
		day = day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	case IntervalMonth:
		day = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	case IntervalYear:
		day = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return day.Format("2006-01-02")
}

// temporalText is a date or date-time value as items show it.
func temporalText(typ catalog.FieldType, v any) (any, error) {
	if typ == catalog.FieldDate {
		if s, ok := v.(string); ok {
			return s, nil
		}
	}
	t, err := rawTime(v)
	if err != nil {
		return nil, err
	}
	if typ == catalog.FieldDate {
		return t.Format("2006-01-02"), nil
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}

// aggregateGrouping is one field of a group's key.
type aggregateGrouping struct {
	field    string
	interval AggregateInterval
	zone     *time.Location
	typ      catalog.FieldType
	def      *catalog.FieldDefinition
	// byDay means the group column holds the UTC day or date of the value;
	// otherwise, with an interval, it holds the value.
	byDay bool
}

// aggregatedField is a field the aggregates read, whatever number of them
// do: one measure works out all they need.
type aggregatedField struct {
	key   string
	typ   catalog.FieldType
	scale int
}

// aggregateFigure is one figure of each group, of one aggregated field.
type aggregateFigure struct {
	spec  Aggregate
	name  string
	field int
}

// aggregationSpec is a validated request for aggregates.
type aggregationSpec struct {
	groups   []aggregateGrouping
	fields   []aggregatedField
	measures []content.Measure
	figures  []aggregateFigure
	orders   []aggregateOrder
	// fingerprint identifies what its cursors page.
	fingerprint string
	after       string
	limit       int
	// maxItems and maxGroups are the limits it answers within.
	maxItems, maxGroups int
}

// aggregateOrder is one sort key of the groups: the group's count, a field
// of its key (group) or a figure.
type aggregateOrder struct {
	count         bool
	group, figure int
	descending    bool
}

// aggregation validates what can be without the collection's fields.
func aggregation(in AggregateSpec) (*aggregationSpec, error) {
	if len(in.Aggregates) == 0 || len(in.Aggregates) > maxAggregates {
		return nil, invalid(fmt.Sprintf("aggregates needs 1 to %d figures", maxAggregates))
	}
	if len(in.Groups) > maxAggregateGroupBy {
		return nil, invalid(fmt.Sprintf("groups names at most %d fields", maxAggregateGroupBy))
	}
	spec := &aggregationSpec{maxItems: maxAggregateItems, maxGroups: maxAggregateGroups}
	for _, group := range in.Groups {
		if group.Field == "" || slices.ContainsFunc(spec.groups, func(g aggregateGrouping) bool { return g.field == group.Field }) {
			return nil, invalid("groups name unique fields")
		}
		if group.Interval != "" && !slices.Contains([]AggregateInterval{IntervalDay, IntervalWeek, IntervalMonth, IntervalYear}, group.Interval) {
			return nil, invalid("interval is day, week, month or year")
		}
		if group.TimeZone != "" && group.Interval == "" {
			return nil, invalid("time_zone belongs to an interval")
		}
		g := aggregateGrouping{field: group.Field, interval: group.Interval, zone: time.UTC}
		if group.TimeZone != "" {
			loc, err := time.LoadLocation(group.TimeZone)
			if err != nil || group.TimeZone == "Local" {
				return nil, invalid("time_zone is an IANA time zone, such as Europe/Madrid")
			}
			g.zone = loc
		}
		spec.groups = append(spec.groups, g)
	}
	names := map[string]bool{}
	for _, a := range in.Aggregates {
		if !slices.Contains([]AggregateOp{AggregateCount, AggregateSum, AggregateAvg, AggregateMin, AggregateMax}, a.Op) {
			return nil, invalid("an aggregate's op is count, sum, avg, min or max")
		}
		if a.Field == "" {
			return nil, invalid("an aggregate names its field; every group has its count")
		}
		name := a.As
		if name == "" {
			// As long as it needs to be: a field key can fill a name's limit.
			name = string(a.Op) + "_" + strings.TrimPrefix(a.Field, "$")
		} else if !aggregateName.MatchString(name) {
			return nil, invalid("an aggregate's name has 1 to 64 lowercase letters, digits or _: " + name)
		}
		if names[name] {
			return nil, invalid("aggregate names are unique: " + name)
		}
		names[name] = true
		index := slices.IndexFunc(spec.fields, func(f aggregatedField) bool { return f.key == a.Field })
		if index < 0 {
			index = len(spec.fields)
			spec.fields = append(spec.fields, aggregatedField{key: a.Field})
			spec.measures = append(spec.measures, content.Measure{})
		}
		m := &spec.measures[index]
		switch a.Op {
		case AggregateSum, AggregateAvg:
			m.Sum = true
		case AggregateMin:
			m.Min = true
		case AggregateMax:
			m.Max = true
		}
		spec.figures = append(spec.figures, aggregateFigure{spec: a, name: name, field: index})
	}
	if len(in.Sort) > 8 {
		return nil, invalid("at most 8 sort keys")
	}
	sorted := map[string]bool{}
	for _, order := range in.Sort {
		if sorted[order.Field] {
			return nil, invalid("sort keys must be unique")
		}
		sorted[order.Field] = true
		if order.Direction != "" && order.Direction != "asc" && order.Direction != "desc" {
			return nil, invalid("sort direction must be asc or desc")
		}
		o := aggregateOrder{count: order.Field == "$count", group: -1, figure: -1, descending: order.Direction == "desc"}
		o.group = slices.IndexFunc(spec.groups, func(g aggregateGrouping) bool { return g.field == order.Field })
		o.figure = slices.IndexFunc(spec.figures, func(f aggregateFigure) bool { return f.name == order.Field })
		switch {
		case o.group >= 0 && o.figure >= 0:
			return nil, invalid("sort field names both a group and a figure: " + order.Field)
		case !o.count && o.group < 0 && o.figure < 0:
			return nil, invalid("sort names a figure, $count or a field of groups: " + order.Field)
		}
		spec.orders = append(spec.orders, o)
	}
	request, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(request)
	spec.fingerprint, spec.after, spec.limit = hex.EncodeToString(sum[:]), in.After, pageSize(in.Limit)
	return spec, nil
}

// resolve checks the aggregates against a collection's fields and says what
// the database works out.
func (a *aggregationSpec) resolve(column func(string) (content.Field, error)) (*content.Aggregation, error) {
	out := &content.Aggregation{Measures: a.measures}
	for i := range a.groups {
		g := &a.groups[i]
		field, err := column(g.field)
		if err != nil {
			return nil, err
		}
		g.def, g.typ = field.Def, fieldType(field)
		group := content.GroupBy{Field: field}
		if g.interval != "" {
			if !temporalType(g.typ) {
				return nil, invalid("interval groups a date or date-time field, not " + g.field)
			}
			// The database can read a date's day and a date-time's UTC day; any
			// other zone's days are worked out from the values here.
			if g.typ == catalog.FieldDate || g.zone == time.UTC {
				group.By, g.byDay = "day", true
			}
		}
		out.Groups = append(out.Groups, group)
	}
	for i := range a.fields {
		f := &a.fields[i]
		field, err := column(f.key)
		if err != nil {
			return nil, err
		}
		f.typ = fieldType(field)
		if field.Def != nil {
			f.scale = field.Def.Scale
		}
		out.Measures[i].Field = field
	}
	for _, figure := range a.figures {
		typ := a.fields[figure.field].typ
		switch figure.spec.Op {
		case AggregateSum, AggregateAvg:
			if !numericType(typ) {
				return nil, invalid(fmt.Sprintf("%s adds up integer, decimal, number or currency fields, not %s", figure.spec.Op, figure.spec.Field))
			}
		case AggregateMin, AggregateMax:
			if !numericType(typ) && !temporalType(typ) {
				return nil, invalid(fmt.Sprintf("%s compares numeric, date or date-time fields, not %s", figure.spec.Op, figure.spec.Field))
			}
		}
	}
	return out, nil
}

// key is a group column's value as the group's key: the period holding it
// with an interval; otherwise a comparable raw value.
func (g aggregateGrouping) key(v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []byte:
		v = string(x)
	case time.Time:
		v = x.UTC()
	}
	if g.interval == "" {
		return v, nil
	}
	at, err := rawTime(v)
	if err != nil {
		return nil, err
	}
	if !g.byDay {
		at = at.In(g.zone)
	}
	return period(at, g.interval), nil
}

// show is a group's key as the API shows it: numeric values as exact strings,
// as figures are; dates and date-times as items show them.
func (g aggregateGrouping) show(kind content.Kind, key any) (any, error) {
	if key == nil || g.interval != "" {
		return key, nil
	}
	shown, err := groupValue(kind, g.def, key)
	if err != nil {
		return nil, err
	}
	if n, ok := shown.(json.Number); ok {
		return n.String(), nil
	}
	if temporalType(g.typ) {
		return temporalText(g.typ, shown)
	}
	return shown, nil
}

// labelled reports whether the group's values are terms that have names.
func (g aggregateGrouping) labelled() bool {
	return g.typ == catalog.FieldTerm || g.typ == catalog.FieldKeywords
}

// aggregateKey is a group's raw key, comparable as a map key.
type aggregateKey [maxAggregateGroupBy]any

// tally gathers what a measure worked out in the rows of a group.
type tally struct {
	count    int
	sum      *big.Int
	min, max any
}

func (t *tally) add(f content.Figures) {
	t.count += f.Count
	if f.Sum != nil {
		if t.sum == nil {
			t.sum = new(big.Int)
		}
		t.sum.Add(t.sum, f.Sum)
	}
	if f.Min != nil && (t.min == nil || compareRaw(f.Min, t.min) < 0) {
		t.min = f.Min
	}
	if f.Max != nil && (t.max == nil || compareRaw(f.Max, t.max) > 0) {
		t.max = f.Max
	}
}

// sumScale is the fraction digits of a field's stored values.
func (f aggregatedField) sumScale() int {
	if f.typ == catalog.FieldNumber || f.typ == catalog.FieldCurrency {
		return content.ExactScale
	}
	return f.scale
}

// averageScale is the fraction digits of an average: four beyond an
// integer or decimal field's scale, ten for number and currency fields.
func (f aggregatedField) averageScale() int {
	if f.typ == catalog.FieldNumber || f.typ == catalog.FieldCurrency {
		return 10
	}
	return f.scale + 4
}

// show is a minimum or maximum as the API shows it.
func (f aggregatedField) show(v any) (any, error) {
	if temporalType(f.typ) {
		return temporalText(f.typ, v)
	}
	if n, ok := v.(int64); ok {
		return decimalText(big.NewInt(n), f.scale, false), nil
	}
	key, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("invalid %s value", f.typ)
	}
	n, err := numberFromKey(key)
	return n.String(), err
}

// value is a figure of a group as the API shows it: counts as numbers; sums,
// minimums and maximums of numeric fields as exact strings in the field's
// form; averages as exact strings rounded half to even (averageScale); dates
// as items show them; null without values.
func (figure aggregateFigure) value(f aggregatedField, t tally) (any, error) {
	if figure.spec.Op == AggregateCount {
		return t.count, nil
	}
	if t.count == 0 {
		return nil, nil
	}
	switch figure.spec.Op {
	case AggregateSum:
		return decimalText(t.sum, f.sumScale(), f.sumScale() == content.ExactScale), nil
	case AggregateAvg:
		scale := f.averageScale()
		num := new(big.Int).Mul(t.sum, pow10(scale))
		den := new(big.Int).Mul(big.NewInt(int64(t.count)), pow10(f.sumScale()))
		return decimalText(divide(num, den), scale, true), nil
	case AggregateMin:
		return f.show(t.min)
	}
	return f.show(t.max)
}

// QueryAggregates works out figures of the items a collection query
// matches. The database reads each matching item once, as the caller may
// read it.
func (s *Service) QueryAggregates(ctx context.Context, subject, containerID string, in AggregateRequest) (AggregateResult, error) {
	if in.Query.GroupBy != "" {
		return AggregateResult{}, invalid("groups name what aggregates group by, not query.group_by")
	}
	if in.Query.Sort != nil {
		return AggregateResult{}, invalid("query.sort orders items; sort orders the groups of aggregates")
	}
	spec, err := aggregation(in.AggregateSpec)
	if err != nil {
		return AggregateResult{}, err
	}
	ctx, err = s.matchQueryText(ctx, subject, containerID, in.Query.Search, in.Query.Archive.Default())
	if err != nil {
		return AggregateResult{}, err
	}
	return snapshot(ctx, s, func(t *Service) (AggregateResult, error) {
		q, e := t.compileQuery(ctx, subject, containerID, QueryRequest{Query: in.Query, Surface: in.Surface, itemsOnly: true, aggregation: spec}, false)
		if e != nil {
			return AggregateResult{}, e
		}
		return t.aggregates(ctx, subject, q, spec)
	})
}

// QueryViewAggregates works out figures of the items a saved view lists, by
// its filters whatever its grouping and sort. They follow the view when it
// changes.
func (s *Service) QueryViewAggregates(ctx context.Context, subject, id string, in ViewAggregateRequest) (AggregateResult, error) {
	spec, err := aggregation(in.AggregateSpec)
	if err != nil {
		return AggregateResult{}, err
	}
	return snapshot(ctx, s, func(t *Service) (AggregateResult, error) {
		v, e := t.View(ctx, subject, id)
		if e != nil {
			return AggregateResult{}, e
		}
		query, _, e := t.viewQuery(ctx, subject, v, in.Selected)
		if e != nil {
			return AggregateResult{}, e
		}
		query.GroupBy, query.Sort = "", nil
		q, e := t.compileQuery(ctx, subject, v.ContainerID, QueryRequest{Query: query, Surface: in.Surface, itemsOnly: true, aggregation: spec}, false)
		if e != nil {
			return AggregateResult{}, e
		}
		return t.aggregates(ctx, subject, q, spec)
	})
}

// QuerySmartFolderAggregates works out figures of the items at a path of a
// smart folder, across its collections.
func (s *Service) QuerySmartFolderAggregates(ctx context.Context, subject, id string, in SmartFolderAggregateRequest) (AggregateResult, error) {
	spec, err := aggregation(in.AggregateSpec)
	if err != nil {
		return AggregateResult{}, err
	}
	return snapshot(ctx, s, func(t *Service) (AggregateResult, error) {
		f, e := t.smartFolder(ctx, subject, id, false)
		if e != nil {
			return AggregateResult{}, e
		}
		d, e := smartDefinition(f)
		if e != nil {
			return AggregateResult{}, e
		}
		q, _, e := t.compileSmartFolder(ctx, subject, f, d, SmartFolderQueryRequest{Path: in.Path, Surface: in.Surface, aggregation: spec}, false)
		if e != nil {
			return AggregateResult{}, e
		}
		return t.aggregates(ctx, subject, q, spec)
	})
}

// aggregatedGroup is a group as it is gathered and sorted.
type aggregatedGroup struct {
	key     aggregateKey
	count   int
	tallies []tally
}

// compareKeys orders present values; a missing value sorts last, whichever
// the direction.
func compareKeys(a, b any, descending bool) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	c := compareRaw(a, b)
	if descending {
		return -c
	}
	return c
}

// compare orders two groups by a figure, without values last.
func (figure aggregateFigure) compare(a, b tally, descending bool) int {
	if figure.spec.Op == AggregateCount {
		c := cmp.Compare(a.count, b.count)
		if descending {
			return -c
		}
		return c
	}
	switch {
	case a.count == 0 && b.count == 0:
		return 0
	case a.count == 0:
		return 1
	case b.count == 0:
		return -1
	}
	c := 0
	switch figure.spec.Op {
	case AggregateSum:
		c = a.sum.Cmp(b.sum)
	case AggregateAvg:
		// a.sum/a.count against b.sum/b.count, without rounding either.
		c = new(big.Int).Mul(a.sum, big.NewInt(int64(b.count))).Cmp(new(big.Int).Mul(b.sum, big.NewInt(int64(a.count))))
	case AggregateMin:
		c = compareRaw(a.min, b.min)
	case AggregateMax:
		c = compareRaw(a.max, b.max)
	}
	if descending {
		return -c
	}
	return c
}

// compareGroups is the groups' order: the request's sort, then their keys,
// which tell any two apart.
func (a *aggregationSpec) compareGroups(x, y *aggregatedGroup) int {
	for _, order := range a.orders {
		c := 0
		switch {
		case order.count:
			if c = cmp.Compare(x.count, y.count); order.descending {
				c = -c
			}
		case order.group >= 0:
			c = compareKeys(x.key[order.group], y.key[order.group], order.descending)
		default:
			figure := a.figures[order.figure]
			c = figure.compare(x.tallies[figure.field], y.tallies[figure.field], order.descending)
		}
		if c != 0 {
			return c
		}
	}
	for i := range a.groups {
		if c := compareKeys(x.key[i], y.key[i], false); c != 0 {
			return c
		}
	}
	return 0
}

// aggregateCursor continues the groups of one request at an offset of their
// order. They are worked out again for each page, so it holds a digest of
// the groups it was taken from: a page whose groups differ would repeat or
// skip some, and is refused.
type aggregateCursor struct {
	Fingerprint string `json:"fingerprint"`
	Offset      int    `json:"offset"`
	Digest      string `json:"digest"`
}

func encodeAggregateCursor(c aggregateCursor) string {
	raw, _ := json.Marshal(c)
	return "a1." + base64.RawURLEncoding.EncodeToString(raw)
}

func decodeAggregateCursor(after, fingerprint string) (aggregateCursor, error) {
	if after == "" {
		return aggregateCursor{}, nil
	}
	if len(after) > 1024 || !strings.HasPrefix(after, "a1.") {
		return aggregateCursor{}, invalid("invalid query cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(after, "a1."))
	if err != nil {
		return aggregateCursor{}, invalid("invalid query cursor")
	}
	var c aggregateCursor
	if err = json.Unmarshal(raw, &c); err != nil || c.Offset < 0 || c.Digest == "" {
		return aggregateCursor{}, invalid("invalid query cursor")
	}
	if c.Fingerprint != fingerprint {
		return aggregateCursor{}, invalid("cursor does not match this query, schema or caller")
	}
	return c, nil
}

// groupsDigest stands for the ordered groups: their keys and what each
// worked out, so groups that moved, came or went change it.
func groupsDigest(ordered []*aggregatedGroup) string {
	h := sha256.New()
	for _, g := range ordered {
		fmt.Fprintf(h, "%d", g.count)
		for _, k := range g.key {
			fmt.Fprintf(h, "|%T:%v", k, k)
		}
		for _, t := range g.tallies {
			fmt.Fprintf(h, "|%d:%v:%T:%v:%T:%v", t.count, t.sum, t.min, t.min, t.max, t.max)
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// aggregates runs a compiled aggregation and writes a page of its groups.
func (s *Service) aggregates(ctx context.Context, subject string, q compiledQuery, spec *aggregationSpec) (AggregateResult, error) {
	sum := sha256.Sum256([]byte(q.fingerprint + spec.fingerprint))
	fingerprint := hex.EncodeToString(sum[:])
	cursor, e := decodeAggregateCursor(spec.after, fingerprint)
	if e != nil {
		return AggregateResult{}, e
	}
	if q.searchCapped {
		return AggregateResult{}, invalid("the search text matches more entries than a query considers, so figures would cover part of them: narrow it")
	}
	groups := map[aggregateKey]*aggregatedGroup{}
	items := 0
	gather := func(row content.AggregateRow) error {
		if items += row.Count; items > spec.maxItems {
			return invalid(fmt.Sprintf("more than %d items match: filter them", spec.maxItems))
		}
		var key aggregateKey
		for i, v := range row.Keys {
			var err error
			if key[i], err = spec.groups[i].key(v); err != nil {
				return err
			}
		}
		g := groups[key]
		if g == nil {
			if len(groups) >= spec.maxGroups {
				return invalid(fmt.Sprintf("more than %d groups: filter the items or group by a longer interval", spec.maxGroups))
			}
			g = &aggregatedGroup{key: key, tallies: make([]tally, len(spec.fields))}
			groups[key] = g
		}
		g.count += row.Count
		for i, f := range row.Figures {
			g.tallies[i].add(f)
		}
		return nil
	}
	if q.q != nil {
		e = s.DAL.Content().Aggregate(ctx, q.q, spec.maxItems+1, gather)
	} else if len(spec.groups) == 0 {
		// A smart folder that covers no collection still has its one group.
		e = gather(content.AggregateRow{})
	}
	if e != nil {
		return AggregateResult{}, e
	}
	ordered := slices.SortedFunc(maps.Values(groups), spec.compareGroups)
	digest := ""
	if spec.after != "" || len(ordered) > spec.limit {
		if digest = groupsDigest(ordered); spec.after != "" && digest != cursor.Digest {
			return AggregateResult{}, ErrFiguresChanged
		}
	}
	out := AggregateResult{Data: []AggregateGroup{}, Total: len(ordered)}
	page := ordered[min(cursor.Offset, len(ordered)):]
	if len(page) > spec.limit {
		page = page[:spec.limit]
		out.NextCursor = encodeAggregateCursor(aggregateCursor{fingerprint, cursor.Offset + spec.limit, digest})
	}
	for _, g := range page {
		keys := make([]AggregateKey, len(spec.groups))
		for i, grouping := range spec.groups {
			if keys[i].Value, e = grouping.show(q.q.GroupKinds[i], g.key[i]); e != nil {
				return AggregateResult{}, e
			}
		}
		values := make(map[string]any, len(spec.figures))
		for _, figure := range spec.figures {
			if values[figure.name], e = figure.value(spec.fields[figure.field], g.tallies[figure.field]); e != nil {
				return AggregateResult{}, e
			}
		}
		out.Data = append(out.Data, AggregateGroup{Keys: keys, Count: g.count, Values: values})
	}
	return out, s.labelAggregateKeys(ctx, subject, spec, out.Data)
}

// labelAggregateKeys names the terms of a page's keys.
func (s *Service) labelAggregateKeys(ctx context.Context, subject string, spec *aggregationSpec, groups []AggregateGroup) error {
	ids := []string{}
	for i, grouping := range spec.groups {
		if grouping.labelled() {
			for _, g := range groups {
				if id, ok := g.Keys[i].Value.(string); ok {
					ids = append(ids, id)
				}
			}
		}
	}
	names, err := s.termNames(ctx, subject, ids)
	if err != nil {
		return err
	}
	for i, grouping := range spec.groups {
		if grouping.labelled() {
			for _, g := range groups {
				if id, ok := g.Keys[i].Value.(string); ok {
					g.Keys[i].Label = names[id]
				}
			}
		}
	}
	return nil
}
