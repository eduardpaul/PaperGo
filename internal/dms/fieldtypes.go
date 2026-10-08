package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/big"
	"net/mail"
	"net/url"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/internal/model"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type fieldNormalizer func(*ent.FieldDefinition, any) (any, error)

// The registry owns canonical payload representations; query encoders use those
// same representations. It is immutable after process initialization.
var fieldTypes = map[string]fieldNormalizer{
	"text": normalizeString, "note": normalizeString, "choice": normalizeChoice,
	"email": normalizeEmail, "url": normalizeURL, "date": normalizeDate,
	"datetime": normalizeDateTime, "lookup": normalizeIdentifier, "term": normalizeIdentifier,
	"integer": normalizeInteger, "decimal": normalizeDecimal, "number": normalizeNumber, "boolean": normalizeBoolean,
}

func FieldTypes() []string {
	out := make([]string, 0, len(fieldTypes))
	for name := range fieldTypes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
func normalizeString(d *ent.FieldDefinition, v any) (any, error) {
	x, ok := v.(string)
	if !ok || !utf8.ValidString(x) {
		return nil, invalid("expected text")
	}
	if d.Options.MaxLength != nil && utf8.RuneCountInString(x) > *d.Options.MaxLength {
		return nil, invalid("text exceeds maximum length")
	}
	return x, nil
}
func normalizeChoice(d *ent.FieldDefinition, v any) (any, error) {
	x, e := normalizeString(d, v)
	if e != nil {
		return nil, e
	}
	for _, c := range d.Choices {
		if x == c {
			return x, nil
		}
	}
	return nil, invalid("unknown choice")
}
func normalizeEmail(d *ent.FieldDefinition, v any) (any, error) {
	x, e := normalizeString(d, v)
	if e != nil {
		return nil, e
	}
	a, e := mail.ParseAddress(x.(string))
	if e != nil || a.Address != x.(string) {
		return nil, invalid("expected email address")
	}
	return x, nil
}
func normalizeURL(d *ent.FieldDefinition, v any) (any, error) {
	x, e := normalizeString(d, v)
	if e != nil {
		return nil, e
	}
	u, e := url.Parse(x.(string))
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, invalid("expected absolute HTTP(S) URL")
	}
	return x, nil
}
func normalizeDate(d *ent.FieldDefinition, v any) (any, error) {
	x, ok := v.(string)
	if !ok {
		return nil, invalid("expected date")
	}
	t, e := time.Parse("2006-01-02", x)
	if e != nil {
		return nil, invalid("expected YYYY-MM-DD date")
	}
	return t.Format("2006-01-02"), nil
}
func normalizeDateTime(d *ent.FieldDefinition, v any) (any, error) {
	x, ok := v.(string)
	if !ok {
		return nil, invalid("expected datetime")
	}
	t, e := time.Parse(time.RFC3339Nano, x)
	if e != nil {
		return nil, invalid("expected RFC3339 datetime")
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}
func normalizeIdentifier(d *ent.FieldDefinition, v any) (any, error) {
	x, ok := v.(string)
	if !ok || strings.TrimSpace(x) != x || x == "" || len(x) > 36 {
		return nil, invalid("expected reference ID")
	}
	return x, nil
}
func normalizeInteger(d *ent.FieldDefinition, v any) (any, error) {
	i, e := integer(v)
	if e != nil {
		return nil, e
	}
	return json.Number(new(big.Int).SetInt64(i).String()), nil
}
func normalizeDecimal(d *ent.FieldDefinition, v any) (any, error) {
	c, _, e := decimal(v, d.Scale)
	return c, e
}
func normalizeNumber(d *ent.FieldDefinition, v any) (any, error) {
	n, e := number(v)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, invalid("expected finite number")
	}
	return v, nil
}
func normalizeBoolean(d *ent.FieldDefinition, v any) (any, error) {
	x, ok := v.(bool)
	if !ok {
		return nil, invalid("expected boolean")
	}
	return x, nil
}
func scalarValue(d *ent.FieldDefinition, v any) (any, error) {
	fn := fieldTypes[string(d.Type)]
	if fn == nil {
		return nil, invalid("unknown field type")
	}
	out, e := fn(d, v)
	if e != nil {
		return nil, e
	}
	if d.Options.Minimum != nil || d.Options.Maximum != nil {
		for i, bound := range []*string{d.Options.Minimum, d.Options.Maximum} {
			if bound != nil {
				c, e := compareNumeric(d, out, *bound)
				if e != nil {
					return nil, e
				}
				if (i == 0 && c < 0) || (i == 1 && c > 0) {
					return nil, invalid("value outside configured bounds")
				}
			}
		}
	}
	return out, nil
}
func compareNumeric(d *ent.FieldDefinition, value any, bound string) (int, error) {
	// Approximate numbers use float comparisons. Expanding arbitrary scientific
	// exponents into rationals could allocate unbounded memory on schema input.
	if d.Type == "number" {
		a, e := number(value)
		if e != nil {
			return 0, e
		}
		b, e := number(json.Number(bound))
		if e != nil || math.IsInf(b, 0) || math.IsNaN(b) {
			return 0, invalid("invalid numeric bound")
		}
		if a < b {
			return -1, nil
		}
		if a > b {
			return 1, nil
		}
		return 0, nil
	}
	a, ok := new(big.Rat).SetString(numericString(value))
	if !ok {
		return 0, invalid("expected exact numeric value")
	}
	b, ok := new(big.Rat).SetString(bound)
	if !ok {
		return 0, invalid("invalid numeric bound")
	}
	return a.Cmp(b), nil
}
func numericString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
func decodeValue(raw json.RawMessage) (any, error) {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	e := d.Decode(&v)
	return v, e
}
func normalizeField(d *ent.FieldDefinition, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if !d.Options.Multiple {
		return scalarValue(d, v)
	}
	list, ok := v.([]any)
	if !ok {
		return nil, invalid("expected an array")
	}
	if len(list) > 100 {
		return nil, invalid("at most 100 values per field")
	}
	result := []any{}
	seen := map[string]bool{}
	for _, x := range list {
		if x == nil {
			return nil, invalid("array values cannot be null")
		}
		x, e := scalarValue(d, x)
		if e != nil {
			return nil, e
		}
		b, _ := json.Marshal(x)
		if !seen[string(b)] {
			result = append(result, x)
			seen[string(b)] = true
		}
	}
	return result, nil
}
func emptyValue(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	}
	return false
}
func validateFieldDefinition(d *ent.FieldDefinition) error {
	if !fieldKey.MatchString(d.Key) {
		return invalid("invalid field key")
	}
	if e := validateName(d.Label); e != nil {
		return e
	}
	typ := string(d.Type)
	if fieldTypes[typ] == nil {
		return invalid("invalid field type")
	}
	if d.Scale < 0 || d.Scale > 9 || (typ != "decimal" && d.Scale != 0) {
		return invalid("scale belongs to decimal fields and must be 0..9")
	}
	if typ == "choice" {
		if len(d.Choices) == 0 {
			return invalid("choice field requires choices")
		}
		if e := validateTags(d.Choices); e != nil {
			return e
		}
	} else if len(d.Choices) > 0 {
		return invalid("choices belong to choice fields")
	}
	o := d.Options
	if o.Unique && (!d.Indexed || o.Multiple || typ == "number") {
		return invalid("unique business keys require an indexed scalar field with an exact type")
	}
	if len(o.Description) > 4096 {
		return invalid("description exceeds 4096 bytes")
	}
	if o.MaxLength != nil {
		if *o.MaxLength < 1 || *o.MaxLength > 65536 || !(typ == "text" || typ == "note" || typ == "email" || typ == "url" || typ == "choice") {
			return invalid("max_length requires a text field and must be 1..65536")
		}
	}
	if o.Multiple && !(typ == "choice" || typ == "term" || typ == "lookup" || typ == "text" || typ == "email" || typ == "url") {
		return invalid("multiple requires a text, choice or reference field")
	}
	if (typ == "lookup") != (o.LookupContainerID != "") || (typ == "term") != (o.TermSetID != "") {
		return invalid("reference fields require their matching reference scope")
	}
	if o.Minimum != nil || o.Maximum != nil {
		if typ != "number" && typ != "integer" && typ != "decimal" {
			return invalid("numeric bounds require a numeric field")
		}
		clone := *d
		clone.Options.Minimum = nil
		clone.Options.Maximum = nil
		for _, p := range []*string{o.Minimum, o.Maximum} {
			if p != nil {
				var v any = json.Number(*p)
				if typ == "decimal" {
					v = *p
				}
				if _, e := scalarValue(&clone, v); e != nil {
					return invalid("invalid numeric bound")
				}
			}
		}
		if o.Minimum != nil && o.Maximum != nil {
			comparison, e := compareNumeric(d, json.Number(*o.Minimum), *o.Maximum)
			if e != nil {
				return e
			}
			if comparison > 0 {
				return invalid("minimum exceeds maximum")
			}
		}
	}
	if len(o.DefaultValue) > 0 {
		if !json.Valid(o.DefaultValue) {
			return invalid("default_value must be JSON")
		}
		v, e := decodeValue(o.DefaultValue)
		if e != nil {
			return invalid("invalid default")
		}
		v, e = normalizeField(d, v)
		if e != nil {
			return invalid("invalid default: " + e.Error())
		}
		if d.Required && emptyValue(v) {
			return invalid("required field default cannot be empty")
		}
		d.Options.DefaultValue, _ = json.Marshal(v)
	}
	return nil
}
func fieldFromInput(in CreateField) *ent.FieldDefinition {
	return &ent.FieldDefinition{Key: in.Key, Label: in.Label, Type: fielddefinition.Type(in.Type), Required: in.Required, Choices: in.Choices, Indexed: in.Indexed, Scale: in.Scale, Options: in.Options}
}
func optionsIdentityEqual(a, b model.FieldOptions) bool {
	return a.Multiple == b.Multiple && a.LookupContainerID == b.LookupContainerID && a.TermSetID == b.TermSetID
}

