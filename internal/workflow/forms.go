package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"papergo/ent"
	"papergo/ent/relationshiptype"
	"papergo/ent/resource"
	"papergo/ent/term"
	"papergo/ent/termset"
	"papergo/internal/dms"
)

// Forms are JSON Schema (a draft-07 subset) objects. Workflows use them for
// launch inputs (input_schema), built-ins for their parameters, and
// activities to describe their inputs and outputs. A UI renders them with
// any JSON Schema form library; the server applies defaults and validates
// every value itself, so a form never needs to be trusted.
//
// Supported: type (string, number, integer, boolean, array, object), title,
// description, default, enum, minimum, maximum, minLength, maxLength,
// minItems, maxItems, uniqueItems, items, properties, required, format,
// examples, readOnly, writeOnly, deprecated and $comment. Other x-* keys are
// presentation hints the server keeps but ignores. Validation keywords the
// server does not enforce (pattern, oneOf, ...) are rejected, so a form
// never promises a check that does not happen.
//
// x-papergo on a property makes it a domain picker whose values are IDs the
// server checks against PaperGo data (see domainKinds). x-papergo-selection
// on the root of a selection workflow's launch schema holds presentation
// hints for choosing and ordering the items.

const (
	maxSchemaDepth     = 6
	maxSchemaFields    = 100
	maxDomainSelection = 100
)

var schemaTypes = map[string]bool{"string": true, "number": true, "integer": true, "boolean": true, "array": true, "object": true}

var schemaKeywords = map[string]bool{
	"type": true, "title": true, "description": true, "default": true, "enum": true,
	"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
	"minItems": true, "maxItems": true, "uniqueItems": true, "items": true,
	"properties": true, "required": true, "format": true, "examples": true,
	"readOnly": true, "writeOnly": true, "deprecated": true, "$comment": true,
}

// domainKinds are the x-papergo pickers and their options. Values are IDs:
// a string property holds one, an array of strings several.
//
//	item          items the person may read; collection_id limits them to one list or library
//	collection    lists and libraries of the workspace the person may read
//	relationship  items to link with relationship_type_id (a type of the workspace); no edge is created
//	terms         taxonomy terms of the workspace, not deprecated; term_set_id and term_ids narrow them
//	people        principal subjects with access (default read) to the workspace
var domainKinds = map[string]map[string]bool{
	"item":         {"collection_id": true},
	"collection":   {},
	"relationship": {"relationship_type_id": true},
	"terms":        {"term_set_id": true, "term_ids": true},
	"people":       {"access": true},
}

var selectionHints = map[string]bool{"preview": true, "item_label": true, "order_label": true, "primary_description": true}

// mustSchema parses a schema written in code.
func mustSchema(raw string) map[string]any {
	var s map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&s); err != nil {
		panic(err)
	}
	if err := validateSchema(s, "schema", 0, true); err != nil {
		panic(err)
	}
	return s
}

