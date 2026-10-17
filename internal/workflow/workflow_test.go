package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"papergo/internal/forms"
)

func TestDefinitionValidation(t *testing.T) {
	ok := `{"triggers": [{"type": "item.created", "collection_id": "c"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`
	for name, tc := range map[string]struct{ def, err string }{
		"valid":                      {ok, ""},
		"permissions changed":        {strings.Replace(ok, "item.created", "permissions.changed", 1), ""},
		"permissions content type":   {strings.Replace(strings.Replace(ok, "item.created", "permissions.changed", 1), `"collection_id": "c"`, `"collection_id": "c", "content_type_id": "ct"`, 1), ""},
		"permissions condition":      {`{"triggers":[{"type":"permissions.changed","collection_id":"c"}],"condition":{"field":"x","op":"eq","value":1},"flow":{"start":"a","nodes":{"a":{"activity":"end"}}}}`, ""},
		"permissions actions":        {`{"triggers":[{"type":"permissions.changed","actions":["replace","move"]}],"flow":{"start":"a","nodes":{"a":{"activity":"end"}}}}`, ""},
		"unknown action":             {`{"triggers":[{"type":"permissions.changed","actions":["grant"]}],"flow":{"start":"a","nodes":{"a":{"activity":"end"}}}}`, "distinct values"},
		"actions on items":           {`{"triggers":[{"type":"item.created","actions":["replace"]}],"flow":{"start":"a","nodes":{"a":{"activity":"end"}}}}`, "belong to permissions.changed"},
		"term changed":               {`{"triggers":[{"type":"term.changed"}],"flow":{"start":"a","nodes":{"a":{"activity":"end"}}}}`, ""},
		"term changed options":       {strings.Replace(ok, "item.created", "term.changed", 1), "take no options"},
		"permissions terms":          {`{"triggers":[{"type":"permissions.changed","terms":["t"]}],"flow":{"start":"a","nodes":{"a":{"activity":"end"}}}}`, "terms belong"},
		"unknown field":              {strings.Replace(ok, `"flow"`, `"flwo"`, 1), "unknown field"},
		"no triggers":                {`{"triggers": [], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "1 to 20 triggers"},
		"unknown trigger":            {strings.Replace(ok, "item.created", "item.renamed", 1), "unknown trigger type"},
		"workflow event":             {strings.Replace(ok, "item.created", "wf.other-flow.done", 1), ""},
		"bad cron":                   {`{"triggers": [{"type": "schedule", "cron": "every day"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "invalid cron"},
		"bad zone":                   {`{"triggers": [{"type": "schedule", "cron": "0 1 * * *", "time_zone": "Mars/Base"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "time_zone"},
		"condition scope":            {`{"triggers": [{"type": "item.created"}], "condition": {"field": "x", "op": "eq", "value": 1}, "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "needs collection_id"},
		"deleted condition":          {`{"triggers": [{"type": "item.deleted", "collection_id": "c"}], "condition": {"field": "x", "op": "eq", "value": 1}, "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "deleted items"},
		"inputs need manual":         {strings.Replace(ok, `"flow"`, `"input_schema": {"type": "object", "properties": {"n": {"type": "string"}}}, "flow"`, 1), "manual trigger"},
		"missing start":              {strings.Replace(ok, `"start": "a"`, `"start": "b"`, 1), "start must name a node"},
		"unknown activity":           {strings.Replace(ok, `"end"`, `"teleport"`, 1), "unknown activity"},
		"unknown port":               {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "next": {"maybe": "b"}}, "b": {"activity": "end"}}}}`, "no port maybe"},
		"dangling port":              {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "next": {"done": "z"}}}}}`, "unknown node z"},
		"unreachable":                {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}, "b": {"activity": "end"}}}}`, "cannot be reached"},
		"terminal next":              {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end", "next": {"done": "a"}}}}}`, "ends the run"},
		"required input":             {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.create", "inputs": {"name": "x"}}}}}`, "collection_id is required"},
		"unknown input":              {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "inputs": {"id": "x"}}}}}`, "has no input id"},
		"if both":                    {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "if", "inputs": {"filter": {}, "op": "eq"}}}}}`, "either filter"},
		"bad retry":                  {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "item.get", "retry": {"attempts": 0}}}}}`, "retry attempts"},
		"bad node name":              {`{"triggers": [{"type": "manual"}], "flow": {"start": "a.b", "nodes": {"a.b": {"activity": "end"}}}}`, "names use"},
		"reserved event":             {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "event.raise", "inputs": {"event": "completed"}}}}}`, "other than completed"},
		"selection":                  {`{"triggers": [{"type": "manual", "collection_id": "c", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, ""},
		"selection across workspace": {`{"triggers": [{"type": "manual", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, ""},
		"selection on items":         {`{"triggers": [{"type": "item.created", "collection_id": "c", "selection": "selection"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "on manual triggers"},
		"bad selection":              {`{"triggers": [{"type": "manual", "collection_id": "c", "selection": "all"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "per_item or selection"},
		"bad duration":               {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "delay", "inputs": {"duration": "soon"}}}}}`, "duration"},
		"concurrency":                {strings.Replace(ok, `"flow"`, `"concurrency": "replace", "flow"`, 1), ""},
		"bad concurrency":            {strings.Replace(ok, `"flow"`, `"concurrency": "queue", "flow"`, 1), "parallel, skip, replace or coalesce"},
		"coalesce":                   {strings.Replace(ok, `"flow"`, `"concurrency": "coalesce", "flow"`, 1), ""},
		"coalesce selection":         {`{"triggers": [{"type": "manual", "collection_id": "c", "selection": "selection"}], "concurrency": "coalesce", "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "one item at a time"},
		"folder trigger":             {strings.Replace(ok, "item.created", "folder.updated", 1), ""},
		"folder condition":           {`{"triggers": [{"type": "folder.created", "collection_id": "c"}], "condition": {"field": "x", "op": "eq", "value": 1}, "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "folder triggers take"},
		"folder cron":                {`{"triggers": [{"type": "folder.created", "cron": "0 1 * * *"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "cron and time_zone"},
		"loop":                       {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "forEach", "inputs": {"items": "{run:items}", "as": "member"}, "next": {"body": "b"}}, "b": {"activity": "end"}}}}`, ""},
		"loop without body":          {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "forEach", "inputs": {"items": [1], "as": "x"}}}}}`, "body port"},
		"loop variable":              {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "forEach", "inputs": {"items": [1], "as": "Bad Name"}, "next": {"body": "a"}}}}}`, "variable name"},
		"loop items text":            {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "forEach", "inputs": {"items": "abc", "as": "x"}, "next": {"body": "a"}}}}}`, "array or a token"},
		"replacement sources":        {`{"triggers": [{"type": "file.updated", "collection_id": "c", "sources": ["upload", "workflow"], "media_types": ["image/*"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, ""},
		"file sources":               {`{"triggers": [{"type": "file.added", "collection_id": "c", "sources": ["upload", "workflow"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, ""},
		"sources on items":           {`{"triggers": [{"type": "item.created", "sources": ["upload"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "belong to file.added"},
		"unknown source":             {`{"triggers": [{"type": "file.added", "sources": ["email"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "distinct values"},
		"media types":                {`{"triggers": [{"type": "file.added", "media_types": ["application/pdf", "image/*"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, ""},
		"media types on items":       {`{"triggers": [{"type": "item.created", "media_types": ["image/*"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "belong to file.added"},
		"bad media type":             {`{"triggers": [{"type": "file.added", "media_types": ["*/*"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "media_types are"},
		"repeated media type":        {`{"triggers": [{"type": "file.added", "media_types": ["image/png", "image/png"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "media_types are"},
		"repeated source":            {`{"triggers": [{"type": "file.added", "sources": ["upload", "upload"]}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "distinct values"},
		"outputs":                    {strings.Replace(ok, `"flow"`, `"output_schema": {"type": "object", "properties": {"total": {"type": "integer"}}}, "outputs": {"total": "{step:a.total}"}, "flow"`, 1), ""},
		"outputs without schema":     {strings.Replace(ok, `"flow"`, `"outputs": {"total": 1}, "flow"`, 1), "need an output_schema"},
		"unknown output":             {strings.Replace(ok, `"flow"`, `"output_schema": {"type": "object", "properties": {"total": {"type": "integer"}}}, "outputs": {"sum": 1}, "flow"`, 1), "outputs.sum is not a property"},
		"bad output schema":          {strings.Replace(ok, `"flow"`, `"output_schema": {"type": "string"}, "flow"`, 1), "must describe an object"},
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
		inputs:  map[string]any{"keywords": []any{"a", "b"}},
		vars:    map[string]any{"limit": 100.0},
		steps:   map[string]any{"read item": map[string]any{"values": map[string]any{"total": 42.5}}},
		run:     map[string]any{"id": "run-1"},
		item: func(context.Context) (map[string]any, error) {
			return map[string]any{"name": "Lease", "values": map[string]any{"owner": "carol"}}, nil
		}}
	for in, want := range map[string]any{
		"{var:limit}":                   100.0,
		"{step:read item.values.total}": 42.5,
		"{input:keywords}":              []any{"a", "b"},
		"keywords {input:keywords}":     "keywords a, b",
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

func TestBuiltInsAreValid(t *testing.T) {
	for _, b := range BuiltIns() {
		if err := forms.Validate(b.ParametersSchema, "parameters"); err != nil {
			t.Errorf("%s: %v", b.Key, err)
		}
		params, err := b.params(map[string]any{"field": "expires"}, "list-1")
		if err != nil && b.Key != "items.expire" {
			params, err = b.params(map[string]any{}, "list-1")
		}
		if err != nil {
			t.Fatalf("%s: %v", b.Key, err)
		}
		raw, err := b.fill(params)
		if err != nil {
			t.Fatalf("%s: %v", b.Key, err)
		}
		def, err := ParseDefinition(raw)
		if err == nil {
			err = def.validate()
		}
		if err != nil {
			t.Errorf("%s: %v\n%s", b.Key, err, raw)
		}
	}
	b := builtins["documents.optimize"]
	params, err := b.params(map[string]any{"target_height": 16}, "lib-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := b.fill(params)
	def, _ := ParseDefinition(raw)
	in := def.Flow.Nodes["prepare"].Inputs
	if in["target_height"] != json.Number("16") && in["target_height"] != 16.0 || def.Concurrency != ConcurrencyReplace || def.Triggers[0].CollectionID != "lib-1" {
		t.Errorf("filled: %s", raw)
	}
	if def.Flow.Nodes["review"].Inputs["assignees"] != nil {
		t.Errorf("no approvers fills %v", def.Flow.Nodes["review"].Inputs["assignees"])
	}
	if _, err = b.params(map[string]any{"quality": 101}, "lib-1"); err == nil {
		t.Error("quality 101 is accepted")
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

func TestActivityCatalogSchemas(t *testing.T) {
	for _, a := range Activities() {
		if a.InputSchema == nil || a.InputSchema["type"] != "object" {
			t.Fatalf("%s has no input schema", a.Key)
		}
		if err := forms.Validate(a.InputSchema, a.Key); err != nil {
			t.Fatalf("%s: %v", a.Key, err)
		}
	}
	if err := activities["item.create"].Validate(map[string]any{"name": "x"}); err == nil || !strings.Contains(err.Error(), "collection_id") {
		t.Fatalf("required from schema: %v", err)
	}
}

func TestTriggerMediaTypes(t *testing.T) {
	tr := Trigger{MediaTypes: []string{"application/pdf", "image/*"}}
	for media, want := range map[string]bool{"application/pdf": true, "image/png": true, "image/tiff": true, "application/zip": false, "imagex/png": false, "": false} {
		if got := tr.acceptsMediaType(media); got != want {
			t.Errorf("%q: %v", media, got)
		}
	}
	if !(Trigger{}).acceptsMediaType("application/zip") {
		t.Error("no media_types accepts every file")
	}
}
