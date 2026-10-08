package dms

import (
	"cmp"
	"papergo/ent"
	"papergo/internal/model"
	"strings"
)

func definitionMap(defs []*ent.FieldDefinition) map[string]*ent.FieldDefinition {
	out := make(map[string]*ent.FieldDefinition, len(defs))
	for _, d := range defs {
		out[d.Key] = d
	}
	return out
}

func validateRules(defs []*ent.FieldDefinition, rules []model.ValidationRule) error {
	if len(rules) > 32 {
		return invalid("at most 32 cross-field rules per content type")
	}
	fields := definitionMap(defs)
	seen := map[string]bool{}
	for _, r := range rules {
		left := fields[r.Field]
		if !fieldKey.MatchString(r.Key) || seen[r.Key] || left == nil || left.Options.Multiple || strings.TrimSpace(r.Message) == "" || len(r.Message) > 1024 {
			return invalid("rules require a unique key, scalar field and message of 1..1024 bytes")
		}
		seen[r.Key] = true
		if r.Op == "required_if" {
			when := fields[r.WhenField]
			if when == nil || when.Options.Multiple || r.OtherField != "" || len(r.WhenValue) == 0 {
				return invalid("required_if requires when_field and when_value")
			}
			v, err := decodeValue(r.WhenValue)
			if err != nil || v == nil {
				return invalid("when_value must be a non-null scalar")
			}
			if _, err := scalarValue(when, v); err != nil {
				return invalid("invalid when_value: " + err.Error())
			}
		} else {
			right := fields[r.OtherField]
			if right == nil || right.Options.Multiple || right.Type != left.Type || right.Scale != left.Scale || r.WhenField != "" || len(r.WhenValue) != 0 {
				return invalid("comparison rules require two scalar fields of the same type and scale")
			}
			switch r.Op {
			case "eq", "ne":
			case "gt", "gte", "lt", "lte":
				if left.Type == "boolean" || left.Type == "lookup" || left.Type == "term" || left.Type == "choice" {
					return invalid("this field type supports only eq/ne rules")
				}
			default:
				return invalid("unsupported cross-field operator")
			}
		}
	}
	return nil
}

func compareValues(d *ent.FieldDefinition, a, b any) (int, error) {
	x, err := queryValue(d, a)
	if err != nil {
		return 0, err
	}
	y, err := queryValue(d, b)
	if err != nil {
		return 0, err
	}
	switch v := x.(type) {
	case int64:
		return cmp.Compare(v, y.(int64)), nil
	case float64:
		return cmp.Compare(v, y.(float64)), nil
	case string:
		return strings.Compare(v, y.(string)), nil
	case bool:
		if v == y.(bool) {
			return 0, nil
		}
		if !v {
			return -1, nil
		}
		return 1, nil
	default:
		return 0, invalid("unsupported rule value")
	}
}

func evaluateRules(defs []*ent.FieldDefinition, rules []model.ValidationRule, values map[string]any) error {
	fields := definitionMap(defs)
	for _, r := range rules {
		pass := true
		if r.Op == "required_if" {
			if values[r.WhenField] == nil {
				continue
			}
			v, err := decodeValue(r.WhenValue)
			if err != nil {
				return err
			}
			comparison, err := compareValues(fields[r.WhenField], values[r.WhenField], v)
			if err != nil {
				return err
			}
			pass = comparison != 0 || !emptyValue(values[r.Field])
		} else {
			if values[r.Field] == nil || values[r.OtherField] == nil {
				continue
			}
			comparison, err := compareValues(fields[r.Field], values[r.Field], values[r.OtherField])
			if err != nil {
				return err
			}
			switch r.Op {
			case "eq":
				pass = comparison == 0
			case "ne":
				pass = comparison != 0
			case "gt":
				pass = comparison > 0
			case "gte":
				pass = comparison >= 0
			case "lt":
				pass = comparison < 0
			case "lte":
				pass = comparison <= 0
			}
		}
		if !pass {
			return invalid(r.Key + ": " + r.Message)
		}
	}
	return nil
}
