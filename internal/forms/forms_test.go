package forms

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
	"papergo/internal/dms"
	"papergo/internal/identity"
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
		"root not object":                 {`{"type": "string"}`, "must describe an object"},
		"no properties":                   {`{"type": "object"}`, "needs properties"},
		"unknown type":                    {`{"type": "object", "properties": {"a": {"type": "date"}}}`, "needs a type"},
		"unenforced keyword":              {`{"type": "object", "properties": {"a": {"type": "string", "pattern": "^x"}}}`, "pattern is not supported"},
		"misplaced keyword":               {`{"type": "object", "properties": {"a": {"type": "string", "minimum": 1}}}`, "does not apply"},
		"bad required":                    {`{"type": "object", "required": ["b"], "properties": {"a": {"type": "string"}}}`, "not a property"},
		"bad default":                     {`{"type": "object", "properties": {"a": {"type": "integer", "default": "1"}}}`, "default"},
		"bad enum":                        {`{"type": "object", "properties": {"a": {"type": "string", "enum": [1]}}}`, "enum"},
		"bad property name":               {`{"type": "object", "properties": {"A b": {"type": "string"}}}`, "lowercase identifiers"},
		"picker type":                     {`{"type": "object", "properties": {"a": {"type": "integer", "x-papergo": {"kind": "item"}}}}`, "string (one) or an array"},
		"picker kind":                     {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "users"}}}}`, "kind must be"},
		"picker option":                   {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "item", "term_set_id": "s"}}}}`, "not an option"},
		"relationship type":               {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "relationship"}}}}`, "relationship_type_id"},
		"keywords":                        {`{"type": "object", "properties": {"k": {"type": "array", "items": {"type": "string"}, "x-papergo": {"kind": "keywords"}}}}`, ""},
		"keywords option":                 {`{"type": "object", "properties": {"k": {"type": "string", "x-papergo": {"kind": "keywords", "collection_id": "c"}}}}`, "not an option"},
		"terms in a group":                {`{"type": "object", "properties": {"t": {"type": "string", "x-papergo": {"kind": "terms", "group_id": "g"}}}}`, ""},
		"select sets":                     {`{"type":"object","properties":{"s":{"type":"string","x-papergo":{"kind":"terms","select":"sets","group_id":"g"}}}}`, ""},
		"select terms":                    {`{"type":"object","properties":{"s":{"type":"string","x-papergo":{"kind":"terms","select":"terms","term_set_id":"s"}}}}`, ""},
		"invalid selection":               {`{"type":"object","properties":{"s":{"type":"string","x-papergo":{"kind":"terms","select":"groups"}}}}`, "select must be"},
		"set selection with terms filter": {`{"type":"object","properties":{"s":{"type":"string","x-papergo":{"kind":"terms","select":"sets","term_ids":["t"]}}}}`, "selecting sets allows"},
		"set selection with set filter":   {`{"type":"object","properties":{"s":{"type":"string","x-papergo":{"kind":"terms","select":"sets","term_set_id":"s"}}}}`, "selecting sets allows"},
		"bad access":                      {`{"type": "object", "properties": {"a": {"type": "string", "x-papergo": {"kind": "people", "access": "own"}}}}`, "access must be"},
		"hint not text":                   {`{"type": "object", "properties": {}, "x-papergo-selection": {"preview": 1}}`, "not a text hint"},
		"nested hint":                     {`{"type": "object", "properties": {"a": {"type": "object", "properties": {}, "x-papergo-selection": {}}}}`, "belongs to the root"},
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

func TestOpenDictionariesDefaultsAndValidation(t *testing.T) {
	closed := MustParse(`{"type":"object","properties":{}}`)
	if _, err := Values(closed, map[string]any{"unexpected": true}, "form"); err == nil {
		t.Fatal("ordinary forms must be closed")
	}
	open := MustParse(`{"type":"object","additionalProperties":true}`)
	payload := map[string]any{"not a form name": []any{false, json.Number("9007199254740993"), map[string]any{"html": "<&>"}}}
	if _, err := Values(open, payload, "form"); err != nil {
		t.Fatal(err)
	}
	typed := MustParse(`{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":{"type":"object","properties":{"enabled":{"type":"boolean","default":true}}}}`)
	got, err := Values(typed, map[string]any{"entry": map[string]any{}}, "form")
	if err != nil {
		t.Fatal(err)
	}
	if got["entry"].(map[string]any)["enabled"] != true {
		t.Fatal(got)
	}
	if _, err = Values(typed, map[string]any{"entry": "wrong"}, "form"); err == nil {
		t.Fatal("ignored dictionary schema")
	}
	if err = Validate(schemaOf(t, `{"type":"object","additionalProperties":3}`), "form"); err == nil {
		t.Fatal("accepted invalid dictionary keyword")
	}
}

