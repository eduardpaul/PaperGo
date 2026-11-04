package dms

import (
	"cmp"
	"context"
	"fmt"
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

// Aggregate limits: figures a request asks for and groups it returns.
const (
	maxAggregates      = 20
	maxAggregateGroups = 10000
)

var aggregateName = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// exact is a number as a coefficient and its scale (decimal digits).
type exact struct {
	n     *big.Int
	scale int
}

// numberScale is the scale of number and currency index keys.
const numberScale = 28

// exactOf reads a measure's raw value as an exact number.
func exactOf(d *catalog.FieldDefinition, v any) (exact, error) {
	switch d.Type {
	case catalog.FieldInteger, catalog.FieldDecimal:
		n, ok := v.(int64)
		if !ok {
			return exact{}, fmt.Errorf("invalid %s value", d.Type)
		}
		scale := 0
		if d.Type == catalog.FieldDecimal {
			scale = d.Scale
		}
		return exact{big.NewInt(n), scale}, nil
	default:
		key, ok := v.(string)
		if !ok || len(key) != 58 || key[0] != '0' && key[0] != '1' {
			return exact{}, fmt.Errorf("invalid %s value", d.Type)
		}
		digits := key[1:]
		if key[0] == '0' {
			digits = complementDigits(digits)
		}
		n, ok := new(big.Int).SetString(digits, 10)
		if !ok {
			return exact{}, fmt.Errorf("invalid %s value", d.Type)
		}
		if key[0] == '0' {
			n.Neg(n)
		}
		return exact{n, numberScale}, nil
	}
}

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

// measure is how one aggregate reads its field and what it has gathered in
// a group.
type measure struct {
	spec  Aggregate
	name  string
	def   *catalog.FieldDefinition
	typ   catalog.FieldType
	index int // the measure column the field is read from
}

type tally struct {
	count    int
	sum      exact
	min, max any // raw values of min and max
}

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

// QueryAggregates works out figures of the items a collection query
// matches, in groups of query.group_by or as one group with a null key.
// It reads each matching item once, as the caller may read it.
func (s *Service) QueryAggregates(ctx context.Context, subject, containerID string, in AggregateRequest) (AggregateResult, error) {
	if len(in.Aggregates) == 0 || len(in.Aggregates) > maxAggregates {
		return AggregateResult{}, invalid(fmt.Sprintf("aggregates needs 1 to %d figures", maxAggregates))
	}
	grouped := in.Query.GroupBy != ""
	if in.Interval != "" && !slices.Contains([]AggregateInterval{IntervalDay, IntervalWeek, IntervalMonth, IntervalYear}, in.Interval) {
		return AggregateResult{}, invalid("interval is day, week, month or year")
	}
	if in.Interval != "" && !grouped {
		return AggregateResult{}, invalid("interval groups the group_by field; name one")
	}
	if in.TimeZone != "" && in.Interval == "" {
		return AggregateResult{}, invalid("time_zone belongs to an interval")
	}
	zone := time.UTC
	if in.TimeZone != "" {
		loc, err := time.LoadLocation(in.TimeZone)
		if err != nil || in.TimeZone == "Local" {
			return AggregateResult{}, invalid("time_zone is an IANA time zone, such as Europe/Madrid")
		}
		zone = loc
	}
	// Figures come by group key; a sort would not change them.
	in.Query.Sort = nil
	fields := []string{}
	measures := make([]measure, len(in.Aggregates))
	names := map[string]bool{}
	for i, a := range in.Aggregates {
		if !slices.Contains([]AggregateOp{AggregateCount, AggregateSum, AggregateAvg, AggregateMin, AggregateMax}, a.Op) {
			return AggregateResult{}, invalid("an aggregate's op is count, sum, avg, min or max")
		}
		if a.Field == "" {
			return AggregateResult{}, invalid("an aggregate names its field; every group has its count")
		}
		name := a.As
		if name == "" {
			name = string(a.Op) + "_" + strings.TrimPrefix(a.Field, "$")
		}
		if !aggregateName.MatchString(name) {
			return AggregateResult{}, invalid("an aggregate's name has 1 to 64 lowercase letters, digits or _: " + name)
		}
		if names[name] {
			return AggregateResult{}, invalid("aggregate names are unique: " + name)
		}
		names[name] = true
		index := slices.Index(fields, a.Field)
		if index < 0 {
			index = len(fields)
			fields = append(fields, a.Field)
		}
		measures[i] = measure{spec: a, name: name, index: index}
	}
	ctx, err := s.matchQueryText(ctx, subject, containerID, in.Query.Search, in.Query.Archive.Default())
	if err != nil {
		return AggregateResult{}, err
	}
	return read(ctx, s, func(t *Service) (AggregateResult, error) {
		q, e := t.compileQuery(ctx, subject, containerID, QueryRequest{Query: in.Query, Surface: in.Surface, measures: fields}, grouped)
		if e != nil {
			return AggregateResult{}, e
		}
		for i := range measures {
			m := &measures[i]
			field := q.measures[m.index]
			m.def, m.typ = field.Def, fieldType(field)
			switch m.spec.Op {
			case AggregateSum, AggregateAvg:
				if !numericType(m.typ) {
					return AggregateResult{}, invalid(fmt.Sprintf("%s adds up integer, decimal, number or currency fields, not %s", m.spec.Op, m.spec.Field))
				}
			case AggregateMin, AggregateMax:
				if !numericType(m.typ) && !temporalType(m.typ) {
					return AggregateResult{}, invalid(fmt.Sprintf("%s compares numeric, date or date-time fields, not %s", m.spec.Op, m.spec.Field))
				}
			}
		}
		groupType := catalog.FieldType("")
		if grouped {
			groupType = fieldType(content.Field{Key: in.Query.GroupBy, Def: q.q.RankDef})
			if in.Interval != "" && !temporalType(groupType) {
				return AggregateResult{}, invalid("interval groups a date or date-time group_by field")
			}
		}
		type group struct {
			raw     any
			count   int
			tallies []tally
		}
		groups := map[string]*group{}
		var nullGroup *group
		e = t.DAL.Content().Measured(ctx, q.q, grouped, len(fields), func(key any, values []any) error {
			if key != nil && in.Interval != "" {
				at, err := rawTime(key)
				if err != nil {
					return err
				}
				if groupType == catalog.FieldDatetime {
					at = at.In(zone)
				}
				key = period(at, in.Interval)
			}
			var g *group
			if key == nil {
				if nullGroup == nil {
					nullGroup = &group{tallies: make([]tally, len(measures))}
				}
				g = nullGroup
			} else {
				id := fmt.Sprintf("%T:%v", key, key)
				if g = groups[id]; g == nil {
					if len(groups) >= maxAggregateGroups {
						return invalid(fmt.Sprintf("more than %d groups: filter the items or group by a longer interval", maxAggregateGroups))
					}
					g = &group{raw: key, tallies: make([]tally, len(measures))}
					groups[id] = g
				}
			}
			g.count++
			for i := range measures {
				m := &measures[i]
				v := values[m.index]
				if v == nil {
					continue
				}
				tl := &g.tallies[i]
				tl.count++
				switch m.spec.Op {
				case AggregateSum, AggregateAvg:
					x, err := exactOf(m.def, v)
					if err != nil {
						return err
					}
					if tl.sum.n == nil {
						tl.sum = exact{new(big.Int), x.scale}
					}
					tl.sum.n.Add(tl.sum.n, x.n)
				case AggregateMin:
					if tl.min == nil || m.less(v, tl.min) {
						tl.min = v
					}
				case AggregateMax:
					if tl.max == nil || m.less(tl.max, v) {
						tl.max = v
					}
				}
			}
			return nil
		})
		if e != nil {
			return AggregateResult{}, e
		}
		ordered := make([]*group, 0, len(groups)+1)
		for _, g := range groups {
			ordered = append(ordered, g)
		}
		slices.SortFunc(ordered, func(a, b *group) int { return compareRaw(a.raw, b.raw) })
		if nullGroup != nil {
			ordered = append(ordered, nullGroup)
		}
		out := AggregateResult{Data: make([]AggregateGroup, 0, len(ordered)), SearchCapped: q.searchCapped}
		for _, g := range ordered {
			key := g.raw
			if key != nil && in.Interval == "" {
				if key, e = groupValue(q.q.Orders[0].Kind, q.q.RankDef, key); e != nil {
					return AggregateResult{}, e
				}
				if temporalType(groupType) {
					if key, e = temporalText(groupType, key); e != nil {
						return AggregateResult{}, e
					}
				}
			}
			values := make(map[string]any, len(measures))
			for i, m := range measures {
				if values[m.name], e = m.figure(g.tallies[i]); e != nil {
					return AggregateResult{}, e
				}
			}
			out.Data = append(out.Data, AggregateGroup{Key: key, Count: g.count, Values: values})
		}
		return out, nil
	})
}

// less orders two raw values of the measure's field.
func (m measure) less(a, b any) bool {
	if numericType(m.typ) {
		x, errX := exactOf(m.def, a)
		y, errY := exactOf(m.def, b)
		if errX == nil && errY == nil {
			return x.n.Cmp(y.n) < 0
		}
	}
	if temporalType(m.typ) {
		x, errX := rawTime(a)
		y, errY := rawTime(b)
		if errX == nil && errY == nil {
			return x.Before(y)
		}
	}
	return compareRaw(a, b) < 0
}

// averageScale is the fraction digits of an average: four beyond an
// integer or decimal field's scale, ten for number and currency fields.
func (m measure) averageScale() int {
	switch m.typ {
	case catalog.FieldInteger:
		return 4
	case catalog.FieldDecimal:
		return m.def.Scale + 4
	}
	return 10
}

// figure is a tally as the API shows it: counts as numbers; sums, minimums
// and maximums of numeric fields as exact strings in the field's form;
// averages as exact strings rounded half to even (averageScale); dates as
// items show them; null without values.
func (m measure) figure(t tally) (any, error) {
	if m.spec.Op == AggregateCount {
		return t.count, nil
	}
	if t.count == 0 {
		return nil, nil
	}
	numeric := func(v exact) string {
		if m.typ == catalog.FieldNumber || m.typ == catalog.FieldCurrency {
			return decimalText(v.n, v.scale, true)
		}
		return decimalText(v.n, v.scale, false)
	}
	switch m.spec.Op {
	case AggregateSum:
		return numeric(t.sum), nil
	case AggregateAvg:
		scale := m.averageScale()
		num := new(big.Int).Mul(t.sum.n, pow10(scale))
		den := new(big.Int).Mul(big.NewInt(int64(t.count)), pow10(t.sum.scale))
		return decimalText(divide(num, den), scale, true), nil
	}
	v := t.min
	if m.spec.Op == AggregateMax {
		v = t.max
	}
	if temporalType(m.typ) {
		return temporalText(m.typ, v)
	}
	x, err := exactOf(m.def, v)
	if err != nil {
		return nil, err
	}
	return numeric(x), nil
}