// validateSchema checks a form schema when it is saved.
func validateSchema(s map[string]any, path string, depth int, root bool) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("%s nests deeper than %d levels", path, maxSchemaDepth)
	}
	typ, _ := s["type"].(string)
	if !schemaTypes[typ] {
		return fmt.Errorf("%s needs a type: string, number, integer, boolean, array or object", path)
	}
	if root && typ != "object" {
		return fmt.Errorf("%s must describe an object", path)
	}
	for key, value := range s {
		switch {
		case schemaKeywords[key]:
		case key == "x-papergo":
			if err := validateDomain(value, typ, s["items"], path); err != nil {
				return err
			}
		case key == "x-papergo-selection":
			if !root {
				return fmt.Errorf("%s: x-papergo-selection belongs to the root of a launch schema", path)
			}
			hints, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.x-papergo-selection must be an object", path)
			}
			for k, v := range hints {
				if _, isText := v.(string); !selectionHints[k] || !isText {
					return fmt.Errorf("%s.x-papergo-selection: %s is not a text hint (preview, item_label, order_label, primary_description)", path, k)
				}
			}
		case strings.HasPrefix(key, "x-"):
		default:
			return fmt.Errorf("%s: %s is not supported; the server would not enforce it", path, key)
		}
	}
	for _, key := range []string{"minLength", "maxLength", "minItems", "maxItems"} {
		if v, ok := s[key]; ok {
			if n, ok := wholeNumber(v); !ok || n < 0 {
				return fmt.Errorf("%s.%s must be a nonnegative integer", path, key)
			}
		}
	}
	for _, key := range []string{"minimum", "maximum"} {
		if v, ok := s[key]; ok {
			if _, ok := number(v); !ok {
				return fmt.Errorf("%s.%s must be a number", path, key)
			}
		}
	}
	if v, ok := s["uniqueItems"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s.uniqueItems must be true or false", path)
		}
	}
	for key, types := range map[string]string{"minimum": "number integer", "maximum": "number integer", "minLength": "string", "maxLength": "string", "minItems": "array", "maxItems": "array", "uniqueItems": "array", "items": "array", "properties": "object", "required": "object"} {
		if _, ok := s[key]; ok && !strings.Contains(types, typ) {
			return fmt.Errorf("%s.%s does not apply to type %s", path, key, typ)
		}
	}
	if typ == "object" {
		props, _ := s["properties"].(map[string]any)
		if root && props == nil {
			return fmt.Errorf("%s needs properties", path)
		}
		if len(props) > maxSchemaFields {
			return fmt.Errorf("%s has more than %d properties", path, maxSchemaFields)
		}
		for name, p := range props {
			child, ok := p.(map[string]any)
			if !ok || !inputNamePattern.MatchString(name) {
				return fmt.Errorf("%s.%s: property names are lowercase identifiers and properties are schemas", path, name)
			}
			if err := validateSchema(child, path+"."+name, depth+1, false); err != nil {
				return err
			}
		}
		if req, ok := s["required"]; ok {
			list, ok := req.([]any)
			if !ok {
				return fmt.Errorf("%s.required must be an array", path)
			}
			for _, r := range list {
				name, _ := r.(string)
				if _, ok := props[name]; !ok {
					return fmt.Errorf("%s.required names %v, which is not a property", path, r)
				}
			}
		}
	}
	if items, ok := s["items"]; ok {
		child, ok := items.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items must be a schema", path)
		}
		if err := validateSchema(child, path+"[]", depth+1, false); err != nil {
			return err
		}
	}
	if e, ok := s["enum"]; ok {
		list, ok := e.([]any)
		if !ok || len(list) == 0 {
			return fmt.Errorf("%s.enum must be a nonempty array", path)
		}
		plain := withoutKey(s, "enum")
		for _, choice := range list {
			if err := checkValue(plain, choice, path); err != nil {
				return fmt.Errorf("%s.enum: %v", path, err)
			}
		}
	}
	if d, ok := s["default"]; ok {
		if err := checkValue(s, d, path); err != nil {
			return fmt.Errorf("%s.default: %v", path, err)
		}
	}
	return nil
}

func validateDomain(value any, typ string, items any, path string) error {
	d, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("%s.x-papergo must be an object", path)
	}
	kind, _ := d["kind"].(string)
	options, ok := domainKinds[kind]
	if !ok {
		return fmt.Errorf("%s.x-papergo.kind must be item, collection, relationship, terms or people", path)
	}
	itemType := ""
	if m, ok := items.(map[string]any); ok {
		itemType, _ = m["type"].(string)
	}
	if typ != "string" && (typ != "array" || itemType != "string") {
		return fmt.Errorf("%s: a %s picker is a string (one) or an array of strings (several)", path, kind)
	}
	for key, v := range d {
		if key == "kind" {
			continue
		}
		if !options[key] {
			return fmt.Errorf("%s.x-papergo: %s is not an option of %s pickers", path, key, kind)
		}
		switch key {
		case "term_ids":
			list, ok := v.([]any)
			if !ok || len(list) == 0 {
				return fmt.Errorf("%s.x-papergo.term_ids must be a nonempty array of term IDs", path)
			}
			for _, id := range list {
				if s, ok := id.(string); !ok || s == "" {
					return fmt.Errorf("%s.x-papergo.term_ids must hold term IDs", path)
				}
			}
		case "access":
			switch v {
			case "read", "read_draft", "write", "publish", "manage":
			default:
				return fmt.Errorf("%s.x-papergo.access must be read, read_draft, write, publish or manage", path)
			}
		default:
			if s, ok := v.(string); !ok || s == "" {
				return fmt.Errorf("%s.x-papergo.%s must be an ID", path, key)
			}
		}
	}
	if kind == "relationship" && d["relationship_type_id"] == nil {
		return fmt.Errorf("%s.x-papergo: relationship pickers need relationship_type_id", path)
	}
	return nil
}

