package forms

import (
	"encoding/json"
	"strings"
	"testing"
)

func schemaOf(t *testing.T, raw string) map[string]any {
	t.Helper()
	var s map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFormSchemaValidation(t *testing.T) {
	for name, tc := range map[string]struct{ schema, err string }{
		"valid": {`{"type": "object", "required": ["a"], "properties": {
			"a": {"type": "string", "minLength": 1, "maxLength": 10, "enum": ["x", "y"], "default": "x"},
			"n": {"type": "integer", "minimum": 0, "maximum": 5},
			"list": {"type": "array", "items": {"type": "string"}, "minItems": 1, "uniqueItems": true},
			"group": {"type": "object", "properties": {"b": {"type": "boolean", "default": true}}},
			"pick": {"type": "array", "items": {"type": "string"}, "x-papergo": {"kind": "terms", "term_set_id": "s"}},
			"who": {"type": "string", "x-papergo": {"kind": "people", "access": "write"}, "x-ui-widget": "select"}}}`, ""},
		"root not object":    {`{"type": "string"}`, "must describe an object"},
		"no properties":      {`{"type": "object"}`, "needs properties"},
		"unknown type":       {`{"type": "object", "properties": {"a": {"type": "date"}}}`, "needs a type"},
		"unenforced keyword": {`{"type": "object", "properties": {"a": {"type": "string", "pattern": "^x"}}}`, "pattern is not supported"},
		"misplaced keyword":  {`{"type": "object", "properties": {"a": {"type": "string", "minimum": 1}}}`, "does not apply"},
		"bad required":       {`{"type": "object", "required": ["b"], "properties": {"a": {"type": "string"}}}`, "not a property"},
		"bad default":        {`{"type": "object", "properties": {"a": {"type": "integer", "default": "1"}}}`, "default"},
		"bad enum":           {`{"type": "object", "properties": {"a": {"type": "string", "enum": [1]}}}`, "enum"},
		"bad property name":  {`{"type": "object", "properties": {"A b": {"type": "string"}}}`, "lowercase identifiers"},
		"picker type":        {`{"type": "object", "properties": {"a": {"type": "integer", "x-papergo": {"kind": "item"}}}}`, "string (one) or an array"},
		"picker kind":        {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "users"}}}}`, "kind must be"},
		"picker option":      {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "item", "term_set_id": "s"}}}}`, "not an option"},
		"relationship type":  {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "relationship"}}}}`, "relationship_type_id"},
		"keywords":           {`{"type": "object", "properties": {"k": {"type": "array", "items": {"type": "string"}, "x-papergo": {"kind": "keywords"}}}}`, ""},
		"keywords option":    {`{"type": "object", "properties": {"k": {"type": "string", "x-papergo": {"kind": "keywords", "collection_id": "c"}}}}`, "not an option"},
		"terms in a group":   {`{"type": "object", "properties": {"t": {"type": "string", "x-papergo": {"kind": "terms", "group_id": "g"}}}}`, ""},
		"bad access":         {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "people", "access": "own"}}}}`, "access must be"},
		"hint not text":      {`{"type": "object", "properties": {}, "x-papergo-selection": {"preview": 1}}`, "not a text hint"},
		"nested hint":        {`{"type": "object", "properties": {"a": {"type": "object", "properties": {}, "x-papergo-selection": {}}}}`, "belongs to the root"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateLaunch(schemaOf(t, tc.schema), "input_schema")
			switch {
			case tc.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
				t.Fatalf("error %v, want %q", err, tc.err)
			}
		})
	}
}

func TestFormValues(t *testing.T) {
	s := schemaOf(t, `{"type": "object", "required": ["title", "pick"], "properties": {
		"title": {"type": "string", "minLength": 2},
		"mode": {"type": "string", "enum": ["fast", "safe"], "default": "safe"},
		"count": {"type": "integer", "minimum": 1, "maximum": 3},
		"tags": {"type": "array", "items": {"type": "string"}, "maxItems": 2, "uniqueItems": true},
		"pick": {"type": "array", "items": {"type": "string"}, "x-papergo": {"kind": "item"}},
		"group": {"type": "object", "properties": {"on": {"type": "boolean", "default": true}, "n": {"type": "number"}}}}}`)
	got, err := Values(s, map[string]any{"title": "ok", "pick": []any{"i1"}, "group": map[string]any{}}, "inputs")
	if err != nil {
		t.Fatal(err)
	}
	if got["mode"] != "safe" || got["group"].(map[string]any)["on"] != true {
		t.Fatalf("defaults: %v", got)
	}
	for name, tc := range map[string]struct {
		values map[string]any
		err    string
	}{
		"missing":       {map[string]any{"pick": []any{"i1"}}, "title is required"},
		"empty picker":  {map[string]any{"title": "ok", "pick": []any{}}, "pick is required"},
		"unknown":       {map[string]any{"title": "ok", "pick": []any{"i1"}, "extra": 1}, "extra is not an input"},
		"type":          {map[string]any{"title": 5, "pick": []any{"i1"}}, "must be of type string"},
		"length":        {map[string]any{"title": "x", "pick": []any{"i1"}}, "at least 2 characters"},
		"enum":          {map[string]any{"title": "ok", "pick": []any{"i1"}, "mode": "slow"}, "declared choices"},
		"integer":       {map[string]any{"title": "ok", "pick": []any{"i1"}, "count": json.Number("1.5")}, "type integer"},
		"range":         {map[string]any{"title": "ok", "pick": []any{"i1"}, "count": 9}, "at most 3"},
		"too many":      {map[string]any{"title": "ok", "pick": []any{"i1"}, "tags": []any{"a", "b", "c"}}, "at most 2 entries"},
		"duplicates":    {map[string]any{"title": "ok", "pick": []any{"i1"}, "tags": []any{"a", "a"}}, "unique entries"},
		"nested":        {map[string]any{"title": "ok", "pick": []any{"i1"}, "group": map[string]any{"n": "x"}}, "inputs.group.n must be of type number"},
		"nested extras": {map[string]any{"title": "ok", "pick": []any{"i1"}, "group": map[string]any{"z": 1}}, "inputs.group.z is not an input"},
	} {
		if _, err := Values(s, tc.values, "inputs"); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Fatalf("%s: error %v, want %q", name, err, tc.err)
		}
	}
	if _, err := Values(nil, map[string]any{"a": 1}, "inputs"); err == nil {
		t.Fatal("values without a form")
	}
}

func TestSelectionHintsOnlyInLaunchForms(t *testing.T) {
	s := schemaOf(t, `{"type": "object", "properties": {}, "x-papergo-selection": {"preview": "image"}}`)
	if err := ValidateLaunch(s, "input_schema"); err != nil {
		t.Fatal(err)
	}
	if err := Validate(s, "input_schema"); err == nil || !strings.Contains(err.Error(), "launch schema") {
		t.Fatalf("a task form takes selection hints: %v", err)
	}
}
