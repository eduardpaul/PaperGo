package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefinitionValidation(t *testing.T) {
	ok := `{"triggers": [{"type": "item.created", "collection_id": "c"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`
	for name, tc := range map[string]struct{ def, err string }{
		"valid":                {ok, ""},
		"unknown field":        {strings.Replace(ok, `"flow"`, `"flwo"`, 1), "unknown field"},
		"no triggers":          {`{"triggers": [], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "1 to 10 triggers"},
		"unknown trigger":      {strings.Replace(ok, "item.created", "item.renamed", 1), "unknown trigger type"},
		"workflow event":       {strings.Replace(ok, "item.created", "wf.other-flow.done", 1), ""},
		"bad cron":             {`{"triggers": [{"type": "schedule", "cron": "every day"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "invalid cron"},
		"bad zone":             {`{"triggers": [{"type": "schedule", "cron": "0 1 * * *", "time_zone": "Mars/Base"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "time_zone"},
		"condition scope":      {`{"triggers": [{"type": "item.created"}], "condition": {"field": "x", "op": "eq", "value": 1}, "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "needs collection_id"},
		"deleted condition":    {`{"triggers": [{"type": "item.deleted", "collection_id": "c"}], "condition": {"field": "x", "op": "eq", "value": 1}, "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "deleted items"},
		"inputs need manual":   {strings.Replace(ok, `"flow"`, `"input_schema": {"type": "object", "properties": {"n": {"type": "string"}}}, "flow"`, 1), "manual trigger"},
		"missing start":        {strings.Replace(ok, `"start": "a"`, `"start": "b"`, 1), "start must name a node"},
		"unknown activity":     {strings.Replace(ok, `"end"`, `"teleport"`, 1), "unknown activity"},
		"unknown port":         {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "next": {"maybe": "b"}}, "b": {"activity": "end"}}}}`, "no port maybe"},
		"dangling port":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "next": {"done": "z"}}}}}`, "unknown node z"},
		"unreachable":          {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}, "b": {"activity": "end"}}}}`, "cannot be reached"},
		"terminal next":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end", "next": {"done": "a"}}}}}`, "ends the run"},
		"required input":       {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.create", "inputs": {"name": "x"}}}}}`, "collection_id is required"},
		"unknown input":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "inputs": {"id": "x"}}}}}`, "has no input id"},
		"if both":              {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "if", "inputs": {"filter": {}, "op": "eq"}}}}}`, "either filter"},
		"bad retry":            {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "retry": {"attempts": 0}}}}}`, "retry attempts"},
		"bad node name":        {`{"triggers": [{"type": "manual"}], "flow": {"start": "a.b", "nodes": {"a.b": {"activity": "end"}}}}`, "names use"},
		"reserved event":       {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "event.raise", "inputs": {"event": "completed"}}}}}`, "other than completed"},
		"selection":            {`{"triggers": [{"type": "manual", "collection_id": "c", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, ""},
		"selection needs list": {`{"triggers": [{"type": "manual", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "need collection_id"},
		"selection on items":   {`{"triggers": [{"type": "item.created", "collection_id": "c", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "on manual triggers"},
		"bad selection":        {`{"triggers": [{"type": "manual", "collection_id": "c", "selection": "all"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "per_item or selection"},
		"bad duration":         {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "delay", "inputs": {"duration": "soon"}}}}}`, "duration"},
	} {
		t.Run(name, func(t *testing.T) {
			def, err := ParseDefinition(json.RawMessage(tc.def))
			if err == nil {
				err = def.validate()
			}
			switch {
			case tc.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
				t.Fatalf("error %v, want %q", err, tc.err)
			}
		})
	}
}

func TestTokens(t *testing.T) {
	s := &scope{ctx: context.Background(), now: time.Date(2026, 10, 9, 8, 30, 0, 0, time.UTC),
		trigger: map[string]any{"actor": "bob", "data": map[string]any{"revision_number": 3.0}},
		inputs:  map[string]any{"tags": []any{"a", "b"}},
		vars:    map[string]any{"limit": 100.0},
		steps:   map[string]any{"read item": map[string]any{"values": map[string]any{"total": 42.5}}},
		run:     map[string]any{"id": "run-1"},
		item: func(context.Context) (map[string]any, error) {
			return map[string]any{"name": "Lease", "values": map[string]any{"owner": "carol"}}, nil
		}}
	for in, want := range map[string]any{
		"{var:limit}":                   100.0,
		"{step:read item.values.total}": 42.5,
		"{input:tags}":                  []any{"a", "b"},
		"tags {input:tags}":             "tags a, b",
		"{item:name} for {item:values.owner} by {trigger:actor}": "Lease for carol by bob",
		"rev {trigger:data.revision_number} on {today}":          "rev 3 on 2026-10-09",
		"{{literal}} {missing:x}":                                "{literal} {missing:x}",
		"{var:absent}":                                           nil,
	} {
		got, err := s.resolve(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		if string(g) != string(w) {
			t.Fatalf("%s = %s, want %s", in, g, w)
		}
	}
	nested, err := s.resolve(map[string]any{"values": map[string]any{"limit": "{var:limit}"}, "list": []any{"{run:id}"}})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := json.Marshal(nested); string(g) != `{"list":["run-1"],"values":{"limit":100}}` {
		t.Fatalf("nested: %s", g)
	}
	s.item = nil
	if _, err = s.resolve("{item:name}"); err == nil {
		t.Fatal("item token without an item")
	}
}

func TestBuiltInFill(t *testing.T) {
	b := builtins["items.expire"]
	params, err := b.params(map[string]any{"field": "expires"}, "list-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := b.fill(params)
	if err != nil {
		t.Fatal(err)
	}
	def, err := ParseDefinition(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = def.validate(); err != nil {
		t.Fatal(err)
	}
	tr := def.Triggers[0]
	if tr.CollectionID != "list-1" || tr.Cron != "0 1 * * *" || tr.TimeZone != "UTC" || def.Condition.Field != "expires" {
		t.Fatalf("filled: %s", raw)
	}
	if _, err = b.params(map[string]any{"field": "expires", "colour": "red"}, "list-1"); err == nil {
		t.Fatal("unknown parameter accepted")
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		l, op, r any
		want     bool
	}{
		{"10", "gt", "9", true},
		{"10", "gt", "9a", false},
		{"Lease", "contains", "eas", true},
		{"", "empty", nil, true},
		{"x", "not_empty", nil, true},
		{2.0, "le", "2", true},
	} {
		if got := compare(tc.l, tc.op.(string), tc.r); got != tc.want {
			t.Fatalf("%v %v %v = %v", tc.l, tc.op, tc.r, got)
		}
	}
}

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
		"keywords":           {`{"type": "object", "properties": {"k": {"type": "array", "items": {"type": "string"}, "x-papergo": {"kind": "keywords", "collection_id": "c", "allow_new": false}}}}`, ""},
		"bad allow_new":      {`{"type": "object", "properties": {"k": {"type": "string", "x-papergo": {"kind": "keywords", "allow_new": "yes"}}}}`, "allow_new must be"},
		"bad access":         {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "people", "access": "own"}}}}`, "access must be"},
		"hint not text":      {`{"type": "object", "properties": {}, "x-papergo-selection": {"preview": 1}}`, "not a text hint"},
		"nested hint":        {`{"type": "object", "properties": {"a": {"type": "object", "properties": {}, "x-papergo-selection": {}}}}`, "belongs to the root"},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateSchema(schemaOf(t, tc.schema), "input_schema", 0, true)
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
	got, err := formValues(s, map[string]any{"title": "ok", "pick": []any{"i1"}, "group": map[string]any{}}, "inputs")
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
		if _, err := formValues(s, tc.values, "inputs"); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Fatalf("%s: error %v, want %q", name, err, tc.err)
		}
	}
	if _, err := formValues(nil, map[string]any{"a": 1}, "inputs"); err == nil {
		t.Fatal("values without a form")
	}
}

func TestActivityCatalogSchemas(t *testing.T) {
	for _, a := range Activities() {
		if a.InputSchema == nil || a.InputSchema["type"] != "object" {
			t.Fatalf("%s has no input schema", a.Key)
		}
		if err := validateSchema(a.InputSchema, a.Key, 0, true); err != nil {
			t.Fatalf("%s: %v", a.Key, err)
		}
	}
	if err := activities["item.create"].Validate(map[string]any{"name": "x"}); err == nil || !strings.Contains(err.Error(), "collection_id") {
		t.Fatalf("required from schema: %v", err)
	}
}