func withoutKey(s map[string]any, key string) map[string]any {
	out := make(map[string]any, len(s))
	for k, v := range s {
		if k != key {
			out[k] = v
		}
	}
	return out
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func wholeNumber(v any) (int, bool) {
	f, ok := number(v)
	if !ok || f != math.Trunc(f) || f > math.MaxInt32 {
		return 0, false
	}
	return int(f), true
}

// withDefaults returns values with the schema's defaults filled in, nested
// objects included.
func withDefaults(s map[string]any, values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	props, _ := s["properties"].(map[string]any)
	for name, p := range props {
		child, _ := p.(map[string]any)
		if _, set := out[name]; !set {
			if d, ok := child["default"]; ok {
				out[name] = jsonish(d)
			}
		}
		if obj, ok := out[name].(map[string]any); ok && child["type"] == "object" {
			out[name] = withDefaults(child, obj)
		}
	}
	return out
}

// checkObject validates values against an object schema: required
// properties, unknown names, and each value.
func checkObject(s map[string]any, values map[string]any, path string) error {
	props, _ := s["properties"].(map[string]any)
	required, _ := s["required"].([]any)
	for _, r := range required {
		name, _ := r.(string)
		v, set := values[name]
		child, _ := props[name].(map[string]any)
		if !set || v == nil || child["x-papergo"] != nil && isEmpty(v) {
			return dms.Invalid(fmt.Sprintf("%s.%s is required", path, name))
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child, ok := props[name].(map[string]any)
		if !ok {
			return dms.Invalid(fmt.Sprintf("%s.%s is not an input of this form", path, name))
		}
		if values[name] == nil {
			continue
		}
		if err := checkValue(child, values[name], path+"."+name); err != nil {
			return dms.Invalid(err.Error())
		}
	}
	return nil
}

func isEmpty(v any) bool {
	switch t := v.(type) {
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	}
	return false
}

// checkValue validates one value against its schema.
func checkValue(s map[string]any, v any, path string) error {
	typ, _ := s["type"].(string)
	fits := false
	switch typ {
	case "string":
		_, fits = v.(string)
	case "number":
		_, fits = number(v)
	case "integer":
		f, ok := number(v)
		fits = ok && f == math.Trunc(f)
	case "boolean":
		_, fits = v.(bool)
	case "array":
		_, fits = v.([]any)
	case "object":
		_, fits = v.(map[string]any)
	}
	if !fits {
		return fmt.Errorf("%s must be of type %s", path, typ)
	}
	if choices, ok := s["enum"].([]any); ok {
		found := false
		for _, c := range choices {
			if sameJSON(c, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s must be one of the declared choices", path)
		}
	}
	switch t := v.(type) {
	case string:
		n := utf8.RuneCountInString(t)
		if min, ok := wholeNumber(s["minLength"]); ok && n < min {
			return fmt.Errorf("%s needs at least %d characters", path, min)
		}
		if max, ok := wholeNumber(s["maxLength"]); ok && n > max {
			return fmt.Errorf("%s takes at most %d characters", path, max)
		}
	case []any:
		if min, ok := wholeNumber(s["minItems"]); ok && len(t) < min {
			return fmt.Errorf("%s needs at least %d entries", path, min)
		}
		if max, ok := wholeNumber(s["maxItems"]); ok && len(t) > max {
			return fmt.Errorf("%s takes at most %d entries", path, max)
		}
		if s["uniqueItems"] == true {
			for i := range t {
				for j := 0; j < i; j++ {
					if sameJSON(t[i], t[j]) {
						return fmt.Errorf("%s needs unique entries", path)
					}
				}
			}
		}
		if s["x-papergo"] != nil && len(t) > maxDomainSelection {
			return fmt.Errorf("%s takes at most %d selections", path, maxDomainSelection)
		}
		if item, ok := s["items"].(map[string]any); ok {
			for i, e := range t {
				if err := checkValue(item, e, path+"["+strconv.Itoa(i)+"]"); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		if err := checkObject(s, t, path); err != nil {
			return err
		}
	default:
		if f, ok := number(v); ok {
			if min, ok := number(s["minimum"]); ok && f < min {
				return fmt.Errorf("%s must be at least %v", path, s["minimum"])
			}
			if max, ok := number(s["maximum"]); ok && f > max {
				return fmt.Errorf("%s must be at most %v", path, s["maximum"])
			}
		}
	}
	return nil
}

func sameJSON(a, b any) bool {
	if fa, ok := number(a); ok {
		fb, ok := number(b)
		return ok && fa == fb
	}
	return reflect.DeepEqual(jsonish(a), jsonish(b))
}

// formValues applies defaults to submitted values and validates them.
func formValues(s map[string]any, values map[string]any, path string) (map[string]any, error) {
	if s == nil {
		if len(values) > 0 {
			return nil, dms.Invalid(path + " takes no values")
		}
		return map[string]any{}, nil
	}
	out := withDefaults(s, values)
	if err := checkObject(s, out, path); err != nil {
		return nil, err
	}
	return out, nil
}

// domainField is one x-papergo picker with the IDs it holds.
type domainField struct {
	path string
	kind string
	opts map[string]any
	ids  []string
}

// domainFields collects the pickers of a schema; with values, it collects
// their selected IDs too.
func domainFields(s map[string]any, values any, path string, out *[]domainField) {
	if d, ok := s["x-papergo"].(map[string]any); ok {
		f := domainField{path: path, kind: d["kind"].(string), opts: d}
		switch v := values.(type) {
		case string:
			if v != "" {
				f.ids = []string{v}
			}
		case []any:
			for _, e := range v {
				if id, ok := e.(string); ok {
					f.ids = append(f.ids, id)
				}
			}
		}
		*out = append(*out, f)
		return
	}
	props, _ := s["properties"].(map[string]any)
	obj, _ := values.(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child, _ := props[name].(map[string]any)
		domainFields(child, obj[name], path+"."+name, out)
	}
	if item, ok := s["items"].(map[string]any); ok {
		if list, ok := values.([]any); ok {
			for i, e := range list {
				domainFields(item, e, path+"["+strconv.Itoa(i)+"]", out)
			}
		} else if values == nil {
			domainFields(item, nil, path+"[]", out)
		}
	}
}

// checkDomainConfig checks, when a workflow is saved, that its pickers
// point at collections, relationship types, term sets and terms of the
// workspace.
func checkDomainConfig(ctx context.Context, t *dms.Service, workspaceID string, s map[string]any, path string) error {
	var fields []domainField
	domainFields(s, nil, path, &fields)
	for _, f := range fields {
		at := f.path + ".x-papergo: "
		if id, _ := f.opts["collection_id"].(string); id != "" {
			ok, err := t.Client.Resource.Query().Where(resource.IDEQ(id), resource.WorkspaceIDEQ(workspaceID), resource.KindIn(resource.KindList, resource.KindLibrary), resource.DeletedAtIsNil()).Exist(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return dms.Invalid(at + "collection_id must be a list or library of the workspace")
			}
		}
		if id, _ := f.opts["relationship_type_id"].(string); id != "" {
			ok, err := t.Client.RelationshipType.Query().Where(relationshiptype.IDEQ(id), relationshiptype.WorkspaceIDEQ(workspaceID)).Exist(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return dms.Invalid(at + "relationship_type_id must be a relationship type of the workspace")
			}
		}
		set, _ := f.opts["term_set_id"].(string)
		if set != "" {
			ok, err := t.Client.TermSet.Query().Where(termset.IDEQ(set), termset.WorkspaceIDEQ(workspaceID)).Exist(ctx)
			if err != nil {
				return err
			}
			if !ok {
				return dms.Invalid(at + "term_set_id must be a term set of the workspace")
			}
		}
		if list, ok := f.opts["term_ids"].([]any); ok {
			for _, v := range list {
				if err := checkTerm(ctx, t, workspaceID, set, nil, v.(string)); err != nil {
					return dms.Invalid(at + err.Error())
				}
			}
		}
	}
	return nil
}

func checkTerm(ctx context.Context, t *dms.Service, workspaceID, set string, allowed []any, id string) error {
	tm, err := t.Client.Term.Query().Where(term.IDEQ(id)).WithTermSet().Only(ctx)
	if ent.IsNotFound(err) {
		return fmt.Errorf("term %s does not exist", id)
	}
	if err != nil {
		return err
	}
	if tm.Edges.TermSet == nil || tm.Edges.TermSet.WorkspaceID != workspaceID || set != "" && tm.TermSetID != set {
		return fmt.Errorf("term %s is outside the allowed term set", id)
	}
	if tm.Deprecated {
		return fmt.Errorf("term %s is deprecated", id)
	}
	if allowed != nil {
		for _, a := range allowed {
			if a == id {
				return nil
			}
		}
		return fmt.Errorf("term %s is not one of the allowed terms", id)
	}
	return nil
}

// checkDomainValues checks, before a run starts (and later before an
// approval is decided), that every picked ID exists and is allowed for
// subject, the person who submits the form.
func checkDomainValues(ctx context.Context, t *dms.Service, subject, workspaceID string, s map[string]any, values map[string]any, path string) error {
	var fields []domainField
	domainFields(s, values, path, &fields)
	for _, f := range fields {
		seen := map[string]bool{}
		for _, id := range f.ids {
			if seen[id] {
				return dms.Invalid(f.path + " has duplicate selections")
			}
			seen[id] = true
			if err := checkPick(ctx, t, subject, workspaceID, f, id); err != nil {
				return dms.Invalid(fmt.Sprintf("%s: %v", f.path, err))
			}
		}
	}
	return nil
}

func checkPick(ctx context.Context, t *dms.Service, subject, workspaceID string, f domainField, id string) error {
	switch f.kind {
	case "item", "relationship":
		r, err := t.Authorize(ctx, subject, id, "read")
		if err != nil || r.Kind != resource.KindItem || r.WorkspaceID != workspaceID {
			return fmt.Errorf("item %s is not a readable item of the workspace", id)
		}
		if c, _ := f.opts["collection_id"].(string); c != "" && (r.ContainerID == nil || *r.ContainerID != c) {
			return fmt.Errorf("item %s is not in the allowed collection", id)
		}
	case "collection":
		r, err := t.Authorize(ctx, subject, id, "read")
		if err != nil || r.WorkspaceID != workspaceID || r.Kind != resource.KindList && r.Kind != resource.KindLibrary {
			return fmt.Errorf("%s is not a readable list or library of the workspace", id)
		}
	case "terms":
		set, _ := f.opts["term_set_id"].(string)
		allowed, _ := f.opts["term_ids"].([]any)
		return checkTerm(ctx, t, workspaceID, set, allowed, id)
	case "people":
		access, _ := f.opts["access"].(string)
		if access == "" {
			access = "read"
		}
		if strings.TrimSpace(id) == "" || len(id) > 255 {
			return fmt.Errorf("%q is not a subject", id)
		}
		if _, err := t.Authorize(ctx, id, workspaceID, access); err != nil {
			return fmt.Errorf("%s has no %s access to the workspace", id, access)
		}
	}
	return nil
}
