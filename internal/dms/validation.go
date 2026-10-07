package dms

import (
	"bytes"
	"encoding/json"
	"math/big"
	"papergo/ent"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var fieldKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var decimalPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

func validateName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 255 {
		return invalid("name must contain 1 to 255 characters")
	}
	return nil
}
func validateTags(tags []string) error {
	if len(tags) > 50 {
		return invalid("at most 50 tags are allowed")
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		if strings.TrimSpace(tag) != tag || tag == "" || utf8.RuneCountInString(tag) > 64 || seen[tag] {
			return invalid("tags must be unique, nonempty, trimmed strings of at most 64 characters")
		}
		seen[tag] = true
	}
	return nil
}
func number(v any) (float64, error) {
	switch n := v.(type) {
	case json.Number:
		return n.Float64()
	case float64:
		return n, nil
	default:
		return 0, invalid("expected JSON number")
	}
}
func integer(v any) (int64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, invalid("integer requires an exact JSON number")
	}
	i, e := strconv.ParseInt(n.String(), 10, 64)
	if e != nil {
		return 0, invalid("integer must fit signed 64 bits")
	}
	return i, nil
}

// Decimals are canonical strings in payloads and scaled signed integers in indexes.
// This deliberately bounds precision rather than silently rounding through float64.
func decimal(v any, scale int) (string, int64, error) {
	var raw string
	switch n := v.(type) {
	case string:
		raw = n
	case json.Number:
		raw = n.String()
	default:
		return "", 0, invalid("decimal requires a string or exact JSON number")
	}
	if scale < 0 || scale > 9 || len(raw) > 80 || !decimalPattern.MatchString(raw) {
		return "", 0, invalid("invalid decimal")
	}
	negative := strings.HasPrefix(raw, "-")
	unsigned := strings.TrimPrefix(raw, "-")
	parts := strings.SplitN(unsigned, ".", 2)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > scale {
		return "", 0, invalid("decimal has more fractional digits than its scale")
	}
	fraction += strings.Repeat("0", scale-len(fraction))
	digits := parts[0] + fraction
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return "", 0, invalid("invalid decimal")
	}
	if negative {
		n.Neg(n)
	}
	if !n.IsInt64() {
		return "", 0, invalid("scaled decimal exceeds signed 64 bits")
	}
	whole := strings.TrimLeft(parts[0], "0")
	if whole == "" {
		whole = "0"
	}
	canonical := whole
	if scale > 0 {
		canonical += "." + fraction
	}
	if negative && n.Sign() != 0 {
		canonical = "-" + canonical
	}
	return canonical, n.Int64(), nil
}
func normalizeValues(defs []*ent.FieldDefinition, values map[string]any) error {
	return normalizeRevisionValues(defs, values, nil)
}

// normalizeRevisionValues validates values for a new revision. Values carried over
// unchanged from previous were valid under the schema they were written with, so
// later tightening (fewer choices, new bounds, required) never blocks other edits.
func normalizeRevisionValues(defs []*ent.FieldDefinition, values, previous map[string]any) error {
	known := make(map[string]*ent.FieldDefinition, len(defs))
	for _, d := range defs {
		known[d.Key] = d
	}
	for key := range values {
		if known[key] == nil {
			return invalid("unknown field: " + key)
		}
	}
	for _, d := range defs {
		v, exists := values[d.Key]
		old, existed := previous[d.Key]
		kept := previous != nil && exists == existed && (!exists || sameJSON(v, old))
		if !exists && len(d.Options.DefaultValue) > 0 {
			var e error
			v, e = decodeValue(d.Options.DefaultValue)
			if e != nil {
				return e
			}
			exists = true
			kept = false
		}
		if kept {
			continue
		}
		if d.Required && (!exists || emptyValue(v)) {
			return invalid("required field missing: " + d.Key)
		}
		if exists {
			out, e := normalizeField(d, v)
			if e != nil {
				return invalid("invalid value for " + d.Key + ": " + e.Error())
			}
			if d.Required && emptyValue(out) {
				return invalid("required field missing: " + d.Key)
			}
			values[d.Key] = out
		}
	}
	encoded, e := json.Marshal(values)
	if e != nil || len(encoded) > 64*1024 {
		return invalid("field values must be valid JSON of at most 64 KiB")
	}
	return nil
}
func sameJSON(a, b any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	return e == nil && bytes.Equal(x, y)
}