func (s *Service) validateReferenceScopes(ctx context.Context, subject string, workspaceID string, d *ent.FieldDefinition) error {
	if d.Type == "lookup" {
		r, e := s.authorize(ctx, subject, d.Options.LookupContainerID, "read")
		if e != nil {
			return e
		}
		if (r.Kind != "list" && r.Kind != "library") || r.WorkspaceID != workspaceID {
			return invalid("lookup must target a collection in this workspace")
		}
	}
	if d.Type == "term" {
		set, e := s.Client.TermSet.Get(ctx, d.Options.TermSetID)
		if ent.IsNotFound(e) {
			return invalid("unknown term set")
		}
		if e != nil {
			return e
		}
		if set.WorkspaceID != workspaceID {
			return invalid("term set must belong to this workspace")
		}
		if _, e = s.authorize(ctx, subject, set.WorkspaceID, "read"); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) normalizeItemValues(ctx context.Context, subject, workspaceID string, defs []*ent.FieldDefinition, values, previous map[string]any) error {
	if e := normalizeRevisionValues(defs, values, previous); e != nil {
		return e
	}
	for _, d := range defs {
		if d.Type != "term" && d.Type != "lookup" {
			continue
		}
		if values[d.Key] == nil {
			continue
		}
		current := []any{values[d.Key]}
		if d.Options.Multiple {
			current = values[d.Key].([]any)
		}
		old := map[string]bool{}
		if previous != nil {
			oldList := []any{previous[d.Key]}
			if d.Options.Multiple {
				if a, ok := previous[d.Key].([]any); ok {
					oldList = a
				}
			}
			for _, v := range oldList {
				if x, ok := v.(string); ok {
					old[x] = true
				}
			}
		}
		for _, v := range current {
			id := v.(string)
			if d.Type == "term" {
				t, e := s.Client.Term.Get(ctx, id)
				if ent.IsNotFound(e) {
					return invalid("unknown term for field: " + d.Key)
				}
				if e != nil {
					return e
				}
				if t.TermSetID != d.Options.TermSetID || (t.Deprecated && !old[id]) {
					return invalid("term must be active and belong to the configured term set")
				}
			} else {
				if old[id] {
					continue
				}
				r, e := s.Get(ctx, subject, id)
				if e != nil {
					return e
				}
				if r.Kind != "item" || r.ContainerID == nil || *r.ContainerID != d.Options.LookupContainerID || r.WorkspaceID != workspaceID {
					return invalid("lookup target belongs to a different collection")
				}
			}
		}
	}
	return nil
}
