package dms

import (
	"cmp"
	"context"
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

// AggregateRequest asks for figures of the items a collection query
// matches: all of them, or in groups of query.group_by. A date or
// date-time group_by groups by Interval, in TimeZone for date-times.
type AggregateRequest struct {
	Query      QuerySpec         `json:"query"`
	Surface    string            `json:"surface,omitempty"`
	Interval   AggregateInterval `json:"interval,omitempty"`
	TimeZone   string            `json:"time_zone,omitempty"`
	Aggregates []Aggregate       `json:"aggregates"`
}

// AggregateGroup is a group's key, its items and its figures, by name.
type AggregateGroup struct {
	Key    any            `json:"key"`
	Count  int            `json:"count"`
	Values map[string]any `json:"values"`
}

type AggregateResult struct {
	Data []AggregateGroup `json:"value"`
	// SearchCapped means the search text matched more entries than a query
	// considers; the figures cover only those.
	SearchCapped bool `json:"search_capped,omitempty"`
}

// Aggregate limits: figures a request asks for, groups it returns and items
// it reads. A request past the items or groups fails rather than answering
// about part of them.
const (
	maxAggregates      = 20
	maxAggregateGroups = 10000
	maxAggregateItems  = 1_000_000
	// maxAggregateGroupBy is the fields a group's key can hold.
	maxAggregateGroupBy = 1
)

var aggregateName = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// decimalText writes n at scale; trim drops trailing fraction zeros.
func decimalText(n *big.Int, scale int, trim bool) string {
	text := new(big.Int).Abs(n).String()
	for len(text) <= scale {
		text = "0" + text
	}
	if scale > 0 {
		text = text[:len(text)-scale] + "." + text[len(text)-scale:]
		if trim {
			text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
		}
	}
	if n.Sign() < 0 && strings.Trim(text, "0.") != "" {
		text = "-" + text
	}
	return text
}

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
	// maxItems and maxGroups are the limits it answers within.
	maxItems, maxGroups int
}

// aggregation validates what can be without the collection's fields.
func aggregation(in AggregateRequest) (*aggregationSpec, error) {
	if len(in.Aggregates) == 0 || len(in.Aggregates) > maxAggregates {
		return nil, invalid(fmt.Sprintf("aggregates needs 1 to %d figures", maxAggregates))
	}
	spec := &aggregationSpec{maxItems: maxAggregateItems, maxGroups: maxAggregateGroups}
	if in.Query.GroupBy != "" {
		g := aggregateGrouping{field: in.Query.GroupBy, interval: in.Interval, zone: time.UTC}
		if in.TimeZone != "" {
			loc, err := time.LoadLocation(in.TimeZone)
			if err != nil || in.TimeZone == "Local" {
				return nil, invalid("time_zone is an IANA time zone, such as Europe/Madrid")
			}
			g.zone = loc
		}
		spec.groups = append(spec.groups, g)
	}
	if in.Interval != "" && !slices.Contains([]AggregateInterval{IntervalDay, IntervalWeek, IntervalMonth, IntervalYear}, in.Interval) {
		return nil, invalid("interval is day, week, month or year")
	}
	if in.Interval != "" && in.Query.GroupBy == "" {
		return nil, invalid("interval groups the group_by field; name one")
	}
	if in.TimeZone != "" && in.Interval == "" {
		return nil, invalid("time_zone belongs to an interval")
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
			name = string(a.Op) + "_" + strings.TrimPrefix(a.Field, "$")
		}
		if !aggregateName.MatchString(name) {
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
				return nil, invalid("interval groups a date or date-time group_by field")
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

// show is a group's key as the API shows it.
func (g aggregateGrouping) show(kind content.Kind, key any) (any, error) {
	if key == nil || g.interval != "" {
		return key, nil
	}
	shown, err := groupValue(kind, g.def, key)
	if err != nil {
		return nil, err
	}
	if temporalType(g.typ) {
		return temporalText(g.typ, shown)
	}
	return shown, nil
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
// matches, in groups of query.group_by or as one group with a null key.
// The database reads each matching item once, as the caller may read it.
func (s *Service) QueryAggregates(ctx context.Context, subject, containerID string, in AggregateRequest) (AggregateResult, error) {
	spec, err := aggregation(in)
	if err != nil {
		return AggregateResult{}, err
	}
	// Figures come by group key; a sort would not change them.
	in.Query.Sort = nil
	ctx, err = s.matchQueryText(ctx, subject, containerID, in.Query.Search, in.Query.Archive.Default())
	if err != nil {
		return AggregateResult{}, err
	}
	return snapshot(ctx, s, func(t *Service) (AggregateResult, error) {
		q, e := t.compileQuery(ctx, subject, containerID, QueryRequest{Query: in.Query, Surface: in.Surface, itemsOnly: true, aggregation: spec}, false)
		if e != nil {
			return AggregateResult{}, e
		}
		return t.aggregates(ctx, q, spec)
	})
}

// aggregates runs a compiled aggregation and writes its groups.
func (s *Service) aggregates(ctx context.Context, q compiledQuery, spec *aggregationSpec) (AggregateResult, error) {
	type group struct {
		key     aggregateKey
		count   int
		tallies []tally
	}
	groups := map[aggregateKey]*group{}
	items := 0
	e := s.DAL.Content().Aggregate(ctx, q.q, spec.maxItems+1, func(row content.AggregateRow) error {
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
			g = &group{key: key, tallies: make([]tally, len(spec.fields))}
			groups[key] = g
		}
		g.count += row.Count
		for i, f := range row.Figures {
			g.tallies[i].add(f)
		}
		return nil
	})
	if e != nil {
		return AggregateResult{}, e
	}
	ordered := slices.Collect(maps.Values(groups))
	slices.SortFunc(ordered, func(a, b *group) int {
		for i := range spec.groups {
			switch x, y := a.key[i], b.key[i]; {
			case x == nil && y == nil:
			case x == nil:
				return 1
			case y == nil:
				return -1
			default:
				if c := compareRaw(x, y); c != 0 {
					return c
				}
			}
		}
		return 0
	})
	out := AggregateResult{Data: make([]AggregateGroup, 0, len(ordered)), SearchCapped: q.searchCapped}
	for _, g := range ordered {
		var key any
		if len(spec.groups) > 0 {
			if key, e = spec.groups[0].show(q.q.GroupKinds[0], g.key[0]); e != nil {
				return AggregateResult{}, e
			}
		}
		values := make(map[string]any, len(spec.figures))
		for _, figure := range spec.figures {
			if values[figure.name], e = figure.value(spec.fields[figure.field], g.tallies[figure.field]); e != nil {
				return AggregateResult{}, e
			}
		}
		out.Data = append(out.Data, AggregateGroup{Key: key, Count: g.count, Values: values})
	}
	return out, nil
}