func TestTermsPickerSelections(t *testing.T) {
	ctx := context.Background()
	service := dms.NewService(daltest.Database(t).SQL)
	group, err := service.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := service.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	term, err := service.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	sets := MustParse(`{"type":"object","properties":{"pick":{"type":"string","x-papergo":{"kind":"terms","select":"sets","group_id":"` + group.ID + `"}}}}`)
	if err = service.Directory().SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	terms := MustParse(`{"type":"object","properties":{"pick":{"type":"string","x-papergo":{"kind":"terms","term_set_id":"` + set.ID + `"}}}}`)
	if err = CheckConfig(ctx, service, "alice", "", sets, "form"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, subject, id string
		schema            map[string]any
		valid             bool
	}{
		{"set", "alice", set.ID, sets, true},
		{"term", "alice", term.ID, terms, true},
		{"term is not a set", "alice", term.ID, sets, false},
		{"set is not a term", "alice", set.ID, terms, false},
		{"missing set", "alice", "missing", sets, false},
		{"taxonomy access required", "bob", set.ID, sets, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckValues(ctx, service, tc.subject, "", tc.schema, map[string]any{"pick": tc.id}, "form")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
	other, err := service.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	sets["properties"].(map[string]any)["pick"].(map[string]any)["x-papergo"].(map[string]any)["group_id"] = other.ID
	if err = CheckValues(ctx, service, "alice", "", sets, map[string]any{"pick": set.ID}, "form"); err == nil {
		t.Fatal("set outside selected group accepted")
	}
}

func TestContentTypePickerSelections(t *testing.T) {
	ctx := context.Background()
	service := dms.NewService(daltest.Database(t).SQL)
	ws, err := service.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	records, err := service.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := service.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Plain"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateContentType(ctx, "alice", dms.ContentTypeInput{Key: "record", Name: "Record"})
	if err != nil {
		t.Fatal(err)
	}
	unused, err := service.CreateContentType(ctx, "alice", dms.ContentTypeInput{Key: "unused", Name: "Unused"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AttachContentType(ctx, "alice", records.ID, records.Version, dms.AttachContentTypeInput{ContentTypeID: record.ID}); err != nil {
		t.Fatal(err)
	}
	listType, err := service.DAL.Catalog().CollectionContentType(ctx, records.ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	anywhere := MustParse(`{"type":"object","properties":{"pick":{"type":"string","x-papergo":{"kind":"content_type"}}}}`)
	inPlain := MustParse(`{"type":"object","properties":{"pick":{"type":"string","x-papergo":{"kind":"content_type","collection_id":"` + plain.ID + `"}}}}`)
	if err = CheckConfig(ctx, service, "alice", ws.ID, inPlain, "form"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id string
		schema   map[string]any
		valid    bool
	}{
		{"used catalog type", record.ID, anywhere, true},
		{"unused catalog type", unused.ID, anywhere, false},
		{"list type", listType.ID, anywhere, false},
		{"type outside the collection", record.ID, inPlain, false},
		{"missing", "missing", anywhere, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckValues(ctx, service, "alice", ws.ID, tc.schema, map[string]any{"pick": tc.id}, "form")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
