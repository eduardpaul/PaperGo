package dms

import (
	"encoding/json"
	"fmt"
	"papergo/ent"
	"time"
)

// Resolving the typed tree bounds input before recursion, keeps literal strings
// literal, and binds cursors to the actual caller/date values used by SQL.
func resolveFilter(f *FilterExpr, subject string, now time.Time, defs map[string]*ent.FieldDefinition) (*FilterExpr, error) {
	nodes := 0
	var walk func(*FilterExpr, int) (*FilterExpr, error)
	walk = func(f *FilterExpr, depth int) (*FilterExpr, error) {
		if f == nil {
			return nil, nil
		}
		nodes++
		if nodes > 32 || depth > 6 {
			return nil, invalid("filter is limited to 32 nodes and depth 6")
		}
		out := *f
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
			if groups != 1 || f.Field != "" || f.Op != "" || len(f.Value) > 0 || f.ValueRef != "" {
				return nil, invalid("filter node must be a group or a condition")
			}
			if f.And != nil {
				if len(f.And) == 0 {
					return nil, invalid("filter groups cannot be empty")
				}
				out.And = make([]FilterExpr, len(f.And))
				for i := range f.And {
					x, e := walk(&f.And[i], depth+1)
					if e != nil {
						return nil, e
					}
					out.And[i] = *x
				}
			}
			if f.Or != nil {
				if len(f.Or) == 0 {
					return nil, invalid("filter groups cannot be empty")
				}
				out.Or = make([]FilterExpr, len(f.Or))
				for i := range f.Or {
					x, e := walk(&f.Or[i], depth+1)
					if e != nil {
						return nil, e
					}
					out.Or[i] = *x
				}
			}
			var e error
			out.Not, e = walk(f.Not, depth+1)
			return &out, e
		}
		if !fieldKey.MatchString(f.Field) {
			if _, d := systemField(f.Field); d == nil && f.Field != "$tags" {
				return nil, invalid("invalid filter field")
			}
		}
		switch f.Op {
		case "", "eq", "ne", "gt", "gte", "lt", "lte", "in", "contains":
		case "missing", "present":
			if len(f.Value) > 0 || f.ValueRef != "" {
				return nil, invalid("missing/present do not accept a value")
			}
			return &out, nil
		default:
			return nil, invalid("unsupported filter operator")
		}
		if f.ValueRef != "" {
			if len(f.Value) > 0 {
				return nil, invalid("give either value or value_ref")
			}
			value, e := relativeValue(f.ValueRef, subject, now)
			if e != nil {
				return nil, e
			}
			d := defs[f.Field]
			if _, builtin := systemField(f.Field); builtin != nil {
				d = builtin
			}
			if f.ValueRef != "me" && d != nil {
				if d.Type != "date" && d.Type != "datetime" {
					return nil, invalid("date value_ref requires a date or datetime field")
				}
				if d.Type == "datetime" {
					value += "T00:00:00Z"
				}
			}
			out.Value, _ = json.Marshal(value)
			out.ValueRef = ""
		}
		value, e := decodeValue(out.Value)
		if e != nil || value == nil {
			return nil, invalid("filter value is required; use missing for null values")
		}
		if f.Op == "in" {
			a, ok := value.([]any)
			if !ok || len(a) < 1 || len(a) > 50 {
				return nil, invalid("in requires 1..50 values")
			}
			for _, v := range a {
				switch v.(type) {
				case string, json.Number, bool:
				default:
					return nil, invalid("in requires non-null scalar values")
				}
			}
		} else {
			switch value.(type) {
			case string, json.Number, bool:
			default:
				return nil, invalid("filter value must be scalar")
			}
		}
		return &out, nil
	}
	return walk(f, 0)
}

func relativeValue(ref, subject string, now time.Time) (string, error) {
	if ref == "me" {
		return subject, nil
	}
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch ref {
	case "today":
	case "yesterday":
		day = day.AddDate(0, 0, -1)
	case "tomorrow":
		day = day.AddDate(0, 0, 1)
	case "last7Days":
		day = day.AddDate(0, 0, -7)
	case "next7Days":
		day = day.AddDate(0, 0, 7)
	case "last30Days":
		day = day.AddDate(0, 0, -30)
	case "next30Days":
		day = day.AddDate(0, 0, 30)
	case "weekStart", "weekEnd":
		offset := (int(day.Weekday()) + 6) % 7
		day = day.AddDate(0, 0, -offset)
		if ref == "weekEnd" {
			day = day.AddDate(0, 0, 6)
		}
	case "monthStart":
		day = time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	case "monthEnd":
		day = time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	default:
		return "", invalid(fmt.Sprintf("unknown value_ref: %s", ref))
	}
	return day.Format("2006-01-02"), nil
}
